package main

// A Chrome DevTools Protocol client over a minimal WebSocket, and the headless Chrome (or Edge, Brave, Chromium)
// it drives. Only what rendering needs: open a tab, run script, capture the screen.

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type wsConn struct {
	conn net.Conn
	br   *bufio.Reader
	wmu  sync.Mutex
}

func wsDial(raw string) (*wsConn, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout("tcp", u.Host, 10*time.Second)
	if err != nil {
		return nil, err
	}
	key := make([]byte, 16)
	rand.Read(key)
	req := "GET " + u.RequestURI() + " HTTP/1.1\r\nHost: " + u.Host +
		"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(key) +
		"\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReaderSize(conn, 1<<16)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, fmt.Errorf("websocket handshake: %s", resp.Status)
	}
	return &wsConn{conn: conn, br: br}, nil
}

// write sends one masked frame, as a client must.
func (w *wsConn) write(op byte, p []byte) error {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	buf := make([]byte, 0, len(p)+14)
	buf = append(buf, 0x80|op)
	switch n := len(p); {
	case n < 126:
		buf = append(buf, 0x80|byte(n))
	case n <= 0xFFFF:
		buf = append(buf, 0x80|126)
		buf = binary.BigEndian.AppendUint16(buf, uint16(n))
	default:
		buf = append(buf, 0x80|127)
		buf = binary.BigEndian.AppendUint64(buf, uint64(n))
	}
	var mask [4]byte
	rand.Read(mask[:])
	buf = append(buf, mask[:]...)
	for i, b := range p {
		buf = append(buf, b^mask[i&3])
	}
	_, err := w.conn.Write(buf)
	return err
}

// read returns the next whole message, answering pings on the way.
func (w *wsConn) read() ([]byte, error) {
	var msg []byte
	for {
		var h [2]byte
		if _, err := io.ReadFull(w.br, h[:]); err != nil {
			return nil, err
		}
		fin, op := h[0]&0x80 != 0, h[0]&0x0F
		n := uint64(h[1] & 0x7F)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(w.br, b[:]); err != nil {
				return nil, err
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(w.br, b[:]); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		if n > 1<<30 {
			return nil, errors.New("websocket frame too large")
		}
		var mask [4]byte
		masked := h[1]&0x80 != 0
		if masked {
			if _, err := io.ReadFull(w.br, mask[:]); err != nil {
				return nil, err
			}
		}
		p := make([]byte, n)
		if _, err := io.ReadFull(w.br, p); err != nil {
			return nil, err
		}
		if masked {
			for i := range p {
				p[i] ^= mask[i&3]
			}
		}
		switch op {
		case 0x8:
			return nil, io.EOF
		case 0x9:
			w.write(0xA, p)
			continue
		case 0xA:
			continue
		}
		msg = append(msg, p...)
		if fin {
			return msg, nil
		}
	}
}

// cdp is one DevTools connection: to a tab, or to the browser itself.
type cdp struct {
	ws      *wsConn
	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan cdpReply
	waiters map[string][]chan json.RawMessage
	err     error
}

type cdpReply struct {
	result json.RawMessage
	err    error
}

func dialCDP(wsURL string) (*cdp, error) {
	ws, err := wsDial(wsURL)
	if err != nil {
		return nil, err
	}
	c := &cdp{ws: ws, pending: map[int64]chan cdpReply{}, waiters: map[string][]chan json.RawMessage{}}
	go c.readLoop()
	return c, nil
}

func (c *cdp) readLoop() {
	for {
		data, err := c.ws.read()
		if err != nil {
			c.fail(err)
			return
		}
		var m struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
				Data    string `json:"data"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		c.mu.Lock()
		if m.ID != 0 {
			ch := c.pending[m.ID]
			delete(c.pending, m.ID)
			c.mu.Unlock()
			if ch != nil {
				r := cdpReply{result: m.Result}
				if m.Error != nil {
					r.err = errors.New(strings.TrimSpace(m.Error.Message + " " + m.Error.Data))
				}
				ch <- r
			}
			continue
		}
		ws := c.waiters[m.Method]
		delete(c.waiters, m.Method)
		c.mu.Unlock()
		for _, ch := range ws {
			ch <- m.Params
		}
		pageMessage(m.Method, m.Params)
	}
}

func (c *cdp) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = fmt.Errorf("browser connection closed: %w", err)
	for id, ch := range c.pending {
		ch <- cdpReply{err: c.err}
		delete(c.pending, id)
	}
}

func (c *cdp) call(method string, params, out any) error {
	return c.callTimeout(method, params, out, 60*time.Second)
}

func (c *cdp) callTimeout(method string, params, out any, timeout time.Duration) error {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.err
	}
	c.nextID++
	id := c.nextID
	ch := make(chan cdpReply, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	if params == nil {
		params = struct{}{}
	}
	msg, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	if err := c.ws.write(0x1, msg); err != nil {
		return err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return fmt.Errorf("%s: %w", method, r.err)
		}
		if out != nil {
			return json.Unmarshal(r.result, out)
		}
		return nil
	case <-time.After(timeout):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("%s timed out after %s", method, timeout)
	}
}

// waitFor returns a channel that receives the next event named method. Register before causing it.
func (c *cdp) waitFor(method string) chan json.RawMessage {
	ch := make(chan json.RawMessage, 1)
	c.mu.Lock()
	c.waiters[method] = append(c.waiters[method], ch)
	c.mu.Unlock()
	return ch
}

// eval runs expression in the page, awaits it if it is a promise, and decodes its value into out.
func (c *cdp) eval(expression string, out any) error {
	var r struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := c.call("Runtime.evaluate", map[string]any{"expression": expression, "awaitPromise": true, "returnByValue": true}, &r); err != nil {
		return err
	}
	if e := r.Exception; e != nil {
		if e.Exception != nil && e.Exception.Description != "" {
			return errors.New(e.Exception.Description)
		}
		return errors.New(e.Text)
	}
	if out != nil && len(r.Result.Value) > 0 {
		return json.Unmarshal(r.Result.Value, out)
	}
	return nil
}

var (
	pageSeenMu sync.Mutex
	pageSeen   = map[string]bool{}
)

// pageMessage prints a composition's errors, failed loads and console warnings once each, whichever tab saw them.
func pageMessage(method string, params json.RawMessage) {
	var msg string
	switch method {
	case "Runtime.exceptionThrown":
		var p struct {
			D struct {
				Text      string `json:"text"`
				Exception *struct {
					Description string `json:"description"`
				} `json:"exception"`
			} `json:"exceptionDetails"`
		}
		json.Unmarshal(params, &p)
		msg = p.D.Text
		if p.D.Exception != nil && p.D.Exception.Description != "" {
			msg = p.D.Exception.Description
		}
	case "Runtime.consoleAPICalled":
		var p struct {
			Type string `json:"type"`
			Args []struct {
				Value       any    `json:"value"`
				Description string `json:"description"`
			} `json:"args"`
		}
		json.Unmarshal(params, &p)
		if p.Type != "error" && p.Type != "warning" {
			return
		}
		var parts []string
		for _, a := range p.Args {
			if a.Description != "" {
				parts = append(parts, a.Description)
			} else {
				parts = append(parts, fmt.Sprint(a.Value))
			}
		}
		msg = p.Type + ": " + strings.Join(parts, " ")
	case "Log.entryAdded":
		var p struct {
			Entry struct {
				Level string `json:"level"`
				Text  string `json:"text"`
				URL   string `json:"url"`
			} `json:"entry"`
		}
		json.Unmarshal(params, &p)
		if p.Entry.Level != "error" {
			return
		}
		msg = strings.TrimSpace(p.Entry.Text + " " + p.Entry.URL)
	default:
		return
	}
	if lines := strings.Split(msg, "\n"); len(lines) > 4 {
		msg = strings.Join(lines[:4], "\n")
	}
	pageSeenMu.Lock()
	seen := pageSeen[msg]
	pageSeen[msg] = true
	pageSeenMu.Unlock()
	if !seen {
		fmt.Fprintln(os.Stderr, "page:", msg)
	}
}

// browser is a headless Chromium with its own throwaway profile.
type browser struct {
	cmd    *exec.Cmd
	dir    string
	host   string // 127.0.0.1:<port>
	ctl    *cdp   // the browser's own connection
	exited chan struct{}
}

func findBrowser() (string, error) {
	if p := os.Getenv("RAV_BROWSER"); p != "" {
		return p, nil
	}
	var paths []string
	switch runtime.GOOS {
	case "windows":
		for _, app := range []string{`Google\Chrome\Application\chrome.exe`, `Microsoft\Edge\Application\msedge.exe`, `BraveSoftware\Brave-Browser\Application\brave.exe`, `Chromium\Application\chrome.exe`} {
			for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
				if root != "" {
					paths = append(paths, filepath.Join(root, app))
				}
			}
		}
	case "darwin":
		paths = []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser", "/Applications/Chromium.app/Contents/MacOS/Chromium"}
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge", "brave-browser", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", errors.New("no Chrome, Edge, Brave or Chromium found; set RAV_BROWSER to one")
}

func launchBrowser() (*browser, error) {
	exe, err := findBrowser()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "rav-browser-")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, "--headless=new", "--remote-debugging-port=0", "--user-data-dir="+dir,
		"--no-first-run", "--no-default-browser-check", "--disable-extensions", "--disable-sync",
		"--disable-background-networking", "--disable-component-update", "--disable-default-apps",
		"--hide-scrollbars", "--mute-audio", "--force-color-profile=srgb", "--force-device-scale-factor=1",
		"--autoplay-policy=no-user-gesture-required", "--disable-background-timer-throttling",
		"--disable-renderer-backgrounding", "--disable-backgrounding-occluded-windows",
		"--disable-features=Translate,MediaRouter,OptimizationHints", "about:blank")
	if err := cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("starting %s: %w", exe, err)
	}
	b := &browser{cmd: cmd, dir: dir, exited: make(chan struct{})}
	go func() { cmd.Wait(); close(b.exited) }()
	portFile := filepath.Join(dir, "DevToolsActivePort")
	deadline := time.After(30 * time.Second)
	for b.host == "" {
		if data, err := os.ReadFile(portFile); err == nil {
			if port, _, _ := strings.Cut(string(data), "\n"); strings.TrimSpace(port) != "" {
				b.host = "127.0.0.1:" + strings.TrimSpace(port)
				break
			}
		}
		select {
		case <-b.exited:
			b.close()
			return nil, fmt.Errorf("%s exited before it was ready", exe)
		case <-deadline:
			b.close()
			return nil, fmt.Errorf("%s did not start within 30 s", exe)
		case <-time.After(50 * time.Millisecond):
		}
	}
	var v struct {
		WS string `json:"webSocketDebuggerUrl"`
	}
	resp, err := http.Get("http://" + b.host + "/json/version")
	if err == nil {
		err = json.NewDecoder(resp.Body).Decode(&v)
		resp.Body.Close()
	}
	if err == nil {
		b.ctl, err = dialCDP(v.WS)
	}
	if err != nil {
		b.close()
		return nil, fmt.Errorf("connecting to %s: %w", exe, err)
	}
	return b, nil
}

// newTab opens a blank page in a window of its own and connects to it. A page in a background tab of a shared
// window never paints, so its screenshots would wait forever.
func (b *browser) newTab() (*cdp, error) {
	var t struct {
		TargetID string `json:"targetId"`
	}
	if err := b.ctl.call("Target.createTarget", map[string]any{"url": "about:blank", "newWindow": true}, &t); err != nil {
		return nil, err
	}
	return dialCDP("ws://" + b.host + "/devtools/page/" + t.TargetID)
}

func (b *browser) close() {
	if b.ctl != nil {
		b.ctl.callTimeout("Browser.close", nil, nil, 5*time.Second)
	}
	select {
	case <-b.exited:
	case <-time.After(5 * time.Second):
		b.cmd.Process.Kill()
		<-b.exited
	}
	for range 20 {
		if os.RemoveAll(b.dir) == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

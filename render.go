package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// meta is what a composition's root declares.
type meta struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	FPS    string `json:"fps"`
	Frames int    `json:"frames"`
	Audio  *struct {
		Src    string  `json:"src"`
		Start  float64 `json:"start"`
		Offset float64 `json:"offset"`
	} `json:"audio"`
	clock *Video
}

// tab is one headless tab with the composition loaded on rav's clock.
type tab struct {
	c *cdp
	m meta
}

func openTab(b *browser, p *project) (*tab, error) {
	c, err := b.newTab()
	if err != nil {
		return nil, err
	}
	steps := []struct {
		method string
		params any
	}{
		{"Page.enable", nil},
		{"Runtime.enable", nil},
		{"Log.enable", nil},
		{"Emulation.setFocusEmulationEnabled", map[string]any{"enabled": true}},
		{"Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": runtimeJS}},
	}
	for _, s := range steps {
		if err := c.call(s.method, s.params, nil); err != nil {
			return nil, err
		}
	}
	loaded := c.waitFor("Page.loadEventFired")
	var nav struct {
		ErrorText string `json:"errorText"`
	}
	if err := c.call("Page.navigate", map[string]any{"url": p.url}, &nav); err != nil {
		return nil, err
	}
	if nav.ErrorText != "" {
		return nil, fmt.Errorf("loading %s: %s", p.url, nav.ErrorText)
	}
	select {
	case <-loaded:
	case <-time.After(60 * time.Second):
		return nil, errors.New("the composition did not finish loading within 60 s")
	}
	t := &tab{c: c}
	if err := c.eval("__rav.meta()", &t.m); err != nil {
		return nil, err
	}
	num, den, ok := rational(t.m.FPS)
	if !ok || t.m.Width <= 0 || t.m.Height <= 0 || t.m.Frames <= 0 {
		return nil, fmt.Errorf("#rav needs data-width, data-height, data-fps and data-frames; it has %+v", t.m)
	}
	t.m.clock = &Video{Width: t.m.Width, Height: t.m.Height, FPS: fpsString(num, den), Frames: t.m.Frames, num: num, den: den}
	err = c.call("Emulation.setDeviceMetricsOverride", map[string]any{"width": t.m.Width, "height": t.m.Height, "deviceScaleFactor": 1, "mobile": false}, nil)
	return t, err
}

// frame shows frame k and captures it as a PNG or JPEG.
func (t *tab) frame(k int, format string) ([]byte, error) {
	sec := strconv.FormatFloat(t.m.clock.at(k), 'g', 17, 64)
	if err := t.c.eval("__rav.seek("+sec+")", nil); err != nil {
		return nil, fmt.Errorf("frame %d: %w", k, err)
	}
	params := map[string]any{
		"format":           format,
		"optimizeForSpeed": true,
		"clip":             map[string]any{"x": 0, "y": 0, "width": t.m.Width, "height": t.m.Height, "scale": 1},
	}
	if format == "jpeg" {
		params["quality"] = 95
	}
	var r struct {
		Data string `json:"data"`
	}
	if err := t.c.callTimeout("Page.captureScreenshot", params, &r, 20*time.Second); err != nil {
		return nil, fmt.Errorf("frame %d: %w", k, err)
	}
	return base64.StdEncoding.DecodeString(r.Data)
}

// encode renders frames [from, to) into one H.264 segment.
func (t *tab) encode(from, to int, path string, png bool, crf string, done *atomic.Int64, stop *atomic.Bool) error {
	codec, format := "mjpeg", "jpeg"
	if png {
		codec, format = "png", "png"
	}
	cmd := exec.Command("ffmpeg", "-v", "error", "-nostdin", "-y",
		"-f", "image2pipe", "-c:v", codec, "-framerate", t.m.clock.FPS, "-i", "pipe:0",
		// Chrome's JPEGs are full-range BT.601. Saying so, and rounding accurately, keeps white white: the
		// default conversion lands chroma one step off and tints white to #fdffff.
		"-vf", "pad=ceil(iw/2)*2:ceil(ih/2)*2,scale=in_color_matrix=bt601:in_range=pc:out_color_matrix=bt709:out_range=tv:flags=accurate_rnd+full_chroma_int,format=yuv420p",
		"-c:v", "libx264", "-preset", "medium", "-crf", crf,
		"-colorspace", "bt709", "-color_primaries", "bt709", "-color_trc", "bt709", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return runErr("ffmpeg", err, nil)
	}
	abort := func(err error) error {
		stdin.Close()
		cmd.Process.Kill()
		cmd.Wait()
		return err
	}
	for k := from; k < to && !stop.Load(); k++ {
		img, err := t.frame(k, format)
		if err != nil {
			return abort(err)
		}
		if _, err := stdin.Write(img); err != nil {
			return abort(runErr("ffmpeg", err, stderr.Bytes()))
		}
		done.Add(1)
	}
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		return runErr("ffmpeg", err, stderr.Bytes())
	}
	return nil
}

func cmdRender(argv []string) error {
	a, err := parseArgs(argv, "o: workers: from: to: crf: png")
	if err != nil {
		return err
	}
	if err := a.need(1, "render <project> -o out.mp4 [--workers n] [--from t] [--to t] [--crf 16] [--png]"); err != nil {
		return err
	}
	out := a.vals["o"]
	if out == "" {
		return errors.New("-o out.mp4 is required")
	}
	crf := a.vals["crf"]
	if crf == "" {
		crf = "16"
	}
	workers, err := a.int("workers", min(4, max(1, runtime.NumCPU()/2)))
	if err != nil {
		return err
	}
	p, err := openProject(a.pos[0])
	if err != nil {
		return err
	}
	defer p.close()
	b, err := launchBrowser()
	if err != nil {
		return err
	}
	defer b.close()
	first, err := openTab(b, p)
	if err != nil {
		return err
	}
	m, clock := first.m, first.m.clock
	from, to := 0, m.Frames
	if s := a.vals["from"]; s != "" {
		if from, err = clock.frameOf(s); err != nil {
			return err
		}
	}
	if s := a.vals["to"]; s != "" {
		if to, err = clock.frameOf(s); err != nil {
			return err
		}
	}
	to = min(to, m.Frames)
	if from >= to {
		return fmt.Errorf("nothing to render between frames %d and %d", from, to)
	}
	n := to - from
	workers = max(1, min(workers, n/15+1))
	tabs := []*tab{first}
	for len(tabs) < workers {
		t, err := openTab(b, p)
		if err != nil {
			return err
		}
		tabs = append(tabs, t)
	}
	tmp, err := os.MkdirTemp("", "rav-render-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	start := time.Now()
	var done atomic.Int64
	var stop atomic.Bool
	stopProgress := progress(&done, n, start)
	errs := make([]error, workers)
	segs := make([]string, workers)
	var wg sync.WaitGroup
	for i, t := range tabs {
		lo, hi := from+n*i/workers, from+n*(i+1)/workers
		segs[i] = filepath.Join(tmp, fmt.Sprintf("seg%02d.mp4", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := t.encode(lo, hi, segs[i], a.bools["png"], crf, &done, &stop); err != nil {
				errs[i] = err
				stop.Store(true)
			}
		}()
	}
	wg.Wait()
	stopProgress()
	if err := errors.Join(errs...); err != nil {
		return err
	}
	if err := mux(p.dir, &m, segs, from, to, out, tmp); err != nil {
		return err
	}
	fmt.Printf("wrote %s: %d frames (%.3f s) at %dx%d, %s fps, in %.1f s with %d workers\n",
		out, n, clock.at(n), m.Width, m.Height, clock.FPS, time.Since(start).Seconds(), workers)
	return nil
}

// mux joins the segments and lays the composition's sound track under them.
func mux(dir string, m *meta, segs []string, from, to int, out, tmp string) error {
	var list strings.Builder
	for _, s := range segs {
		abs, err := filepath.Abs(s)
		if err != nil {
			return err
		}
		fmt.Fprintf(&list, "file '%s'\n", strings.ReplaceAll(filepath.ToSlash(abs), "'", `'\''`))
	}
	listPath := filepath.Join(tmp, "segments.txt")
	if err := os.WriteFile(listPath, []byte(list.String()), 0o644); err != nil {
		return err
	}
	clock := m.clock
	args := []string{"-f", "concat", "-safe", "0", "-i", listPath}
	maps := []string{"-map", "0:v"}
	if src := trackPath(dir, m); src != "" {
		offset, delay := m.Audio.Offset, m.Audio.Start-clock.at(from)
		if delay < 0 {
			offset -= delay
			delay = 0
		}
		args = append(args, "-ss", ftoa(offset), "-itsoffset", ftoa(delay), "-i", src)
		maps = append(maps, "-map", "1:a:0?", "-c:a", "aac", "-b:a", "192k")
	}
	args = append(args, maps...)
	args = append(args, "-c:v", "copy", "-t", ftoa(clock.at(to-from)), "-movflags", "+faststart", out)
	return ffmpeg(args...)
}

// trackPath is the file behind <audio data-track>, when it is one in the project.
func trackPath(dir string, m *meta) string {
	if m.Audio == nil || m.Audio.Src == "" {
		return ""
	}
	u, err := url.Parse(m.Audio.Src)
	if err != nil || u.Scheme != "" || u.Host != "" {
		fmt.Fprintf(os.Stderr, "rav: the sound track %s is not a file in the project; rendering without sound\n", m.Audio.Src)
		return ""
	}
	p := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(u.Path, "/")))
	if _, err := os.Stat(p); err != nil {
		fmt.Fprintf(os.Stderr, "rav: the sound track %s is missing; rendering without sound\n", p)
		return ""
	}
	return p
}

func progress(done *atomic.Int64, total int, start time.Time) func() {
	quit, fin := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(fin)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-quit:
				return
			case <-tick.C:
				d := done.Load()
				fmt.Fprintf(os.Stderr, "\rrendering %d/%d frames, %.1f fps   ", d, total, float64(d)/time.Since(start).Seconds())
			}
		}
	}()
	return func() {
		close(quit)
		<-fin
		fmt.Fprintf(os.Stderr, "\r%60s\r", "")
	}
}

func cmdStill(argv []string) error {
	a, err := parseArgs(argv, "at: o:")
	if err != nil {
		return err
	}
	if err := a.need(1, "still <project> --at t,... [-o dir]"); err != nil {
		return err
	}
	if a.vals["at"] == "" {
		return errors.New("--at t,... is required")
	}
	dir := a.vals["o"]
	if dir == "" {
		dir = "stills"
	}
	paths, ks, m, err := renderStills(a.pos[0], func(clock *Video) ([]int, error) { return clock.framesOf(a.vals["at"]) }, dir)
	if err != nil {
		return err
	}
	for i, p := range paths {
		fmt.Printf("%s  frame %d  %.6f s\n", p, ks[i], m.clock.stamp(ks[i]))
	}
	return nil
}

// renderStills writes the frames pick chooses (on the composition's clock) as PNGs in out.
func renderStills(dir string, pick func(*Video) ([]int, error), out string) ([]string, []int, *meta, error) {
	p, err := openProject(dir)
	if err != nil {
		return nil, nil, nil, err
	}
	defer p.close()
	b, err := launchBrowser()
	if err != nil {
		return nil, nil, nil, err
	}
	defer b.close()
	t, err := openTab(b, p)
	if err != nil {
		return nil, nil, nil, err
	}
	ks, err := pick(t.m.clock)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, nil, nil, err
	}
	var paths []string
	for _, k := range ks {
		if k >= t.m.Frames {
			return nil, nil, nil, fmt.Errorf("frame %d is past the composition's last frame (%d)", k, t.m.Frames-1)
		}
		img, err := t.frame(k, "png")
		if err != nil {
			return nil, nil, nil, err
		}
		path := filepath.Join(out, fmt.Sprintf("f%06d.png", k))
		if err := os.WriteFile(path, img, 0o644); err != nil {
			return nil, nil, nil, err
		}
		paths = append(paths, path)
	}
	return paths, ks, &t.m, nil
}

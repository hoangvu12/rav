package main

import (
	_ "embed"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

//go:embed runtime.js
var runtimeJS string

//go:embed preview.js
var previewJS string

// project is a composition folder served over local HTTP, so videos get range requests and scripts load as on
// the web.
type project struct {
	dir string
	srv *http.Server
	url string
}

func openProject(dir string) (*project, error) {
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		return nil, fmt.Errorf("%s has no index.html", dir)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: projectFiles(dir)}
	go srv.Serve(ln)
	return &project{dir: dir, srv: srv, url: "http://" + ln.Addr().String() + "/index.html"}, nil
}

func (p *project) close() { p.srv.Close() }

// projectFiles serves a project's files uncached, and an empty favicon when it has none.
func projectFiles(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/favicon.ico" {
			if _, err := os.Stat(filepath.Join(dir, "favicon.ico")); err != nil {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		files.ServeHTTP(w, r)
	})
}

const indexTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<style>
  html, body { margin: 0; background: #000; }
  #rav { position: relative; overflow: hidden; width: %dpx; height: %dpx; }
</style>
</head>
<body>
<div id="rav" data-width="%d" data-height="%d" data-fps="%s" data-frames="%d">
</div>%s
</body>
</html>
`

func cmdInit(argv []string) error {
	a, err := parseArgs(argv, "video:")
	if err != nil {
		return err
	}
	if err := a.need(1, "init <project> --video <video>"); err != nil {
		return err
	}
	src := a.vals["video"]
	if src == "" {
		return errors.New("--video <the original> is required")
	}
	v, err := probe(src)
	if err != nil {
		return err
	}
	dir := a.pos[0]
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s already has files in it", dir)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		return err
	}
	name := filepath.Base(src)
	if err := copyFile(src, filepath.Join(dir, name)); err != nil {
		return err
	}
	track := ""
	if v.HasAudio {
		track = fmt.Sprintf("\n<audio data-track src=\"%s\"></audio>", html.EscapeString(name))
	}
	page := fmt.Sprintf(indexTemplate, v.Width, v.Height, v.Width, v.Height, v.FPS, v.Frames, track)
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(page), 0o644); err != nil {
		return err
	}
	sound := "no sound"
	if v.HasAudio {
		sound = "the original's sound as the track"
	}
	fmt.Printf("made %s: a %dx%d stage at %s fps, %d frames (%.6f s), %s\n", dir, v.Width, v.Height, v.FPS, v.Frames, v.Duration, sound)
	if v.VFR {
		fmt.Printf("note: %s has a variable frame rate; rav reads it at %s fps\n", src, v.FPS)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func cmdServe(argv []string) error {
	a, err := parseArgs(argv, "port:")
	if err != nil {
		return err
	}
	if err := a.need(1, "serve <project> [--port 4747]"); err != nil {
		return err
	}
	dir := a.pos[0]
	index := filepath.Join(dir, "index.html")
	if _, err := os.Stat(index); err != nil {
		return fmt.Errorf("%s has no index.html", dir)
	}
	port := a.vals["port"]
	if port == "" {
		port = "4747"
	}
	js := func(src string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			io.WriteString(w, src)
		}
	}
	files := projectFiles(dir)
	mux := http.NewServeMux()
	mux.HandleFunc("/__rav/runtime.js", js(runtimeJS))
	mux.HandleFunc("/__rav/preview.js", js(previewJS))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			files.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		page, err := os.ReadFile(index)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, injectPreview(string(page)))
	})
	addr := "127.0.0.1:" + port
	fmt.Printf("previewing %s at http://%s/  (space plays, arrows step a frame; ctrl+c stops)\n", dir, addr)
	return http.ListenAndServe(addr, mux)
}

// injectPreview puts rav's clock ahead of the page's scripts and the scrubber after them.
func injectPreview(page string) string {
	clock := `<script src="/__rav/runtime.js"></script>`
	if i := strings.Index(strings.ToLower(page), "<head>"); i >= 0 {
		page = page[:i+6] + clock + page[i+6:]
	} else {
		page = clock + page
	}
	ui := `<script src="/__rav/preview.js"></script>`
	if i := strings.LastIndex(strings.ToLower(page), "</body>"); i >= 0 {
		return page[:i] + ui + page[i:]
	}
	return page + ui
}

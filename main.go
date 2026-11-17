// rav rebuilds a video as an editable HTML composition and checks the rebuild against the original frame by
// frame. It needs only ffmpeg and a Chromium browser already on the machine: no account, no API, no telemetry.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const usage = `rav rebuilds a video as an editable HTML composition and checks it frame by frame.

Measure the original:
  rav probe  <video> [--json]                       size, frame rate, frames, sound
  rav cuts   <video> [--json]                       every cut, jump cut and punch-in
  rav frames <video> --range a,b | --at t,... [--roi x,y,w,h] [-o dir]
                                                     frames as PNGs, plus a contact sheet for a range
  rav color  <video|image> [--at t] x,y[,r] ...     exact colours, averaged over a (2r+1) box
  rav changes <video> [--roi x,y,w,h] [--from t] [--to t] [--min 3]
                                                     every frame a box changes: captions, pop-ons, fades
  rav ink    <video|image> [--at t] [--roi x,y,w,h] the exact box of text or a shape in a box
  rav cut    <video> -o assets/x.mp4|x.png [--from t] [--to t] [--at t] [--roi x,y,w,h]
              [--remove "x,y,w,h;..."]               a sub-asset: a clip or still on its own box,
                                                     burned-in marks painted out, colour-tagged

Build and check:
  rav init   <project> --video <video>              a stage at the original's size, rate and length
  rav serve  <project> [--port 4747]                preview in a browser, with a scrubber
  rav still  <project> --at t,... [-o dir]          render single frames as PNGs
  rav render <project> -o out.mp4 [--workers n] [--from t] [--to t] [--crf 16] [--png]
  rav diff   <video> <out.mp4> [-o dir] [--sbs compare.mp4] [--worst 6] [--scale 480] [--json]
  rav diff   <video> <project|out.mp4> --at t,... [-o dir] [--json]

Times are seconds (2.5) or frames (75f). Needs ffmpeg on PATH and Chrome, Edge, Brave or
Chromium (set RAV_BROWSER to pick one).
`

var commands = map[string]func([]string) error{
	"probe":   cmdProbe,
	"cuts":    cmdCuts,
	"frames":  cmdFrames,
	"color":   cmdColor,
	"changes": cmdChanges,
	"ink":     cmdInk,
	"cut":     cmdCut,
	"init":    cmdInit,
	"serve":   cmdServe,
	"still":   cmdStill,
	"render":  cmdRender,
	"diff":    cmdDiff,
}

func main() {
	if len(os.Args) < 2 || os.Args[1] == "help" || os.Args[1] == "-h" || os.Args[1] == "--help" {
		fmt.Print(usage)
		return
	}
	run, ok := commands[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "rav: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err := run(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "rav:", err)
		os.Exit(1)
	}
}

// args is a command line split into positionals and flags.
type args struct {
	pos   []string
	vals  map[string]string
	bools map[string]bool
}

// parseArgs reads in against spec, the flags a command takes: "o: workers: json" means -o and --workers take a
// value and --json is a switch. Flags and positionals may come in any order.
func parseArgs(in []string, spec string) (*args, error) {
	takes := map[string]bool{}
	for _, f := range strings.Fields(spec) {
		name, valued := strings.CutSuffix(f, ":")
		takes[name] = valued
	}
	a := &args{vals: map[string]string{}, bools: map[string]bool{}}
	for i := 0; i < len(in); i++ {
		s := in[i]
		if len(s) < 2 || s[0] != '-' {
			a.pos = append(a.pos, s)
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(s, "-"), "=")
		valued, known := takes[name]
		switch {
		case !known:
			return nil, fmt.Errorf("unknown flag %s", s)
		case valued && hasVal:
			a.vals[name] = val
		case valued:
			if i+1 >= len(in) {
				return nil, fmt.Errorf("%s needs a value", s)
			}
			i++
			a.vals[name] = in[i]
		case hasVal:
			return nil, fmt.Errorf("%s takes no value", s)
		default:
			a.bools[name] = true
		}
	}
	return a, nil
}

func (a *args) need(n int, use string) error {
	if len(a.pos) != n {
		return fmt.Errorf("usage: rav %s", use)
	}
	return nil
}

func (a *args) int(name string, def int) (int, error) {
	s, ok := a.vals[name]
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("--%s wants a positive whole number", name)
	}
	return n, nil
}

// ints reads "x,y,w,h" style lists.
func ints(s string) ([]int, error) {
	var out []int
	for _, p := range strings.Split(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return nil, fmt.Errorf("bad number in %q", s)
		}
		out = append(out, n)
	}
	return out, nil
}

func printJSON(v any) error {
	e := json.NewEncoder(os.Stdout)
	e.SetIndent("", "  ")
	return e.Encode(v)
}

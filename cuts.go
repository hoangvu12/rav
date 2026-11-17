package main

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"slices"
)

// Cuts come from frame-to-frame change on tiny grey frames. A cut changes far more than the motion around it.
// ffmpeg's scene score misses jump cuts and punch-ins inside one take, which change less but stand out as much.
const (
	sigW, sigH   = 64, 36
	hardLevel    = 12.0 // a cut changes at least this many grey levels on average
	hardRatio    = 4.0  // and this many times the motion around it
	softLevel    = 6.0  // a jump cut or punch-in changes less, so it has to stand out more
	softRatio    = 8.0
	maxCutFrames = 3  // a dissolve or whip spans a few frames; a longer change is motion
	around       = 12 // frames either side that say how much the picture moves
	flashFrames  = 4  // a flash comes back to the shot before it within this many frames
	returned     = 0.4
)

type Cut struct {
	Frame  int     `json:"frame"`
	Time   float64 `json:"time"`
	Frames int     `json:"frames"` // more than 1: a dissolve or whip
	Change float64 `json:"change"`
	Soft   bool    `json:"soft,omitempty"`
	Flash  bool    `json:"flash,omitempty"` // the picture comes back: not a new shot
}

func cmdCuts(argv []string) error {
	a, err := parseArgs(argv, "json")
	if err != nil {
		return err
	}
	if err := a.need(1, "cuts <video> [--json]"); err != nil {
		return err
	}
	v, err := probe(a.pos[0])
	if err != nil {
		return err
	}
	cuts, err := videoCuts(v.Path, v)
	if err != nil {
		return err
	}
	if a.bools["json"] {
		return printJSON(cuts)
	}
	fmt.Printf("%d cuts in %s (%d frames at %s fps). A shot starts at each time.\n", len(cuts), v.Path, v.Frames, v.FPS)
	for _, c := range cuts {
		note := ""
		switch {
		case c.Flash:
			note = "flash: the picture comes back, not a new shot"
		case c.Frames > 1:
			note = fmt.Sprintf("over %d frames: a dissolve or whip", c.Frames)
		case c.Soft:
			note = "soft: a jump cut or punch-in"
		}
		fmt.Printf("  frame %6d  %11.6f s  change %5.1f  %s\n", c.Frame, c.Time, c.Change, note)
	}
	return nil
}

// videoCuts finds the cuts in path, read on clock's frame grid.
func videoCuts(path string, clock *Video) ([]Cut, error) {
	f, err := readFrames(sigW*sigH, greyArgs(path, clock.FPS, sigW, sigH)...)
	if err != nil {
		return nil, err
	}
	defer f.close()
	var frames [][]byte
	for {
		px, err := f.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		frames = append(frames, bytes.Clone(px))
	}
	cuts := findCuts(frames)
	for i := range cuts {
		cuts[i].Time = clock.stamp(cuts[i].Frame)
	}
	return cuts, nil
}

func findCuts(f [][]byte) []Cut {
	n := len(f)
	d := make([]float64, n)
	for i := 1; i < n; i++ {
		d[i] = meanAbsDiff(f[i-1], f[i])
	}
	var cuts []Cut
	for i := 1; i < n; {
		if d[i] < softLevel {
			i++
			continue
		}
		e := i
		for e+1 < n && d[e+1] >= softLevel {
			e++
		}
		if e-i+1 <= maxCutFrames {
			if c, ok := judge(f, d, i, e); ok {
				cuts = append(cuts, c)
			}
		} else {
			// Sustained motion: only a frame far above both its neighbours is a cut inside it.
			for k := i; k <= e; k++ {
				next := 0.0
				if k+1 < n {
					next = d[k+1]
				}
				if d[k] >= hardLevel && d[k] >= 3*math.Max(d[k-1], next) {
					cuts = append(cuts, Cut{Frame: k, Frames: 1, Change: math.Round(d[k]*10) / 10})
				}
			}
		}
		i = e + 1
	}
	return cuts
}

// judge decides whether the change over frames s..e is a cut.
func judge(f [][]byte, d []float64, s, e int) (Cut, bool) {
	peak := 0.0
	for k := s; k <= e; k++ {
		peak = math.Max(peak, d[k])
	}
	var nb []float64
	for k := max(1, s-around); k < s; k++ {
		nb = append(nb, d[k])
	}
	for k := e + 1; k < min(len(d), e+1+around); k++ {
		nb = append(nb, d[k])
	}
	ratio := peak / baseline(nb)
	c := Cut{Frame: s, Frames: e - s + 1, Change: math.Round(peak*10) / 10}
	switch {
	case peak >= hardLevel && ratio >= hardRatio:
	case peak >= softLevel && ratio >= softRatio:
		c.Soft = true
	default:
		return c, false
	}
	for k := 1; k <= flashFrames && e+k < len(f); k++ {
		if meanAbsDiff(f[s-1], f[e+k]) < returned*peak {
			c.Flash = true
			break
		}
	}
	return c, true
}

// baseline is how much the picture moves around a candidate: the median change, or for animation held on twos
// (moving and holding in turn, so the median reads as no motion) a share of the moving frames' mean.
func baseline(nb []float64) float64 {
	if len(nb) == 0 {
		return 0.5
	}
	s := slices.Sorted(slices.Values(nb))
	b := s[len(s)/2]
	sum, cnt := 0.0, 0
	for _, x := range nb {
		if x >= 0.5 {
			sum += x
			cnt++
		}
	}
	if cnt > 0 {
		b = math.Max(b, 0.6*sum/float64(cnt))
	}
	return math.Max(b, 0.5)
}

func meanAbsDiff(a, b []byte) float64 {
	s := 0
	for i := range a {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		s += d
	}
	return float64(s) / float64(len(a))
}

package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// cmdCut cuts a sub-asset out of the original: a still or a clip, on the frame grid, cropped to its own box, with
// burned-in marks painted out. Clips are tagged BT.709 so Chrome shows them in the colours ffmpeg reads; an
// untagged clip in a page can be off by a visible ΔE 5 or more.
func cmdCut(argv []string) error {
	a, err := parseArgs(argv, "o: from: to: at: roi: remove:")
	if err != nil {
		return err
	}
	if err := a.need(1, `cut <video> -o <assets/x.mp4|assets/x.png> [--from t] [--to t] [--at t] [--roi x,y,w,h] [--remove "x,y,w,h;..."]`); err != nil {
		return err
	}
	out := a.vals["o"]
	if out == "" {
		return errors.New("-o <file> is required")
	}
	v, err := probe(a.pos[0])
	if err != nil {
		return err
	}
	var filters []string
	if v.VFR {
		filters = append(filters, "fps="+v.FPS)
	}
	if rm := a.vals["remove"]; rm != "" {
		for _, box := range strings.Split(rm, ";") {
			n, err := ints(box)
			if err != nil || len(n) != 4 {
				return fmt.Errorf("--remove wants x,y,w,h boxes separated by ';', got %q", box)
			}
			// delogo needs a pixel of picture around the box to paint from.
			x, y := max(1, n[0]), max(1, n[1])
			w, h := min(n[2], v.Width-1-x), min(n[3], v.Height-1-y)
			if w < 1 || h < 1 {
				return fmt.Errorf("--remove box %q is outside the %dx%d frame", box, v.Width, v.Height)
			}
			filters = append(filters, fmt.Sprintf("delogo=x=%d:y=%d:w=%d:h=%d", x, y, w, h))
		}
	}
	box := [4]int{0, 0, v.Width, v.Height}
	if roi := a.vals["roi"]; roi != "" {
		n, err := ints(roi)
		if err != nil || len(n) != 4 {
			return errors.New("--roi wants x,y,w,h in the video's pixels")
		}
		box = [4]int{n[0], n[1], n[2], n[3]}
	}
	still := false
	switch strings.ToLower(filepath.Ext(out)) {
	case ".png", ".jpg", ".jpeg", ".webp":
		still = true
	}
	if !still {
		box[2], box[3] = box[2]/2*2, box[3]/2*2 // 4:2:0 video needs even sides
	}
	if box != [4]int{0, 0, v.Width, v.Height} {
		filters = append(filters, fmt.Sprintf("crop=%d:%d:%d:%d", box[2], box[3], box[0], box[1]))
	}
	place := fmt.Sprintf("left:%dpx;top:%dpx;width:%dpx;height:%dpx", box[0], box[1], box[2], box[3])
	src := filepath.ToSlash(out)

	if still {
		at := a.vals["at"]
		if at == "" {
			at = a.vals["from"]
		}
		if at == "" {
			return errors.New("a still needs --at t")
		}
		k, err := v.frameOf(at)
		if err != nil {
			return err
		}
		if err := extractFrame(v, k, strings.Join(filters, ","), out); err != nil {
			return err
		}
		fmt.Printf("wrote %s: frame %d, %dx%d\n", out, k, box[2], box[3])
		fmt.Printf(`<img src="%s" style="position:absolute;%s">`+"\n", src, place)
		return nil
	}

	from, to := 0, v.Frames
	if s := a.vals["from"]; s != "" {
		if from, err = v.frameOf(s); err != nil {
			return err
		}
	}
	if s := a.vals["to"]; s != "" {
		if to, err = v.frameOf(s); err != nil {
			return err
		}
	}
	to = min(to, v.Frames)
	if from >= to {
		return fmt.Errorf("nothing between frames %d and %d (--to is the first frame left out)", from, to)
	}
	filters = append(filters, "scale=out_color_matrix=bt709:out_range=tv,format=yuv420p")
	if err := ffmpeg("-ss", ftoa(v.seekTo(from)), "-i", v.Path, "-frames:v", strconv.Itoa(to-from), "-an",
		"-vf", afterSeek(filters...), "-c:v", "libx264", "-preset", "medium", "-crf", "12", "-g", "15",
		"-colorspace", "bt709", "-color_primaries", "bt709", "-color_trc", "bt709", "-movflags", "+faststart", out); err != nil {
		return err
	}
	fmt.Printf("wrote %s: frames %d to %d (%d frames), %dx%d\n", out, from, to-1, to-from, box[2], box[3])
	fmt.Printf(`<video src="%s" data-start="%.6f" data-duration="%.6f" muted playsinline preload="auto" style="position:absolute;%s"></video>`+"\n",
		src, v.stamp(from), v.at(to)-v.at(from), place)
	return nil
}

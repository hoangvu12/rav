package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func cmdProbe(argv []string) error {
	a, err := parseArgs(argv, "json")
	if err != nil {
		return err
	}
	if err := a.need(1, "probe <video> [--json]"); err != nil {
		return err
	}
	v, err := probe(a.pos[0])
	if err != nil {
		return err
	}
	if a.bools["json"] {
		return printJSON(v)
	}
	sound := "no sound"
	if v.HasAudio {
		sound = "sound"
	}
	fmt.Printf("%s: %dx%d, %s fps, %d frames, %.6f s, %s\n", v.Path, v.Width, v.Height, v.FPS, v.Frames, v.Duration, sound)
	if v.VFR {
		fmt.Printf("variable frame rate: rav reads it at %s fps\n", v.FPS)
	}
	return nil
}

func cmdFrames(argv []string) error {
	a, err := parseArgs(argv, "range: every: at: roi: o:")
	if err != nil {
		return err
	}
	if err := a.need(1, "frames <video> --range a,b [--every n] | --at t,... [--roi x,y,w,h] [-o dir]"); err != nil {
		return err
	}
	v, err := probe(a.pos[0])
	if err != nil {
		return err
	}
	dir := a.vals["o"]
	if dir == "" {
		dir = "frames"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	crop, w := "", v.Width
	if roi := a.vals["roi"]; roi != "" {
		n, err := ints(roi)
		if err != nil || len(n) != 4 {
			return errors.New("--roi wants x,y,w,h in the video's pixels")
		}
		crop, w = fmt.Sprintf("crop=%d:%d:%d:%d", n[2], n[3], n[0], n[1]), n[2]
	}
	switch {
	case a.vals["range"] != "":
		lo, hi, ok := strings.Cut(a.vals["range"], ",")
		if !ok {
			return errors.New("--range wants a,b")
		}
		first, err := v.frameOf(lo)
		if err != nil {
			return err
		}
		last, err := v.frameOf(hi)
		if err != nil {
			return err
		}
		if first >= v.Frames {
			return fmt.Errorf("%s is past the last frame (%d); plain numbers are seconds, frames end in f (75f)", lo, v.Frames-1)
		}
		last = min(last, v.Frames-1)
		if last < first {
			return fmt.Errorf("the range %s is empty", a.vals["range"])
		}
		every, err := a.int("every", 1)
		if err != nil {
			return err
		}
		if every < 1 {
			return errors.New("--every wants a whole number of frames, 1 or more")
		}
		count := (last-first)/every + 1
		last = first + (count-1)*every
		if count > 240 {
			return fmt.Errorf("%d frames is more than anyone can look at; read 240 or fewer at a time (raise --every)", count)
		}
		var filters []string
		if v.VFR {
			filters = append(filters, "fps="+v.FPS)
		}
		if every > 1 {
			filters = append(filters, fmt.Sprintf("select='not(mod(n\\,%d))'", every))
		}
		if crop != "" {
			filters = append(filters, crop)
		}
		// Frames are written in order as s<i>.png, tiled into the sheet, then named after their frame.
		pattern := filepath.Join(dir, "s%06d.png")
		args := []string{"-ss", ftoa(v.seekTo(first)), "-i", v.Path, "-vf", afterSeek(filters...),
			"-fps_mode", "vfr", "-frames:v", strconv.Itoa(count), "-start_number", "0", pattern}
		if err := ffmpeg(args...); err != nil {
			return err
		}
		cols := min(count, 6)
		rows := (count + cols - 1) / cols
		tw := min(w, 2400/cols) / 2 * 2
		sheet := filepath.Join(dir, fmt.Sprintf("sheet_f%06d-f%06d.png", first, last))
		if err := ffmpeg("-start_number", "0", "-i", pattern,
			"-vf", fmt.Sprintf("scale=%d:-2,tile=%dx%d:padding=4:color=white", tw, cols, rows), "-frames:v", "1", sheet); err != nil {
			return err
		}
		for i := 0; i < count; i++ {
			k := first + i*every
			if err := os.Rename(filepath.Join(dir, fmt.Sprintf("s%06d.png", i)), filepath.Join(dir, fmt.Sprintf("f%06d.png", k))); err != nil {
				return err
			}
		}
		step := ""
		if every > 1 {
			step = fmt.Sprintf(" every %d", every)
		}
		fmt.Printf("%d frames, f%d to f%d%s (%.6f to %.6f s), as %s\n", count, first, last, step, v.stamp(first), v.stamp(last), filepath.Join(dir, "f<frame>.png"))
		fmt.Printf("sheet: %s, %d per row, left to right then down; tile i is frame %d + %d*i\n", sheet, cols, first, every)
	case a.vals["at"] != "":
		ks, err := v.framesOf(a.vals["at"])
		if err != nil {
			return err
		}
		for _, k := range ks {
			p := filepath.Join(dir, fmt.Sprintf("f%06d.png", k))
			if err := extractFrame(v, k, crop, p); err != nil {
				return err
			}
			fmt.Printf("%s  frame %d  %.6f s\n", p, k, v.stamp(k))
		}
	default:
		return errors.New("give --range a,b or --at t,...")
	}
	return nil
}

func cmdColor(argv []string) error {
	a, err := parseArgs(argv, "at:")
	if err != nil {
		return err
	}
	if len(a.pos) < 2 {
		return errors.New("usage: rav color <video|image> [--at t] x,y[,r] ...")
	}
	img, err := loadPicture(a.pos[0], a.vals["at"])
	if err != nil {
		return err
	}
	b := img.Bounds()
	for _, pt := range a.pos[1:] {
		n, err := ints(pt)
		if err != nil || len(n) < 2 || len(n) > 3 {
			return fmt.Errorf("bad point %q: use x,y or x,y,r", pt)
		}
		x, y, r := n[0], n[1], 0
		if len(n) == 3 {
			r = n[2]
		}
		var sr, sg, sb, count int
		for yy := y - r; yy <= y+r; yy++ {
			for xx := x - r; xx <= x+r; xx++ {
				if !image.Pt(xx, yy).In(b) {
					continue
				}
				cr, cg, cb, _ := img.At(xx, yy).RGBA()
				sr, sg, sb = sr+int(cr>>8), sg+int(cg>>8), sb+int(cb>>8)
				count++
			}
		}
		if count == 0 {
			return fmt.Errorf("%s is outside the %dx%d frame", pt, b.Dx(), b.Dy())
		}
		R, G, B := (sr+count/2)/count, (sg+count/2)/count, (sb+count/2)/count
		fmt.Printf("%-14s #%02x%02x%02x  rgb(%d, %d, %d)\n", pt, R, G, B, R, G, B)
	}
	return nil
}

// loadPicture reads an image file, or frame at (default the first) of a video.
func loadPicture(path, at string) (image.Image, error) {
	if ext := strings.ToLower(filepath.Ext(path)); ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
		v, err := probe(path)
		if err != nil {
			return nil, err
		}
		k := 0
		if at != "" {
			if k, err = v.frameOf(at); err != nil {
				return nil, err
			}
		}
		tmp, err := os.MkdirTemp("", "rav-frame-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmp)
		path = filepath.Join(tmp, "frame.png")
		if err := extractFrame(v, k, "", path); err != nil {
			return nil, err
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

func rgb8(c color.Color) (int, int, int) {
	r, g, b, _ := c.RGBA()
	return int(r >> 8), int(g >> 8), int(b >> 8)
}

// cmdInk finds the ink of text or a shape in a box: the pixels that differ from the box's backdrop, read from its
// border. Its bounding box sizes text exactly; compared on the original and on a still of yours, it says how far
// font size, spacing and position are off.
func cmdInk(argv []string) error {
	a, err := parseArgs(argv, "at: roi: json")
	if err != nil {
		return err
	}
	if err := a.need(1, "ink <video|image> [--at t] [--roi x,y,w,h] [--json]"); err != nil {
		return err
	}
	img, err := loadPicture(a.pos[0], a.vals["at"])
	if err != nil {
		return err
	}
	b := img.Bounds()
	box := image.Rect(b.Min.X, b.Min.Y, b.Max.X, b.Max.Y)
	if roi := a.vals["roi"]; roi != "" {
		n, err := ints(roi)
		if err != nil || len(n) != 4 {
			return errors.New("--roi wants x,y,w,h in the picture's pixels")
		}
		box = image.Rect(n[0], n[1], n[0]+n[2], n[1]+n[3]).Intersect(b)
	}
	if box.Dx() < 3 || box.Dy() < 3 {
		return errors.New("the box is too small")
	}
	// The backdrop is the median colour around the box's edge.
	var rs, gs, bs []int
	for x := box.Min.X; x < box.Max.X; x++ {
		for _, y := range []int{box.Min.Y, box.Max.Y - 1} {
			r, g, bl := rgb8(img.At(x, y))
			rs, gs, bs = append(rs, r), append(gs, g), append(bs, bl)
		}
	}
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for _, x := range []int{box.Min.X, box.Max.X - 1} {
			r, g, bl := rgb8(img.At(x, y))
			rs, gs, bs = append(rs, r), append(gs, g), append(bs, bl)
		}
	}
	med := func(v []int) int { slices.Sort(v); return v[len(v)/2] }
	br, bg, bb := med(rs), med(gs), med(bs)
	const inkLevel = 48 // the largest channel difference from the backdrop that counts as ink
	ink := image.Rectangle{}
	count, sr, sg, sb := 0, 0, 0, 0
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			r, g, bl := rgb8(img.At(x, y))
			if max(abs(r-br), abs(g-bg), abs(bl-bb)) < inkLevel {
				continue
			}
			p := image.Rect(x, y, x+1, y+1)
			if count == 0 {
				ink = p
			} else {
				ink = ink.Union(p)
			}
			count++
			sr, sg, sb = sr+r, sg+g, sb+bl
		}
	}
	if count == 0 {
		return fmt.Errorf("no ink in %v: nothing differs from the backdrop #%02x%02x%02x", box, br, bg, bb)
	}
	res := struct {
		Box      [4]int  `json:"box"`
		Fill     float64 `json:"fill"`
		Ink      string  `json:"ink"`
		Backdrop string  `json:"backdrop"`
	}{
		Box:      [4]int{ink.Min.X, ink.Min.Y, ink.Dx(), ink.Dy()},
		Fill:     math.Round(float64(count)/float64(ink.Dx()*ink.Dy())*1000) / 1000,
		Ink:      fmt.Sprintf("#%02x%02x%02x", sr/count, sg/count, sb/count),
		Backdrop: fmt.Sprintf("#%02x%02x%02x", br, bg, bb),
	}
	if a.bools["json"] {
		return printJSON(res)
	}
	fmt.Printf("ink box %d,%d,%d,%d (x,y,w,h): %d px wide, %d px tall\n", res.Box[0], res.Box[1], res.Box[2], res.Box[3], res.Box[2], res.Box[3])
	fmt.Printf("ink covers %.1f%% of its box (heavier weight covers more), mean ink %s on backdrop %s\n", res.Fill*100, res.Ink, res.Backdrop)
	return nil
}

// cmdChanges lists the frames where a box changes: captions switching, a counter ticking, a layer popping on.
// Consecutive changing frames are one change: a single frame is a hard switch, several are a fade or a move.
func cmdChanges(argv []string) error {
	a, err := parseArgs(argv, "roi: from: to: min: json")
	if err != nil {
		return err
	}
	if err := a.need(1, "changes <video> [--roi x,y,w,h] [--from t] [--to t] [--min 3] [--json]"); err != nil {
		return err
	}
	v, err := probe(a.pos[0])
	if err != nil {
		return err
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
		return fmt.Errorf("nothing between frames %d and %d", from, to)
	}
	level := 3.0
	if s := a.vals["min"]; s != "" {
		if level, err = strconv.ParseFloat(s, 64); err != nil || level <= 0 {
			return errors.New("--min wants grey levels, e.g. 3")
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
	w, h := fitSize(box[2], box[3], 160)
	filters := []string{fmt.Sprintf("crop=%d:%d:%d:%d,scale=%d:%d:flags=area,format=gray", box[2], box[3], box[0], box[1], w, h)}
	if v.VFR {
		filters = append([]string{"fps=" + v.FPS}, filters...)
	}
	vf := afterSeek(filters...)
	f, err := readFrames(w*h, "-ss", ftoa(v.seekTo(from)), "-i", v.Path, "-an", "-frames:v", strconv.Itoa(to-from), "-vf", vf, "-f", "rawvideo", "pipe:1")
	if err != nil {
		return err
	}
	defer f.close()
	type change struct {
		Frame  int     `json:"frame"`
		Time   float64 `json:"time"`
		Frames int     `json:"frames"`
		Peak   float64 `json:"peak"`
	}
	var changes []change
	var prev []byte
	for k := from; ; k++ {
		px, err := f.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if prev != nil {
			if d := meanAbsDiff(prev, px); d >= level {
				d = math.Round(d*10) / 10
				if n := len(changes); n > 0 && changes[n-1].Frame+changes[n-1].Frames == k {
					changes[n-1].Frames++
					changes[n-1].Peak = math.Max(changes[n-1].Peak, d)
				} else {
					changes = append(changes, change{Frame: k, Time: v.stamp(k), Frames: 1, Peak: d})
				}
			}
		}
		prev = bytes.Clone(px)
	}
	if a.bools["json"] {
		return printJSON(changes)
	}
	fmt.Printf("%d changes in %v between frames %d and %d (a change is %.1f+ grey levels; --min sets it)\n", len(changes), box, from, to-1, level)
	for _, c := range changes {
		how := "hard switch"
		if c.Frames > 1 {
			how = fmt.Sprintf("over %d frames: a fade or a move, ends on frame %d", c.Frames, c.Frame+c.Frames-1)
		}
		fmt.Printf("  frame %6d  %11.6f s  peak %5.1f  %s\n", c.Frame, c.Time, c.Peak, how)
	}
	return nil
}

package main

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// A rebuild against its original, frame by frame on the original's clock: SSIM and colour difference for the
// whole frame and each cell of a 4x4 grid, cuts on both sides, shots, missing content, and the worst moments as
// pictures.

type moment struct {
	Frame     int      `json:"frame"`
	Time      float64  `json:"time"`
	SSIM      float64  `json:"ssim"`
	DE        float64  `json:"color_de"`
	Cell      string   `json:"cell"`
	Box       [4]int   `json:"box"`
	CellSSIM  float64  `json:"cell_ssim"`
	ColorCell string   `json:"color_cell"`
	ColorDE   float64  `json:"color_cell_de"`
	Missing   []string `json:"missing,omitempty"`
	Image     string   `json:"image"`
}

type segment struct {
	From     float64 `json:"from"`
	To       float64 `json:"to"`
	Mean     float64 `json:"mean"`
	Min      float64 `json:"min"`
	DE       float64 `json:"color_de"`
	Cell     string  `json:"worst_cell"`
	CellMean float64 `json:"worst_cell_mean"`
}

type gap struct {
	Cell string  `json:"cell"`
	Box  [4]int  `json:"box"`
	From float64 `json:"from"`
	To   float64 `json:"to"`
}

type cutOff struct {
	Time   float64 `json:"time"`
	Frames int     `json:"frames"` // positive: yours is late
}

type cutReport struct {
	Original int       `json:"original"`
	Yours    int       `json:"yours"`
	Off      []cutOff  `json:"off"`
	Missing  []float64 `json:"missing"`
	Extra    []float64 `json:"extra"`
}

type report struct {
	Original string     `json:"original"`
	Yours    string     `json:"yours"`
	Frames   [2]int     `json:"frames"`
	Size     [2]string  `json:"size"`
	Compared int        `json:"compared"`
	Mean     float64    `json:"ssim_mean"`
	P05      float64    `json:"ssim_p05"`
	Min      float64    `json:"ssim_min"`
	CellP05  float64    `json:"cell_p05"`
	DEMean   float64    `json:"color_de_mean"`
	DEP95    float64    `json:"color_de_p95"`
	Cuts     *cutReport `json:"cuts,omitempty"`
	Missing  []gap      `json:"missing_content,omitempty"`
	Segments []segment  `json:"shots,omitempty"`
	Moments  []moment   `json:"moments"`
	SBS      string     `json:"side_by_side,omitempty"`
}

func cmdDiff(argv []string) error {
	a, err := parseArgs(argv, "at: o: sbs: scale: worst: json")
	if err != nil {
		return err
	}
	if err := a.need(2, "diff <video> <out.mp4|project> [--at t,...] [-o dir] [--sbs compare.mp4] [--json]"); err != nil {
		return err
	}
	scale, err := a.int("scale", 480)
	if err != nil {
		return err
	}
	worst, err := a.int("worst", 6)
	if err != nil {
		return err
	}
	orig, err := probe(a.pos[0])
	if err != nil {
		return err
	}
	other := a.pos[1]
	st, err := os.Stat(other)
	if err != nil {
		return err
	}
	dir := a.vals["o"]
	if dir == "" {
		dir = "rav-diff"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var rep *report
	switch {
	case a.vals["at"] != "":
		ks, err := orig.framesOf(a.vals["at"])
		if err != nil {
			return err
		}
		if rep, err = diffAt(orig, other, st.IsDir(), ks, dir, scale); err != nil {
			return err
		}
	case st.IsDir():
		return errors.New("diff a project at chosen moments with --at t,..., or render it and diff the video")
	default:
		if rep, err = diffVideos(orig, other, dir, scale, worst); err != nil {
			return err
		}
	}
	if sbs := a.vals["sbs"]; sbs != "" {
		if st.IsDir() {
			return errors.New("--sbs needs a rendered video")
		}
		if err := sideBySide(orig, other, sbs); err != nil {
			return err
		}
		rep.SBS = sbs
	}
	if a.bools["json"] {
		return printJSON(rep)
	}
	printReport(rep)
	return nil
}

func diffVideos(orig *Video, path, dir string, scale, worst int) (*report, error) {
	yours, err := probe(path)
	if err != nil {
		return nil, err
	}
	w, h := fitSize(orig.Width, orig.Height, scale)
	ra, err := readFrames(3*w*h, rgbArgs(orig.Path, orig.FPS, w, h)...)
	if err != nil {
		return nil, err
	}
	defer ra.close()
	// Yours is read on the original's clock and at its size, whatever it was rendered at.
	rb, err := readFrames(3*w*h, rgbArgs(path, orig.FPS, w, h)...)
	if err != nil {
		return nil, err
	}
	defer rb.close()
	s := newSSIM(w, h)
	var scores []score
	for {
		pa, err := ra.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		pb, err := rb.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		scores = append(scores, s.score(pa, pb))
	}
	if len(scores) == 0 {
		return nil, errors.New("no frames to compare")
	}
	ca, err := videoCuts(orig.Path, orig)
	if err != nil {
		return nil, err
	}
	cb, err := videoCuts(path, orig)
	if err != nil {
		return nil, err
	}
	rep := &report{Original: orig.Path, Yours: path, Frames: [2]int{orig.Frames, yours.Frames}, Size: [2]string{dims(orig), dims(yours)}}
	rep.Cuts = compareCuts(ca, cb)
	stats(rep, scores)
	shots(rep, scores, orig, ca)
	gaps(rep, scores, orig)
	for i, k := range worstFrames(scores, worst, max(1, int(math.Round(orig.rate()/2)))) {
		m := momentAt(orig, k, scores[k])
		m.Image = filepath.Join(dir, fmt.Sprintf("worst_%02d_f%06d.png", i+1, k))
		seek := ftoa(orig.seekTo(k))
		if err := triptych(m.Image, orig, []string{"-ss", seek, "-i", orig.Path}, []string{"-ss", seek, "-i", path}, m.Box); err != nil {
			return nil, err
		}
		rep.Moments = append(rep.Moments, m)
	}
	return rep, nil
}

// diffAt compares chosen frames only. A project is rendered at just those frames, which takes seconds.
func diffAt(orig *Video, other string, isProject bool, ks []int, dir string, scale int) (*report, error) {
	rep := &report{Original: orig.Path, Yours: other}
	yoursDir, origDir := filepath.Join(dir, "yours"), filepath.Join(dir, "original")
	for _, d := range []string{yoursDir, origDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	var yours []string
	if isProject {
		paths, _, m, err := renderStills(other, func(*Video) ([]int, error) { return ks, nil }, yoursDir)
		if err != nil {
			return nil, err
		}
		yours = paths
		rep.Frames = [2]int{orig.Frames, m.Frames}
		rep.Size = [2]string{dims(orig), dims(m.clock)}
	} else {
		yv, err := probe(other)
		if err != nil {
			return nil, err
		}
		rep.Frames = [2]int{orig.Frames, yv.Frames}
		rep.Size = [2]string{dims(orig), dims(yv)}
		onOrigClock := &Video{Path: other, num: orig.num, den: orig.den}
		for _, k := range ks {
			p := filepath.Join(yoursDir, fmt.Sprintf("f%06d.png", k))
			if err := extractFrame(onOrigClock, k, "", p); err != nil {
				return nil, err
			}
			yours = append(yours, p)
		}
	}
	w, h := fitSize(orig.Width, orig.Height, scale)
	s := newSSIM(w, h)
	var scores []score
	for i, k := range ks {
		op := filepath.Join(origDir, fmt.Sprintf("f%06d.png", k))
		if err := extractFrame(orig, k, "", op); err != nil {
			return nil, err
		}
		ga, err := rgbImage(op, w, h)
		if err != nil {
			return nil, err
		}
		gb, err := rgbImage(yours[i], w, h)
		if err != nil {
			return nil, err
		}
		sc := s.score(ga, gb)
		scores = append(scores, sc)
		m := momentAt(orig, k, sc)
		m.Image = filepath.Join(dir, fmt.Sprintf("at_f%06d.png", k))
		if err := triptych(m.Image, orig, []string{"-i", op}, []string{"-i", yours[i]}, m.Box); err != nil {
			return nil, err
		}
		rep.Moments = append(rep.Moments, m)
	}
	stats(rep, scores)
	return rep, nil
}

func stats(rep *report, scores []score) {
	n := len(scores)
	rep.Compared = n
	all := make([]float64, n)
	des := make([]float64, n)
	var cells []float64
	sum, deSum := 0.0, 0.0
	for i, s := range scores {
		all[i], des[i] = s.SSIM, s.DE
		sum += s.SSIM
		deSum += s.DE
		cells = append(cells, s.Cells[:]...)
	}
	slices.Sort(all)
	slices.Sort(des)
	slices.Sort(cells)
	rep.Mean = round3(sum / float64(n))
	rep.P05 = round3(all[int(0.05*float64(n-1))])
	rep.Min = round3(all[0])
	rep.CellP05 = round3(cells[int(0.05*float64(len(cells)-1))])
	rep.DEMean = math.Round(deSum/float64(n)*10) / 10
	rep.DEP95 = math.Round(des[int(math.Ceil(0.95*float64(n-1)))]*10) / 10
}

// shots scores each stretch between the original's cuts.
func shots(rep *report, scores []score, clock *Video, cuts []Cut) {
	n := len(scores)
	bounds := []int{0}
	for _, c := range cuts {
		if !c.Flash && c.Frame > 0 && c.Frame < n {
			bounds = append(bounds, c.Frame)
		}
	}
	bounds = append(bounds, n)
	for i := 0; i+1 < len(bounds); i++ {
		s, e := bounds[i], bounds[i+1]
		if e <= s {
			continue
		}
		seg := segment{From: clock.stamp(s), To: clock.stamp(e), Min: 1}
		var cellSum [grid * grid]float64
		total, de := 0.0, 0.0
		for k := s; k < e; k++ {
			total += scores[k].SSIM
			de += scores[k].DE
			seg.Min = math.Min(seg.Min, scores[k].SSIM)
			for c := range cellSum {
				cellSum[c] += scores[k].Cells[c]
			}
		}
		count := float64(e - s)
		worst := 0
		for c := range cellSum {
			if cellSum[c] < cellSum[worst] {
				worst = c
			}
		}
		seg.Mean, seg.Min, seg.DE = round3(total/count), round3(seg.Min), math.Round(de/count*10)/10
		seg.Cell, seg.CellMean = cellName(worst), round3(cellSum[worst]/count)
		rep.Segments = append(rep.Segments, seg)
	}
}

// gaps finds cells where the original shows detail and yours stays flat for more than a few frames.
func gaps(rep *report, scores []score, clock *Video) {
	n := len(scores)
	minRun := max(3, int(clock.rate()/10))
	for c := range grid * grid {
		start := -1
		for k := 0; k <= n; k++ {
			on := k < n && scores[k].Missing&(1<<c) != 0
			if on && start < 0 {
				start = k
			}
			if !on && start >= 0 {
				if k-start >= minRun {
					rep.Missing = append(rep.Missing, gap{Cell: cellName(c), Box: cellBox(c, clock), From: clock.stamp(start), To: clock.stamp(k)})
				}
				start = -1
			}
		}
	}
	slices.SortFunc(rep.Missing, func(a, b gap) int { return cmp.Compare(a.From, b.From) })
}

// compareCuts matches the original's cuts to yours: on the same frame, a frame or two off, or missing.
func compareCuts(ca, cb []Cut) *cutReport {
	newShots := func(cs []Cut) []Cut { return slices.DeleteFunc(slices.Clone(cs), func(c Cut) bool { return c.Flash }) }
	a, b := newShots(ca), newShots(cb)
	r := &cutReport{Original: len(a), Yours: len(b), Off: []cutOff{}, Missing: []float64{}, Extra: []float64{}}
	nearest := func(c Cut, in []Cut) (int, bool) {
		best, found := 0, false
		for _, d := range in {
			if off := d.Frame - c.Frame; off >= -2 && off <= 2 && (!found || abs(off) < abs(best)) {
				best, found = off, true
			}
		}
		return best, found
	}
	for _, c := range a {
		switch off, ok := nearest(c, b); {
		case !ok:
			r.Missing = append(r.Missing, c.Time)
		case off != 0:
			r.Off = append(r.Off, cutOff{Time: c.Time, Frames: off})
		}
	}
	for _, c := range b {
		if _, ok := nearest(c, a); !ok {
			r.Extra = append(r.Extra, c.Time)
		}
	}
	return r
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// worstFrames picks up to n of the worst frames, at least spacing frames apart, worst first.
func worstFrames(scores []score, n, spacing int) []int {
	idx := make([]int, len(scores))
	for i := range idx {
		idx[i] = i
	}
	slices.SortStableFunc(idx, func(a, b int) int { return cmp.Compare(scores[b].badness(), scores[a].badness()) })
	var picks []int
	for _, k := range idx {
		if len(picks) == n {
			break
		}
		if !slices.ContainsFunc(picks, func(p int) bool { return abs(p-k) < spacing }) {
			picks = append(picks, k)
		}
	}
	return picks
}

func momentAt(clock *Video, k int, s score) moment {
	worst, hue := 0, 0
	for c := range s.Cells {
		if s.Cells[c] < s.Cells[worst] {
			worst = c
		}
		if s.DECells[c] > s.DECells[hue] {
			hue = c
		}
	}
	m := moment{Frame: k, Time: clock.stamp(k), SSIM: round3(s.SSIM), DE: math.Round(s.DE*10) / 10,
		Cell: cellName(worst), Box: cellBox(worst, clock), CellSSIM: round3(s.Cells[worst]),
		ColorCell: cellName(hue), ColorDE: math.Round(s.DECells[hue]*10) / 10}
	// A cell that is the wrong colour but right in shape is the one to point at.
	if s.DECells[hue] >= 10 && s.Cells[worst] > 0.9 {
		m.Box = cellBox(hue, clock)
	}
	for c := range grid * grid {
		if s.Missing&(1<<c) != 0 {
			m.Missing = append(m.Missing, cellName(c))
		}
	}
	return m
}

// triptych writes original | yours | difference side by side, at most 720 px tall, with box outlined.
func triptych(out string, orig *Video, inA, inB []string, box [4]int) error {
	ph := min(orig.Height, 720) / 2 * 2
	pw := max(2, int(math.Round(float64(orig.Width)*float64(ph)/float64(orig.Height)/2))*2)
	s := float64(ph) / float64(orig.Height)
	draw := fmt.Sprintf("drawbox=x=%d:y=%d:w=%d:h=%d:color=red@0.9:t=3",
		int(float64(box[0])*s), int(float64(box[1])*s), max(1, int(float64(box[2])*s)), max(1, int(float64(box[3])*s)))
	fc := fmt.Sprintf("[0:v]scale=%[1]d:%[2]d,setsar=1,format=rgb24,split[a1][a2];[1:v]scale=%[1]d:%[2]d,setsar=1,format=rgb24,split[b1][b2];"+
		"[a2][b2]blend=all_mode=difference[d];[a1]%[3]s[a];[b1]%[3]s[b];[a][b][d]hstack=inputs=3[v]", pw, ph, draw)
	args := append(slices.Clone(inA), inB...)
	return ffmpeg(append(args, "-filter_complex", fc, "-map", "[v]", "-frames:v", "1", out)...)
}

// sideBySide writes original | yours as one video with the original's sound, to watch them together.
func sideBySide(orig *Video, yours, out string) error {
	fc := fmt.Sprintf("[0:v]setsar=1[a];[1:v]scale=%d:%d,setsar=1[b];[a][b]hstack=inputs=2,pad=ceil(iw/2)*2:ceil(ih/2)*2,format=yuv420p[v]", orig.Width, orig.Height)
	return ffmpeg("-i", orig.Path, "-i", yours, "-filter_complex", fc, "-map", "[v]", "-map", "0:a:0?",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-c:a", "aac", "-shortest", out)
}

func times(ts []float64) string {
	var s []string
	for _, t := range ts {
		s = append(s, fmt.Sprintf("%.6f", t))
	}
	return strings.Join(s, ", ")
}

func printReport(r *report) {
	fmt.Printf("%s vs %s\n", r.Original, r.Yours)
	fmt.Printf("  frames   %d vs %d", r.Frames[0], r.Frames[1])
	if d := r.Frames[1] - r.Frames[0]; d != 0 {
		fmt.Printf("   <- yours is %+d frames", d)
	}
	fmt.Println()
	if r.Size[0] != r.Size[1] {
		fmt.Printf("  size     %s vs %s   <- they differ\n", r.Size[0], r.Size[1])
	}
	fmt.Printf("  SSIM     mean %.3f  p05 %.3f  min %.3f  worst-cell p05 %.3f  over %d frames\n", r.Mean, r.P05, r.Min, r.CellP05, r.Compared)
	fmt.Printf("  colour   ΔE mean %.1f  p95 %.1f  (2 is just visible, 10 obvious)\n", r.DEMean, r.DEP95)
	if c := r.Cuts; c != nil {
		fmt.Printf("  cuts     %d in the original, %d in yours\n", c.Original, c.Yours)
		for _, o := range c.Off {
			way := "late"
			if o.Frames < 0 {
				way = "early"
			}
			fmt.Printf("           %.6f: yours is %d frame(s) %s\n", o.Time, abs(o.Frames), way)
		}
		if len(c.Missing) > 0 {
			fmt.Printf("           missing in yours: %s\n", times(c.Missing))
		}
		if len(c.Extra) > 0 {
			fmt.Printf("           extra in yours: %s\n", times(c.Extra))
		}
	}
	if len(r.Missing) > 0 {
		fmt.Println("  missing content (detail in the original, flat in yours)")
		for _, g := range r.Missing {
			fmt.Printf("           %s %v  %.3f to %.3f s\n", g.Cell, g.Box, g.From, g.To)
		}
	}
	if len(r.Segments) > 0 {
		fmt.Println("  shots")
		for _, s := range r.Segments {
			fmt.Printf("           %8.3f to %8.3f s  mean %.3f  min %.3f  ΔE %.1f  worst cell %s %.3f\n", s.From, s.To, s.Mean, s.Min, s.DE, s.Cell, s.CellMean)
		}
	}
	fmt.Println("  moments  (pictures: original | yours | difference, worst cell boxed in red)")
	for _, m := range r.Moments {
		note := fmt.Sprintf("  worst cell %s %.3f", m.Cell, m.CellSSIM)
		if m.ColorDE >= 5 {
			note += fmt.Sprintf("  colour off in %s ΔE %.1f", m.ColorCell, m.ColorDE)
		}
		if len(m.Missing) > 0 {
			note += "  missing " + strings.Join(m.Missing, " ")
		}
		fmt.Printf("           %8.3f s  f%-6d ssim %.3f  ΔE %.1f%s\n             %s\n", m.Time, m.Frame, m.SSIM, m.DE, note, m.Image)
	}
	if r.SBS != "" {
		fmt.Printf("  side by side: %s\n", r.SBS)
	}
}

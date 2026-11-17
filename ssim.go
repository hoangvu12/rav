package main

import (
	"fmt"
	"math"
)

// SSIM over 8x8 windows on a 4-pixel step, from integral images so a frame costs a few passes over its pixels.
// Each window also counts toward the cell of a 4x4 grid its centre falls in, so a score says where it fails.
// SSIM reads brightness only, so each cell's mean colour is compared too, as CIE ΔE: a wrong flat colour barely
// moves SSIM but stands out in ΔE (about 2 is a just-visible difference, 10 an obvious one).
const grid = 4

type score struct {
	SSIM    float64
	Cells   [grid * grid]float64
	DE      float64 // the cells' mean colour difference
	DECells [grid * grid]float64
	Missing uint16 // bit i: cell i shows detail in the original and is flat in yours
}

// badness ranks frames for the worst moments: structure and colour together.
func (s score) badness() float64 { return 1 - s.SSIM + s.DE/100 }

// A cell is missing content when the original's spread there reaches missingDetail grey levels and yours stays
// under missingFlat and under missingShare of the original's.
const (
	missingDetail = 18.0
	missingFlat   = 6.0
	missingShare  = 0.35
)

type ssimer struct {
	w, h                  int
	la, lb                []byte // luma
	cellOf                []int  // pixel index -> cell
	sa, sb, saa, sbb, sab []int64
}

func newSSIM(w, h int) *ssimer {
	n := (w + 1) * (h + 1)
	s := &ssimer{w: w, h: h, la: make([]byte, w*h), lb: make([]byte, w*h), cellOf: make([]int, w*h),
		sa: make([]int64, n), sb: make([]int64, n), saa: make([]int64, n), sbb: make([]int64, n), sab: make([]int64, n)}
	for y := range h {
		for x := range w {
			s.cellOf[y*w+x] = min(y*grid/h, grid-1)*grid + min(x*grid/w, grid-1)
		}
	}
	return s
}

// score compares two w×h RGB frames.
func (s *ssimer) score(a, b []byte) score {
	var sc score
	var rgbA, rgbB [grid * grid][3]float64
	var px [grid * grid]float64
	for i := range s.w * s.h {
		c := s.cellOf[i]
		ra, ga, ba := a[3*i], a[3*i+1], a[3*i+2]
		rb, gb, bb := b[3*i], b[3*i+1], b[3*i+2]
		s.la[i] = luma(ra, ga, ba)
		s.lb[i] = luma(rb, gb, bb)
		rgbA[c][0] += float64(ra)
		rgbA[c][1] += float64(ga)
		rgbA[c][2] += float64(ba)
		rgbB[c][0] += float64(rb)
		rgbB[c][1] += float64(gb)
		rgbB[c][2] += float64(bb)
		px[c]++
	}
	for c := range sc.DECells {
		if px[c] == 0 {
			continue
		}
		la, aa, ba := lab(rgbA[c][0]/px[c], rgbA[c][1]/px[c], rgbA[c][2]/px[c])
		lb, ab, bb := lab(rgbB[c][0]/px[c], rgbB[c][1]/px[c], rgbB[c][2]/px[c])
		sc.DECells[c] = math.Sqrt((la-lb)*(la-lb) + (aa-ab)*(aa-ab) + (ba-bb)*(ba-bb))
		sc.DE += sc.DECells[c] / float64(grid*grid)
	}
	s.structure(&sc)
	return sc
}

func luma(r, g, b byte) byte { return byte((299*int(r) + 587*int(g) + 114*int(b) + 500) / 1000) }

// lab converts an sRGB colour (0–255) to CIE L*a*b* under D65.
func lab(r, g, b float64) (float64, float64, float64) {
	lin := func(c float64) float64 {
		c /= 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	R, G, B := lin(r), lin(g), lin(b)
	x := (0.4124*R + 0.3576*G + 0.1805*B) / 0.95047
	y := 0.2126*R + 0.7152*G + 0.0722*B
	z := (0.0193*R + 0.1192*G + 0.9505*B) / 1.08883
	f := func(t float64) float64 {
		if t > 216.0/24389 {
			return math.Cbrt(t)
		}
		return (24389.0/27*t + 16) / 116
	}
	fx, fy, fz := f(x), f(y), f(z)
	return 116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)
}

// structure fills SSIM, its cells and missing content from the luma planes.
func (s *ssimer) structure(sc *score) {
	W := s.w + 1
	a, b := s.la, s.lb
	for y := 0; y < s.h; y++ {
		var ra, rb, raa, rbb, rab int64
		row := y * s.w
		for x := 0; x < s.w; x++ {
			pa, pb := int64(a[row+x]), int64(b[row+x])
			ra += pa
			rb += pb
			raa += pa * pa
			rbb += pb * pb
			rab += pa * pb
			i, up := (y+1)*W+x+1, y*W+x+1
			s.sa[i] = s.sa[up] + ra
			s.sb[i] = s.sb[up] + rb
			s.saa[i] = s.saa[up] + raa
			s.sbb[i] = s.sbb[up] + rbb
			s.sab[i] = s.sab[up] + rab
		}
	}
	const win, step, n = 8, 4, 64.0
	c1, c2 := math.Pow(0.01*255, 2), math.Pow(0.03*255, 2)
	var sum [grid * grid]float64
	var cnt [grid * grid]int
	total, count := 0.0, 0
	for y := 0; y+win <= s.h; y += step {
		cy := min((y+win/2)*grid/s.h, grid-1)
		for x := 0; x+win <= s.w; x += step {
			ma := float64(s.box(s.sa, x, y, x+win, y+win)) / n
			mb := float64(s.box(s.sb, x, y, x+win, y+win)) / n
			va := float64(s.box(s.saa, x, y, x+win, y+win))/n - ma*ma
			vb := float64(s.box(s.sbb, x, y, x+win, y+win))/n - mb*mb
			cov := float64(s.box(s.sab, x, y, x+win, y+win))/n - ma*mb
			v := ((2*ma*mb + c1) * (2*cov + c2)) / ((ma*ma + mb*mb + c1) * (va + vb + c2))
			c := cy*grid + min((x+win/2)*grid/s.w, grid-1)
			sum[c] += v
			cnt[c]++
			total += v
			count++
		}
	}
	sc.SSIM = 1
	if count > 0 {
		sc.SSIM = total / float64(count)
	}
	for i := range sc.Cells {
		sc.Cells[i] = sc.SSIM
		if cnt[i] > 0 {
			sc.Cells[i] = sum[i] / float64(cnt[i])
		}
		x0, y0, x1, y1 := cellRect(i, s.w, s.h)
		sdA, sdB := s.spread(s.sa, s.saa, x0, y0, x1, y1), s.spread(s.sb, s.sbb, x0, y0, x1, y1)
		if sdA >= missingDetail && sdB < missingFlat && sdB < missingShare*sdA {
			sc.Missing |= 1 << i
		}
	}
}

func (s *ssimer) box(t []int64, x0, y0, x1, y1 int) int64 {
	W := s.w + 1
	return t[y1*W+x1] - t[y0*W+x1] - t[y1*W+x0] + t[y0*W+x0]
}

// spread is the standard deviation of grey levels in a box.
func (s *ssimer) spread(sum, sq []int64, x0, y0, x1, y1 int) float64 {
	n := float64((x1 - x0) * (y1 - y0))
	if n <= 0 {
		return 0
	}
	m := float64(s.box(sum, x0, y0, x1, y1)) / n
	return math.Sqrt(math.Max(0, float64(s.box(sq, x0, y0, x1, y1))/n-m*m))
}

// cellRect is grid cell i's box in a w×h picture.
func cellRect(i, w, h int) (x0, y0, x1, y1 int) {
	r, c := i/grid, i%grid
	return c * w / grid, r * h / grid, (c + 1) * w / grid, (r + 1) * h / grid
}

// cellBox is grid cell i as x, y, w, h in v's pixels.
func cellBox(i int, v *Video) [4]int {
	x0, y0, x1, y1 := cellRect(i, v.Width, v.Height)
	return [4]int{x0, y0, x1 - x0, y1 - y0}
}

// cellName names a cell by row and column, from the top left: r1c1 … r4c4.
func cellName(i int) string { return fmt.Sprintf("r%dc%d", i/grid+1, i%grid+1) }

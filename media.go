package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Video is what rav knows about a clip, on the composition clock: frame k shows at k / fps seconds.
type Video struct {
	Path     string  `json:"path"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	FPS      string  `json:"fps"`
	Frames   int     `json:"frames"`
	Duration float64 `json:"duration"`
	HasAudio bool    `json:"has_audio"`
	VFR      bool    `json:"vfr,omitempty"`
	num, den int64
}

func (v *Video) rate() float64 { return float64(v.num) / float64(v.den) }

// at is frame k's time in seconds.
func (v *Video) at(k int) float64 { return float64(k) * float64(v.den) / float64(v.num) }

// stamp is frame k's time as a composition should write it: rounded down to the microsecond, so a layer timed
// with it never shows a frame late.
func (v *Video) stamp(k int) float64 { return math.Floor(v.at(k)*1e6+1e-6) / 1e6 }

// seekTo is an ffmpeg input seek that decodes to frame k: half a frame before it, so rounding never lands a
// frame off.
func (v *Video) seekTo(k int) float64 {
	if k <= 0 {
		return 0
	}
	return v.at(k) - 0.5/v.rate()
}

// frameOf reads "2.5" (seconds) or "75f" (a frame) as a frame index.
func (v *Video) frameOf(s string) (int, error) {
	s = strings.TrimSpace(s)
	if f, ok := strings.CutSuffix(s, "f"); ok {
		k, err := strconv.Atoi(f)
		if err != nil || k < 0 {
			return 0, fmt.Errorf("bad frame %q", s)
		}
		return k, nil
	}
	t, err := strconv.ParseFloat(s, 64)
	if err != nil || t < 0 {
		return 0, fmt.Errorf("bad time %q: use seconds (2.5) or a frame (75f)", s)
	}
	return int(math.Round(t * v.rate())), nil
}

func (v *Video) framesOf(list string) ([]int, error) {
	var ks []int
	for _, s := range strings.Split(list, ",") {
		if strings.TrimSpace(s) == "" {
			continue
		}
		k, err := v.frameOf(s)
		if err != nil {
			return nil, err
		}
		if v.Frames > 0 && k >= v.Frames {
			return nil, fmt.Errorf("%s is past the last frame (%d)", s, v.Frames-1)
		}
		ks = append(ks, k)
	}
	if len(ks) == 0 {
		return nil, errors.New("no times given")
	}
	return ks, nil
}

func dims(v *Video) string { return fmt.Sprintf("%dx%d", v.Width, v.Height) }

func probe(path string) (*Video, error) {
	out, err := exec.Command("ffprobe", "-v", "error", "-count_packets",
		"-show_entries", "stream=codec_type,width,height,r_frame_rate,avg_frame_rate,nb_read_packets:stream_side_data=rotation:format=duration",
		"-of", "json", path).Output()
	if err != nil {
		var ee *exec.ExitError
		var stderr []byte
		if errors.As(err, &ee) {
			stderr = ee.Stderr
		}
		return nil, runErr("ffprobe", err, stderr)
	}
	var p struct {
		Streams []struct {
			CodecType    string `json:"codec_type"`
			Width        int    `json:"width"`
			Height       int    `json:"height"`
			RFrameRate   string `json:"r_frame_rate"`
			AvgFrameRate string `json:"avg_frame_rate"`
			Packets      string `json:"nb_read_packets"`
			SideData     []struct {
				Rotation float64 `json:"rotation"`
			} `json:"side_data_list"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &p); err != nil {
		return nil, fmt.Errorf("ffprobe %s: %w", path, err)
	}
	v := &Video{Path: path}
	found := false
	for _, s := range p.Streams {
		switch s.CodecType {
		case "audio":
			v.HasAudio = true
		case "video":
			if found {
				continue
			}
			found = true
			v.Width, v.Height = s.Width, s.Height
			for _, sd := range s.SideData {
				// ffmpeg turns a phone's rotated picture upright when it decodes, so rav sees it upright too.
				if math.Mod(math.Abs(sd.Rotation), 180) == 90 {
					v.Width, v.Height = v.Height, v.Width
				}
			}
			rn, rd, rok := rational(s.RFrameRate)
			an, ad, aok := rational(s.AvgFrameRate)
			switch {
			case rok && aok && math.Abs(float64(rn)/float64(rd)-float64(an)/float64(ad)) > 0.005*float64(an)/float64(ad):
				// A phone's variable rate: put it on the nearest standard rate.
				v.VFR = true
				v.num, v.den = standardRate(float64(an) / float64(ad))
			case rok:
				v.num, v.den = rn, rd
			case aok:
				v.num, v.den = an, ad
			}
			v.Frames, _ = strconv.Atoi(s.Packets)
		}
	}
	if !found {
		return nil, fmt.Errorf("%s has no picture", path)
	}
	if v.num == 0 {
		return nil, fmt.Errorf("%s has no frame rate", path)
	}
	dur, _ := strconv.ParseFloat(p.Format.Duration, 64)
	if v.VFR || v.Frames == 0 {
		v.Frames = int(math.Round(dur * v.rate()))
	}
	v.Frames = max(v.Frames, 1)
	v.FPS = fpsString(v.num, v.den)
	v.Duration = math.Round(v.at(v.Frames)*1e6) / 1e6
	return v, nil
}

// rational reads a frame rate written "30000/1001", "30" or "29.97".
func rational(s string) (int64, int64, bool) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, ".") && !strings.Contains(s, "/") {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || f <= 0 {
			return 0, 0, false
		}
		if n, d := standardRate(f); math.Abs(float64(n)/float64(d)-f) < 0.001*f {
			return n, d, true
		}
		return rational(strconv.FormatInt(int64(math.Round(f*1000)), 10) + "/1000")
	}
	n, d, ok := strings.Cut(s, "/")
	if !ok {
		d = "1"
	}
	a, err1 := strconv.ParseInt(n, 10, 64)
	b, err2 := strconv.ParseInt(d, 10, 64)
	if err1 != nil || err2 != nil || a <= 0 || b <= 0 {
		return 0, 0, false
	}
	g := gcd(a, b)
	return a / g, b / g, true
}

func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

var standardRates = [][2]int64{{24000, 1001}, {24, 1}, {25, 1}, {30000, 1001}, {30, 1}, {48, 1}, {50, 1}, {60000, 1001}, {60, 1}, {120, 1}}

func standardRate(f float64) (int64, int64) {
	best := standardRates[0]
	dist := func(r [2]int64) float64 { return math.Abs(float64(r[0])/float64(r[1]) - f) }
	for _, r := range standardRates {
		if dist(r) < dist(best) {
			best = r
		}
	}
	return best[0], best[1]
}

func fpsString(num, den int64) string {
	if den == 1 {
		return strconv.FormatInt(num, 10)
	}
	return fmt.Sprintf("%d/%d", num, den)
}

func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', 6, 64) }

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }

// fitSize scales w×h down so its longer side is at most long, keeping both sides even.
func fitSize(w, h, long int) (int, int) {
	s := math.Min(1, float64(long)/float64(max(w, h)))
	return max(2, int(math.Round(float64(w)*s/2))*2), max(2, int(math.Round(float64(h)*s/2))*2)
}

// ffmpeg runs one ffmpeg command, returning its own error message when it fails.
func ffmpeg(args ...string) error {
	cmd := exec.Command("ffmpeg", append([]string{"-v", "error", "-nostdin", "-y"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return runErr("ffmpeg", err, stderr.Bytes())
	}
	return nil
}

func runErr(name string, err error, stderr []byte) error {
	msg := strings.TrimSpace(string(stderr))
	if msg == "" {
		return fmt.Errorf("%s: %w", name, err)
	}
	lines := strings.Split(msg, "\n")
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	return fmt.Errorf("%s: %s", name, strings.Join(lines, "\n"))
}

// afterSeek starts a filter chain that follows an input seek. The first frame after a seek is stamped half a
// frame in; ffmpeg fills that gap by repeating it, which would put every later frame one late.
func afterSeek(filters ...string) string {
	return strings.Join(append([]string{"setpts=PTS-STARTPTS"}, filters...), ",")
}

// extractFrame writes frame k of v as a PNG, through the filter vf when it is not empty.
func extractFrame(v *Video, k int, vf, out string) error {
	var filters []string
	if vf != "" {
		filters = append(filters, vf)
	}
	args := []string{"-ss", ftoa(v.seekTo(k)), "-i", v.Path, "-frames:v", "1", "-vf", afterSeek(filters...)}
	if err := ffmpeg(append(args, out)...); err != nil {
		return err
	}
	if _, err := os.Stat(out); err != nil {
		return fmt.Errorf("no frame %d in %s", k, v.Path)
	}
	return nil
}

// greyArgs has ffmpeg decode path as w×h grey bytes, resampled onto the fps frame grid when fps is not empty.
func greyArgs(path, fps string, w, h int) []string { return pixelArgs(path, fps, w, h, "gray") }

// rgbArgs is greyArgs in RGB, three bytes a pixel.
func rgbArgs(path, fps string, w, h int) []string { return pixelArgs(path, fps, w, h, "rgb24") }

func pixelArgs(path, fps string, w, h int, format string) []string {
	vf := fmt.Sprintf("scale=%d:%d:flags=area,format=rgb24", w, h)
	if format != "rgb24" {
		vf += ",format=" + format
	}
	if fps != "" {
		vf = "setpts=PTS-STARTPTS,fps=" + fps + "," + vf
	}
	return []string{"-i", path, "-an", "-sn", "-vf", vf, "-f", "rawvideo", "pipe:1"}
}

// frameReader streams raw frames out of ffmpeg.
type frameReader struct {
	cmd    *exec.Cmd
	r      *bufio.Reader
	buf    []byte
	stderr bytes.Buffer
}

func readFrames(size int, ffargs ...string) (*frameReader, error) {
	cmd := exec.Command("ffmpeg", append([]string{"-v", "error", "-nostdin"}, ffargs...)...)
	f := &frameReader{cmd: cmd, buf: make([]byte, size)}
	cmd.Stderr = &f.stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, runErr("ffmpeg", err, nil)
	}
	f.r = bufio.NewReaderSize(out, 1<<20)
	return f, nil
}

// next returns the next frame, valid until the following call, or io.EOF after the last.
func (f *frameReader) next() ([]byte, error) {
	if f.cmd == nil {
		return nil, io.EOF
	}
	if _, err := io.ReadFull(f.r, f.buf); err != nil {
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			return nil, err
		}
		werr := f.cmd.Wait()
		f.cmd = nil
		if werr != nil {
			return nil, runErr("ffmpeg", werr, f.stderr.Bytes())
		}
		return nil, io.EOF
	}
	return f.buf, nil
}

func (f *frameReader) close() {
	if f.cmd != nil {
		f.cmd.Process.Kill()
		f.cmd.Wait()
		f.cmd = nil
	}
}

// rgbImage reads one picture file as w×h RGB bytes.
func rgbImage(path string, w, h int) ([]byte, error) {
	f, err := readFrames(3*w*h, rgbArgs(path, "", w, h)...)
	if err != nil {
		return nil, err
	}
	defer f.close()
	px, err := f.next()
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return bytes.Clone(px), nil
}

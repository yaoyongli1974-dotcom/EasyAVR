// Package vqd implements lightweight video quality diagnosis (视频质量诊断) on
// captured frames: brightness/contrast, blur (Laplacian variance), color cast,
// black-screen ratio, noise, blue-screen, occlusion, blockiness (mosaic) from a
// single frame, plus freeze/jitter from consecutive frames. It turns pixel
// metrics into discrete issues.
package vqd

import (
	"image"
	"math"
)

// Metrics holds per-frame (spatial) image quality measurements.
type Metrics struct {
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	Brightness float64 `json:"brightness"` // mean luminance 0-255
	Contrast   float64 `json:"contrast"`   // luminance std-dev
	Sharpness  float64 `json:"sharpness"`  // variance of Laplacian
	BlackRatio float64 `json:"blackRatio"` // fraction of near-black pixels
	BlueRatio  float64 `json:"blueRatio"`  // fraction of distinctly blue pixels
	DomRatio   float64 `json:"domRatio"`   // largest single-luminance bucket share
	Blockiness float64 `json:"blockiness"` // 8px block-boundary vs inner energy
	Noise      float64 `json:"noise"`      // high-frequency energy
	ColorCast  string  `json:"colorCast"`  // neutral|red|green|blue|cyan|magenta|yellow
	CastScore  float64 `json:"castScore"`
	MeanR      float64 `json:"meanR"`
	MeanG      float64 `json:"meanG"`
	MeanB      float64 `json:"meanB"`
}

// Temporal holds measurements across consecutive frames.
type Temporal struct {
	Frames    int     `json:"frames"`
	MeanDiff  float64 `json:"meanDiff"`  // average inter-frame luminance diff
	MaxShift  int     `json:"maxShift"`  // largest estimated global shift (px)
	ShakeHits int     `json:"shakeHits"` // count of significant alternating shifts
}

// Issue is one diagnosed problem.
type Issue struct {
	Code    string `json:"code"`
	Level   string `json:"level"` // info | warning | critical
	Message string `json:"message"`
}

const maxWidth = 320

// Analyze computes spatial quality metrics from an image (down-scaled for speed).
func Analyze(img image.Image) Metrics {
	b := img.Bounds()
	w0, h0 := b.Dx(), b.Dy()
	if w0 <= 0 || h0 <= 0 {
		return Metrics{ColorCast: "neutral"}
	}
	step := 1
	if w0 > maxWidth {
		step = w0 / maxWidth
		if step < 1 {
			step = 1
		}
	}
	w := w0 / step
	h := h0 / step
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}

	gray := make([]float64, w*h)
	hist := make([]int, 256)
	var sr, sg, sb, blue float64
	idx := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, _ := img.At(b.Min.X+x*step, b.Min.Y+y*step).RGBA()
			rf, gf, bf := float64(r>>8), float64(g>>8), float64(bl>>8)
			sr += rf
			sg += gf
			sb += bf
			if bf > rf+50 && bf > gf+50 {
				blue++
			}
			v := 0.299*rf + 0.587*gf + 0.114*bf
			gray[idx] = v
			hi := int(v)
			if hi < 0 {
				hi = 0
			} else if hi > 255 {
				hi = 255
			}
			hist[hi]++
			idx++
		}
	}
	n := float64(w * h)

	var sum float64
	for _, v := range gray {
		sum += v
	}
	mean := sum / n

	var varSum float64
	black := 0
	for _, v := range gray {
		d := v - mean
		varSum += d * d
		if v < 20 {
			black++
		}
	}
	contrast := math.Sqrt(varSum / n)

	peak := 0
	for _, c := range hist {
		if c > peak {
			peak = c
		}
	}

	colorCast, castScore := colorCast(sr/n, sg/n, sb/n)

	return Metrics{
		Width: w0, Height: h0,
		Brightness: mean, Contrast: contrast, Sharpness: laplacianVariance(gray, w, h),
		BlackRatio: float64(black) / n, BlueRatio: blue / n,
		DomRatio:   float64(peak) / n,
		Blockiness: blockiness(gray, w, h),
		Noise:      noiseLevel(gray, w, h),
		ColorCast:  colorCast, CastScore: castScore,
		MeanR: sr / n, MeanG: sg / n, MeanB: sb / n,
	}
}

func laplacianVariance(gray []float64, w, h int) float64 {
	if w < 3 || h < 3 {
		return 0
	}
	var sum, sumSq, cnt float64
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			c := gray[y*w+x]
			lap := 4*c - gray[(y-1)*w+x] - gray[(y+1)*w+x] - gray[y*w+x-1] - gray[y*w+x+1]
			sum += lap
			sumSq += lap * lap
			cnt++
		}
	}
	if cnt == 0 {
		return 0
	}
	mean := sum / cnt
	v := sumSq/cnt - mean*mean
	if v < 0 {
		return 0
	}
	return v
}

func noiseLevel(gray []float64, w, h int) float64 {
	if w < 3 || h < 3 {
		return 0
	}
	var sum, cnt float64
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			var s float64
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					s += gray[(y+dy)*w+(x+dx)]
				}
			}
			blur := s / 9
			sum += math.Abs(gray[y*w+x] - blur)
			cnt++
		}
	}
	if cnt == 0 {
		return 0
	}
	return sum / cnt
}

// blockiness measures the energy of horizontal/vertical seams on an 8px grid
// relative to the inner-block energy; compression mosaic raises this ratio.
func blockiness(gray []float64, w, h int) float64 {
	if w < 16 || h < 16 {
		return 0
	}
	var edgeSum, edgeCnt float64
	for y := 0; y < h; y++ {
		for x := 8; x < w; x += 8 {
			edgeSum += math.Abs(gray[y*w+x] - gray[y*w+x-1])
			edgeCnt++
		}
	}
	for y := 8; y < h; y += 8 {
		for x := 0; x < w; x++ {
			edgeSum += math.Abs(gray[y*w+x] - gray[(y-1)*w+x])
			edgeCnt++
		}
	}
	var inSum, inCnt float64
	for y := 0; y < h; y++ {
		for x := 1; x < w; x++ {
			if x%8 == 0 {
				continue
			}
			inSum += math.Abs(gray[y*w+x] - gray[y*w+x-1])
			inCnt++
		}
	}
	if edgeCnt == 0 || inCnt == 0 {
		return 0
	}
	inner := inSum / inCnt
	if inner < 0.5 {
		inner = 0.5
	}
	return (edgeSum / edgeCnt) / inner
}

var opposite = map[string]string{"red": "cyan", "green": "magenta", "blue": "yellow"}

func colorCast(meanR, meanG, meanB float64) (string, float64) {
	grayAvg := (meanR + meanG + meanB) / 3
	deltas := map[string]float64{"red": meanR - grayAvg, "green": meanG - grayAvg, "blue": meanB - grayAvg}
	cast := "neutral"
	best := 0.0
	for name, v := range deltas {
		if math.Abs(v) > best {
			best = math.Abs(v)
			if v > 0 {
				cast = name
			} else {
				cast = opposite[name]
			}
		}
	}
	if best <= 12 {
		return "neutral", best
	}
	return cast, best
}

// Evaluate turns spatial metrics into issues using heuristic thresholds.
func Evaluate(m Metrics) []Issue {
	var out []Issue
	switch {
	case m.Brightness < 15 || m.BlackRatio > 0.9:
		out = append(out, Issue{"black_screen", "critical", "画面接近全黑（可能信号丢失或被遮挡）"})
	case m.Brightness < 40:
		out = append(out, Issue{"too_dark", "warning", "画面偏暗"})
	}
	if m.Brightness > 235 {
		out = append(out, Issue{"overexposed", "warning", "画面过曝"})
	}
	if m.Sharpness < 30 {
		out = append(out, Issue{"blur", "warning", "画面模糊（清晰度低）"})
	}
	if m.Contrast < 15 {
		out = append(out, Issue{"low_contrast", "warning", "对比度过低"})
	}
	if m.CastScore > 40 && m.ColorCast != "neutral" {
		out = append(out, Issue{"color_cast", "warning", "画面偏色：" + m.ColorCast})
	}
	if m.BlueRatio > 0.6 && m.Brightness > 15 {
		out = append(out, Issue{"blue_screen", "critical", "画面接近纯蓝（蓝屏/无信号）"})
	}
	if m.DomRatio > 0.8 && m.Brightness > 20 && m.Brightness < 235 {
		out = append(out, Issue{"occlusion", "warning", "画面被大面积遮挡或镜头被遮盖"})
	}
	if m.Blockiness > 1.6 && m.Sharpness > 20 {
		out = append(out, Issue{"mosaic", "warning", "画面存在马赛克/方块伪影"})
	}
	switch {
	case m.Noise > 35:
		out = append(out, Issue{"screen_distortion", "warning", "画面花屏/噪声严重"})
	case m.Noise > 18:
		out = append(out, Issue{"noise", "info", "画面噪声偏高"})
	}
	return out
}

// EvaluateTemporal turns inter-frame metrics into freeze/jitter issues.
func EvaluateTemporal(t Temporal) []Issue {
	if t.Frames < 2 {
		return nil
	}
	var out []Issue
	if t.MeanDiff < 1.5 {
		out = append(out, Issue{"freeze", "warning", "画面冻结（连续帧无变化）"})
	}
	if t.ShakeHits >= 2 && t.MaxShift >= 2 {
		out = append(out, Issue{"jitter", "warning", "画面抖动/机位晃动"})
	}
	return out
}

// Compare estimates inter-frame change over a sequence of same-sized frames.
func Compare(frames []image.Image) Temporal {
	t := Temporal{Frames: len(frames)}
	if len(frames) < 2 {
		return t
	}
	const cw = 64
	grays := make([][]float64, 0, len(frames))
	for _, f := range frames {
		grays = append(grays, smallGray(f, cw))
	}
	dimsOK := true
	for _, g := range grays {
		if len(g) != len(grays[0]) {
			dimsOK = false
		}
	}
	if !dimsOK {
		return t
	}
	var diffs []float64
	prevDx, prevDy, havePrev := 0, 0, false
	for i := 1; i < len(grays); i++ {
		d := meanAbsDiff(grays[i-1], grays[i])
		diffs = append(diffs, d)
		if d > 0.5 {
			dx, dy := estimateShift(grays[i-1], grays[i], cw)
			shift := abs(dx) + abs(dy)
			if shift > t.MaxShift {
				t.MaxShift = shift
			}
			if shift >= 2 {
				if havePrev && dx*prevDx+dy*prevDy < 0 {
					t.ShakeHits++
				}
				prevDx, prevDy, havePrev = dx, dy, true
			}
		}
	}
	if len(diffs) > 0 {
		var s float64
		for _, d := range diffs {
			s += d
		}
		t.MeanDiff = s / float64(len(diffs))
	}
	return t
}

func smallGray(img image.Image, targetW int) []float64 {
	b := img.Bounds()
	w0, h0 := b.Dx(), b.Dy()
	if w0 <= 0 || h0 <= 0 {
		return nil
	}
	w := targetW
	h := h0 * targetW / w0
	if h < 1 {
		h = 1
	}
	out := make([]float64, 0, w*h)
	for y := 0; y < h; y++ {
		sy := b.Min.Y + y*h0/h
		for x := 0; x < w; x++ {
			sx := b.Min.X + x*w0/w
			r, g, bl, _ := img.At(sx, sy).RGBA()
			out = append(out, 0.299*float64(r>>8)+0.587*float64(g>>8)+0.114*float64(bl>>8))
		}
	}
	return out
}

func meanAbsDiff(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var s float64
	for i := range a {
		s += math.Abs(a[i] - b[i])
	}
	return s / float64(len(a))
}

// estimateShift finds the integer (dx,dy) in [-8,8] minimizing SAD between two
// same-length row-major luminance slices laid out as a grid of width cw.
func estimateShift(a, b []float64, w int) (int, int) {
	h := len(a) / w
	if h == 0 {
		return 0, 0
	}
	bestDx, bestDy := 0, 0
	best := math.MaxFloat64
	for dy := -8; dy <= 8; dy++ {
		for dx := -8; dx <= 8; dx++ {
			var sad float64
			var cnt int
			for y := 8; y < h-8; y++ {
				for x := 8; x < w-8; x++ {
					sad += math.Abs(a[y*w+x] - b[(y+dy)*w+(x+dx)])
					cnt++
				}
			}
			if cnt == 0 {
				continue
			}
			sad = sad/float64(cnt) + 0.001*float64(abs(dx)+abs(dy))
			if sad < best {
				best = sad
				bestDx, bestDy = dx, dy
			}
		}
	}
	return bestDx, bestDy
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// WorstLevel returns the highest severity among issues ("" when none).
func WorstLevel(issues []Issue) string {
	rank := map[string]int{"": 0, "info": 1, "warning": 2, "critical": 3}
	worst := ""
	for _, i := range issues {
		if rank[i.Level] > rank[worst] {
			worst = i.Level
		}
	}
	return worst
}

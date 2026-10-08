package vqd

import (
	"image"
	"image/color"
	"math"
	"math/rand"
	"testing"
)

func solid(w, h int, c color.Gray) image.Image {
	img := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetGray(x, y, c)
		}
	}
	return img
}

func solidRGBA(w, h int, c color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func checker(w, h int) image.Image {
	img := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if (x+y)%2 == 0 {
				img.SetGray(x, y, color.Gray{Y: 255})
			} else {
				img.SetGray(x, y, color.Gray{Y: 0})
			}
		}
	}
	return img
}

func TestAnalyzeBlackScreen(t *testing.T) {
	m := Analyze(solid(160, 90, color.Gray{Y: 0}))
	if m.Brightness > 1 || m.BlackRatio < 0.99 {
		t.Fatalf("not black: %+v", m)
	}
	issues := Evaluate(m)
	if WorstLevel(issues) != "critical" {
		t.Fatalf("expected critical black screen, got %+v", issues)
	}
	found := false
	for _, i := range issues {
		if i.Code == "black_screen" {
			found = true
		}
	}
	if !found {
		t.Fatalf("black_screen issue missing: %+v", issues)
	}
}

func TestAnalyzeOverexposed(t *testing.T) {
	m := Analyze(solid(160, 90, color.Gray{Y: 255}))
	if m.Brightness < 250 {
		t.Fatalf("expected bright: %+v", m)
	}
	issues := Evaluate(m)
	found := false
	for _, i := range issues {
		if i.Code == "overexposed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("overexposed issue missing: %+v", issues)
	}
}

func TestAnalyzeBlurVsDetail(t *testing.T) {
	flat := Analyze(solid(160, 90, color.Gray{Y: 128}))
	detail := Analyze(checker(160, 90))
	if flat.Sharpness >= detail.Sharpness {
		t.Fatalf("flat sharpness %v should be < checkboard %v", flat.Sharpness, detail.Sharpness)
	}
	if detail.Sharpness < 30 {
		t.Fatalf("checkerboard should be sharp, got %v", detail.Sharpness)
	}
	flatIssues := Evaluate(flat)
	hasBlur := false
	for _, i := range flatIssues {
		if i.Code == "blur" {
			hasBlur = true
		}
	}
	if !hasBlur {
		t.Fatalf("flat image should be diagnosed blurry: %+v", flatIssues)
	}
}

func TestAnalyzeColorCast(t *testing.T) {
	m := Analyze(solidRGBA(160, 90, color.RGBA{R: 255, G: 0, B: 0, A: 255}))
	if m.ColorCast != "red" || m.CastScore < 40 {
		t.Fatalf("expected red cast: %+v", m)
	}
	neutral := Analyze(solid(160, 90, color.Gray{Y: 128}))
	if neutral.ColorCast != "neutral" {
		t.Fatalf("gray should be neutral: %+v", neutral)
	}
}

func hasIssue(issues []Issue, code string) bool {
	for _, i := range issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

func TestAnalyzeBlueScreen(t *testing.T) {
	m := Analyze(solidRGBA(160, 90, color.RGBA{R: 0, G: 0, B: 255, A: 255}))
	if m.BlueRatio < 0.9 {
		t.Fatalf("expected blue ratio, got %+v", m)
	}
	if !hasIssue(Evaluate(m), "blue_screen") {
		t.Fatalf("blue_screen missing: %+v", Evaluate(m))
	}
}

func TestAnalyzeOcclusion(t *testing.T) {
	m := Analyze(solid(160, 90, color.Gray{Y: 90}))
	if m.DomRatio < 0.9 {
		t.Fatalf("expected dominant ratio: %+v", m)
	}
	if !hasIssue(Evaluate(m), "occlusion") {
		t.Fatalf("occlusion missing: %+v", Evaluate(m))
	}
}

func TestAnalyzeMosaic(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 160, 96))
	for y := 0; y < 96; y++ {
		for x := 0; x < 160; x++ {
			v := uint8(60)
			if ((x/8)+(y/8))%2 == 0 {
				v = 200
			}
			img.SetGray(x, y, color.Gray{Y: v})
		}
	}
	m := Analyze(img)
	if m.Blockiness <= 1.6 {
		t.Fatalf("expected blockiness, got %+v", m)
	}
	if !hasIssue(Evaluate(m), "mosaic") {
		t.Fatalf("mosaic missing: %+v", Evaluate(m))
	}
}

func TestAnalyzeScreenDistortion(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	img := image.NewGray(image.Rect(0, 0, 160, 90))
	for y := 0; y < 90; y++ {
		for x := 0; x < 160; x++ {
			img.SetGray(x, y, color.Gray{Y: uint8(rnd.Intn(256))})
		}
	}
	m := Analyze(img)
	if m.Noise <= 35 {
		t.Fatalf("expected high noise: %+v", m)
	}
	if !hasIssue(Evaluate(m), "screen_distortion") {
		t.Fatalf("screen_distortion missing: %+v", Evaluate(m))
	}
}

func testSequence(w, h int) *image.Gray {
	return image.NewGray(image.Rect(0, 0, w, h))
}

func setShiftPattern(img *image.Gray, ox int) {
	b := img.Bounds()
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			v := 128 + 50*math.Sin(float64(x-ox)/6.0) + 20*math.Sin(float64(y)/5.0)
			img.SetGray(x, y, color.Gray{Y: uint8(v)})
		}
	}
}

func TestEvaluateTemporalFreeze(t *testing.T) {
	f := solid(160, 90, color.Gray{Y: 128})
	tm := Compare([]image.Image{f, f, f})
	if tm.MeanDiff > 1.5 {
		t.Fatalf("expected freeze, got %+v", tm)
	}
	if !hasIssue(EvaluateTemporal(tm), "freeze") {
		t.Fatalf("freeze missing: %+v", EvaluateTemporal(tm))
	}
}

func TestEvaluateTemporalJitter(t *testing.T) {
	var frames []image.Image
	for _, ox := range []int{0, 3, -3, 3} {
		img := testSequence(64, 36)
		setShiftPattern(img, ox)
		frames = append(frames, img)
	}
	tm := Compare(frames)
	if tm.MaxShift < 2 || tm.ShakeHits < 2 {
		t.Fatalf("expected jitter, got %+v", tm)
	}
	if !hasIssue(EvaluateTemporal(tm), "jitter") {
		t.Fatalf("jitter missing: %+v", EvaluateTemporal(tm))
	}
}

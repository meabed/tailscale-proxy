package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// proxy/router glyph: two opposing arrows (data exchange ⇄), as fractions of the
// canvas. Each entry is a stroked segment {x1,y1,x2,y2}.
var glyphSegs = [][4]float64{
	{0.16, 0.37, 0.80, 0.37}, // top shaft →
	{0.80, 0.37, 0.665, 0.28},
	{0.80, 0.37, 0.665, 0.46},
	{0.84, 0.63, 0.20, 0.63}, // bottom shaft ←
	{0.20, 0.63, 0.335, 0.54},
	{0.20, 0.63, 0.335, 0.72},
}

func segDist(px, py, x1, y1, x2, y2 float64) float64 {
	dx, dy := x2-x1, y2-y1
	l2 := dx*dx + dy*dy
	if l2 == 0 {
		return math.Hypot(px-x1, py-y1)
	}
	t := math.Max(0, math.Min(1, ((px-x1)*dx+(py-y1)*dy)/l2))
	return math.Hypot(px-(x1+t*dx), py-(y1+t*dy))
}

// makeIcon renders the proxy glyph in col as a 44px PNG (22pt @2x), anti-aliased.
func makeIcon(col color.NRGBA) []byte {
	const s = 44
	const half = 2.5 // stroke half-width, px
	img := image.NewNRGBA(image.Rect(0, 0, s, s))
	for y := 0; y < s; y++ {
		for x := 0; x < s; x++ {
			var cov float64
			for sy := 0; sy < 3; sy++ {
				for sx := 0; sx < 3; sx++ {
					px := float64(x) + (float64(sx)+0.5)/3
					py := float64(y) + (float64(sy)+0.5)/3
					min := math.MaxFloat64
					for _, g := range glyphSegs {
						d := segDist(px, py, g[0]*s, g[1]*s, g[2]*s, g[3]*s)
						if d < min {
							min = d
						}
					}
					if min <= half {
						cov++
					}
				}
			}
			if cov > 0 {
				c := col
				c.A = uint8(float64(col.A) * cov / 9)
				img.SetNRGBA(x, y, c)
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

var (
	iconIdle    = makeIcon(color.NRGBA{R: 0, G: 0, B: 0, A: 255})     // template (system-tinted)
	iconRunning = makeIcon(color.NRGBA{R: 52, G: 199, B: 89, A: 255}) // green
)

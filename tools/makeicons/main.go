// Command makeicons generates the extension's PNG icons without any external
// image tooling. It draws a simple download glyph (a downward arrow over a tray)
// on a rounded blue background at the two sizes the manifest references.
//
// Run from the repo root:
//
//	go run ./tools/makeicons
package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
)

var (
	bg    = color.NRGBA{R: 0x1f, G: 0x6f, B: 0xeb, A: 0xff} // blue
	glyph = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff} // white
)

func main() {
	outDir := filepath.Join("extension", "icons")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		panic(err)
	}
	for _, size := range []int{48, 128} {
		img := drawIcon(size)
		f, err := os.Create(filepath.Join(outDir, iconName(size)))
		if err != nil {
			panic(err)
		}
		if err := png.Encode(f, img); err != nil {
			f.Close()
			panic(err)
		}
		f.Close()
	}
}

func iconName(size int) string {
	switch size {
	case 48:
		return "icon48.png"
	default:
		return "icon128.png"
	}
}

// drawIcon renders the glyph at the given square size.
func drawIcon(size int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	fs := float64(size)
	radius := fs * 0.22

	// Rounded-rectangle background.
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if insideRounded(float64(x), float64(y), fs, radius) {
				img.Set(x, y, bg)
			} else {
				img.Set(x, y, color.NRGBA{0, 0, 0, 0}) // transparent corners
			}
		}
	}

	// Download arrow: a vertical shaft + triangular head, centered horizontally.
	cx := fs / 2
	shaftW := fs * 0.14
	shaftTop := fs * 0.20
	shaftBot := fs * 0.52
	for y := int(shaftTop); y < int(shaftBot); y++ {
		for x := int(cx - shaftW/2); x < int(cx+shaftW/2); x++ {
			setIfInside(img, x, y, size, glyph)
		}
	}
	// Arrow head (triangle pointing down).
	headTop := shaftBot - fs*0.04
	headBot := fs * 0.72
	headHalf := fs * 0.20
	for y := int(headTop); y < int(headBot); y++ {
		t := (float64(y) - headTop) / (headBot - headTop) // 0 at top, 1 at tip
		half := headHalf * (1 - t)
		for x := int(cx - half); x < int(cx+half); x++ {
			setIfInside(img, x, y, size, glyph)
		}
	}
	// Tray/base line under the arrow.
	trayY0 := fs * 0.78
	trayY1 := fs * 0.86
	trayX0 := fs * 0.26
	trayX1 := fs * 0.74
	for y := int(trayY0); y < int(trayY1); y++ {
		for x := int(trayX0); x < int(trayX1); x++ {
			setIfInside(img, x, y, size, glyph)
		}
	}

	return img
}

// insideRounded reports whether (x,y) is inside a square of side s with rounded
// corners of the given radius.
func insideRounded(x, y, s, r float64) bool {
	if x < 0 || y < 0 || x >= s || y >= s {
		return false
	}
	// Corner regions: check distance to the corner circle center.
	corners := [4][2]float64{
		{r, r}, {s - r, r}, {r, s - r}, {s - r, s - r},
	}
	nearLeft := x < r
	nearRight := x > s-r
	nearTop := y < r
	nearBottom := y > s-r
	if (nearLeft || nearRight) && (nearTop || nearBottom) {
		var c [2]float64
		switch {
		case nearLeft && nearTop:
			c = corners[0]
		case nearRight && nearTop:
			c = corners[1]
		case nearLeft && nearBottom:
			c = corners[2]
		default:
			c = corners[3]
		}
		dx, dy := x-c[0], y-c[1]
		return dx*dx+dy*dy <= r*r
	}
	return true
}

func setIfInside(img *image.NRGBA, x, y, size int, c color.NRGBA) {
	if x >= 0 && y >= 0 && x < size && y < size {
		img.Set(x, y, c)
	}
}

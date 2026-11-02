// SPDX-License-Identifier: AGPL-3.0-or-later

// Command logo derives the transparent logo set from the black-background master
// of the logo pack (logo/HD/nasbuster-2048.png).
//
// The background is the near-black area connected to the image border (flood
// fill), so black lines inside the drawing stay. A band of black is kept around
// the drawing: its own black outlines (hands, NAS) touch the background and
// would otherwise vanish with it. On a dark page the band is invisible; on a
// light page it reads as the classic pixel-art outline. The result is then
// cropped to a square around the drawing, so small icons use every pixel.
//
// From the repository root:
//
//	go run ./tools/logo                      # writes logo/transparent/
//	go run ./tools/logo -preview preview.png # plus a light/dark comparison sheet
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
)

var (
	logoSizes    = []int{1024, 512, 256, 128, 64} // the cropped drawing is ~1700 px: no upscale
	faviconSizes = []int{16, 32, 48}
	icoSizes     = []int{16, 24, 32, 48, 64, 128, 256}
)

func main() {
	in := flag.String("in", "logo/HD/nasbuster-2048.png", "black-background master (square PNG)")
	out := flag.String("out", "logo/transparent", "output directory")
	threshold := flag.Int("threshold", 10, "highest channel value counted as background (the NAS body is ~0x13)")
	outline := flag.Int("outline", 22, "width of the black band kept around the drawing, in master pixels")
	preview := flag.String("preview", "", "optional PNG showing the result on light and dark backgrounds")
	flag.Parse()

	if err := run(*in, *out, uint8(*threshold), *outline, *preview); err != nil {
		fmt.Fprintln(os.Stderr, "logo:", err)
		os.Exit(1)
	}
}

func run(in, out string, threshold uint8, outline int, preview string) error {
	src, err := load(in)
	if err != nil {
		return err
	}
	if src.Bounds().Dx() != src.Bounds().Dy() {
		return errors.New("the master must be square")
	}
	cut := cropSquare(cutout(src, threshold, outline), 0.02)
	if side := cut.Bounds().Dx(); side < logoSizes[0] {
		return fmt.Errorf("cropped drawing is %d px, smaller than the largest output (%d px)", side, logoSizes[0])
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	for _, n := range logoSizes {
		if err := save(filepath.Join(out, fmt.Sprintf("nasbuster-%d.png", n)), resize(cut, n)); err != nil {
			return err
		}
	}
	for _, n := range faviconSizes {
		if err := save(filepath.Join(out, fmt.Sprintf("favicon-%dx%d.png", n, n)), resize(cut, n)); err != nil {
			return err
		}
	}
	ico, err := encodeICO(cut, icoSizes)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "favicon.ico"), ico, 0o644); err != nil {
		return err
	}
	if preview != "" {
		return save(preview, previewSheet(resize(cut, 512)))
	}
	return nil
}

func load(path string) (*image.NRGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	dst := image.NewNRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
	draw.Draw(dst, dst.Bounds(), img, img.Bounds().Min, draw.Src)
	return dst, nil
}

func cutout(src *image.NRGBA, threshold uint8, outline int) *image.NRGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	bg := make([]bool, w*h)
	var stack []int
	push := func(x, y int) {
		i := y*w + x
		c := src.NRGBAAt(x, y)
		if bg[i] || max(c.R, c.G, c.B) > threshold {
			return
		}
		bg[i] = true
		stack = append(stack, i)
	}
	for x := 0; x < w; x++ {
		push(x, 0)
		push(x, h-1)
	}
	for y := 0; y < h; y++ {
		push(0, y)
		push(w-1, y)
	}
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		x, y := i%w, i/w
		if x > 0 {
			push(x-1, y)
		}
		if x < w-1 {
			push(x+1, y)
		}
		if y > 0 {
			push(x, y-1)
		}
		if y < h-1 {
			push(x, y+1)
		}
	}

	near := dilate(bg, w, h, outline)
	dst := image.NewNRGBA(src.Bounds())
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			switch {
			case bg[i] && !near[i]:
				// transparent: zero value
			case bg[i]:
				dst.SetNRGBA(x, y, color.NRGBA{A: 255})
			default:
				c := src.NRGBAAt(x, y)
				c.A = 255
				dst.SetNRGBA(x, y, c)
			}
		}
	}
	return dst
}

// cropSquare trims the transparent margins to the smallest square around the
// drawing (centered), plus `pad` of its side on each edge, so small icons use
// every pixel.
func cropSquare(img *image.NRGBA, pad float64) *image.NRGBA {
	b := img.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X-1, b.Min.Y-1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.NRGBAAt(x, y).A != 0 {
				minX, minY, maxX, maxY = min(minX, x), min(minY, y), max(maxX, x), max(maxY, y)
			}
		}
	}
	if maxX < minX {
		return img
	}
	side := max(maxX-minX+1, maxY-minY+1)
	side += 2 * int(float64(side)*pad)
	cx, cy := (minX+maxX+1)/2, (minY+maxY+1)/2
	dst := image.NewNRGBA(image.Rect(0, 0, side, side))
	draw.Draw(dst, dst.Bounds(), img, image.Pt(cx-side/2, cy-side/2), draw.Src)
	return dst
}

// dilate reports, for each pixel, whether a drawing pixel (!bg) lies within r
// (square neighborhood), with two separable running-window passes.
func dilate(bg []bool, w, h, r int) []bool {
	horiz := make([]bool, w*h)
	for y := 0; y < h; y++ {
		count := 0
		for x := -r; x < w; x++ {
			if xa := x + r; xa < w && !bg[y*w+xa] {
				count++
			}
			if xr := x - r - 1; xr >= 0 && !bg[y*w+xr] {
				count--
			}
			if x >= 0 {
				horiz[y*w+x] = count > 0
			}
		}
	}
	out := make([]bool, w*h)
	for x := 0; x < w; x++ {
		count := 0
		for y := -r; y < h; y++ {
			if ya := y + r; ya < h && horiz[ya*w+x] {
				count++
			}
			if yr := y - r - 1; yr >= 0 && horiz[yr*w+x] {
				count--
			}
			if y >= 0 {
				out[y*w+x] = count > 0
			}
		}
	}
	return out
}

// resize downscales by averaging premultiplied colors over each target pixel's
// exact footprint (box filter), so non-integer ratios stay sharp and halo-free.
func resize(src *image.NRGBA, n int) *image.NRGBA {
	sw := src.Bounds().Dx()
	if n == sw {
		return src
	}
	scale := float64(sw) / float64(n)
	dst := image.NewNRGBA(image.Rect(0, 0, n, n))
	for dy := 0; dy < n; dy++ {
		y0, y1 := float64(dy)*scale, float64(dy+1)*scale
		for dx := 0; dx < n; dx++ {
			x0, x1 := float64(dx)*scale, float64(dx+1)*scale
			var r, g, b, a, area float64
			for sy := int(y0); float64(sy) < y1 && sy < sw; sy++ {
				wy := min(y1, float64(sy+1)) - max(y0, float64(sy))
				for sx := int(x0); float64(sx) < x1 && sx < sw; sx++ {
					wt := (min(x1, float64(sx+1)) - max(x0, float64(sx))) * wy
					c := src.NRGBAAt(sx, sy)
					ca := float64(c.A) / 255 * wt
					r += float64(c.R) * ca
					g += float64(c.G) * ca
					b += float64(c.B) * ca
					a += ca
					area += wt
				}
			}
			if a == 0 {
				continue
			}
			dst.SetNRGBA(dx, dy, color.NRGBA{
				R: clamp(r / a), G: clamp(g / a), B: clamp(b / a), A: clamp(a / area * 255),
			})
		}
	}
	return dst
}

func clamp(v float64) uint8 { return uint8(min(255, v+0.5)) }

// encodeICO writes a Windows icon whose entries are PNG images (Vista and later,
// and every current browser).
func encodeICO(src *image.NRGBA, sizes []int) ([]byte, error) {
	var images [][]byte
	for _, n := range sizes {
		var buf bytes.Buffer
		if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, resize(src, n)); err != nil {
			return nil, err
		}
		images = append(images, buf.Bytes())
	}
	var out bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&out, le, [3]uint16{0, 1, uint16(len(sizes))}) // reserved, type icon, count
	offset := 6 + 16*len(sizes)
	for i, n := range sizes {
		dim := uint8(n % 256) // 0 means 256
		_ = binary.Write(&out, le, struct {
			W, H, Colors, Reserved uint8
			Planes, BPP            uint16
			Size, Offset           uint32
		}{dim, dim, 0, 0, 1, 32, uint32(len(images[i])), uint32(offset)})
		offset += len(images[i])
	}
	for _, img := range images {
		out.Write(img)
	}
	return out.Bytes(), nil
}

func previewSheet(img *image.NRGBA) *image.NRGBA {
	s := img.Bounds().Dx()
	out := image.NewNRGBA(image.Rect(0, 0, s*2, s))
	draw.Draw(out, image.Rect(0, 0, s, s), image.NewUniform(color.NRGBA{0xfa, 0xfa, 0xfa, 255}), image.Point{}, draw.Src)
	draw.Draw(out, image.Rect(s, 0, 2*s, s), image.NewUniform(color.NRGBA{0x0a, 0x0a, 0x0a, 255}), image.Point{}, draw.Src)
	draw.Draw(out, image.Rect(0, 0, s, s), img, image.Point{}, draw.Over)
	draw.Draw(out, image.Rect(s, 0, 2*s, s), img, image.Point{}, draw.Over)
	return out
}

func save(path string, img image.Image) error {
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, img); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"

	"github.com/MediaMolder/MediaMolder/av"
)

// ErrFrameDataUnavailable is deprecated. FrameToRGBA and FrameToFloat32Tensor
// are now functional via libswscale. This sentinel is kept only for backward
// compatibility with code that checks for it.
var ErrFrameDataUnavailable = errors.New("processors: av.Frame does not yet expose raw pixel data; use image-based helpers instead")

// FrameToRGBA converts a video *av.Frame to *image.RGBA using libswscale.
// Supports any pixel format that FFmpeg can convert (YUV420P, NV12, RGB24,
// etc.). Hardware-surface frames must be transferred to system memory first.
func FrameToRGBA(frame *av.Frame) (*image.RGBA, error) {
	if frame == nil {
		return nil, errors.New("processors: nil frame")
	}
	w, h := frame.Width(), frame.Height()
	if w == 0 || h == 0 {
		return nil, fmt.Errorf("processors: invalid frame dimensions %d×%d", w, h)
	}
	return frame.ToRGBA()
}

// FrameToFloat32Tensor converts a video frame to a normalised [3, H, W]
// float32 tensor (channel-first / NCHW layout without batch dim, RGB order,
// values in [0,1]) — the format expected by most ONNX / TensorRT models.
//
// The frame is first converted via [FrameToRGBA], then letterboxed to
// targetSize × targetSize, then written into the tensor.
func FrameToFloat32Tensor(frame *av.Frame, targetSize int) ([]float32, error) {
	rgba, err := FrameToRGBA(frame)
	if err != nil {
		return nil, err
	}
	return ImageToFloat32Tensor(rgba, targetSize), nil
}

// ImageToFloat32Tensor converts any image.Image to a [3, H, W] float32
// tensor (channel-first, RGB, values normalised to [0,1]).
//
// The image is first letterboxed to targetSize × targetSize (preserving
// aspect ratio, black bars). The returned slice has length 3*H*W in planar
// layout: [R-plane, G-plane, B-plane].
func ImageToFloat32Tensor(img image.Image, targetSize int) []float32 {
	return rgbaToPlanar(Letterbox(img, targetSize, targetSize))
}

// ResizeMode selects the sampler used by LetterboxTensor.
type ResizeMode int

const (
	// ResizeNearest is nearest-neighbour sampling, matching Letterbox.
	ResizeNearest ResizeMode = iota
	// ResizeBilinear is bilinear sampling inside the fitted image.
	ResizeBilinear
)

// LetterboxTensor letterboxes img to size×size and returns a planar RGB
// float32 tensor in [0, 1]. pad is the bar colour on every channel.
// ResizeNearest with pad 0 matches ImageToFloat32Tensor, including
// Letterbox's integer pad. It does not use the float reverse-mapping.
func LetterboxTensor(img image.Image, size int, pad uint8, mode ResizeMode) []float32 {
	var lb *image.RGBA
	if mode == ResizeBilinear {
		lb = letterboxBilinear(img, size, size, pad)
	} else {
		lb = letterboxNearest(img, size, size, pad)
	}
	return rgbaToPlanar(lb)
}

func rgbaToPlanar(lb *image.RGBA) []float32 {
	h := lb.Bounds().Dy()
	w := lb.Bounds().Dx()
	plane := h * w
	tensor := make([]float32, 3*plane)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := lb.RGBAAt(x, y)
			off := y*w + x
			tensor[off] = float32(c.R) / 255.0
			tensor[plane+off] = float32(c.G) / 255.0
			tensor[2*plane+off] = float32(c.B) / 255.0
		}
	}
	return tensor
}

// Letterbox resizes src to fit within targetW × targetH while preserving
// aspect ratio. The image is centered on a black background with padding
// (letterbox bars) on the shorter dimension. Uses nearest-neighbour sampling.
func Letterbox(src image.Image, targetW, targetH int) *image.RGBA {
	return letterboxNearest(src, targetW, targetH, 0)
}

// letterboxGeom is the fitted rectangle inside a letterbox canvas.
// Offsets are the integer (target-new)/2 pad Letterbox has always used.
type letterboxGeom struct {
	srcB             image.Rectangle
	scale            float64
	newW, newH       int
	offsetX, offsetY int
	origW, origH     int
}

func letterboxGeomOf(src image.Image, targetW, targetH int) (letterboxGeom, bool) {
	srcB := src.Bounds()
	origW := srcB.Dx()
	origH := srcB.Dy()
	if origW == 0 || origH == 0 {
		return letterboxGeom{}, false
	}
	scale := math.Min(float64(targetW)/float64(origW), float64(targetH)/float64(origH))
	newW := int(math.Round(float64(origW) * scale))
	newH := int(math.Round(float64(origH) * scale))
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}
	return letterboxGeom{
		srcB: srcB, scale: scale,
		newW: newW, newH: newH,
		offsetX: (targetW - newW) / 2,
		offsetY: (targetH - newH) / 2,
		origW:   origW, origH: origH,
	}, true
}

func newLetterboxCanvas(targetW, targetH int, pad uint8) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	if pad == 0 {
		return dst
	}
	for i := 0; i < len(dst.Pix); i += 4 {
		dst.Pix[i] = pad
		dst.Pix[i+1] = pad
		dst.Pix[i+2] = pad
		dst.Pix[i+3] = 255
	}
	return dst
}

func letterboxNearest(src image.Image, targetW, targetH int, pad uint8) *image.RGBA {
	g, ok := letterboxGeomOf(src, targetW, targetH)
	if !ok {
		return newLetterboxCanvas(targetW, targetH, pad)
	}
	dst := newLetterboxCanvas(targetW, targetH, pad)
	for dy := 0; dy < g.newH; dy++ {
		srcY := g.srcB.Min.Y + int(float64(dy)/g.scale+0.5)
		if srcY >= g.srcB.Max.Y {
			srcY = g.srcB.Max.Y - 1
		}
		for dx := 0; dx < g.newW; dx++ {
			srcX := g.srcB.Min.X + int(float64(dx)/g.scale+0.5)
			if srcX >= g.srcB.Max.X {
				srcX = g.srcB.Max.X - 1
			}
			r, gch, b, a := src.At(srcX, srcY).RGBA()
			dst.SetRGBA(g.offsetX+dx, g.offsetY+dy, color.RGBA{
				R: uint8(r >> 8),
				G: uint8(gch >> 8),
				B: uint8(b >> 8),
				A: uint8(a >> 8),
			})
		}
	}
	return dst
}

func letterboxBilinear(src image.Image, targetW, targetH int, pad uint8) *image.RGBA {
	g, ok := letterboxGeomOf(src, targetW, targetH)
	if !ok {
		return newLetterboxCanvas(targetW, targetH, pad)
	}
	dst := newLetterboxCanvas(targetW, targetH, pad)
	scaleX := float64(g.origW) / float64(g.newW)
	scaleY := float64(g.origH) / float64(g.newH)
	maxX := float64(g.origW - 1)
	maxY := float64(g.origH - 1)
	for dy := 0; dy < g.newH; dy++ {
		fy := (float64(dy)+0.5)*scaleY - 0.5
		if fy < 0 {
			fy = 0
		} else if fy > maxY {
			fy = maxY
		}
		y0 := int(math.Floor(fy))
		y1 := y0 + 1
		if y1 > g.origH-1 {
			y1 = g.origH - 1
		}
		ty := fy - float64(y0)
		for dx := 0; dx < g.newW; dx++ {
			fx := (float64(dx)+0.5)*scaleX - 0.5
			if fx < 0 {
				fx = 0
			} else if fx > maxX {
				fx = maxX
			}
			x0 := int(math.Floor(fx))
			x1 := x0 + 1
			if x1 > g.origW-1 {
				x1 = g.origW - 1
			}
			tx := fx - float64(x0)
			c00 := colorRGBA16(src.At(g.srcB.Min.X+x0, g.srcB.Min.Y+y0))
			c10 := colorRGBA16(src.At(g.srcB.Min.X+x1, g.srcB.Min.Y+y0))
			c01 := colorRGBA16(src.At(g.srcB.Min.X+x0, g.srcB.Min.Y+y1))
			c11 := colorRGBA16(src.At(g.srcB.Min.X+x1, g.srcB.Min.Y+y1))
			dst.SetRGBA(g.offsetX+dx, g.offsetY+dy, color.RGBA{
				R: bilinearChannel(c00, c10, c01, c11, 0, tx, ty),
				G: bilinearChannel(c00, c10, c01, c11, 1, tx, ty),
				B: bilinearChannel(c00, c10, c01, c11, 2, tx, ty),
				A: bilinearChannel(c00, c10, c01, c11, 3, tx, ty),
			})
		}
	}
	return dst
}

func colorRGBA16(c color.Color) [4]uint32 {
	r, g, b, a := c.RGBA()
	return [4]uint32{r, g, b, a}
}

func bilinearChannel(c00, c10, c01, c11 [4]uint32, ch int, tx, ty float64) uint8 {
	v := (1-tx)*(1-ty)*float64(c00[ch]) + tx*(1-ty)*float64(c10[ch]) +
		(1-tx)*ty*float64(c01[ch]) + tx*ty*float64(c11[ch])
	rounded := int(math.Round(v))
	if rounded < 0 {
		rounded = 0
	} else if rounded > 65535 {
		rounded = 65535
	}
	return uint8(rounded >> 8)
}

// DrawDetections draws bounding-box rectangles onto img for each detection.
// BBox values in each Detection are interpreted as pixel coordinates
// [x1, y1, x2, y2]. Boxes are drawn in red with a 1-pixel border.
func DrawDetections(img *image.RGBA, dets []Detection) {
	boxColor := color.RGBA{R: 255, A: 255}
	for _, d := range dets {
		x1 := clampInt(int(d.BBox[0]), 0, img.Rect.Dx()-1)
		y1 := clampInt(int(d.BBox[1]), 0, img.Rect.Dy()-1)
		x2 := clampInt(int(d.BBox[2]), 0, img.Rect.Dx()-1)
		y2 := clampInt(int(d.BBox[3]), 0, img.Rect.Dy()-1)
		drawRect(img, x1, y1, x2, y2, boxColor)
	}
}

// drawRect draws a 1-pixel rectangle outline on img.
func drawRect(img *image.RGBA, x1, y1, x2, y2 int, c color.RGBA) {
	for x := x1; x <= x2; x++ {
		img.SetRGBA(x, y1, c)
		img.SetRGBA(x, y2, c)
	}
	for y := y1 + 1; y < y2; y++ {
		img.SetRGBA(x1, y, c)
		img.SetRGBA(x2, y, c)
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

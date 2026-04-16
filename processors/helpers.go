// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"errors"
	"image"
	"image/color"
	"math"

	"github.com/MediaMolder/MediaMolder/av"
)

// ErrFrameDataUnavailable is returned by helpers that require raw pixel access
// from av.Frame. These will become functional once av.Frame exposes pixel
// plane accessors (Data, Linesize, PixFmt).
var ErrFrameDataUnavailable = errors.New("processors: av.Frame does not yet expose raw pixel data; use image-based helpers instead")

// FrameToRGBA converts a video *av.Frame to *image.RGBA.
//
// Currently returns [ErrFrameDataUnavailable] because av.Frame does not yet
// expose raw pixel planes. Once those accessors are added, this function will
// support YUV420P, RGB24, RGBA, and other common pixel formats.
//
// In the meantime, use [ImageToFloat32Tensor] and [Letterbox] directly if you
// already have an image.Image from another source.
func FrameToRGBA(frame *av.Frame) (*image.RGBA, error) {
	w, h := frame.Width(), frame.Height()
	if w == 0 || h == 0 {
		return nil, errors.New("processors: invalid frame dimensions (0×0)")
	}
	// TODO: implement pixel plane copy once av.Frame exposes Data()/Linesize()/PixFmt().
	return nil, ErrFrameDataUnavailable
}

// FrameToFloat32Tensor converts a video frame to a normalised [3, H, W]
// float32 tensor (channel-first / NCHW layout without batch dim, RGB order,
// values in [0,1]) — the format expected by most ONNX / TensorRT models.
//
// The frame is first converted via [FrameToRGBA], then letterboxed to
// targetSize × targetSize, then written into the tensor.
//
// Currently returns [ErrFrameDataUnavailable]. Use [ImageToFloat32Tensor]
// directly if you already have an image.Image.
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
	lb := Letterbox(img, targetSize, targetSize)
	H := lb.Bounds().Dy()
	W := lb.Bounds().Dx()
	plane := H * W
	tensor := make([]float32, 3*plane)
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			c := lb.RGBAAt(x, y)
			off := y*W + x
			tensor[off] = float32(c.R) / 255.0         // R plane
			tensor[plane+off] = float32(c.G) / 255.0   // G plane
			tensor[2*plane+off] = float32(c.B) / 255.0 // B plane
		}
	}
	return tensor
}

// Letterbox resizes src to fit within targetW × targetH while preserving
// aspect ratio. The image is centered on a black background with padding
// (letterbox bars) on the shorter dimension. Uses nearest-neighbour sampling.
func Letterbox(src image.Image, targetW, targetH int) *image.RGBA {
	srcB := src.Bounds()
	origW := srcB.Dx()
	origH := srcB.Dy()
	if origW == 0 || origH == 0 {
		return image.NewRGBA(image.Rect(0, 0, targetW, targetH))
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

	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	// dst is already zeroed (black) by NewRGBA.

	offsetX := (targetW - newW) / 2
	offsetY := (targetH - newH) / 2

	// Nearest-neighbour resize + place at offset.
	for dy := 0; dy < newH; dy++ {
		srcY := srcB.Min.Y + int(float64(dy)/scale+0.5)
		if srcY >= srcB.Max.Y {
			srcY = srcB.Max.Y - 1
		}
		for dx := 0; dx < newW; dx++ {
			srcX := srcB.Min.X + int(float64(dx)/scale+0.5)
			if srcX >= srcB.Max.X {
				srcX = srcB.Max.X - 1
			}
			r, g, b, a := src.At(srcX, srcY).RGBA()
			dst.SetRGBA(offsetX+dx, offsetY+dy, color.RGBA{
				R: uint8(r >> 8),
				G: uint8(g >> 8),
				B: uint8(b >> 8),
				A: uint8(a >> 8),
			})
		}
	}
	return dst
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

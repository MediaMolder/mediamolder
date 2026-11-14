// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package detect

import (
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"math"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MediaMolder/MediaMolder/face"
	"github.com/MediaMolder/MediaMolder/processors"
)

func testImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: uint8(x + y), A: 255})
		}
	}
	return img
}

func obj(class int, conf, x1, y1, x2, y2 float64) processors.Detection {
	return processors.Detection{ClassID: class, Confidence: conf, BBox: [4]float64{x1, y1, x2, y2}}
}

var (
	scriptedObjects = []processors.Detection{
		obj(ClassPerson, 0.9, 10, 20, 50, 100),
		obj(ClassSportsBall, 0.4, 60, 70, 68, 78),
		obj(16, 0.8, 1, 1, 9, 9),
	}
	scriptedFaces = []face.Face{{
		BBox:      [4]int{20, 24, 16, 18},
		Landmarks: [5][2]int{{24, 30}, {32, 30}, {28, 34}, {25, 38}, {32, 39}},
		Score:     0.75,
	}}
)

// scripted returns a detector whose models return the fixed frame above
// and count their calls.
func scripted(objCalls, faceCalls *int) *Detector {
	return &Detector{
		objects: func(image.Image) ([]processors.Detection, error) {
			*objCalls++
			return scriptedObjects, nil
		},
		faces: func(image.Image, float64) ([]face.Face, error) {
			*faceCalls++
			return scriptedFaces, nil
		},
	}
}

func TestObjectBox(t *testing.T) {
	cases := []struct {
		name string
		in   processors.Detection
		want Box
		ok   bool
	}{
		{name: "person", in: obj(0, 0.9, 10, 20, 50, 100), want: Box{X: 10, Y: 20, W: 40, H: 80, Score: 0.9}, ok: true},
		{name: "ball", in: obj(32, 0.06, 60.5, 70.25, 68.5, 78.75), want: Box{X: 60.5, Y: 70.25, W: 8, H: 8.5, Score: 0.06}, ok: true},
		{name: "other class", in: obj(1, 0.9, 10, 20, 50, 100)},
		{name: "negative class", in: obj(-1, 0.9, 10, 20, 50, 100)},
		{name: "zero width", in: obj(0, 0.9, 10, 20, 10, 100)},
		{name: "negative width", in: obj(0, 0.9, 50, 20, 10, 100)},
		{name: "zero height", in: obj(32, 0.9, 10, 20, 50, 20)},
		{name: "negative height", in: obj(32, 0.9, 10, 100, 50, 20)},
		{name: "zero confidence", in: obj(0, 0, 10, 20, 50, 100)},
		{name: "negative confidence", in: obj(0, -0.1, 10, 20, 50, 100)},
		{name: "NaN confidence", in: obj(0, math.NaN(), 10, 20, 50, 100)},
		{name: "NaN width", in: obj(0, 0.9, math.NaN(), 20, 50, 100)},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ObjectBox(tt.in)
			if ok != tt.ok || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ObjectBox = %+v, %v; want %+v, %v", got, ok, tt.want, tt.ok)
			}
			if got.Mouth != nil {
				t.Fatal("an object box has a mouth")
			}
		})
	}
}

func TestFaceBox(t *testing.T) {
	got, ok := FaceBox(scriptedFaces[0])
	if !ok {
		t.Fatal("face dropped")
	}
	if got.X != 20 || got.Y != 24 || got.W != 16 || got.H != 18 || got.Score != 0.75 {
		t.Fatalf("FaceBox = %+v", got)
	}
	if got.Mouth == nil || *got.Mouth != [2]float64{28.5, 38.5} {
		t.Fatalf("mouth = %v, want the midpoint (28.5, 38.5)", got.Mouth)
	}

	for _, bbox := range [][4]int{{20, 24, 0, 18}, {20, 24, 16, 0}, {20, 24, -3, 18}, {20, 24, 16, -3}} {
		f := scriptedFaces[0]
		f.BBox = bbox
		if _, ok := FaceBox(f); ok {
			t.Errorf("face with bbox %v was kept", bbox)
		}
	}
}

func TestDetectWantSelectsKinds(t *testing.T) {
	cases := []struct {
		name                  string
		want                  Want
		persons, balls, faces int
	}{
		{name: "persons", want: Want{Persons: true}, persons: 1},
		{name: "balls", want: Want{Balls: true}, balls: 1},
		{name: "faces", want: Want{Faces: true}, faces: 1},
		{name: "all", want: Want{Persons: true, Balls: true, Faces: true}, persons: 1, balls: 1, faces: 1},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var oc, fc int
			res, err := scripted(&oc, &fc).Detect(testImage(120, 110), tt.want)
			if err != nil {
				t.Fatal(err)
			}
			if res.Persons == nil || res.Balls == nil || res.Faces == nil {
				t.Fatalf("a kind is a nil slice: %+v", res)
			}
			if len(res.Persons) != tt.persons || len(res.Balls) != tt.balls || len(res.Faces) != tt.faces {
				t.Fatalf("got %d persons, %d balls, %d faces; want %d, %d, %d",
					len(res.Persons), len(res.Balls), len(res.Faces), tt.persons, tt.balls, tt.faces)
			}
		})
	}

	var oc, fc int
	res, err := scripted(&oc, &fc).Detect(testImage(120, 110), Want{Persons: true, Balls: true, Faces: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Box{X: 10, Y: 20, W: 40, H: 80, Score: 0.9}); !reflect.DeepEqual(res.Persons[0], want) {
		t.Errorf("person = %+v, want %+v", res.Persons[0], want)
	}
	if want := (Box{X: 60, Y: 70, W: 8, H: 8, Score: 0.4}); !reflect.DeepEqual(res.Balls[0], want) {
		t.Errorf("ball = %+v, want %+v", res.Balls[0], want)
	}
	if res.Faces[0].Mouth == nil {
		t.Error("face has no mouth")
	}
}

func TestDetectObjectsRunOnce(t *testing.T) {
	var oc, fc int
	d := scripted(&oc, &fc)
	if _, err := d.Detect(testImage(120, 110), Want{Persons: true, Balls: true}); err != nil {
		t.Fatal(err)
	}
	if oc != 1 || fc != 0 {
		t.Fatalf("persons+balls: %d object calls, %d face calls; want 1, 0", oc, fc)
	}
	if _, err := d.Detect(testImage(120, 110), Want{Faces: true}); err != nil {
		t.Fatal(err)
	}
	if oc != 1 || fc != 1 {
		t.Fatalf("faces: %d object calls, %d face calls; want 1, 1", oc, fc)
	}
}

func TestDetectMissingModel(t *testing.T) {
	var oc, fc int
	d := scripted(&oc, &fc)
	d.objects = nil
	res, err := d.Detect(testImage(8, 8), Want{Persons: true, Faces: true})
	if err == nil || !strings.Contains(err.Error(), "object model") {
		t.Fatalf("persons without an object model: %v", err)
	}
	if fc != 0 || !reflect.DeepEqual(res, Result{}) {
		t.Fatalf("faces ran %d times, result %+v", fc, res)
	}
	if d.HasObjects() || !d.HasFaces() {
		t.Fatalf("HasObjects %v, HasFaces %v", d.HasObjects(), d.HasFaces())
	}

	d = scripted(&oc, &fc)
	d.faces = nil
	res, err = d.Detect(testImage(8, 8), Want{Balls: true, Faces: true})
	if err == nil || !strings.Contains(err.Error(), "face models") {
		t.Fatalf("faces without face models: %v", err)
	}
	if oc != 0 || !reflect.DeepEqual(res, Result{}) {
		t.Fatalf("objects ran %d times, result %+v", oc, res)
	}
	if !d.HasObjects() || d.HasFaces() {
		t.Fatalf("HasObjects %v, HasFaces %v", d.HasObjects(), d.HasFaces())
	}
}

func TestDetectBadInput(t *testing.T) {
	var oc, fc int
	all := Want{Persons: true, Balls: true, Faces: true}
	cases := []struct {
		name string
		img  image.Image
		want Want
	}{
		{name: "empty want", img: testImage(8, 8), want: Want{}},
		{name: "nil image", img: nil, want: all},
		{name: "zero width", img: image.NewRGBA(image.Rect(0, 0, 0, 8)), want: all},
		{name: "zero height", img: image.NewRGBA(image.Rect(0, 0, 8, 0)), want: all},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			res, err := scripted(&oc, &fc).Detect(tt.img, tt.want)
			if err == nil || !reflect.DeepEqual(res, Result{}) {
				t.Fatalf("Detect = %+v, %v; want an error", res, err)
			}
		})
	}

	closes := 0
	d := scripted(&oc, &fc)
	d.closeFn = func() error { closes++; return nil }
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if closes != 1 {
		t.Fatalf("the model was closed %d times", closes)
	}
	if _, err := d.Detect(testImage(8, 8), all); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed detector: %v", err)
	}
	if oc != 0 || fc != 0 {
		t.Fatalf("bad input reached a model: %d object calls, %d face calls", oc, fc)
	}
}

func TestDetectModelError(t *testing.T) {
	boom := errors.New("boom")
	var oc, fc int

	d := scripted(&oc, &fc)
	d.objects = func(image.Image) ([]processors.Detection, error) { return scriptedObjects, boom }
	res, err := d.Detect(testImage(8, 8), Want{Persons: true, Faces: true})
	if !errors.Is(err, boom) || !reflect.DeepEqual(res, Result{}) {
		t.Fatalf("object error: %+v, %v", res, err)
	}

	d = scripted(&oc, &fc)
	d.faces = func(image.Image, float64) ([]face.Face, error) { return scriptedFaces, boom }
	res, err = d.Detect(testImage(8, 8), Want{Persons: true, Faces: true})
	if !errors.Is(err, boom) || !reflect.DeepEqual(res, Result{}) {
		t.Fatalf("face error: %+v, %v", res, err)
	}
}

func TestDetectSubImage(t *testing.T) {
	src := testImage(100, 80)
	sub := src.SubImage(image.Rect(40, 30, 90, 70))
	var seen []image.Image
	d := &Detector{
		objects: func(img image.Image) ([]processors.Detection, error) {
			seen = append(seen, img)
			return nil, nil
		},
		faces: func(img image.Image, _ float64) ([]face.Face, error) {
			seen = append(seen, img)
			return nil, nil
		},
	}
	if _, err := d.Detect(sub, Want{Persons: true, Faces: true}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 {
		t.Fatalf("%d model calls, want 2", len(seen))
	}
	for i, img := range seen {
		if got, want := img.Bounds(), image.Rect(0, 0, 50, 40); got != want {
			t.Fatalf("model %d saw bounds %v, want %v", i, got, want)
		}
		for y := 0; y < 40; y++ {
			for x := 0; x < 50; x++ {
				if got, want := img.At(x, y), src.At(x+40, y+30); got != want {
					t.Fatalf("model %d pixel (%d,%d) = %v, want %v", i, x, y, got, want)
				}
			}
		}
	}

	// An image already at the origin is passed through untouched.
	seen = nil
	if _, err := d.Detect(src, Want{Faces: true}); err != nil {
		t.Fatal(err)
	}
	if seen[0] != image.Image(src) {
		t.Fatal("an origin image was copied")
	}
}

func TestDetectFaceFloor(t *testing.T) {
	var got float64
	d := &Detector{faces: func(_ image.Image, conf float64) ([]face.Face, error) {
		got = conf
		return nil, nil
	}}

	d.faceConf = Config{FaceConf: 0.35}.withDefaults().FaceConf
	if _, err := d.Detect(testImage(8, 8), Want{Faces: true}); err != nil {
		t.Fatal(err)
	}
	if got != 0.35 {
		t.Fatalf("configured floor reached the model as %v", got)
	}

	d.faceConf = Config{}.withDefaults().FaceConf
	if _, err := d.Detect(testImage(8, 8), Want{Faces: true}); err != nil {
		t.Fatal(err)
	}
	if got != 0.6 {
		t.Fatalf("default floor reached the model as %v, want 0.6", got)
	}

	d.faceConf = 0
	if _, err := d.Detect(testImage(8, 8), Want{Faces: true}); err != nil {
		t.Fatal(err)
	}
	if got != 0.6 {
		t.Fatalf("unset floor reached the model as %v, want 0.6", got)
	}

	if c := (Config{}).withDefaults(); c.ObjectConf != 0.05 {
		t.Fatalf("default object floor = %v, want 0.05", c.ObjectConf)
	}
}

func TestResultEmptyJSON(t *testing.T) {
	d := &Detector{
		objects: func(image.Image) ([]processors.Detection, error) { return nil, nil },
		faces:   func(image.Image, float64) ([]face.Face, error) { return nil, nil },
	}
	res, err := d.Detect(testImage(8, 8), Want{Persons: true, Balls: true, Faces: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"persons":[],"balls":[],"faces":[]}`; string(got) != want {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
}

func TestDetectSerialized(t *testing.T) {
	var inside, overlaps atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	d := &Detector{objects: func(image.Image) ([]processors.Detection, error) {
		if inside.Add(1) > 1 {
			overlaps.Add(1)
		}
		entered <- struct{}{}
		<-release
		inside.Add(-1)
		return nil, nil
	}}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := d.Detect(testImage(8, 8), Want{Persons: true})
			errs <- err
		}()
	}

	// The first call is parked inside the model. The second can only be
	// waiting on the detector's lock, so it must not announce itself
	// until the first is released.
	<-entered
	select {
	case <-entered:
		t.Fatal("a second Detect entered the model while the first was inside")
	default:
	}
	release <- struct{}{}
	<-entered
	release <- struct{}{}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := overlaps.Load(); n != 0 {
		t.Fatalf("%d overlapping model calls", n)
	}
}

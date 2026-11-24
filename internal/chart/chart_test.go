package chart

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRenderGrid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		img := image.NewRGBA(image.Rect(0, 0, 64, 48)) // non-square on purpose
		for x := 0; x < 64; x++ {
			for y := 0; y < 48; y++ {
				img.Set(x, y, color.RGBA{200, 30, 30, 255})
			}
		}
		png.Encode(w, img)
	}))
	defer srv.Close()

	tiles := []Tile{
		{ImageURL: srv.URL + "/a.png", Title: "Night Signals", Subtitle: "Glass Harbor"},
		{ImageURL: "", Title: "No Cover"},
		{ImageURL: srv.URL + "/404-is-fine-too", Title: "Broken"},
	}
	out, err := Render(context.Background(), tiles, Options{Width: 2, Height: 2, Titles: true})
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	size := TileSize(2, 2)
	if b := img.Bounds(); b.Dx() != 2*size || b.Dy() != 2*size {
		t.Fatalf("bounds = %v", b)
	}
	// Top-left tile centre should be the red cover.
	r, g, _, _ := img.At(size/2, size/4).RGBA()
	if r>>8 < 150 || g>>8 > 80 {
		t.Errorf("expected red cover pixel, got r=%d g=%d", r>>8, g>>8)
	}

	px, err := Pixelate(context.Background(), srv.URL+"/a.png", 5)
	if err != nil || len(px) == 0 {
		t.Fatalf("pixelate: %v", err)
	}
}

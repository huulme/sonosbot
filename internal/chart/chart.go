// Package chart renders image grids of album covers or artist images.
package chart

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	_ "golang.org/x/image/webp"
)

type Tile struct {
	ImageURL string
	Title    string
	Subtitle string
}

type Options struct {
	Width, Height int // grid dimensions
	Titles        bool
}

var (
	httpClient = &http.Client{Timeout: 15 * time.Second}
	cacheMu    sync.Mutex
	imgCache   = map[string][]byte{}
	cacheOrder []string
)

const cacheCap = 600

func fetch(ctx context.Context, url string) image.Image {
	if url == "" {
		return nil
	}
	cacheMu.Lock()
	b, ok := imgCache[url]
	cacheMu.Unlock()
	if !ok {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil
		}
		req.Header.Set("User-Agent", "sonosbot/1.0")
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil
		}
		b, err = io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if err != nil {
			return nil
		}
		cacheMu.Lock()
		if _, exists := imgCache[url]; !exists {
			imgCache[url] = b
			cacheOrder = append(cacheOrder, url)
			if len(cacheOrder) > cacheCap {
				delete(imgCache, cacheOrder[0])
				cacheOrder = cacheOrder[1:]
			}
		}
		cacheMu.Unlock()
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil
	}
	return img
}

// TileSize picks a tile size that keeps the output a reasonable size.
func TileSize(w, h int) int {
	switch m := max(w, h); {
	case m <= 4:
		return 300
	case m <= 6:
		return 240
	case m <= 8:
		return 180
	default:
		return 150
	}
}

var (
	fontOnce            sync.Once
	regularTTF, boldTTF *opentype.Font
	faceMu              sync.Mutex
	faceCache           = map[float64][2]font.Face{}
)

func faces(size float64) (font.Face, font.Face) {
	fontOnce.Do(func() {
		regularTTF, _ = opentype.Parse(goregular.TTF)
		boldTTF, _ = opentype.Parse(gobold.TTF)
	})
	faceMu.Lock()
	defer faceMu.Unlock()
	if f, ok := faceCache[size]; ok {
		return f[0], f[1]
	}
	r, _ := opentype.NewFace(regularTTF, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	b, _ := opentype.NewFace(boldTTF, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	faceCache[size] = [2]font.Face{r, b}
	return r, b
}

// Render draws the grid and returns JPEG bytes.
func Render(ctx context.Context, tiles []Tile, o Options) ([]byte, error) {
	size := TileSize(o.Width, o.Height)
	canvas := image.NewRGBA(image.Rect(0, 0, o.Width*size, o.Height*size))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{color.RGBA{18, 18, 24, 255}}, image.Point{}, draw.Src)

	imgs := make([]image.Image, len(tiles))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)
	for i, t := range tiles {
		wg.Add(1)
		go func(i int, url string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			imgs[i] = fetch(ctx, url)
		}(i, t.ImageURL)
	}
	wg.Wait()

	regular, bold := faces(float64(size) / 15)
	for i, t := range tiles {
		if i >= o.Width*o.Height {
			break
		}
		x, y := (i%o.Width)*size, (i/o.Width)*size
		rect := image.Rect(x, y, x+size, y+size)
		if img := imgs[i]; img != nil {
			xdraw.CatmullRom.Scale(canvas, rect, img, cropSquare(img.Bounds()), draw.Over, nil)
		} else {
			draw.Draw(canvas, rect, &image.Uniform{placeholderColor(t.Title)}, image.Point{}, draw.Src)
			if !o.Titles {
				drawCentered(canvas, bold, t.Title, rect)
			}
		}
		if o.Titles {
			shade(canvas, rect, size)
			lh := size / 13
			by := y + size - lh/2 - 4
			if t.Subtitle != "" {
				drawText(canvas, regular, t.Subtitle, x+6, by, size-12)
				by -= lh + 2
			}
			drawText(canvas, bold, t.Title, x+6, by, size-12)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: 88}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Pixelate downloads an image and renders it as blocks x blocks pixels, upscaled to 512px.
func Pixelate(ctx context.Context, url string, blocks int) ([]byte, error) {
	img := fetch(ctx, url)
	if img == nil {
		return nil, errNoImage
	}
	small := image.NewRGBA(image.Rect(0, 0, blocks, blocks))
	xdraw.ApproxBiLinear.Scale(small, small.Bounds(), img, cropSquare(img.Bounds()), draw.Src, nil)
	out := image.NewRGBA(image.Rect(0, 0, 512, 512))
	xdraw.NearestNeighbor.Scale(out, out.Bounds(), small, small.Bounds(), draw.Src, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var errNoImage = errors.New("image unavailable")

func cropSquare(b image.Rectangle) image.Rectangle {
	w, h := b.Dx(), b.Dy()
	if w == h {
		return b
	}
	if w > h {
		off := (w - h) / 2
		return image.Rect(b.Min.X+off, b.Min.Y, b.Min.X+off+h, b.Max.Y)
	}
	off := (h - w) / 2
	return image.Rect(b.Min.X, b.Min.Y+off, b.Max.X, b.Min.Y+off+w)
}

func placeholderColor(s string) color.RGBA {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h = (h ^ uint32(s[i])) * 16777619
	}
	return color.RGBA{uint8(40 + h%60), uint8(40 + (h>>8)%60), uint8(70 + (h>>16)%80), 255}
}

// shade darkens the bottom of a tile so text stays readable.
func shade(dst *image.RGBA, r image.Rectangle, size int) {
	start := r.Max.Y - size*2/5
	for y := start; y < r.Max.Y; y++ {
		a := uint8(200 * (y - start) / (r.Max.Y - start))
		for x := r.Min.X; x < r.Max.X; x++ {
			c := dst.RGBAAt(x, y)
			c.R = uint8(int(c.R) * int(255-a) / 255)
			c.G = uint8(int(c.G) * int(255-a) / 255)
			c.B = uint8(int(c.B) * int(255-a) / 255)
			dst.SetRGBA(x, y, c)
		}
	}
}

func fit(face font.Face, s string, maxW int) string {
	if font.MeasureString(face, s).Ceil() <= maxW {
		return s
	}
	r := []rune(s)
	for len(r) > 1 {
		r = r[:len(r)-1]
		t := string(r) + "…"
		if font.MeasureString(face, t).Ceil() <= maxW {
			return t
		}
	}
	return ""
}

func drawText(dst draw.Image, face font.Face, s string, x, y, maxW int) {
	s = fit(face, s, maxW)
	// 1px shadow then text.
	for _, off := range []struct {
		dx, dy int
		c      color.Color
	}{{1, 1, color.RGBA{0, 0, 0, 200}}, {0, 0, color.White}} {
		d := &font.Drawer{Dst: dst, Src: image.NewUniform(off.c), Face: face,
			Dot: fixed.P(x+off.dx, y+off.dy)}
		d.DrawString(s)
	}
}

func drawCentered(dst draw.Image, face font.Face, s string, r image.Rectangle) {
	s = fit(face, s, r.Dx()-12)
	w := font.MeasureString(face, s).Ceil()
	x := r.Min.X + (r.Dx()-w)/2
	y := r.Min.Y + r.Dy()/2
	drawText(dst, face, s, x, y, r.Dx()-12)
}

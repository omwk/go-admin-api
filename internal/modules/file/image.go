package file

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/draw"
	_ "image/gif" // Register the supported GIF decoder.
	"image/jpeg"
	_ "image/png" // Register the supported PNG decoder.
	"io"
	"time"
)

const (
	imageInputLimit = 4 << 20
	imagePixelLimit = 4_000_000
	thumbnailBudget = 1500 * time.Millisecond
)

type imageResult struct {
	data []byte
	err  error
}

// renderImage limits decoded allocations and lets optional previews meet their deadline.
func renderImage(ctx context.Context, data []byte, side int, square bool) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) > imageInputLimit {
		return nil, errors.New("image input too large")
	}
	result := make(chan imageResult, 1)
	go func() {
		out, err := decodeImage(ctx, data, side, square)
		result <- imageResult{out, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case out := <-result:
		return out.data, out.err
	}
}

func decodeImage(ctx context.Context, data []byte, side int, square bool) ([]byte, error) {
	return decodeImageLimit(ctx, data, side, square, imagePixelLimit)
}

func decodeImageLimit(ctx context.Context, data []byte, side int, square bool, pixels int64) ([]byte, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if (format != "jpeg" && format != "png" && format != "gif") || config.Width <= 0 || config.Height <= 0 || int64(config.Width) > pixels/int64(config.Height) {
		return nil, errors.New("unsupported image dimensions or encoding")
	}
	source, _, err := image.Decode(&imageReader{ctx: ctx, Reader: bytes.NewReader(data)})
	if err != nil {
		return nil, err
	}
	output, err := resizeImage(ctx, source, side, square)
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, output, &jpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	return buffer.Bytes(), ctx.Err()
}

type imageReader struct {
	ctx context.Context
	io.Reader
}

func (r *imageReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func resizeImage(ctx context.Context, source image.Image, side int, square bool) (image.Image, error) {
	bounds := source.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if square {
		crop := min(w, h)
		bounds = image.Rect(bounds.Min.X+(w-crop)/2, bounds.Min.Y+(h-crop)/2, bounds.Min.X+(w+crop)/2, bounds.Min.Y+(h+crop)/2)
		w, h = side, side
	} else if max(w, h) > side {
		w, h = max(1, w*side/max(w, h)), max(1, h*side/max(w, h))
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := 0; x < w; x++ {
			out.Set(x, y, source.At(bounds.Min.X+x*bounds.Dx()/w, bounds.Min.Y+y*bounds.Dy()/h))
		}
	}
	// Composite transparency on white before JPEG encoding.
	opaque := image.NewRGBA(out.Bounds())
	draw.Draw(opaque, opaque.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(opaque, opaque.Bounds(), out, image.Point{}, draw.Over)
	return opaque, nil
}

func (s *Service) thumbnail(ctx context.Context, rowKey string, data []byte) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, thumbnailBudget)
	defer cancel()
	thumbnail, err := renderImage(ctx, data, 320, false)
	if err != nil {
		return "", err
	}
	key := rowKey + ".thumb.jpg"
	created, err := s.object.Put(ctx, key, "image/jpeg", bytes.NewReader(thumbnail), int64(len(thumbnail)))
	if err != nil {
		return "", err
	}
	if !created {
		return "", errors.New("thumbnail object already exists")
	}
	return key, nil
}

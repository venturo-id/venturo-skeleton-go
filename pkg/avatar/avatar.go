// Package avatar turns profile photos into the one shape the apps show — a
// JPEG of at most MaxSide pixels a side — and keeps them in object storage
// (see Store). A photo either comes from the user as an upload, or is
// copied from their Google account at sign-in (FetchGooglePicture).
package avatar

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // registers the PNG decoder
	"io"

	"golang.org/x/image/draw"
	"golang.org/x/image/riff"
	"golang.org/x/image/vp8"
	"golang.org/x/image/vp8l"
	_ "golang.org/x/image/webp" // registers the WebP decoder
)

const (
	// MaxSide is the longest side of a stored avatar. A larger image is
	// scaled down to it; the client usually crops to a square this size
	// already.
	MaxSide = 512
	// MaxSourceSide is the longest side of an image Normalize accepts. It
	// is checked before decoding, so a small file claiming a huge size (a
	// decompression bomb) never gets its pixels allocated.
	MaxSourceSide = 4096

	jpegQuality = 88
)

// ErrInvalidImage is returned for data that is not a JPEG, PNG or WebP
// image, or whose size is zero or above MaxSourceSide.
var ErrInvalidImage = errors.New("avatar: not a supported image")

// formats are the image formats Normalize accepts. Checked by name, since
// any package in the binary can register another decoder with image.
var formats = map[string]bool{"jpeg": true, "png": true, "webp": true}

// Normalize decodes a JPEG, PNG or WebP image and re-encodes it as a JPEG,
// scaled down so its longest side is at most MaxSide. Transparency is
// flattened onto white. Re-encoding drops every metadata block (EXIF, GPS,
// ICC, XMP); an EXIF orientation is not applied, so the client sends the
// pixels the way they should be shown.
func Normalize(data []byte) (out []byte, err error) {
	// The decoders are fuzzed, but the bytes come from strangers: a panic
	// in one is a bad image, not a failed request.
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("%w: decoder panic: %v", ErrInvalidImage, r)
		}
	}()

	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidImage, err)
	}
	if !formats[format] {
		return nil, fmt.Errorf("%w: format %s", ErrInvalidImage, format)
	}
	if err := checkSize(cfg.Width, cfg.Height); err != nil {
		return nil, err
	}
	if format == "webp" {
		// The header DecodeConfig reads in an extended WebP file is only the
		// canvas; Decode allocates for the frame inside, which may claim a
		// different, far larger size.
		w, h, err := webpFrameSize(data)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidImage, err)
		}
		if w != cfg.Width || h != cfg.Height {
			return nil, fmt.Errorf("%w: webp frame is %dx%d on a %dx%d canvas", ErrInvalidImage, w, h, cfg.Width, cfg.Height)
		}
	}

	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidImage, err)
	}

	sb := src.Bounds()
	w, h := fit(sb.Dx(), sb.Dy())
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	if w == sb.Dx() && h == sb.Dy() {
		draw.Draw(dst, dst.Bounds(), src, sb.Min, draw.Over)
	} else {
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, sb, draw.Over, nil)
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, fmt.Errorf("avatar: encode jpeg: %w", err)
	}
	return buf.Bytes(), nil
}

// checkSize rejects an empty image and one larger than MaxSourceSide.
func checkSize(w, h int) error {
	if w <= 0 || h <= 0 || w > MaxSourceSide || h > MaxSourceSide {
		return fmt.Errorf("%w: %dx%d is outside 1-%d pixels a side", ErrInvalidImage, w, h, MaxSourceSide)
	}
	return nil
}

// fit returns w×h scaled down, keeping the aspect ratio, so that the
// longest side is MaxSide; a smaller image keeps its size.
func fit(w, h int) (int, int) {
	switch {
	case w <= MaxSide && h <= MaxSide:
		return w, h
	case w >= h:
		return MaxSide, max(1, (h*MaxSide+w/2)/w)
	default:
		return max(1, (w*MaxSide+h/2)/h), MaxSide
	}
}

// webpFrameSize reads the size of the image frame (VP8 or VP8L chunk) of a
// WebP file without decoding it.
func webpFrameSize(data []byte) (int, int, error) {
	_, chunks, err := riff.NewReader(bytes.NewReader(data))
	if err != nil {
		return 0, 0, err
	}
	for {
		id, size, chunk, err := chunks.Next()
		if err == io.EOF {
			return 0, 0, errors.New("webp has no image frame")
		}
		if err != nil {
			return 0, 0, err
		}
		switch id {
		case riff.FourCC{'V', 'P', '8', ' '}:
			d := vp8.NewDecoder()
			d.Init(chunk, int(size))
			fh, err := d.DecodeFrameHeader()
			return fh.Width, fh.Height, err
		case riff.FourCC{'V', 'P', '8', 'L'}:
			cfg, err := vp8l.DecodeConfig(chunk)
			return cfg.Width, cfg.Height, err
		}
	}
}

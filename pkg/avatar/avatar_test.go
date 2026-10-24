package avatar

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// Tiny WebP files: a lossy (VP8) and a lossless (VP8L) 1×1, and a 1×1 with
// alpha in the extended format (VP8X + ALPH + VP8).
const (
	webpLossy    = "UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA"
	webpLossless = "UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA=="
	webpAlpha    = "UklGRkoAAABXRUJQVlA4WAoAAAAQAAAAAAAAAAAAQUxQSAwAAAARBxAR/Q9ERP8DAABWUDggGAAAABQBAJ0BKgEAAQAAAP4AAA3AAP7mtQAAAA=="
)

// solid returns a w×h image filled with c.
func solid(w, h int, c color.Color) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("jpeg.Encode() error = %v", err)
	}
	return buf.Bytes()
}

// decodeJPEG decodes Normalize's output, failing unless it is a JPEG.
func decodeJPEG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("output does not decode: %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("output format = %s, want jpeg", format)
	}
	return img
}

// pngHeader returns a PNG signature and IHDR chunk claiming w×h, without
// any pixel data: what DecodeConfig reads, nothing more.
func pngHeader(w, h uint32) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6 // 8-bit RGBA

	var buf bytes.Buffer
	buf.WriteString("\x89PNG\r\n\x1a\n")
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(ihdr)))
	chunk := append([]byte("IHDR"), ihdr...)
	buf.Write(chunk)
	_ = binary.Write(&buf, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return buf.Bytes()
}

func TestNormalize_Formats(t *testing.T) {
	webp := func(s string) []byte {
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			t.Fatalf("bad fixture: %v", err)
		}
		return b
	}

	tests := []struct {
		name         string
		data         []byte
		wantW, wantH int
	}{
		{"jpeg", encodeJPEG(t, solid(200, 100, color.NRGBA{R: 200, G: 30, B: 30, A: 255})), 200, 100},
		{"png", encodePNG(t, solid(64, 48, color.NRGBA{R: 30, G: 200, B: 30, A: 255})), 64, 48},
		{"webp lossy", webp(webpLossy), 1, 1},
		{"webp lossless", webp(webpLossless), 1, 1},
		{"webp extended with alpha", webp(webpAlpha), 1, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := Normalize(tt.data)
			if err != nil {
				t.Fatalf("Normalize() error = %v", err)
			}
			b := decodeJPEG(t, out).Bounds()
			if b.Dx() != tt.wantW || b.Dy() != tt.wantH {
				t.Errorf("size = %dx%d, want %dx%d", b.Dx(), b.Dy(), tt.wantW, tt.wantH)
			}
		})
	}
}

func TestNormalize_FlattensAlphaOntoWhite(t *testing.T) {
	out, err := Normalize(encodePNG(t, solid(32, 32, color.NRGBA{R: 0, G: 0, B: 0, A: 0})))
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}

	r, g, b, _ := decodeJPEG(t, out).At(16, 16).RGBA()
	// JPEG is lossy: near white is white.
	if r>>8 < 250 || g>>8 < 250 || b>>8 < 250 {
		t.Errorf("transparent pixel became (%d, %d, %d), want white", r>>8, g>>8, b>>8)
	}
}

func TestNormalize_ScalesDown(t *testing.T) {
	tests := []struct {
		w, h         int
		wantW, wantH int
	}{
		{1024, 1024, 512, 512},
		{1024, 768, 512, 384},
		{768, 1024, 384, 512},
		{4096, 8, 512, 1},
		{512, 512, 512, 512},
		{300, 200, 300, 200},
	}

	for _, tt := range tests {
		out, err := Normalize(encodePNG(t, solid(tt.w, tt.h, color.NRGBA{R: 10, G: 20, B: 200, A: 255})))
		if err != nil {
			t.Fatalf("Normalize(%dx%d) error = %v", tt.w, tt.h, err)
		}
		b := decodeJPEG(t, out).Bounds()
		if b.Dx() != tt.wantW || b.Dy() != tt.wantH {
			t.Errorf("Normalize(%dx%d) size = %dx%d, want %dx%d", tt.w, tt.h, b.Dx(), b.Dy(), tt.wantW, tt.wantH)
		}
	}
}

func TestNormalize_StripsMetadata(t *testing.T) {
	src := encodeJPEG(t, solid(16, 16, color.NRGBA{R: 90, G: 90, B: 90, A: 255}))

	// An EXIF segment (APP1) right after the start-of-image marker.
	payload := []byte("Exif\x00\x00GPS 6.2088 S 106.8456 E")
	app1 := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(app1[2:], uint16(len(payload)+2))
	withExif := append(append(append([]byte{}, src[:2]...), append(app1, payload...)...), src[2:]...)

	out, err := Normalize(withExif)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if bytes.Contains(out, []byte("Exif")) || bytes.Contains(out, []byte("106.8456")) {
		t.Error("output still carries the EXIF segment")
	}
}

func TestNormalize_Rejects(t *testing.T) {
	gif := []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;")

	// An extended WebP whose canvas (VP8X) is 1×1 while its frame (VP8L)
	// claims 16000×16000.
	vp8x := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0} // flags, reserved, canvas 1×1
	vp8l := make([]byte, 5)
	vp8l[0] = 0x2f
	binary.LittleEndian.PutUint32(vp8l[1:], uint32(16000-1)|uint32(16000-1)<<14)
	var body bytes.Buffer
	body.WriteString("WEBP")
	for _, c := range []struct {
		id   string
		data []byte
	}{{"VP8X", vp8x}, {"VP8L", vp8l}} {
		body.WriteString(c.id)
		_ = binary.Write(&body, binary.LittleEndian, uint32(len(c.data)))
		body.Write(c.data)
		if len(c.data)%2 == 1 {
			body.WriteByte(0)
		}
	}
	var bomb bytes.Buffer
	bomb.WriteString("RIFF")
	_ = binary.Write(&bomb, binary.LittleEndian, uint32(body.Len()))
	bomb.Write(body.Bytes())

	tests := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"garbage", []byte("definitely not an image")},
		{"truncated png", encodePNG(t, solid(8, 8, color.White))[:40]},
		{"gif", gif},
		{"png claiming 10000x10000", pngHeader(10000, 10000)},
		{"png wider than the limit", pngHeader(MaxSourceSide+1, 1)},
		{"png taller than the limit", pngHeader(1, MaxSourceSide+1)},
		{"webp frame larger than its canvas", bomb.Bytes()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := Normalize(tt.data)
			if !errors.Is(err, ErrInvalidImage) {
				t.Errorf("Normalize() error = %v, want ErrInvalidImage", err)
			}
			if out != nil {
				t.Errorf("Normalize() returned %d bytes with an error", len(out))
			}
		})
	}
}

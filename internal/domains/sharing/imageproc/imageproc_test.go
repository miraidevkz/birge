package imageproc_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"birge/internal/domains/sharing/imageproc"
	"birge/internal/domains/sharing/model"
)

// jpegWithEXIF builds a tiny JPEG that carries an APP1 Exif segment so tests
// can assert Process strips it (ADR-0015).
func jpegWithEXIF(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 8), B: 40, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	if len(raw) < 4 || raw[0] != 0xFF || raw[1] != 0xD8 {
		t.Fatal("expected JPEG SOI")
	}
	// APP1 length includes the two length bytes; payload = "Exif\0\0" + 8 pad.
	app1 := []byte{0xFF, 0xE1, 0x00, 0x10, 'E', 'x', 'i', 'f', 0, 0, 1, 2, 3, 4, 5, 6, 7, 8}
	out := make([]byte, 0, 2+len(app1)+len(raw)-2)
	out = append(out, 0xFF, 0xD8)
	out = append(out, app1...)
	out = append(out, raw[2:]...)
	if !bytes.Contains(out, []byte("Exif")) {
		t.Fatal("fixture missing Exif marker")
	}
	return out
}

func TestProcessStripsEXIF(t *testing.T) {
	raw := jpegWithEXIF(t)
	out, err := (imageproc.Processor{}).Process(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("Exif")) {
		t.Fatal("processed JPEG still contains Exif")
	}
	if len(out) < 4 || out[0] != 0xFF || out[1] != 0xD8 {
		t.Fatal("processed output is not a JPEG")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil || format != "jpeg" {
		t.Fatalf("decode processed: format=%s err=%v", format, err)
	}
	if cfg.Width > 1600 || cfg.Height > 1600 {
		t.Fatalf("dimension too large: %dx%d", cfg.Width, cfg.Height)
	}
}

func TestProcessRejectsGarbage(t *testing.T) {
	_, err := (imageproc.Processor{}).Process(context.Background(), []byte("not-an-image"))
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestProcessRejectsDecompressionBomb builds a tiny PNG header that declares a
// huge image; Process must refuse it from the header, before decoding pixels.
func TestProcessRejectsDecompressionBomb(t *testing.T) {
	var buf bytes.Buffer
	buf.Write([]byte("\x89PNG\r\n\x1a\n"))
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], 30000)
	binary.BigEndian.PutUint32(ihdr[4:8], 30000)
	ihdr[8], ihdr[9] = 8, 2 // 8-bit RGB
	chunk := func(typ string, data []byte) {
		_ = binary.Write(&buf, binary.BigEndian, uint32(len(data)))
		buf.WriteString(typ)
		buf.Write(data)
		crc := crc32.NewIEEE()
		crc.Write([]byte(typ))
		crc.Write(data)
		_ = binary.Write(&buf, binary.BigEndian, crc.Sum32())
	}
	chunk("IHDR", ihdr)
	chunk("IEND", nil)
	if _, err := (imageproc.Processor{}).Process(context.Background(), buf.Bytes()); !errors.Is(err, model.ErrPhoto) {
		t.Fatalf("Process(bomb) error = %v, want ErrPhoto", err)
	}
}

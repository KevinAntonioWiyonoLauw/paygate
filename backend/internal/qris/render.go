package qris

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"

	qrcode "github.com/skip2/go-qrcode"
)

// RenderOptions controls PNG output.
type RenderOptions struct {
	// Size is the output edge length in pixels. 0 uses 512.
	Size int
	// QuietZone is the border in modules. Negative uses the library default (4).
	QuietZone int
	// Foreground overrides the module colour. Defaults to near-black.
	Foreground color.Color
	// Background overrides the light colour. Defaults to white.
	Background color.Color
}

// RenderPNG encodes a QR payload as a PNG byte slice.
func RenderPNG(payload string, opts RenderOptions) ([]byte, error) {
	if payload == "" {
		return nil, fmt.Errorf("qris: empty payload")
	}
	size := opts.Size
	if size <= 0 {
		size = 512
	}
	if size < 64 || size > 2048 {
		return nil, fmt.Errorf("qris: size %d out of range 64..2048", size)
	}

	code, err := qrcode.New(payload, qrcode.Medium)
	if err != nil {
		return nil, fmt.Errorf("qris: encode payload: %w", err)
	}
	// skip2/go-qrcode hardcodes a 4-module quiet zone and only exposes colour
	// control; the option is kept for the SVG path and future encoders.
	_ = opts.QuietZone
	if opts.Foreground != nil {
		code.ForegroundColor = opts.Foreground
	}
	if opts.Background != nil {
		code.BackgroundColor = opts.Background
	}

	img := code.Image(size)
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("qris: encode png: %w", err)
	}
	return buf.Bytes(), nil
}

// RenderDataURI returns a data: URL suitable for an <img src>.
func RenderDataURI(payload string, opts RenderOptions) (string, error) {
	raw, err := RenderPNG(payload, opts)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw), nil
}

// Modules returns the QR matrix, for tests and for SVG output.
func Modules(payload string) ([][]bool, error) {
	code, err := qrcode.New(payload, qrcode.Medium)
	if err != nil {
		return nil, err
	}
	bitmap := code.Bitmap()
	matrix := make([][]bool, len(bitmap))
	for y, row := range bitmap {
		matrix[y] = make([]bool, len(row))
		copy(matrix[y], row)
	}
	return matrix, nil
}

// RenderSVG builds a crisp, scalable SVG of the payload. Preferred over PNG for
// print and for retina screens: no resampling, no blur.
func RenderSVG(payload string, moduleSize int) ([]byte, error) {
	if moduleSize <= 0 {
		moduleSize = 8
	}
	matrix, err := Modules(payload)
	if err != nil {
		return nil, err
	}
	quiet := 4
	dim := (len(matrix) + quiet*2) * moduleSize

	var b bytes.Buffer
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" shape-rendering="crispEdges">`,
		dim, dim, dim, dim)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#ffffff"/>`, dim, dim)
	b.WriteString(`<path fill="#000000" d="`)
	for y, row := range matrix {
		for x, on := range row {
			if !on {
				continue
			}
			fmt.Fprintf(&b, "M%d %dh%dv%dh-%dz", (x+quiet)*moduleSize, (y+quiet)*moduleSize,
				moduleSize, moduleSize, moduleSize)
		}
	}
	b.WriteString(`"/></svg>`)
	return b.Bytes(), nil
}

// ensure image import stays used if render paths change.
var _ image.Image = (*image.RGBA)(nil)

package main

import (
	"bytes"
	"encoding/binary"
	"image"

	"github.com/malashin/dds"
)

// Mod focus icons are very often DXT compressed at sizes like 94x82 that are
// not a multiple of four. The game reads them fine, but the DDS decoder we use
// refuses them outright, so those icons all came out as the unknown goal
// placeholder.
//
// The pixel data in such a file is already stored padded up to whole 4x4
// blocks, so the fix is to hand the decoder a header rounded up to the block
// grid and crop the result back to the real size.

const (
	ddsMagicLen      = 4
	ddsHeaderLen     = 124
	offHeight        = 4 + 8
	offWidth         = 4 + 12
	offLinearSize    = 4 + 16
	offPixelFormatCC = 4 + 72 + 8
)

// decodeDDSPadded decodes a DDS whose dimensions are not block aligned.
// It returns false when the file is not a DDS, is already aligned, or cannot
// be repaired.
func decodeDDSPadded(data []byte) (image.Image, bool) {
	if len(data) < ddsMagicLen+ddsHeaderLen {
		return nil, false
	}
	if string(data[:4]) != "DDS " {
		return nil, false
	}

	height := int(binary.LittleEndian.Uint32(data[offHeight:]))
	width := int(binary.LittleEndian.Uint32(data[offWidth:]))
	if width <= 0 || height <= 0 {
		return nil, false
	}
	if width%4 == 0 && height%4 == 0 {
		return nil, false
	}

	var blockBytes int
	switch string(data[offPixelFormatCC : offPixelFormatCC+4]) {
	case "DXT1":
		blockBytes = 8
	case "DXT3", "DXT5":
		blockBytes = 16
	default:
		// Uncompressed formats do not have the block restriction.
		return nil, false
	}

	paddedW := (width + 3) / 4 * 4
	paddedH := (height + 3) / 4 * 4
	linearSize := (paddedW / 4) * (paddedH / 4) * blockBytes

	if len(data) < ddsMagicLen+ddsHeaderLen+linearSize {
		return nil, false
	}

	patched := make([]byte, len(data))
	copy(patched, data)
	binary.LittleEndian.PutUint32(patched[offHeight:], uint32(paddedH))
	binary.LittleEndian.PutUint32(patched[offWidth:], uint32(paddedW))
	binary.LittleEndian.PutUint32(patched[offLinearSize:], uint32(linearSize))

	img, err := dds.Decode(bytes.NewReader(patched))
	if err != nil {
		return nil, false
	}

	sub, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	})
	if !ok {
		return img, true
	}
	cropped := sub.SubImage(image.Rect(0, 0, width, height))

	// SubImage keeps the parent bounds origin; normalise so callers can treat
	// it like any other image.
	out := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			out.Set(x, y, cropped.At(x, y))
		}
	}
	return out, true
}

package main

import (
	"image"
	"image/color"
	"image/draw"
	"strings"

	"github.com/KeKsBoTer/gofnt"
	"github.com/malashin/bmfonter"
)

// Colour code support for focus titles.
//
// HOI4 localisation marks coloured runs with "§" plus a one letter code and
// ends them with "§!". Those markers used to be drawn as literal characters in
// the middle of the focus name. They are now parsed out and each glyph is
// tinted with the matching colour.

// hoi4TextColors are the game's built in text colours.
var hoi4TextColors = map[rune]color.RGBA{
	'W': {255, 255, 255, 255}, // white
	'b': {0, 0, 0, 255},       // black
	'B': {76, 146, 255, 255},  // blue
	'G': {70, 220, 70, 255},   // green
	'R': {225, 55, 55, 255},   // red
	'Y': {245, 225, 70, 255},  // yellow
	'H': {255, 189, 0, 255},   // gold highlight
	'O': {255, 140, 40, 255},  // orange
	'g': {160, 160, 160, 255}, // grey
	'T': {120, 120, 120, 255}, // dark grey
	'L': {130, 200, 255, 255}, // light blue
	'P': {190, 120, 255, 255}, // purple
	'C': {90, 220, 220, 255},  // cyan
}

// textRun is a single character together with the colour it should be drawn
// in. A zero alpha means "use the font's own colouring".
type textRun struct {
	r   rune
	col color.RGBA
}

// stripColorCodes removes the markers without applying them, used when colour
// rendering is turned off.
func stripColorCodes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] == '§' {
			if i+1 < len(rs) {
				i++
			}
			continue
		}
		b.WriteRune(rs[i])
	}
	return b.String()
}

// parseColoredText splits a localisation string into coloured runs.
func parseColoredText(s string, enabled bool) []textRun {
	if !enabled {
		s = stripColorCodes(s)
	}

	rs := []rune(s)
	runs := make([]textRun, 0, len(rs))
	var current color.RGBA

	for i := 0; i < len(rs); i++ {
		if enabled && rs[i] == '§' {
			if i+1 >= len(rs) {
				break
			}
			code := rs[i+1]
			i++
			if code == '!' {
				current = color.RGBA{}
				continue
			}
			if c, ok := hoi4TextColors[code]; ok {
				current = c
			} else {
				// Unknown code: fall back to the default colour rather than
				// printing the marker.
				current = color.RGBA{}
			}
			continue
		}
		runs = append(runs, textRun{r: rs[i], col: current})
	}
	return runs
}

// glyphFontFor returns the font (main or sub font) that actually has a glyph
// for r, mirroring how bmfonter picks one.
func glyphFontFor(f *bmfonter.Font, r rune) *bmfonter.Font {
	for i := range f.Subfonts {
		if _, ok := f.Subfonts[i].Chars[int(r)]; ok {
			return &f.Subfonts[i]
		}
	}
	return f
}

func glyphFor(f *bmfonter.Font, r rune) (gofnt.Char, *bmfonter.Font) {
	gf := glyphFontFor(f, r)
	return gf.Chars[int(r)], gf
}

func advanceOf(f *bmfonter.Font, r rune) int {
	c, _ := glyphFor(f, r)
	return c.XAdvanced
}

func lineHeightOf(f *bmfonter.Font) int {
	h := f.Font.Common.LineHeight
	if h <= 0 {
		h = 16
	}
	return h
}

// wrapRuns breaks the runs into lines that fit into width, honouring explicit
// newlines and splitting words that are too long to fit on their own.
func wrapRuns(f *bmfonter.Font, runs []textRun, width int) [][]textRun {
	if width <= 0 {
		width = 1 << 30
	}
	spaceWidth := advanceOf(f, ' ')

	var lines [][]textRun
	var line []textRun
	lineWidth := 0

	flush := func() {
		lines = append(lines, line)
		line = nil
		lineWidth = 0
	}

	// Split into words, keeping explicit newlines as their own marker.
	var word []textRun
	wordWidth := 0

	pushWord := func() {
		if len(word) == 0 {
			return
		}
		sep := 0
		if len(line) > 0 {
			sep = spaceWidth
		}
		if len(line) > 0 && lineWidth+sep+wordWidth > width {
			flush()
			sep = 0
		}
		// A single word wider than the box has to be cut.
		for wordWidth > width && len(word) > 1 {
			cut := 0
			w := 0
			for cut < len(word) {
				a := advanceOf(f, word[cut].r)
				if w+a > width && cut > 0 {
					break
				}
				w += a
				cut++
			}
			if len(line) > 0 {
				flush()
			}
			line = append(line, word[:cut]...)
			flush()
			word = word[cut:]
			wordWidth = 0
			for _, r := range word {
				wordWidth += advanceOf(f, r.r)
			}
			sep = 0
		}
		if len(line) > 0 {
			line = append(line, textRun{r: ' '})
			lineWidth += spaceWidth
		}
		line = append(line, word...)
		lineWidth += wordWidth
		word = nil
		wordWidth = 0
	}

	for _, tr := range runs {
		switch tr.r {
		case '\n':
			pushWord()
			flush()
		case ' ', '\t':
			pushWord()
		default:
			word = append(word, tr)
			wordWidth += advanceOf(f, tr.r)
		}
	}
	pushWord()
	if len(line) > 0 {
		flush()
	}
	return lines
}

func lineWidthOf(f *bmfonter.Font, line []textRun) int {
	w := 0
	for _, tr := range line {
		w += advanceOf(f, tr.r)
	}
	return w
}

// renderColoredTextBox draws text into a box, wrapping and centring it the way
// the game does, with per character colours.
func renderColoredTextBox(dst draw.Image, f *bmfonter.Font, x, y, width, height int, centeredX, centeredY bool, s string, colored bool) {
	if f == nil || len(f.Chars) == 0 {
		return
	}
	runs := parseColoredText(s, colored)
	if len(runs) == 0 {
		return
	}

	lines := wrapRuns(f, runs, width)
	lh := lineHeightOf(f)

	// Keep the same overflow behaviour as before: never grow past the box by
	// more than one extra line.
	if height > 0 {
		maxLines := height/lh + 1
		if maxLines < 1 {
			maxLines = 1
		}
		if len(lines) > maxLines {
			lines = lines[:maxLines]
		}
	}

	if centeredY {
		y -= len(lines) * lh / 2
	}

	for _, line := range lines {
		lx := x
		if centeredX {
			lx -= lineWidthOf(f, line) / 2
		}
		for _, tr := range line {
			lx += drawGlyph(dst, f, lx, y, tr.r, tr.col)
		}
		y += lh
	}
}

// drawGlyph draws one character and returns how far to advance.
func drawGlyph(dst draw.Image, f *bmfonter.Font, x, y int, r rune, col color.RGBA) int {
	c, gf := glyphFor(f, r)
	if c.Width <= 0 || c.Height <= 0 {
		return c.XAdvanced
	}

	rect := image.Rect(x+c.XOffset, y+c.YOffset, x+c.Width+c.XOffset, y+c.Height+c.YOffset)
	sp := image.Point{X: c.X, Y: c.Y}

	if col.A == 0 {
		draw.Draw(dst, rect, gf.Image, sp, draw.Over)
		return c.XAdvanced
	}
	drawTinted(dst, rect, gf.Image, sp, col)
	return c.XAdvanced
}

// drawTinted composites a glyph over dst multiplying its colour channels by
// col. Multiplying rather than replacing keeps the dark outline the HOI4 font
// atlas bakes into every glyph, so coloured text still reads over any plaque.
func drawTinted(dst draw.Image, r image.Rectangle, src image.Image, sp image.Point, col color.RGBA) {
	r = r.Intersect(dst.Bounds())
	if r.Empty() {
		return
	}
	offset := sp.Sub(r.Min)

	tr := uint32(col.R)
	tg := uint32(col.G)
	tb := uint32(col.B)

	rgba, fast := dst.(*image.RGBA)

	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			sr, sg, sb, sa := src.At(x+offset.X, y+offset.Y).RGBA()
			if sa == 0 {
				continue
			}

			// Source is alpha premultiplied 16 bit; tint the colour channels.
			sr = sr * tr / 255
			sg = sg * tg / 255
			sb = sb * tb / 255

			inv := 0xffff - sa

			if fast {
				i := rgba.PixOffset(x, y)
				dr := uint32(rgba.Pix[i+0]) * 0x101
				dg := uint32(rgba.Pix[i+1]) * 0x101
				db := uint32(rgba.Pix[i+2]) * 0x101
				da := uint32(rgba.Pix[i+3]) * 0x101

				rgba.Pix[i+0] = uint8((sr + dr*inv/0xffff) >> 8)
				rgba.Pix[i+1] = uint8((sg + dg*inv/0xffff) >> 8)
				rgba.Pix[i+2] = uint8((sb + db*inv/0xffff) >> 8)
				rgba.Pix[i+3] = uint8((sa + da*inv/0xffff) >> 8)
				continue
			}

			dr, dg, db, da := dst.At(x, y).RGBA()
			dst.Set(x, y, color.RGBA64{
				R: uint16(sr + dr*inv/0xffff),
				G: uint16(sg + dg*inv/0xffff),
				B: uint16(sb + db*inv/0xffff),
				A: uint16(sa + da*inv/0xffff),
			})
		}
	}
}

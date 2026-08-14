package main

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/malashin/bmfonter"
)

// Decoded textures are cached for the whole run. The previous version decoded
// the plaque and the icon from disk again for every single focus, which meant
// a few hundred DDS decodes per image; now each file is decoded once.
var (
	textureMu    sync.Mutex
	textureCache = map[string]image.Image{}
	textureFail  = map[string]textureStatus{}
	frameCache   = map[string]image.Image{}
)

var (
	font          bmfonter.Font
	fontTreeTitle bmfonter.Font
	fontLoaded    bool
)

func resetCaches() {
	textureMu.Lock()
	textureCache = map[string]image.Image{}
	textureFail = map[string]textureStatus{}
	frameCache = map[string]image.Image{}
	textureMu.Unlock()
}

type textureStatus int

const (
	textureOK textureStatus = iota
	textureMissing
	textureBroken
)

// loadTexture decodes an image file, remembering both successes and failures.
func loadTexture(path string) (image.Image, textureStatus) {
	textureMu.Lock()
	if img, ok := textureCache[path]; ok {
		textureMu.Unlock()
		return img, textureOK
	}
	if st, ok := textureFail[path]; ok {
		textureMu.Unlock()
		return nil, st
	}
	textureMu.Unlock()

	remember := func(st textureStatus) {
		textureMu.Lock()
		textureFail[path] = st
		textureMu.Unlock()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		remember(textureMissing)
		return nil, textureMissing
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		// A DXT texture whose size is not a multiple of four can still be
		// decoded by padding it out to whole blocks.
		if fixed, ok := decodeDDSPadded(data); ok {
			textureMu.Lock()
			textureCache[path] = fixed
			textureMu.Unlock()
			return fixed, textureOK
		}
		warnf("texture could not be decoded: %v: %v", path, err)
		remember(textureBroken)
		return nil, textureBroken
	}

	textureMu.Lock()
	textureCache[path] = img
	textureMu.Unlock()
	return img, textureOK
}

// readTexture resolves the sprite's texture, looking through the other search
// paths when the declared one is missing.
func (s *SpriteType) readTexture() bool {
	if s.Image != nil {
		return true
	}
	if s.TextureFile == "" {
		warnf("sprite %q has no textureFile", s.Name)
		return false
	}

	img, st := loadTexture(s.TextureFile)
	if st == textureOK {
		s.Image = img
		return true
	}
	if st == textureBroken {
		// The file is there but unreadable; loadTexture already said so.
		return false
	}

	rel := s.TextureFile
	for _, p := range modPaths {
		if strings.HasPrefix(rel, p) {
			rel = strings.TrimPrefix(rel, p)
			break
		}
	}
	for i := len(modPaths) - 1; i >= 0; i-- {
		if img, st := loadTexture(filepath.Join(modPaths[i], rel)); st == textureOK {
			s.Image = img
			return true
		}
	}

	warnf("texture file not found: %v (sprite %q)", s.TextureFile, s.Name)
	return false
}

// getFrame cuts one frame out of a strip sprite.
func (s *SpriteType) getFrame(f int) (image.Image, bool) {
	if !s.readTexture() {
		return nil, false
	}
	if f < 1 {
		f = 1
	}
	frames := s.NoOfFrames
	if frames < 1 {
		frames = 1
	}
	if f > frames {
		warnf("sprite %q only has %v frames, frame %v was requested", s.Name, frames, f)
		f = frames
	}

	key := fmt.Sprintf("%s|%d|%d", s.TextureFile, frames, f)
	textureMu.Lock()
	if img, ok := frameCache[key]; ok {
		textureMu.Unlock()
		return img, true
	}
	textureMu.Unlock()

	bounds := s.Image.Bounds()
	frameSize := image.Point{X: bounds.Dx() / frames, Y: bounds.Dy()}
	if frameSize.X <= 0 || frameSize.Y <= 0 {
		warnf("sprite %q has an unusable frame size", s.Name)
		return nil, false
	}

	dst := image.NewRGBA(image.Rectangle{Max: frameSize})
	draw.Draw(dst, dst.Bounds(), s.Image, image.Point{X: bounds.Min.X + frameSize.X*(f-1), Y: bounds.Min.Y}, draw.Src)

	textureMu.Lock()
	frameCache[key] = dst
	textureMu.Unlock()
	return dst, true
}

// spriteFrame looks a sprite up by gfx name and returns one of its frames.
func spriteFrame(name string, frame int) (image.Image, bool) {
	s, ok := gfxMap[name]
	if !ok {
		warnf("sprite %q is not declared in any .gfx file", name)
		return nil, false
	}
	return s.getFrame(frame)
}

// renderFocus draws one focus box. Anything missing is logged and skipped so
// the rest of the tree still comes out.
func renderFocus(dst draw.Image, x, y int, id string) {
	f, ok := focusMap[id]
	if !ok {
		warnf("focus id %q not found", id)
		return
	}
	if !f.AllowBranch {
		return
	}

	// The game uses "GFX_technology_unavailable_item_bg" here and swaps it for
	// "GFX_focus_unavailable" in hardcoded logic.
	bg := gfxMap["GFX_focus_unavailable"]
	if len(f.Prerequisite) == 0 && f.Available {
		bg = gfxMap["GFX_focus_can_start"]
	}
	renderSprite(dst, x+gui.BG.Position.X, y+gui.BG.Position.Y, gui.BG.Orientation, gui.BG.CenterPosition, bg)

	symbol, ok := gfxMap[f.Icon]
	if !ok {
		if f.Icon != "" {
			warnf("focus %v uses icon %q which is not declared in any .gfx file", f.ID, f.Icon)
		}
		symbol = gfxMap["GFX_goal_unknown"]
	}
	renderSprite(dst, x+gui.Symbol.Position.X, y+gui.Symbol.Position.Y, gui.Symbol.Orientation, gui.Symbol.CenterPosition, symbol)

	if !fontLoaded {
		return
	}

	key := f.Text
	if key == "" {
		key = f.ID
	}
	text := resolveText(key)
	if text == key && locMap[key] == "" {
		warnf("focus %v has no localisation for %q, drawing the key instead", f.ID, key)
	}

	textX := x + gui.Name.Position.X
	textY := y + gui.Name.Position.Y
	if strings.EqualFold(gui.Name.Format, "center") {
		textX += gui.Name.MaxWidth / 2
	}
	if strings.EqualFold(gui.Name.VerticalAlignment, "center") {
		textY += gui.Name.MaxHeight / 2
	}

	renderColoredTextBox(dst, &font, textX, textY,
		gui.Name.MaxWidth+2, gui.Name.MaxHeight,
		strings.EqualFold(gui.Name.Format, "center"),
		strings.EqualFold(gui.Name.VerticalAlignment, "center"),
		text, cfg.ColoredText)
}

func renderSprite(dst draw.Image, x, y int, orientation, centerPosition string, sprite SpriteType) {
	if !sprite.readTexture() {
		return
	}

	if strings.EqualFold(orientation, "center") {
		x += gui.NationalFocusItem.Width / 2
		y += gui.NationalFocusItem.Height / 2
	}

	b := sprite.Image.Bounds()
	if strings.EqualFold(centerPosition, "yes") {
		x -= b.Dx() / 2
		y -= b.Dy() / 2
	}

	draw.Draw(dst, image.Rect(x, y, x+b.Dx(), y+b.Dy()), sprite.Image, b.Min, draw.Over)
}

func drawAt(dst draw.Image, x, y int, img image.Image) {
	if img == nil {
		return
	}
	b := img.Bounds()
	draw.Draw(dst, image.Rect(x, y, x+b.Dx(), y+b.Dy()), img, b.Min, draw.Over)
}

func renderExclusiveLines(dst *image.RGBA) {
	mid, hasMid := spriteFrame(gui.Mid.SpriteType, gui.Mid.Frame)
	link1, hasLink1 := spriteFrame(gui.Link1.SpriteType, gui.Link1.Frame)
	left, hasLeft := spriteFrame(gui.Left.SpriteType, gui.Left.Frame)
	right, hasRight := spriteFrame(gui.Right.SpriteType, gui.Right.Frame)

	if !hasMid {
		warnf("mutually exclusive links cannot be drawn, the connector sprite is missing")
		return
	}

	spacing := gui.LinkSpacing.X
	if spacing <= 0 {
		spacing = 32
	}

	for _, f1 := range focusMap {
		if !f1.AllowBranch {
			continue
		}
	OUTER:
		for _, e1 := range f1.MutuallyExclusive {
			f2, ok := focusMap[e1]
			if !ok {
				warnf("focus %v is mutually exclusive with %q which is not in this tree", f1.ID, e1)
				continue
			}
			if !f2.AllowBranch {
				continue
			}

			// Exclusivity links are only drawn between focuses on the same row,
			// and only once, starting from the left hand focus.
			if f1.Y != f2.Y || f1.X > f2.X {
				continue
			}

			// Skip links that would pass straight through another focus.
			for _, e2 := range f1.MutuallyExclusive {
				f3 := focusMap[e2]
				if f1.Y == f3.Y && f2.X > f3.X && f1.X < f3.X {
					continue OUTER
				}
			}

			x := f1.X*gui.FocusSpacing.X + gui.NationalFocusExclusiveItem.Position.X + gui.ExclusiveOffset.X + spacingX
			y := f1.Y*gui.FocusSpacing.Y + gui.NationalFocusExclusiveItem.Position.Y + gui.ExclusiveOffset.Y + spacingY

			xDifference := f2.X - f1.X
			switch {
			case xDifference == 2:
				drawAt(dst, x, y, mid)

			case xDifference > 2:
				lineSize := (xDifference - 2) * 3 * 32

				if hasLink1 {
					for i := 0; i < lineSize/spacing; i++ {
						drawAt(dst, x+gui.Link1.Position.X+spacing*i, y+gui.Link1.Position.Y-2, link1)
					}
				}
				if hasLeft {
					drawAt(dst, x+gui.Right.Position.X, y+gui.Right.Position.Y, left)
				}
				if hasRight {
					drawAt(dst, x+lineSize+gui.Right.Position.X, y+gui.Right.Position.Y, right)
				}
				drawAt(dst, x+lineSize/2+gui.Right.Position.X, y+gui.Right.Position.Y, mid)
			}
		}
	}
}

// linkSprites are the eleven corner/segment sprites the tree lines are built
// from. Frame 3 is the solid version, frame 4 the dashed one.
var linkSprites = []struct {
	name  string
	solid *image.Image
	dash  *image.Image
}{
	{"GFX_focus_link_up_down", &UD, &UDdash},
	{"GFX_focus_link_up_left", &UL, &ULdash},
	{"GFX_focus_link_up_right", &UR, &URdash},
	{"GFX_focus_link_down_left", &DL, &DLdash},
	{"GFX_focus_link_down_right", &DR, &DRdash},
	{"GFX_focus_link_left_right", &LR, &LRdash},
	{"GFX_focus_link_up_down_left", &UDL, &UDLdash},
	{"GFX_focus_link_up_down_right", &UDR, &UDRdash},
	{"GFX_focus_link_up_left_right", &ULR, &ULRdash},
	{"GFX_focus_link_down_left_right", &DLR, &DLRdash},
	{"GFX_focus_link_up_down_left_right", &UDLR, &UDLRdash},
}

func loadLinkSprites() bool {
	ok := true
	for _, ls := range linkSprites {
		solid, okSolid := spriteFrame(ls.name, 3)
		dash, okDash := spriteFrame(ls.name, 4)
		*ls.solid = solid
		*ls.dash = dash
		if !okSolid || !okDash {
			ok = false
		}
	}
	return ok
}

func renderLines(dst *image.RGBA) {
	if !loadLinkSprites() {
		warnf("some focus link sprites are missing, parts of the tree lines will be blank")
	}
	if UD == nil {
		errorf("the focus link sprites could not be loaded, tree lines are skipped")
		return
	}

	spacingLinkX := gui.LinkSpacing.X
	if spacingLinkX <= 0 {
		spacingLinkX = 32
	}
	spacingLinkY := gui.LinkSpacing.Y
	if spacingLinkY <= 0 {
		spacingLinkY = 32
	}

	// A map instead of the old linear scan over a growing slice: on a large
	// tree that scan was the single slowest part of the whole run.
	drawn := make(map[image.Point]bool)

	for _, p := range focusMap {
		if len(p.Children) == 0 || !p.AllowBranch {
			continue
		}

		x := p.X*gui.FocusSpacing.X + gui.NationalFocusLink.Position.X + gui.LinkBegin.X + gui.LinkOffsets.X + spacingX
		y := p.Y*gui.FocusSpacing.Y + gui.NationalFocusLink.Position.Y + gui.LinkBegin.Y + gui.LinkOffsets.Y + spacingY - 16

		// First link (out).
		img := UD
		if p.Out.Dir < 16 {
			img = UDdash
		}
		drawAt(dst, x, y, img)

		y += UD.Bounds().Dy()

		// First corner (out).
		drawAt(dst, x, y, p.Out.Get())
		drawn[image.Point{X: x, Y: y}] = true

		cornerXvalues := []int{x}
		for _, c := range p.Children {
			c := focusMap[c.ID]
			if c.AllowBranch {
				cornerXvalues = append(cornerXvalues, c.X*gui.FocusSpacing.X+gui.NationalFocusLink.Position.X+gui.LinkBegin.X+gui.LinkOffsets.X+spacingX)
			}
		}

		var isPrevSolid bool
		for _, child := range p.Children {
			c := focusMap[child.ID]
			if !c.AllowBranch {
				continue
			}

			cx := c.X*gui.FocusSpacing.X + gui.NationalFocusLink.Position.X + gui.LinkEnd.X + gui.LinkOffsets.X + spacingX

			// Children horizontal lines.
			if c.X != p.X {
				step := spacingLinkX
				if c.X > p.X {
					step = -spacingLinkX
					isPrevSolid = false
					for _, c2 := range p.Children {
						c2 := focusMap[c2.ID]
						if c2.X > c.X && c2.In[p.Y].Dir > 16 {
							isPrevSolid = true
						}
					}
				}

				hx := c.X*gui.FocusSpacing.X + gui.NationalFocusLink.Position.X + gui.LinkBegin.X + gui.LinkOffsets.X + spacingX
				length := int(math.Abs(float64(c.X-p.X)))*gui.FocusSpacing.Y + gui.LinkBegin.X + gui.LinkOffsets.X + spacingX

				lineImg := LRdash
				if (c.In[p.Y].Dir > 16 || isPrevSolid) && p.Out.Dir > 16 {
					lineImg = LR
					isPrevSolid = true
				}

				for i := 1; i < length/spacingLinkX; i++ {
					hx += step
					if containsInt(cornerXvalues, hx) {
						break
					}
					drawAt(dst, hx, y, lineImg)
				}
			}

			// Children corner (in).
			a := c.In[p.Y]
			if !drawn[image.Point{X: cx, Y: y}] {
				drawAt(dst, cx, y, a.Get())
			}
			drawn[image.Point{X: cx, Y: y}] = true

			// Children vertical lines (in).
			if c.Y-p.Y > 0 {
				vImg := UD
				if c.In[p.Y].Dir < 16 {
					vImg = UDdash
				}

				nextCornerY := maxYinRange(c.In, p.Y)
				childY := c.Y
				if nextCornerY != 0 {
					childY = nextCornerY
				}

				length := (childY-p.Y)*gui.FocusSpacing.Y + gui.LinkEnd.Y - spacingLinkY*2
				if nextCornerY != 0 {
					length += spacingLinkY
				}

				var i int
				for i = 1; i <= length/spacingLinkY; i++ {
					pt := image.Point{X: cx, Y: y + spacingLinkY*i}
					if !drawn[pt] {
						drawAt(dst, pt.X, pt.Y, vImg)
					}
					drawn[pt] = true
				}
				if leftover := length - (i-1)*spacingLinkY; leftover > 0 && vImg != nil {
					b := vImg.Bounds()
					draw.Draw(dst,
						image.Rect(cx, y+spacingLinkY*i, cx+b.Dx(), y+leftover+spacingLinkY*i),
						vImg, b.Min, draw.Over)
				}
			}
		}
	}
}

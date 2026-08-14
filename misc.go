package main

import (
	"image"
	"io/fs"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/malashin/bmfonter"
)

// walkFiles returns every file under root with the given extension. A missing
// folder is not an error: mods routinely ship without an interface or
// localisation folder.
func walkFiles(root, ext string) []string {
	if !dirExists(root) {
		return nil
	}
	var match []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			warnf("folder could not be read: %v: %v", p, err)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ext) {
			match = append(match, p)
		}
		return nil
	})
	if err != nil {
		warnf("folder could not be read: %v: %v", root, err)
	}
	sort.Strings(match)
	return match
}

// parseFilesConcurrently runs fn over every path on all cores and returns the
// results in the original order, so later files still override earlier ones.
func parseFilesConcurrently[T any](paths []string, fn func(string) (T, bool)) []T {
	if len(paths) == 0 {
		return nil
	}

	type slot struct {
		value T
		ok    bool
	}
	slots := make([]slot, len(paths))

	workers := runtime.NumCPU()
	if workers > len(paths) {
		workers = len(paths)
	}
	if workers < 1 {
		workers = 1
	}

	var wg sync.WaitGroup
	jobs := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				v, ok := fn(paths[i])
				slots[i] = slot{value: v, ok: ok}
			}
		}()
	}
	for i := range paths {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	out := make([]T, 0, len(paths))
	for _, s := range slots {
		if s.ok {
			out = append(out, s.value)
		}
	}
	return out
}

// fillAbsoluteFocusPositions turns relative_position_id coordinates into
// absolute ones. The iteration count is capped so that a mod with a circular
// reference reports the problem instead of hanging the program.
func fillAbsoluteFocusPositions() {
	const maxPasses = 64

	for pass := 0; pass < maxPasses; pass++ {
		changed := false
		for _, f1 := range focusMap {
			if f1.RelativePositionID != "" {
				continue
			}
			for _, f2 := range focusMap {
				if f2.RelativePositionID != f1.ID {
					continue
				}
				f2.X += f1.X
				f2.Y += f1.Y
				f2.RelativePositionID = ""
				focusMap[f2.ID] = f2
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	// Anything still relative points at a focus that is missing or part of a
	// loop. Leave it where it is and say so rather than dropping it.
	for _, f := range focusMap {
		if f.RelativePositionID == "" {
			continue
		}
		if _, ok := focusMap[f.RelativePositionID]; !ok {
			warnf("focus %v is positioned relative to %q which is not in this tree, drawing it at its own coordinates", f.ID, f.RelativePositionID)
		} else {
			warnf("focus %v is part of a circular relative_position_id chain, drawing it at its own coordinates", f.ID)
		}
		f.RelativePositionID = ""
		focusMap[f.ID] = f
	}
}

func moveAbsoluteFocusPositionsToPositiveValues() {
	lowestX := 0
	lowestY := 0

	for _, f := range focusMap {
		if !f.AllowBranch {
			continue
		}
		if f.X < lowestX {
			lowestX = f.X
		}
		if f.Y < lowestY {
			lowestY = f.Y
		}
	}

	if lowestX < 0 || lowestY < 0 {
		for _, f := range focusMap {
			f.X -= lowestX
			f.Y -= lowestY
			focusMap[f.ID] = f
		}
	}
}

// fillFocusChildAndParentData adds children data to each focus and works out
// which line segment each connection needs. Children are sorted left to right.
func fillFocusChildAndParentData() {
	for _, c := range focusMap {
		for _, g := range c.Prerequisite {
			solid := len(g) <= 1
			for _, id := range g {
				p, ok := focusMap[id]
				if !ok {
					warnf("focus %v lists %q as a prerequisite but that focus is not in this tree", c.ID, id)
					continue
				}
				p.Children = append(p.Children, Child{c.ID, solid})
				focusMap[p.ID] = p
			}
		}
	}

	visited := make(map[string]bool, len(focusMap))
	for _, f := range focusMap {
		sort.Slice(f.Children, func(i, j int) bool { return focusMap[f.Children[i].ID].X < focusMap[f.Children[j].ID].X })
		focusMap[f.ID] = f
		fillAllowBranchData(f, visited)
	}

	for _, p := range focusMap {
		for i, child := range p.Children {
			c := focusMap[child.ID]
			if !c.AllowBranch {
				continue
			}

			if c.In == nil {
				c.In = make(map[int]FocusLine)
			}

			a := FocusLine{Dir: 0}
			if val, ok := c.In[p.Y]; ok {
				a = val
			}

			if child.Solid {
				a.Set(S)
				p.Out.Set(S)
			} else {
				for _, child2 := range p.Children {
					c2 := focusMap[child2.ID]
					switch {
					case c.X < p.X && c2.X < c.X && child2.Solid:
						a.Set(S)
					case c.X > p.X && c2.X > c.X && child2.Solid:
						a.Set(S)
					case c.X == p.X && child2.Solid:
						a.Set(S)
					}
				}
			}

			switch {
			case c.X < p.X:
				a.Set(D | R)
				if i != 0 {
					a.Set(L)
				}
				p.Out.Set(U | L)
			case c.X == p.X:
				a.Set(U | D)
				if i > 0 && focusMap[p.Children[i-1].ID].AllowBranch {
					a.Set(L)
				}
				if i != len(p.Children)-1 && focusMap[p.Children[i+1].ID].AllowBranch {
					a.Set(R)
				}
				p.Out.Set(U | D)
			case c.X > p.X:
				a.Set(D | L)
				if i != len(p.Children)-1 {
					a.Set(R)
				}
				p.Out.Set(U | R)
			}

			for _, pSlice := range c.Prerequisite {
				for _, id := range pSlice {
					p2 := focusMap[id]
					if p.Y > p2.Y {
						a.Set(U)
					}
				}
			}

			c.In[p.Y] = a
			focusMap[c.ID] = c
		}
		focusMap[p.ID] = p
	}
}

func (l *FocusLine) Set(d Dir) {
	l.Dir |= d
}

func (l *FocusLine) Get() image.Image {
	switch l.Dir {
	case 3:
		return UDdash
	case 5:
		return ULdash
	case 6:
		return DLdash
	case 7:
		return UDLdash
	case 9:
		return URdash
	case 10:
		return DRdash
	case 11:
		return UDRdash
	case 12:
		return LRdash
	case 13:
		return ULRdash
	case 14:
		return DLRdash
	case 15:
		return UDLRdash

	case 19:
		return UD
	case 21:
		return UL
	case 22:
		return DL
	case 23:
		return UDL
	case 25:
		return UR
	case 26:
		return DR
	case 27:
		return UDR
	case 28:
		return LR
	case 29:
		return ULR
	case 30:
		return DLR
	case 31:
		return UDLR
	}
	return nil
}

func containsInt(s []int, a int) bool {
	for _, b := range s {
		if a == b {
			return true
		}
	}
	return false
}

func containsString(s []string, a string) bool {
	for _, b := range s {
		if strings.EqualFold(a, b) {
			return true
		}
	}
	return false
}

// maxFocusPos returns maximum x and y values in focus tree.
func maxFocusPos(m map[string]Focus) (x, y int) {
	for _, f := range m {
		if !f.AllowBranch {
			continue
		}
		if f.X > x {
			x = f.X
		}
		if f.Y > y {
			y = f.Y
		}
	}
	return
}

// fillAllowBranchData hides the children of a hidden branch. The visited set
// stops a prerequisite loop from recursing forever.
func fillAllowBranchData(f Focus, visited map[string]bool) {
	if f.AllowBranch || visited[f.ID] {
		return
	}
	visited[f.ID] = true

	for _, child := range f.Children {
		c := focusMap[child.ID]

		allowBranchInGroup := false
		for _, parentGroup := range c.Prerequisite {
			allowBranchInGroup = false
			for _, parent := range parentGroup {
				if focusMap[parent].AllowBranch {
					allowBranchInGroup = true
				}
			}
			if !allowBranchInGroup {
				break
			}
		}

		c.AllowBranch = allowBranchInGroup
		focusMap[child.ID] = c
		fillAllowBranchData(c, visited)
	}
}

func maxYinRange(m map[int]FocusLine, y int) int {
	var max int
	for i := range m {
		if i > max && i > y {
			max = i
		}
	}
	return max
}

// useModsTexturesIfPresent lets a mod replace a texture the game declares
// without redeclaring the sprite.
func useModsTexturesIfPresent() {
	if len(modPaths) <= 1 {
		return
	}
	for k, v := range gfxMap {
		if v.TextureFile == "" || !strings.HasPrefix(v.TextureFile, modPaths[0]) {
			continue
		}
		rel := strings.TrimPrefix(v.TextureFile, modPaths[0])
		for _, p := range modPaths[1:] {
			if fileExists(filepath.Join(p, rel)) {
				v.TextureFile = filepath.Join(p, rel)
				gfxMap[k] = v
			}
		}
	}
}

// replaceFontPathsIfNotFound falls back to the game's copy of a font when a
// mod declares one but does not ship the files.
func replaceFontPathsIfNotFound() {
	if len(modPaths) <= 1 {
		return
	}
	game := modPaths[0]

	for fontName, fontBitmap := range fontMap {
		for i, filePath := range fontBitmap.Fontfiles {
			if fileExists(filePath + ".fnt") {
				continue
			}
			for _, modPath := range modPaths[1:] {
				if !strings.HasPrefix(filePath, modPath) {
					continue
				}
				candidate := filepath.Join(game, strings.TrimPrefix(filePath, modPath))
				if fileExists(candidate + ".fnt") {
					fontBitmap.Fontfiles[i] = candidate
					fontMap[fontName] = fontBitmap
					break
				}
			}
		}
	}
}

// fallbackFonts are tried in order when the font the gui asks for is missing.
var fallbackFonts = []string{
	"hoi_16mbs", "hoi_18mbs", "hoi_20b", "hoi_20mbs",
	"Arial12", "vic_18", "vic_16",
}

// initFont loads a bitmap font, falling back to a similar one instead of
// aborting the whole image when the requested one cannot be loaded.
func initFont(fontName string) (bmfonter.Font, bool) {
	if f, ok := tryInitFont(fontName); ok {
		return f, true
	}

	for _, name := range fallbackFonts {
		if strings.EqualFold(name, fontName) {
			continue
		}
		if f, ok := tryInitFont(name); ok {
			warnf("font %q could not be loaded, using %q instead", fontName, name)
			return f, true
		}
	}

	// Last resort: any font that loads at all.
	names := make([]string, 0, len(fontMap))
	for name := range fontMap {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if f, ok := tryInitFont(name); ok {
			warnf("font %q could not be loaded, using %q instead", fontName, name)
			return f, true
		}
	}

	errorf("no usable bitmap font was found, focus names will be left blank")
	return bmfonter.Font{}, false
}

func tryInitFont(fontName string) (bmfonter.Font, bool) {
	var font bmfonter.Font
	if fontName == "" {
		return font, false
	}

	bmfont, ok := fontMap[fontName]
	if !ok {
		warnf("font %q is not declared in any .gfx file", fontName)
		return font, false
	}
	if len(bmfont.Fontfiles) == 0 {
		warnf("font %q has no associated files", fontName)
		return font, false
	}

	font, err := bmfonter.InitFont(bmfont.Fontfiles[0]+".fnt", bmfont.Fontfiles[0]+".dds")
	if err != nil {
		warnf("font %q could not be loaded: %v", fontName, err)
		return font, false
	}

	// The original returned right after the first sub font, so a font with
	// three files only ever got two of them.
	for i := 1; i < len(bmfont.Fontfiles); i++ {
		if err := font.AddSubFont(bmfont.Fontfiles[i]+".fnt", bmfont.Fontfiles[i]+".dds"); err != nil {
			warnf("sub font %v of %q could not be loaded: %v", filepath.Base(bmfont.Fontfiles[i]), fontName, err)
		}
	}
	return font, true
}

// openFolder shows a folder in the system file manager.
func openFolder(path string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", filepath.Clean(path))
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	// explorer.exe reports a non zero exit code even when it worked.
	_ = cmd.Start()
}

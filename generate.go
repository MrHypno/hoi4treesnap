package main

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// progressFunc reports how far along the run is (0 to 1) plus a short status
// line for the window.
type progressFunc func(value float64, status string)

// maxImagePixels guards against a mod with a broken coordinate producing a
// multi gigabyte image.
const maxImagePixels = 400 * 1000 * 1000

// generateAll renders every selected focus tree and returns the files written.
func generateAll(report progressFunc) ([]string, error) {
	if len(cfg.FocusTreePaths) == 0 {
		return nil, errors.New("no focus tree file is selected")
	}
	if cfg.GamePath == "" {
		return nil, errors.New("the Hearts of Iron IV folder is not selected")
	}
	if !dirExists(cfg.GamePath) {
		return nil, fmt.Errorf("the selected HOI4 folder does not exist: %v", cfg.GamePath)
	}

	start := time.Now()
	var written []string

	// With shared focuses on, the first file is the tree to draw and the rest
	// are the files that define the shared branches it references, so all of
	// them end up in a single image. With it off, each file gets its own
	// image as before.
	batches := [][]string{}
	if cfg.IncludeShared && len(cfg.FocusTreePaths) > 1 {
		batches = append(batches, cfg.FocusTreePaths)
	} else {
		for _, p := range cfg.FocusTreePaths {
			batches = append(batches, []string{p})
		}
	}

	for i, batch := range batches {
		base := float64(i) / float64(len(batches))
		span := 1.0 / float64(len(batches))
		step := func(v float64, status string) {
			if report != nil {
				report(base+v*span, status)
			}
		}

		out, err := generateOne(batch[0], batch[1:], step)
		if err != nil {
			// One bad tree must not take the rest of the batch with it.
			errorf("%v: %v", filepath.Base(batch[0]), err)
			continue
		}
		written = append(written, out)
	}

	rememberSeenDLCs()
	cfg.save()

	infof("finished in %s", time.Since(start).Round(time.Millisecond))

	if len(written) == 0 {
		return nil, errors.New("no image could be generated, see the log for details")
	}
	return written, nil
}

// generateOne draws one image. sharedSources are extra focus files whose
// shared_focus definitions should be merged into it.
func generateOne(treePath string, sharedSources []string, report progressFunc) (string, error) {
	resetState()

	treeName := strings.TrimSuffix(filepath.Base(treePath), filepath.Ext(treePath))
	infof("focus tree: %v", treePath)

	buildSearchPaths(treePath)

	// ------------------------------------------------------------ focus tree
	report(0.02, "Reading the focus tree")
	parsed, err := parseFocusFile(treePath)
	if err != nil {
		return "", fmt.Errorf("focus tree could not be read: %v", err)
	}
	for _, f := range parsed.Focuses {
		if _, dup := focusMap[f.ID]; dup {
			warnf("focus %v is defined more than once in %v, the last one wins", f.ID, filepath.Base(treePath))
		}
		focusMap[f.ID] = f
	}
	if len(focusMap) == 0 {
		return "", errors.New("the file contains no focuses")
	}
	infof("%v focuses read from %v", len(focusMap), filepath.Base(treePath))

	// --------------------------------------------------------- shared focuses
	report(0.06, "Looking up shared focus branches")
	switch {
	case !cfg.IncludeShared:
		if len(parsed.SharedRefs) > 0 {
			infof("%v shared focus references were skipped, shared branches are turned off", len(parsed.SharedRefs))
		}

	default:
		pool := loadSharedFocusPool(modPaths)

		// Files the user added by hand take priority over whatever the scan
		// of common/national_focus turned up.
		for _, src := range sharedSources {
			extra, err := parseFocusFile(src)
			if err != nil {
				errorf("shared focus file could not be read: %v: %v", src, err)
				continue
			}
			if !extra.HasShared {
				warnf("%v defines no shared_focus, nothing was taken from it", filepath.Base(src))
			}
			for _, f := range extra.Focuses {
				pool[f.ID] = sharedEntry{Focus: f, File: extra.Path}
			}
			infof("shared focus file: %v", src)
		}

		refs := parsed.SharedRefs
		if len(refs) == 0 && len(sharedSources) > 0 {
			// The tree references nothing, so draw every shared branch the
			// added files define rather than silently producing nothing.
			for _, src := range sharedSources {
				for _, e := range pool {
					if e.File == src && e.Focus.Shared {
						refs = append(refs, e.Focus.ID)
					}
				}
			}
			if len(refs) > 0 {
				infof("the tree has no shared_focus references, all %v shared roots of the added file(s) are drawn", len(refs))
			}
		}

		if len(refs) == 0 {
			break
		}

		added := 0
		for _, f := range expandSharedRefs(refs, pool) {
			// The country's own tree always wins over the shared definition.
			if _, exists := focusMap[f.ID]; exists {
				continue
			}
			focusMap[f.ID] = f
			added++
		}
		if added > 0 {
			infof("%v shared focuses added from %v referenced branch(es)", added, len(refs))
		}
	}

	if !cfg.FilterAllowBranch {
		hidden := 0
		for id, f := range focusMap {
			if !f.AllowBranch {
				f.AllowBranch = true
				focusMap[id] = f
				hidden++
			}
		}
		if hidden > 0 {
			infof("allow_branch filtering is off, %v otherwise hidden focuses are drawn", hidden)
		}
	}

	// ------------------------------------------------------------------- gui
	report(0.1, "Reading the interface layout")
	if !loadGUI(modPaths) {
		return "", errors.New("nationalfocusview.gui could not be read")
	}

	// ------------------------------------------------------------------- gfx
	report(0.15, "Reading sprites")
	loadGFX(modPaths, func(d float64) { report(0.15+d*0.25, "Reading sprites") })
	infof("%v sprites and %v fonts declared", len(gfxMap), len(fontMap))

	// ---------------------------------------------------------- localisation
	report(0.4, "Reading localisation")
	loadLocalisation(modPaths, cfg.Language, func(d float64) { report(0.4+d*0.2, "Reading localisation") })
	if cfg.ResolveScripted {
		report(0.6, "Reading scripted localisation")
		loadScriptedLocalisation(modPaths)
		infof("%v localisation keys, %v scripted localisation entries", len(locMap), len(scriptedLoc))
	} else {
		infof("%v localisation keys", len(locMap))
	}
	if len(locMap) == 0 {
		warnf("no localisation was found for %v, focus names will fall back to their ids", cfg.Language)
	}

	// -------------------------------------------------------------- geometry
	report(0.65, "Working out the layout")
	useModsTexturesIfPresent()
	fillAbsoluteFocusPositions()
	fillFocusChildAndParentData()
	moveAbsoluteFocusPositionsToPositiveValues()

	maxX, maxY := maxFocusPos(focusMap)
	w := (maxX+2)*gui.FocusSpacing.X + spacingX + 17
	h := (maxY+1)*gui.FocusSpacing.Y + spacingY
	if w <= 0 || h <= 0 {
		return "", errors.New("the tree has no visible focuses")
	}
	if int64(w)*int64(h) > maxImagePixels {
		return "", fmt.Errorf("the resulting image would be %vx%v pixels, which usually means a focus has a broken x or y value", w, h)
	}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{}}, image.Point{}, draw.Src)

	// ----------------------------------------------------------------- fonts
	report(0.7, "Loading fonts")
	replaceFontPathsIfNotFound()
	font, fontLoaded = initFont(gui.Name.Font)
	fontTreeTitle, _ = initFont(gui.NationalFocusTitle.Font)

	// ---------------------------------------------------------------- render
	if !cfg.DisableLines {
		report(0.75, "Drawing tree lines")
		renderLines(img)
		renderExclusiveLines(img)
	}

	report(0.85, "Drawing focuses")
	drawn := 0
	for _, id := range sortedFocusIDs() {
		f := focusMap[id]
		if f.AllowBranch {
			drawn++
		}
		renderFocus(img, f.X*gui.FocusSpacing.X+spacingX, f.Y*gui.FocusSpacing.Y+spacingY, f.ID)
	}
	infof("%v of %v focuses drawn", drawn, len(focusMap))

	// ------------------------------------------------------------------ save
	report(0.95, "Saving the image")
	outPath := filepath.Join(cfg.outputDir(), treeName+".png")
	if err := writePNG(outPath, img); err != nil {
		return "", err
	}

	infof("image saved: %v (%vx%v)", outPath, w, h)
	report(1, "Done")
	return outPath, nil
}

// sortedFocusIDs keeps the draw order stable so two runs of the same tree
// produce byte identical images.
func sortedFocusIDs() []string {
	ids := make([]string, 0, len(focusMap))
	for id := range focusMap {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := focusMap[ids[i]], focusMap[ids[j]]
		if a.Y != b.Y {
			return a.Y < b.Y
		}
		if a.X != b.X {
			return a.X < b.X
		}
		return ids[i] < ids[j]
	})
	return ids
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("image could not be created at %v: %v", path, err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return fmt.Errorf("image could not be written: %v", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("image could not be closed: %v", err)
	}
	return nil
}

// buildSearchPaths sets the lookup order: game folder first, then the
// dependency mods in the order they were added, then the mod that holds the
// selected focus tree.
func buildSearchPaths(treePath string) {
	modPaths = []string{filepath.Clean(cfg.GamePath)}

	for _, p := range cfg.ModPaths {
		p = filepath.Clean(p)
		if !containsString(modPaths, p) {
			modPaths = append(modPaths, p)
		}
	}

	dir := filepath.Dir(treePath)
	treeMod := filepath.Clean(strings.TrimSuffix(dir, filepath.Join("common", "national_focus")))
	if treeMod != "" && treeMod != "." && dirExists(treeMod) && !containsString(modPaths, treeMod) {
		modPaths = append(modPaths, treeMod)
	}

	// Give the log short labels for these so its lines stay readable.
	aliases := make([][2]string, 0, len(modPaths)+1)
	for i, p := range modPaths {
		label := "<game>"
		if i > 0 {
			label = "<" + filepath.Base(p) + ">"
		}
		aliases = append(aliases, [2]string{p, label})
	}
	if binPath != "" {
		aliases = append(aliases, [2]string{binPath, "<output>"})
	}
	setPathAliases(aliases)

	infof("search order: %v", strings.Join(modPaths, "  |  "))
}

func resetState() {
	focusMap = make(map[string]Focus)
	gfxMap = make(map[string]SpriteType)
	fontMap = make(map[string]BitmapFont)
	locMap = make(map[string]string)
	scriptedLoc = make(map[string]string)
	gui = FocusGUI{}
	fontLoaded = false
	resetCaches()
}

package main

import (
	"image"
	"path/filepath"
	"strings"
)

// spriteTypeKeys are the .gfx entries that carry a name and a texture we can
// draw. Anything else in the file is ignored.
var spriteTypeKeys = map[string]bool{
	"spritetype":              true,
	"corneredtilespritetype":  true,
	"frameanimatedspritetype": true,
	"maskedshieldtype":        true,
	"textspritetype":          true,
	"progressbartype":         true,
	"circularprogressbartype": true,
}

// ---------------------------------------------------------------- focus trees

type focusFile struct {
	Path       string
	TreeIDs    []string
	SharedRefs []string
	Focuses    []Focus
	HasShared  bool
}

// parseFocusFile reads one national_focus file.
func parseFocusFile(path string) (*focusFile, error) {
	src, err := readFileText(path)
	if err != nil {
		return nil, err
	}
	res := &focusFile{Path: path}
	root := ParsePDX(src, filepath.Base(path))
	collectFocuses(root, res, filepath.Base(path), false)
	return res, nil
}

// collectFocuses walks the top level of a focus file. It deliberately does not
// descend into effect or trigger scopes: "focus = X" inside a prerequisite is
// a reference, not a definition.
func collectFocuses(blk *PBlock, res *focusFile, source string, insideTree bool) {
	if blk == nil {
		return
	}
	for _, n := range blk.Nodes {
		key := strings.ToLower(n.Key)
		switch {
		case key == "focus_tree" && n.Block != nil:
			if id := n.Block.Str("id"); id != "" {
				res.TreeIDs = append(res.TreeIDs, id)
			}
			collectFocuses(n.Block, res, source, true)

		case key == "shared_focus":
			if n.Block != nil {
				res.HasShared = true
				if f, ok := focusFromBlock(n.Block, true, source); ok {
					res.Focuses = append(res.Focuses, f)
				}
			} else if n.Value != "" {
				res.SharedRefs = append(res.SharedRefs, n.Value)
			}

		case key == "focus" && n.Block != nil:
			if f, ok := focusFromBlock(n.Block, false, source); ok {
				res.Focuses = append(res.Focuses, f)
			}

		case n.Block != nil && !insideTree && key != "focus_tree":
			// Wrapper scopes some mods use around their trees.
			if key == "country" || key == "default" || key == "continuous_focus_position" {
				continue
			}
			collectFocuses(n.Block, res, source, insideTree)
		}
	}
}

func focusFromBlock(blk *PBlock, shared bool, source string) (Focus, bool) {
	var f Focus
	f.AllowBranch = true
	f.Available = true
	f.Shared = shared
	f.Source = source

	f.ID = blk.Str("id")
	if f.ID == "" {
		warnf("%v: a focus without an id was skipped (line %v)", source, blk.Line)
		return f, false
	}

	if n := blk.Get("icon"); n != nil {
		f.Icon = pickDynamicValue(n, "icon")
	}
	if n := blk.Get("text"); n != nil {
		f.Text = pickDynamicValue(n, "text")
	}

	if v, ok := blk.Int("x"); ok {
		f.X = v
	} else if blk.Has("x") {
		warnf("%v: focus %v has an x coordinate that is not a number, using 0", source, f.ID)
	}
	if v, ok := blk.Int("y"); ok {
		f.Y = v
	} else if blk.Has("y") {
		warnf("%v: focus %v has a y coordinate that is not a number, using 0", source, f.ID)
	}

	f.RelativePositionID = blk.Str("relative_position_id")

	for _, p := range blk.GetAll("prerequisite") {
		if p.Block == nil {
			continue
		}
		var group []string
		for _, fn := range p.Block.GetAll("focus") {
			if fn.Value != "" {
				group = append(group, fn.Value)
			}
		}
		if len(group) > 0 {
			f.Prerequisite = append(f.Prerequisite, group)
		}
	}

	for _, m := range blk.GetAll("mutually_exclusive") {
		if m.Block == nil {
			continue
		}
		for _, fn := range m.Block.GetAll("focus") {
			if fn.Value != "" {
				f.MutuallyExclusive = append(f.MutuallyExclusive, fn.Value)
			}
		}
	}

	if n := blk.Get("allow_branch"); n != nil && n.Block != nil {
		f.AllowBranch = evalAllowBranch(n.Block)
	}

	if n := blk.Get("available"); n != nil && n.Block != nil {
		if len(n.Block.Nodes) > 0 || len(n.Block.Values) > 0 {
			f.Available = false
		}
	}

	return f, true
}

// tri is a three valued result: a trigger we can decide, or one we cannot.
type tri int

const (
	triUnknown tri = iota
	triTrue
	triFalse
)

func (t tri) not() tri {
	switch t {
	case triTrue:
		return triFalse
	case triFalse:
		return triTrue
	}
	return triUnknown
}

// evalAllowBranch decides whether a branch is drawn. There is no country to
// evaluate the triggers against, so a branch is only hidden when its condition
// is decidably false; anything we cannot judge is drawn.
//
// has_dlc is decided against the DLCs ticked in the window, which is what
// makes the DLC picker work.
func evalAllowBranch(blk *PBlock) bool {
	return evalTriggerBlock(blk, 0) != triFalse
}

// evalTriggerBlock evaluates the children of a scope as a logical AND.
func evalTriggerBlock(blk *PBlock, depth int) tri {
	if blk == nil || depth > 16 {
		return triUnknown
	}

	result := triTrue
	for _, n := range blk.Nodes {
		switch evalTrigger(n, depth) {
		case triFalse:
			return triFalse
		case triUnknown:
			result = triUnknown
		}
	}
	return result
}

// evalTriggerAny evaluates the children of a scope as a logical OR.
func evalTriggerAny(blk *PBlock, depth int) tri {
	if blk == nil || depth > 16 || len(blk.Nodes) == 0 {
		return triUnknown
	}

	result := triFalse
	for _, n := range blk.Nodes {
		switch evalTrigger(n, depth) {
		case triTrue:
			return triTrue
		case triUnknown:
			result = triUnknown
		}
	}
	return result
}

func evalTrigger(n *PNode, depth int) tri {
	switch strings.ToLower(n.Key) {
	case "always":
		if strings.EqualFold(n.Value, "no") {
			return triFalse
		}
		if strings.EqualFold(n.Value, "yes") {
			return triTrue
		}

	case "has_dlc":
		noteDLC(n.Value)
		if dlcEnabled(n.Value) {
			return triTrue
		}
		return triFalse

	case "has_country_flag":
		// Poland's tree hides its alternative branch behind this flag.
		if strings.EqualFold(n.Value, "romanov_enabled") {
			return triFalse
		}

	case "not":
		return evalTriggerAny(n.Block, depth+1).not()

	case "or", "any_of":
		return evalTriggerAny(n.Block, depth+1)

	case "and", "all_of":
		return evalTriggerBlock(n.Block, depth+1)
	}
	return triUnknown
}

// pickDynamicValue reads either "icon = GFX_x" or the dynamic form
//
//	icon = {
//	    GFX_a = { <triggers> }
//	    GFX_b = yes
//	}
//
// Without a country to test the triggers against, the unconditional entry is
// the right one to draw: it is what the focus falls back to.
func pickDynamicValue(n *PNode, kind string) string {
	if n.Value != "" {
		return n.Value
	}
	if n.Block == nil {
		return ""
	}

	// Some mods write "text = { text = KEY trigger = { ... } }".
	if inner := n.Block.Get(kind); inner != nil && inner.Value != "" {
		return inner.Value
	}
	if inner := n.Block.Get("localization_key"); inner != nil && inner.Value != "" {
		return inner.Value
	}

	fallback := ""
	last := ""
	for _, e := range n.Block.Nodes {
		if e.Key == "" || strings.EqualFold(e.Key, "trigger") {
			continue
		}
		last = e.Key
		if strings.EqualFold(e.Value, "yes") {
			fallback = e.Key
		}
	}
	if fallback != "" {
		return fallback
	}
	return last
}

// ------------------------------------------------------------- shared focuses

type sharedEntry struct {
	Focus Focus
	File  string
}

// loadSharedFocusPool reads every national_focus file that defines shared
// focuses so referenced branches can be pulled into the image.
func loadSharedFocusPool(paths []string) map[string]sharedEntry {
	pool := make(map[string]sharedEntry)

	for _, root := range paths {
		files := walkFiles(filepath.Join(root, "common", "national_focus"), ".txt")
		results := parseFilesConcurrently(files, func(path string) (*focusFile, bool) {
			res, err := parseFocusFile(path)
			if err != nil {
				warnf("focus file could not be read: %v: %v", path, err)
				return nil, false
			}
			if !res.HasShared {
				return nil, false
			}
			return res, true
		})
		for _, res := range results {
			for _, f := range res.Focuses {
				// Later paths (mods) override the game's definitions.
				pool[f.ID] = sharedEntry{Focus: f, File: res.Path}
			}
		}
	}
	return pool
}

// expandSharedRefs collects each referenced shared focus together with the
// branch hanging off it. Only focuses declared in the same file are followed,
// which keeps an unrelated country tree from being dragged in through a
// prerequisite that happens to point at the shared root.
func expandSharedRefs(refs []string, pool map[string]sharedEntry) []Focus {
	if len(refs) == 0 {
		return nil
	}

	// Index by file so the branch walk stays inside its own file.
	byFile := make(map[string][]sharedEntry)
	for _, e := range pool {
		byFile[e.File] = append(byFile[e.File], e)
	}

	visited := make(map[string]bool)
	var out []Focus
	queue := append([]string(nil), refs...)

	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if visited[id] {
			continue
		}
		visited[id] = true

		entry, ok := pool[id]
		if !ok {
			warnf("shared focus %q is referenced but was not found in any national_focus file", id)
			continue
		}
		out = append(out, entry.Focus)

		for _, cand := range byFile[entry.File] {
			if visited[cand.Focus.ID] {
				continue
			}
			if focusDependsOn(cand.Focus, id) {
				queue = append(queue, cand.Focus.ID)
			}
		}
	}
	return out
}

func focusDependsOn(f Focus, id string) bool {
	if f.RelativePositionID == id {
		return true
	}
	for _, group := range f.Prerequisite {
		for _, p := range group {
			if p == id {
				return true
			}
		}
	}
	for _, m := range f.MutuallyExclusive {
		if m == id {
			return true
		}
	}
	return false
}

// -------------------------------------------------------------------- the gui

// loadGUI reads nationalfocusview.gui from the last search path that has one.
func loadGUI(paths []string) bool {
	guiPath := ""
	for _, p := range paths {
		candidate := filepath.Join(p, "interface", "nationalfocusview.gui")
		if fileExists(candidate) {
			guiPath = candidate
		}
	}
	if guiPath == "" {
		errorf("nationalfocusview.gui was not found in the game folder or any selected mod")
		return false
	}

	src, err := readFileText(guiPath)
	if err != nil {
		errorf("nationalfocusview.gui could not be read: %v", err)
		return false
	}

	infof("interface layout: %v", guiPath)
	traverseGUI(ParsePDX(src, "nationalfocusview.gui"))

	if gui.FocusSpacing.X == 0 || gui.FocusSpacing.Y == 0 {
		warnf("focus_spacing was missing from the gui file, falling back to the vanilla 96x130 grid")
		if gui.FocusSpacing.X == 0 {
			gui.FocusSpacing.X = 96
		}
		if gui.FocusSpacing.Y == 0 {
			gui.FocusSpacing.Y = 130
		}
	}
	if gui.NationalFocusItem.Width == 0 {
		gui.NationalFocusItem.Width = 96
	}
	if gui.NationalFocusItem.Height == 0 {
		gui.NationalFocusItem.Height = 96
	}
	return true
}

func pointOf(n *PNode) image.Point {
	var p image.Point
	if n == nil || n.Block == nil {
		return p
	}
	if v, ok := n.Block.Int("x"); ok {
		p.X = v
	}
	if v, ok := n.Block.Int("y"); ok {
		p.Y = v
	}
	return p
}

func traverseGUI(blk *PBlock) {
	if blk == nil {
		return
	}
	for _, n := range blk.Nodes {
		if n.Block == nil {
			continue
		}
		switch strings.ToLower(n.Key) {
		case "containerwindowtype":
			switch strings.ToLower(n.Block.Str("name")) {
			case "nationalfocusview":
				readFocusViewContainer(n.Block)
			case "national_focus_item":
				readFocusItemContainer(n.Block)
			case "national_focus_link":
				readFocusLinkContainer(n.Block)
			case "national_focus_exclusive_item":
				readExclusiveItemContainer(n.Block)
			}
			traverseGUI(n.Block)

		case "positiontype":
			readPositionType(n.Block)

		default:
			traverseGUI(n.Block)
		}
	}
}

func readTextbox(blk *PBlock) InstantTextboxType {
	var t InstantTextboxType
	t.Name = blk.Str("name")
	t.Position = pointOf(blk.Get("position"))
	t.Font = blk.Str("font")
	t.Text = blk.Str("text")
	t.Format = blk.Str("format")
	t.Orientation = blk.Str("orientation")
	t.VerticalAlignment = blk.Str("vertical_alignment")
	if v, ok := blk.Int("maxwidth"); ok {
		t.MaxWidth = v
	}
	if v, ok := blk.Int("maxheight"); ok {
		t.MaxHeight = v
	}
	return t
}

func readButton(blk *PBlock) ButtonType {
	var b ButtonType
	b.Name = blk.Str("name")
	b.Position = pointOf(blk.Get("position"))
	b.SpriteType = blk.Str("spriteType")
	if b.SpriteType == "" {
		b.SpriteType = blk.Str("quadTextureSprite")
	}
	b.CenterPosition = blk.Str("centerPosition")
	b.Orientation = blk.Str("orientation")
	return b
}

func readIcon(blk *PBlock) IconType {
	var i IconType
	i.Name = blk.Str("name")
	i.Position = pointOf(blk.Get("position"))
	i.SpriteType = blk.Str("spriteType")
	if i.SpriteType == "" {
		i.SpriteType = blk.Str("quadTextureSprite")
	}
	if v, ok := blk.Int("frame"); ok {
		i.Frame = v
	}
	return i
}

func readContainer(blk *PBlock) ContainerWindowType {
	var c ContainerWindowType
	c.Name = blk.Str("name")
	c.Position = pointOf(blk.Get("position"))
	if size := blk.Get("size"); size != nil && size.Block != nil {
		if v, ok := size.Block.Int("width"); ok {
			c.Width = v
		}
		if v, ok := size.Block.Int("height"); ok {
			c.Height = v
		}
	}
	return c
}

func readFocusViewContainer(blk *PBlock) {
	for _, n := range blk.GetAll("instantTextBoxType") {
		if n.Block == nil {
			continue
		}
		t := readTextbox(n.Block)
		if strings.EqualFold(t.Name, "national_focus_title") {
			gui.NationalFocusTitle = t
		}
	}
}

func readFocusItemContainer(blk *PBlock) {
	gui.NationalFocusItem = readContainer(blk)

	for _, n := range blk.GetAll("buttonType") {
		if n.Block == nil {
			continue
		}
		b := readButton(n.Block)
		switch strings.ToLower(b.Name) {
		case "bg":
			gui.BG = b
		case "symbol":
			gui.Symbol = b
		}
	}

	for _, n := range blk.GetAll("instantTextBoxType") {
		if n.Block == nil {
			continue
		}
		t := readTextbox(n.Block)
		if strings.EqualFold(t.Name, "name") {
			gui.Name = t
		}
	}
}

func readFocusLinkContainer(blk *PBlock) {
	gui.NationalFocusLink = readContainer(blk)
	for _, n := range blk.GetAll("iconType") {
		if n.Block == nil {
			continue
		}
		i := readIcon(n.Block)
		if strings.EqualFold(i.Name, "link") {
			gui.Link = i
		}
	}
}

func readExclusiveItemContainer(blk *PBlock) {
	gui.NationalFocusExclusiveItem = readContainer(blk)
	for _, n := range blk.GetAll("iconType") {
		if n.Block == nil {
			continue
		}
		i := readIcon(n.Block)
		switch strings.ToLower(i.Name) {
		case "link1":
			gui.Link1 = i
		case "link2":
			gui.Link2 = i
		case "left":
			gui.Left = i
		case "right":
			gui.Right = i
		case "mid":
			gui.Mid = i
		}
	}
}

func readPositionType(blk *PBlock) {
	pos := pointOf(blk.Get("position"))
	switch strings.ToLower(blk.Str("name")) {
	case "focus_spacing":
		gui.FocusSpacing = pos
	case "link_spacing":
		gui.LinkSpacing = pos
	case "link_offsets":
		gui.LinkOffsets = pos
	case "link_begin":
		gui.LinkBegin = pos
	case "link_end":
		gui.LinkEnd = pos
	case "exclusive_offset":
		gui.ExclusiveOffset = pos
	case "exclusive_offset_left":
		gui.ExclusiveOffsetLeft = pos
	case "exclusive_positioning":
		gui.ExclusivePositioning = pos
	}
}

// -------------------------------------------------------------------- sprites

type gfxResult struct {
	Sprites map[string]SpriteType
	Fonts   map[string]BitmapFont
}

// loadGFX reads every .gfx file under interface/ in each search path.
//
// The old version only opened files whose raw text contained one of the sprite
// names it was looking for, which quietly lost sprites declared through a
// different spelling. Reading them all is both simpler and, with the new
// parser, faster than the substring scan it replaced.
func loadGFX(paths []string, progress func(float64)) {
	step := 0.0
	if len(paths) > 0 {
		step = 1.0 / float64(len(paths))
	}

	for _, root := range paths {
		files := walkFiles(filepath.Join(root, "interface"), ".gfx")
		results := parseFilesConcurrently(files, func(path string) (gfxResult, bool) {
			src, err := readFileText(path)
			if err != nil {
				warnf("gfx file could not be read: %v: %v", path, err)
				return gfxResult{}, false
			}
			res := gfxResult{
				Sprites: make(map[string]SpriteType),
				Fonts:   make(map[string]BitmapFont),
			}
			traverseGFX(ParsePDX(src, filepath.Base(path)), root, &res)
			if len(res.Sprites) == 0 && len(res.Fonts) == 0 {
				return gfxResult{}, false
			}
			return res, true
		})

		for _, res := range results {
			for k, v := range res.Sprites {
				gfxMap[k] = v
			}
			for k, v := range res.Fonts {
				fontMap[k] = v
			}
		}

		if progress != nil {
			progress(step)
		}
	}
}

func traverseGFX(blk *PBlock, root string, out *gfxResult) {
	if blk == nil {
		return
	}
	for _, n := range blk.Nodes {
		if n.Block == nil {
			continue
		}
		key := strings.ToLower(n.Key)
		switch {
		case spriteTypeKeys[key]:
			var s SpriteType
			s.Name = n.Block.Str("name")
			if s.Name == "" {
				continue
			}
			if tf := n.Block.Str("textureFile"); tf != "" {
				s.TextureFile = filepath.Join(root, filepath.FromSlash(tf))
			}
			if v, ok := n.Block.Int("noOfFrames"); ok {
				s.NoOfFrames = v
			}
			if s.NoOfFrames < 1 {
				s.NoOfFrames = 1
			}
			out.Sprites[s.Name] = s

		case key == "bitmapfont":
			var b BitmapFont
			b.Name = n.Block.Str("name")
			if b.Name == "" {
				continue
			}
			if p := n.Block.Str("path"); p != "" {
				b.Path = filepath.Join(root, filepath.FromSlash(p))
			}
			if ff := n.Block.Get("fontfiles"); ff != nil && ff.Block != nil {
				for _, v := range ff.Block.Values {
					b.Fontfiles = append(b.Fontfiles, filepath.Join(root, filepath.FromSlash(v)))
				}
			}
			if len(b.Fontfiles) == 0 && b.Path != "" {
				b.Fontfiles = append(b.Fontfiles, b.Path)
			}
			out.Fonts[b.Name] = b

		default:
			traverseGFX(n.Block, root, out)
		}
	}
}

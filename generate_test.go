package main

import (
	"os"
	"path/filepath"
	"testing"
)

// These tests run against a real Hearts of Iron IV installation. Point
// HOI4_PATH at the game folder (and optionally HOI4_MOD_PATH at a mod) to
// enable them; without it they are skipped.
func gamePathForTest(t *testing.T) string {
	t.Helper()
	p := os.Getenv("HOI4_PATH")
	if p == "" {
		t.Skip("HOI4_PATH is not set")
	}
	if !dirExists(p) {
		t.Skipf("HOI4_PATH does not exist: %v", p)
	}
	return p
}

func TestPDXParserHandlesModernSyntax(t *testing.T) {
	src := `
focus_tree = {
	id = test_tree
	shared_focus = SHARED_ROOT

	focus = {
		id = test_focus
		icon = {
			GFX_conditional = { has_country_flag = x }
			GFX_fallback = yes
		}
		x = 4
		y = 0
		cost = @[ base_cost * 2 ]
		available = {
			num_of_factories >= 10
			threat != 0.5
			has_tech ?= infantry_weapons
		}
		bypass = { has_dlc = "Man the Guns" }
		text = MY_TITLE
		prerequisite = { focus = a focus = b }
		mutually_exclusive = { focus = c }
	}
}
`
	root := ParsePDX(src, "test")
	res := &focusFile{}
	collectFocuses(root, res, "test", false)

	if len(res.TreeIDs) != 1 || res.TreeIDs[0] != "test_tree" {
		t.Fatalf("tree id not read: %v", res.TreeIDs)
	}
	if len(res.SharedRefs) != 1 || res.SharedRefs[0] != "SHARED_ROOT" {
		t.Fatalf("shared focus reference not read: %v", res.SharedRefs)
	}
	if len(res.Focuses) != 1 {
		t.Fatalf("expected 1 focus, got %v", len(res.Focuses))
	}

	f := res.Focuses[0]
	if f.ID != "test_focus" {
		t.Errorf("id = %q", f.ID)
	}
	if f.Icon != "GFX_fallback" {
		t.Errorf("dynamic icon = %q, want the unconditional entry", f.Icon)
	}
	if f.Text != "MY_TITLE" {
		t.Errorf("text = %q", f.Text)
	}
	if f.X != 4 || f.Y != 0 {
		t.Errorf("position = %v,%v", f.X, f.Y)
	}
	if f.Available {
		t.Error("a focus with an available block should not be marked available")
	}
	if len(f.Prerequisite) != 1 || len(f.Prerequisite[0]) != 2 {
		t.Errorf("prerequisite = %v", f.Prerequisite)
	}
	if len(f.MutuallyExclusive) != 1 || f.MutuallyExclusive[0] != "c" {
		t.Errorf("mutually exclusive = %v", f.MutuallyExclusive)
	}
}

func TestPDXParserSurvivesGarbage(t *testing.T) {
	src := `
focus = {
	id = ok_focus
	x = 1
	y = 2
}
} } =
focus = {
	id = second_focus
	icon = gfx/interface/goals/some|icon.dds
	x = 3
	y = 4
	broken =
}
`
	root := ParsePDX(src, "garbage")
	res := &focusFile{}
	collectFocuses(root, res, "garbage", false)

	if len(res.Focuses) != 2 {
		t.Fatalf("expected both focuses to survive, got %v", len(res.Focuses))
	}
	if res.Focuses[1].Icon != "gfx/interface/goals/some|icon.dds" {
		t.Errorf("unquoted path with a pipe was mangled: %q", res.Focuses[1].Icon)
	}
}

func TestLocLineSplitting(t *testing.T) {
	cases := []struct{ line, key, value string }{
		{`KEY:0 "Simple"`, "KEY", "Simple"},
		{`KEY: "No number"`, "KEY", "No number"},
		{`KEY:1 "§HGold§! and §Yyellow§!"`, "KEY", "§HGold§! and §Yyellow§!"},
		{`KEY:0 "[Root.GetNameDefCap] has [?Root.var|+0]"`, "KEY", "[Root.GetNameDefCap] has [?Root.var|+0]"},
		{`KEY:0 "He said \"hi\"" # comment`, "KEY", `He said \"hi\"`},
	}
	for _, c := range cases {
		k, v, ok := splitLocLine(c.line)
		if !ok || k != c.key || v != c.value {
			t.Errorf("splitLocLine(%q) = %q, %q, %v", c.line, k, v, ok)
		}
	}
}

func TestColorCodeParsing(t *testing.T) {
	runs := parseColoredText("a§Rb§!c", true)
	if len(runs) != 3 {
		t.Fatalf("expected 3 characters, got %v", len(runs))
	}
	if runs[0].col.A != 0 {
		t.Error("first character should use the default colour")
	}
	if runs[1].col != hoi4TextColors['R'] {
		t.Error("second character should be red")
	}
	if runs[2].col.A != 0 {
		t.Error("§! should reset the colour")
	}

	if got := stripColorCodes("a§Rb§!c"); got != "abc" {
		t.Errorf("stripColorCodes = %q", got)
	}
}

func TestScriptedLocalisationPicksBaseText(t *testing.T) {
	src := `
defined_text = {
	name = GetThing
	text = {
		trigger = { original_tag = GER }
		localization_key = GERMAN_KEY
	}
	text = {
		localization_key = BASE_KEY
	}
}
`
	out := map[string]string{}
	collectDefinedText(ParsePDX(src, "scripted"), out)
	if out["GetThing"] != "BASE_KEY" {
		t.Errorf("scripted loc resolved to %q, want the unconditional BASE_KEY", out["GetThing"])
	}
}

func TestAllowBranchFollowsDLCSelection(t *testing.T) {
	parse := func(src string) *PBlock {
		blk := ParsePDX("allow_branch = "+src, "allow_branch")
		return blk.Get("allow_branch").Block
	}

	needsDLC := parse(`{ has_dlc = "No Step Back" }`)
	withoutDLC := parse(`{ NOT = { has_dlc = "No Step Back" } }`)
	either := parse(`{ OR = { has_dlc = "No Step Back" has_dlc = "Man the Guns" } }`)
	unknown := parse(`{ has_country_flag = something_we_cannot_know }`)
	never := parse(`{ always = no }`)

	old := cfg
	defer func() { cfg = old }()

	cfg = defaultConfig()
	if !evalAllowBranch(needsDLC) {
		t.Error("a branch requiring an owned DLC should be drawn")
	}
	if evalAllowBranch(withoutDLC) {
		t.Error("a branch for players without the DLC should be hidden when it is owned")
	}
	if !evalAllowBranch(either) {
		t.Error("an OR over owned DLCs should be drawn")
	}

	setDLCEnabled("No Step Back", false)
	if evalAllowBranch(needsDLC) {
		t.Error("a branch requiring a DLC that is switched off should be hidden")
	}
	if !evalAllowBranch(withoutDLC) {
		t.Error("a branch for players without the DLC should be drawn when it is switched off")
	}
	if !evalAllowBranch(either) {
		t.Error("the OR should still hold through the other DLC")
	}

	setDLCEnabled("Man the Guns", false)
	if evalAllowBranch(either) {
		t.Error("the OR should fail once both DLCs are switched off")
	}

	if !evalAllowBranch(unknown) {
		t.Error("a condition we cannot decide must not hide the branch")
	}
	if evalAllowBranch(never) {
		t.Error("always = no must hide the branch")
	}

	if !containsString(cfg.DisabledDLCs, "No Step Back") {
		t.Errorf("disabled list = %v", cfg.DisabledDLCs)
	}
	setDLCEnabled("No Step Back", true)
	if containsString(cfg.DisabledDLCs, "No Step Back") {
		t.Errorf("re-enabling should remove it, list = %v", cfg.DisabledDLCs)
	}
}

func TestOnlyBaseGameLanguagesAreOffered(t *testing.T) {
	want := map[string]bool{
		"l_braz_por": true, "l_english": true, "l_french": true, "l_german": true,
		"l_japanese": true, "l_korean": true, "l_polish": true, "l_russian": true,
		"l_simp_chinese": true, "l_spanish": true,
	}
	if len(hoi4Languages) != len(want) {
		t.Fatalf("%v languages offered, want %v", len(hoi4Languages), len(want))
	}
	for _, l := range hoi4Languages {
		if !want[l.Tag] {
			t.Errorf("%v is not a base game language", l.Tag)
		}
	}
}

func TestGenerateVanillaTree(t *testing.T) {
	game := gamePathForTest(t)

	out := t.TempDir()
	binPath = out
	cfg = defaultConfig()
	cfg.GamePath = game
	cfg.OutputDir = out
	cfg.FocusTreePaths = []string{filepath.Join(game, "common", "national_focus", "germany.txt")}

	resetLog()
	files, err := generateAll(nil)
	if err != nil {
		t.Fatalf("generateAll: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 image, got %v", len(files))
	}
	fi, err := os.Stat(files[0])
	if err != nil {
		t.Fatalf("output missing: %v", err)
	}
	if fi.Size() < 10000 {
		t.Errorf("output looks empty: %v bytes", fi.Size())
	}
	t.Logf("wrote %v (%v bytes)", files[0], fi.Size())

	_, errs := logSummary()
	if errs > 0 {
		t.Errorf("%v errors were logged", errs)
	}
}

// TestDLCSelectionChangesWhatIsDrawn checks the DLC picker end to end on the
// Soviet tree, which gates several branches behind No Step Back.
func TestDLCSelectionChangesWhatIsDrawn(t *testing.T) {
	game := gamePathForTest(t)
	tree := filepath.Join(game, "common", "national_focus", "soviet.txt")
	if !fileExists(tree) {
		t.Skip("soviet.txt not found")
	}

	count := func(disabled []string, filter bool) (drawn, total int) {
		out := t.TempDir()
		binPath = out
		cfg = defaultConfig()
		cfg.GamePath = game
		cfg.OutputDir = out
		cfg.FocusTreePaths = []string{tree}
		cfg.DisabledDLCs = disabled
		cfg.FilterAllowBranch = filter

		resetLog()
		if _, err := generateAll(nil); err != nil {
			t.Fatalf("generateAll: %v", err)
		}
		for _, f := range focusMap {
			total++
			if f.AllowBranch {
				drawn++
			}
		}
		return drawn, total
	}

	withDLC, total := count(nil, true)
	withoutDLC, _ := count([]string{"No Step Back"}, true)
	unfiltered, _ := count([]string{"No Step Back"}, false)

	t.Logf("all DLCs: %v/%v drawn, without No Step Back: %v, filtering off: %v",
		withDLC, total, withoutDLC, unfiltered)

	if withoutDLC >= withDLC {
		t.Errorf("switching off No Step Back should hide focuses, got %v then %v", withDLC, withoutDLC)
	}
	if unfiltered != total {
		t.Errorf("with filtering off every focus should be drawn, got %v of %v", unfiltered, total)
	}
}

func TestGenerateModTreeWithSharedFocuses(t *testing.T) {
	game := gamePathForTest(t)
	mod := os.Getenv("HOI4_MOD_PATH")
	if mod == "" || !dirExists(mod) {
		t.Skip("HOI4_MOD_PATH is not set")
	}
	tree := os.Getenv("HOI4_TREE")
	if tree == "" || !fileExists(tree) {
		t.Skip("HOI4_TREE is not set")
	}

	out := os.Getenv("HOI4_OUT")
	if out == "" {
		out = t.TempDir()
	}
	binPath = out
	cfg = defaultConfig()
	cfg.GamePath = game
	cfg.ModPaths = []string{mod}
	cfg.OutputDir = out
	cfg.FocusTreePaths = []string{tree}

	resetLog()
	files, err := generateAll(nil)
	if err != nil {
		t.Fatalf("generateAll: %v", err)
	}
	fi, err := os.Stat(files[0])
	if err != nil {
		t.Fatalf("output missing: %v", err)
	}
	t.Logf("wrote %v (%v bytes)", files[0], fi.Size())

	// Handy when checking a particular focus in the generated image.
	if id := os.Getenv("HOI4_FOCUS"); id != "" {
		f, ok := focusMap[id]
		if !ok {
			t.Fatalf("focus %v is not in the tree", id)
		}
		key := f.Text
		if key == "" {
			key = f.ID
		}
		t.Logf("focus %v drawn at pixel %v,%v (grid %v,%v)\n  raw = %q\n  shown = %q",
			id, f.X*gui.FocusSpacing.X+spacingX, f.Y*gui.FocusSpacing.Y+spacingY,
			f.X, f.Y, locMap[key], resolveText(key))
	}
}

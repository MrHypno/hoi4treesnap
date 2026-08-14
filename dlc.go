package main

import (
	"sort"
	"strings"
	"sync"
)

// DLC handling for allow_branch.
//
// Branches are commonly gated with "allow_branch = { has_dlc = "..." }" or its
// negation. Which of those are drawn now depends on the DLCs ticked in the
// window instead of the old rule that hid every has_dlc branch outright.

// builtinDLCs are the DLCs that gate focus tree content, with the short name
// the community uses where there is an established one. The game folder is not
// scanned for this: it only carries a descriptor for the DLCs that are
// installed, so a tree behind a DLC the user does not own would be impossible
// to tick, and it also lists music and unit packs that gate nothing.
//
// The list is what actually appears in a has_dlc inside an allow_branch in the
// vanilla focus files. Anything a mod uses is picked up while parsing and
// added on top, so a DLC released later shows up on its own.
var builtinDLCs = []struct {
	Name  string
	Short string
}{
	{"Arms Against Tyranny", "AAT"},
	{"Battle for the Bosporus", "BftB"},
	{"By Blood Alone", "BBA"},
	{"Death or Dishonor", "DoD"},
	{"Gotterdammerung", "GD"},
	{"Graveyard of Empires", "GoE"},
	{"La Resistance", "LaR"},
	{"Man the Guns", "MtG"},
	{"No Compromise, No Surrender", "NCNS"},
	{"No Step Back", "NSB"},
	{"Peace For Our Time", ""},
	{"Thunder at Our Gates", "TAOG"},
	{"Together for Victory", "TfV"},
	{"Trial of Allegiance", "ToA"},
	{"Waking the Tiger", "WtT"},
}

// dlcDisplayName appends the short name in brackets when there is one.
func dlcDisplayName(name string) string {
	for _, d := range builtinDLCs {
		if strings.EqualFold(d.Name, name) && d.Short != "" {
			return name + " (" + d.Short + ")"
		}
	}
	return name
}

var (
	seenDLCMu sync.Mutex
	seenDLCs  = map[string]bool{}
)

func noteDLC(name string) {
	if name == "" {
		return
	}
	seenDLCMu.Lock()
	seenDLCs[name] = true
	seenDLCMu.Unlock()
}

func takeSeenDLCs() []string {
	seenDLCMu.Lock()
	defer seenDLCMu.Unlock()
	out := make([]string, 0, len(seenDLCs))
	for name := range seenDLCs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// knownDLCs is the list shown in the DLC window: the built in ones plus
// anything a parsed focus file has referenced so far.
func knownDLCs() []string {
	set := map[string]bool{}
	for _, d := range builtinDLCs {
		set[d.Name] = true
	}
	for _, n := range cfg.SeenDLCs {
		set[n] = true
	}
	for _, n := range takeSeenDLCs() {
		set[n] = true
	}

	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// dlcEnabled reports whether a DLC counts as owned. Everything is on unless it
// was explicitly switched off, so a DLC released later does not silently hide
// branches that used to be drawn.
func dlcEnabled(name string) bool {
	for _, d := range cfg.DisabledDLCs {
		if strings.EqualFold(d, name) {
			return false
		}
	}
	return true
}

func setDLCEnabled(name string, on bool) {
	var out []string
	for _, d := range cfg.DisabledDLCs {
		if !strings.EqualFold(d, name) {
			out = append(out, d)
		}
	}
	if !on {
		out = append(out, name)
	}
	sort.Strings(out)
	cfg.DisabledDLCs = out
}

// rememberSeenDLCs folds the DLC names met during a run into the settings so
// the window can offer them next time.
func rememberSeenDLCs() {
	set := map[string]bool{}
	for _, n := range cfg.SeenDLCs {
		set[n] = true
	}
	for _, n := range takeSeenDLCs() {
		set[n] = true
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	cfg.SeenDLCs = out
}

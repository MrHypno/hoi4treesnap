package main

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"os"
	"path/filepath"
)

// Config is everything the tool remembers between runs. Previously only the
// game folder survived a restart, so dependency mods, language and options had
// to be picked again every single time.
type Config struct {
	GamePath        string   `json:"game_path"`
	ModPaths        []string `json:"mod_paths"`
	FocusTreePaths  []string `json:"focus_tree_paths"`
	LastFocusDir    string   `json:"last_focus_dir"`
	OutputDir       string   `json:"output_dir"`
	Language        string   `json:"language"`
	DisableLines    bool     `json:"disable_lines"`
	IncludeShared   bool     `json:"include_shared"`
	ResolveScripted bool     `json:"resolve_scripted_loc"`
	ColoredText     bool     `json:"colored_text"`
	KeepFocusTrees  bool     `json:"keep_focus_trees"`

	// FilterAllowBranch decides whether allow_branch is honoured at all.
	// DisabledDLCs lists the DLCs treated as not owned when it is; SeenDLCs
	// remembers the DLC names met while parsing so the picker can offer them.
	FilterAllowBranch bool     `json:"filter_allow_branch"`
	DisabledDLCs      []string `json:"disabled_dlcs"`
	SeenDLCs          []string `json:"seen_dlcs"`
}

const configFileName = "hoi4treesnap.json"

// legacyGamePathFile is the gob encoded file older versions wrote.
const legacyGamePathFile = "hoi4treesnapGamePath.txt"

func defaultConfig() Config {
	return Config{
		Language:          "l_english",
		IncludeShared:     true,
		ResolveScripted:   true,
		ColoredText:       true,
		KeepFocusTrees:    true,
		FilterAllowBranch: true,
	}
}

func configPath() string {
	return filepath.Join(binPath, configFileName)
}

// loadConfig reads the saved settings, falling back to the old game path file
// so that existing users keep their game folder after updating.
func loadConfig() Config {
	c := defaultConfig()

	b, err := os.ReadFile(configPath())
	if err == nil {
		saved := defaultConfig()
		if err := json.Unmarshal(b, &saved); err != nil {
			warnf("settings file could not be read (%v), starting from defaults", err)
		} else {
			c = saved
		}
	}

	if c.GamePath == "" {
		if p, ok := readLegacyGamePath(); ok {
			c.GamePath = p
		}
	}
	if c.Language == "" {
		c.Language = "l_english"
	}

	// Drop folders and files that no longer exist so the UI does not claim to
	// have something selected that has since been moved or deleted.
	if c.GamePath != "" && !dirExists(c.GamePath) {
		warnf("saved HOI4 folder no longer exists: %v", c.GamePath)
		c.GamePath = ""
	}
	c.ModPaths = filterExisting(c.ModPaths, dirExists)
	c.FocusTreePaths = filterExisting(c.FocusTreePaths, fileExists)
	if c.OutputDir != "" && !dirExists(c.OutputDir) {
		c.OutputDir = ""
	}

	return c
}

func (c *Config) save() {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		warnf("settings could not be encoded: %v", err)
		return
	}
	if err := os.WriteFile(configPath(), b, 0644); err != nil {
		warnf("settings could not be saved to %v: %v", configPath(), err)
	}
}

// outputDir is where generated images are written.
func (c *Config) outputDir() string {
	if c.OutputDir != "" && dirExists(c.OutputDir) {
		return c.OutputDir
	}
	return binPath
}

func readLegacyGamePath() (string, bool) {
	b, err := os.ReadFile(filepath.Join(binPath, legacyGamePathFile))
	if err != nil {
		return "", false
	}
	var p string
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(&p); err != nil {
		return "", false
	}
	if p == "" || !dirExists(p) {
		return "", false
	}
	return p, true
}

func filterExisting(paths []string, ok func(string) bool) []string {
	out := paths[:0]
	for _, p := range paths {
		if ok(p) {
			out = append(out, p)
		}
	}
	return out
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

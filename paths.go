package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Platform differences in where things live.
//
// On Windows and Linux the program is a plain executable and everything it
// writes goes next to it. On macOS a GUI program is normally an .app bundle,
// where the executable sits three levels down in Contents/MacOS. Writing the
// settings, the log and the images in there would bury them inside the bundle,
// where the user cannot reasonably find them, and a signed bundle is not even
// writable.

// resolveBinPath returns the folder the program should treat as its own: the
// folder holding the .app on macOS, the folder holding the executable
// everywhere else.
func resolveBinPath(executable string) string {
	return resolveBinPathFor(runtime.GOOS, executable)
}

// resolveBinPathFor is the logic behind resolveBinPath with the platform
// passed in, so the macOS behaviour can be tested from any machine.
func resolveBinPathFor(goos, executable string) string {
	dir := filepath.Dir(executable)
	if goos != "darwin" {
		return dir
	}
	if bundle, ok := enclosingAppBundle(dir); ok {
		return filepath.Dir(bundle)
	}
	return dir
}

// enclosingAppBundle reports the .app directory the given path sits in.
func enclosingAppBundle(dir string) (string, bool) {
	// .../Something.app/Contents/MacOS
	if !strings.EqualFold(filepath.Base(dir), "MacOS") {
		return "", false
	}
	contents := filepath.Dir(dir)
	if !strings.EqualFold(filepath.Base(contents), "Contents") {
		return "", false
	}
	app := filepath.Dir(contents)
	if !strings.EqualFold(filepath.Ext(app), ".app") {
		return "", false
	}
	return app, true
}

// gameDataFolders are the subfolders every HOI4 installation has. They are
// what tells a game folder apart from the folder that merely contains one.
var gameDataFolders = []string{"interface", "common", "localisation"}

// looksLikeGameFolder reports whether dir holds the game's data directly.
func looksLikeGameFolder(dir string) bool {
	if dir == "" {
		return false
	}
	for _, sub := range gameDataFolders {
		if dirExists(filepath.Join(dir, sub)) {
			return true
		}
	}
	return false
}

// resolveGameFolder accepts what the user picked and returns the folder the
// data actually sits in.
//
// On macOS a Paradox game is shipped as an .app, and depending on the title the
// data is either next to the bundle or inside its Contents/Resources. Rather
// than guess which one HOI4 uses, both are accepted: pick the Steam folder, or
// the bundle itself, and the right one is found either way.
func resolveGameFolder(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	if looksLikeGameFolder(dir) {
		return dir, true
	}

	// The user picked the .app itself.
	if strings.EqualFold(filepath.Ext(dir), ".app") {
		if inside := filepath.Join(dir, "Contents", "Resources"); looksLikeGameFolder(inside) {
			return inside, true
		}
	}

	// The user picked a folder that contains an .app.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".app") {
			continue
		}
		if inside := filepath.Join(dir, e.Name(), "Contents", "Resources"); looksLikeGameFolder(inside) {
			return inside, true
		}
	}

	return "", false
}

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveBinPathEscapesTheAppBundle(t *testing.T) {
	cases := []struct {
		name string
		goos string
		exe  string
		want string
	}{
		{
			name: "macOS app bundle writes next to the bundle",
			goos: "darwin",
			exe:  "/Applications/TreeSnap.app/Contents/MacOS/treesnap",
			want: "/Applications",
		},
		{
			name: "macOS bare binary writes next to itself",
			goos: "darwin",
			exe:  "/Users/someone/tools/treesnap",
			want: "/Users/someone/tools",
		},
		{
			name: "a MacOS folder that is not a bundle is left alone",
			goos: "darwin",
			exe:  "/Users/someone/MacOS/treesnap",
			want: "/Users/someone/MacOS",
		},
		{
			// Slashes rather than a Windows path: filepath splits on the
			// separator of whatever machine the test runs on, and this case is
			// about the platform check, not about path syntax.
			name: "other platforms always use the executable's folder",
			goos: "windows",
			exe:  "/tools/treesnap/hoi4treesnap.exe",
			want: "/tools/treesnap",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveBinPathFor(c.goos, c.exe)
			if filepath.ToSlash(got) != filepath.ToSlash(c.want) {
				t.Errorf("resolveBinPathFor(%q, %q) = %q, want %q", c.goos, c.exe, got, c.want)
			}
		})
	}
}

func TestResolveGameFolderFindsBothLayouts(t *testing.T) {
	root := t.TempDir()

	// The layout used on Windows and Linux: the data sits in the folder.
	plain := filepath.Join(root, "Hearts of Iron IV")
	mustMakeGameData(t, plain)

	// The layout a Paradox macOS build may use: the data lives inside the
	// application bundle.
	steam := filepath.Join(root, "steam", "common", "Hearts of Iron IV")
	bundle := filepath.Join(steam, "Hearts of Iron IV.app")
	mustMakeGameData(t, filepath.Join(bundle, "Contents", "Resources"))

	empty := filepath.Join(root, "somewhere else")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		in    string
		want  string
		found bool
	}{
		{"data directly in the folder", plain, plain, true},
		{"folder holding the app bundle", steam, filepath.Join(bundle, "Contents", "Resources"), true},
		{"the bundle itself", bundle, filepath.Join(bundle, "Contents", "Resources"), true},
		{"an unrelated folder", empty, "", false},
		{"nothing selected", "", "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := resolveGameFolder(c.in)
			if ok != c.found {
				t.Fatalf("resolveGameFolder(%q) found = %v, want %v", c.in, ok, c.found)
			}
			if got != c.want {
				t.Errorf("resolveGameFolder(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func mustMakeGameData(t *testing.T, dir string) {
	t.Helper()
	for _, sub := range []string{"interface", "common", "localisation"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

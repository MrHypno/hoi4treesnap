//go:build !windows

package main

import (
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
)

// Away from Windows there is no native picker to call into, so Fyne's own
// browser is used. It has the same shape: it returns immediately and answers
// on the Fyne goroutine.

func askForFiles(u *ui, title string, exts []string, startDir string, multi bool, done func([]string)) {
	d := dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
		if err != nil || rc == nil {
			u.dialogResult(err, nil)
			return
		}
		defer rc.Close()
		done([]string{uriToPath(rc.URI())})
	}, u.win)

	if len(exts) > 0 {
		filter := make([]string, 0, len(exts))
		for _, e := range exts {
			if len(e) > 0 && e[0] != '.' {
				e = "." + e
			}
			filter = append(filter, e)
		}
		d.SetFilter(storage.NewExtensionFileFilter(filter))
	}
	if list, err := startingList(startDir); err == nil && list != nil {
		d.SetLocation(list)
	}
	d.Show()
}

func askForFolder(u *ui, title, startDir string, done func(string)) {
	d := dialog.NewFolderOpen(func(list fyne.ListableURI, err error) {
		if err != nil || list == nil {
			u.dialogResult(err, nil)
			return
		}
		done(uriToPath(list))
	}, u.win)

	if list, err := startingList(startDir); err == nil && list != nil {
		d.SetLocation(list)
	}
	d.Show()
}

func startingList(dir string) (fyne.ListableURI, error) {
	if dir == "" || !dirExists(dir) {
		return nil, nil
	}
	return storage.ListerForURI(storage.NewFileURI(dir))
}

func uriToPath(u fyne.URI) string {
	if u == nil {
		return ""
	}
	if p, err := url.PathUnescape(u.Path()); err == nil {
		return p
	}
	return u.Path()
}

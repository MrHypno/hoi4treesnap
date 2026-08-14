package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const maxLogRows = 4000

// errDialogCancelled is what a picker returns when the user closes it.
var errDialogCancelled = errors.New("cancelled")

type logRow struct {
	level logLevel
	text  string
	count int
}

type ui struct {
	app fyne.App
	win fyne.Window

	focusValue *widget.Label
	focusBtn   *widget.Button
	gameValue  *widget.Label
	outValue   *widget.Label
	modBox     *fyne.Container

	langSelect  *widget.Select
	sharedChk   *widget.Check
	scriptedChk *widget.Check
	colorChk    *widget.Check
	linesChk    *widget.Check
	allowChk    *widget.Check
	dlcBtn      *widget.Button

	genBtn *widget.Button
	pBar   *widget.ProgressBar
	status *widget.Label

	logMu       sync.Mutex
	logRows     []logRow
	logShown    []int
	logFilter   logLevel
	logLists    []*widget.List
	mainLogList *widget.List
	logWin      fyne.Window

	running bool
}

func setupUI(a fyne.App) {
	u := &ui{app: a}
	u.win = a.NewWindow("TreeSnap " + appVersion)

	u.layout()
	setLogSink(u.appendLog)

	u.win.Resize(fyne.NewSize(900, 960))
	u.win.CenterOnScreen()
	u.win.SetOnClosed(func() {
		cfg.save()
		if u.logWin != nil {
			u.logWin.Close()
		}
	})
	u.win.ShowAndRun()
}

// layout builds the window contents. With the log detached the main window is
// just the settings and the run bar.
//
// The widgets are rebuilt rather than moved between containers: Fyne keeps a
// cached texture per label, and a label that changes canvas comes back as a
// blank rectangle.
func (u *ui) layout() {
	if u.mainLogList != nil {
		u.dropLogList(u.mainLogList)
		u.mainLogList = nil
	}

	settings := container.NewVScroll(container.NewVBox(
		u.buildFilesCard(),
		u.buildOptionsCard(),
	))
	run := u.buildRunCard()

	if u.logWin != nil {
		u.win.SetContent(container.NewBorder(nil, run, nil, nil, settings))
	} else {
		// A split keeps the log usable on short screens and lets the settings
		// scroll instead of squeezing it out of the window. The run bar sits
		// outside both so the button is always reachable.
		split := container.NewVSplit(settings, u.buildLogCard())
		split.Offset = 0.76
		u.win.SetContent(container.NewBorder(nil, run, nil, nil, split))
	}

	u.refreshAll()
	u.rebuildLogView()
}

// ------------------------------------------------------------------ files

func (u *ui) buildFilesCard() fyne.CanvasObject {
	u.focusValue = newPathLabel()
	u.gameValue = newPathLabel()
	u.outValue = newPathLabel()
	u.modBox = container.NewVBox()

	u.focusBtn = widget.NewButtonWithIcon("Select", theme.FileIcon(), u.selectFocusFiles)
	focusRow := pathRow("Focus tree file(s)", u.focusValue,
		u.focusBtn,
		widget.NewButtonWithIcon("", theme.ContentClearIcon(), func() {
			cfg.FocusTreePaths = nil
			cfg.save()
			u.refreshFocus()
		}),
	)

	gameRow := pathRow("Hearts of Iron IV folder", u.gameValue,
		widget.NewButtonWithIcon("Select", theme.FolderIcon(), u.selectGameFolder),
	)

	modRow := container.NewVBox(
		container.NewBorder(nil, nil,
			widget.NewLabelWithStyle("Dependency mod folder(s)", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			container.NewHBox(
				widget.NewButtonWithIcon("Add", theme.ContentAddIcon(), u.addModFolder),
				widget.NewButtonWithIcon("", theme.ContentClearIcon(), func() {
					cfg.ModPaths = nil
					cfg.save()
					u.refreshMods()
				}),
			),
		),
		u.modBox,
	)

	outRow := pathRow("Output folder", u.outValue,
		widget.NewButtonWithIcon("Change", theme.FolderIcon(), u.selectOutputFolder),
		widget.NewButtonWithIcon("", theme.FolderOpenIcon(), func() { openFolder(cfg.outputDir()) }),
	)

	return widget.NewCard("Files", "Remembered between runs, only the focus tree needs picking again",
		container.NewVBox(focusRow, gameRow, modRow, outRow))
}

// ---------------------------------------------------------------- options

func (u *ui) buildOptionsCard() fyne.CanvasObject {
	labels := make([]string, 0, len(hoi4Languages))
	for _, l := range hoi4Languages {
		labels = append(labels, l.Label)
	}
	u.langSelect = widget.NewSelect(labels, func(s string) {
		cfg.Language = languageTag(s)
		cfg.save()
	})

	u.sharedChk = widget.NewCheck("Include shared focus branches referenced by this tree", func(b bool) {
		cfg.IncludeShared = b
		cfg.save()
		u.refreshFocus()
	})
	sharedHint := hintLabel("Select the country tree first, then use Add to select the file that " +
		"defines its shared focuses. Everything ends up in one image.")

	u.allowChk = widget.NewCheck("Hide branches that allow_branch rules out", func(b bool) {
		cfg.FilterAllowBranch = b
		cfg.save()
		u.refreshDLCButton()
	})
	u.dlcBtn = widget.NewButtonWithIcon("DLCs...", theme.SettingsIcon(), u.showDLCWindow)
	allowRow := container.NewBorder(nil, nil, nil, u.dlcBtn, u.allowChk)
	allowHint := hintLabel("Focuses without an allow_branch are always drawn. Those behind one are " +
		"drawn only if it matches the DLCs you tick.")

	u.scriptedChk = widget.NewCheck("Resolve scripted localisation to its base text", func(b bool) {
		cfg.ResolveScripted = b
		cfg.save()
	})
	u.colorChk = widget.NewCheck("Draw §-coloured focus names in colour", func(b bool) {
		cfg.ColoredText = b
		cfg.save()
	})
	u.linesChk = widget.NewCheck("Disable line rendering", func(b bool) {
		cfg.DisableLines = b
		cfg.save()
	})

	langRow := container.NewBorder(nil, nil,
		widget.NewLabelWithStyle("Localisation language", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		nil, u.langSelect)

	return widget.NewCard("Options", "", container.NewVBox(
		langRow,
		u.sharedChk,
		sharedHint,
		allowRow,
		allowHint,
		u.scriptedChk,
		u.colorChk,
		u.linesChk,
	))
}

// -------------------------------------------------------------------- run

func (u *ui) buildRunCard() fyne.CanvasObject {
	u.genBtn = widget.NewButtonWithIcon("Generate image", theme.MediaPhotoIcon(), u.generate)
	u.genBtn.Importance = widget.HighImportance

	u.pBar = widget.NewProgressBar()
	u.pBar.Hide()

	u.status = widget.NewLabel("Ready")

	return widget.NewCard("", "", container.NewVBox(
		u.genBtn,
		u.pBar,
		u.status,
	))
}

// -------------------------------------------------------------------- log

func (u *ui) buildLogCard() fyne.CanvasObject {
	u.mainLogList = u.newLogList()
	return widget.NewCard("Log", "", container.NewBorder(
		u.buildLogControls(true),
		nil, nil, nil,
		u.mainLogList,
	))
}

func (u *ui) buildLogControls(detachable bool) fyne.CanvasObject {
	filter := widget.NewSelect(
		[]string{"Everything", "Warnings and errors", "Errors only"},
		func(s string) {
			switch s {
			case "Warnings and errors":
				u.logFilter = levelWarn
			case "Errors only":
				u.logFilter = levelError
			default:
				u.logFilter = levelInfo
			}
			u.rebuildLogView()
		})
	switch u.logFilter {
	case levelWarn:
		filter.SetSelected("Warnings and errors")
	case levelError:
		filter.SetSelected("Errors only")
	default:
		filter.SetSelected("Everything")
	}

	buttons := container.NewHBox()
	if detachable {
		buttons.Add(widget.NewButtonWithIcon("Open in a window", theme.ViewFullScreenIcon(), u.detachLog))
	}
	buttons.Add(widget.NewButtonWithIcon("Output folder", theme.FolderOpenIcon(), func() {
		openFolder(cfg.outputDir())
	}))
	buttons.Add(widget.NewButtonWithIcon("Clear", theme.ContentClearIcon(), u.clearLog))

	return container.NewBorder(nil, nil, filter, buttons)
}

// newLogList builds a list view over the shared rows. Several of them can
// exist at once, one per window showing the log.
func (u *ui) newLogList() *widget.List {
	list := widget.NewList(
		func() int {
			u.logMu.Lock()
			defer u.logMu.Unlock()
			return len(u.logShown)
		},
		func() fyne.CanvasObject {
			rt := widget.NewRichText()
			rt.Truncation = fyne.TextTruncateEllipsis
			return rt
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			row, ok := u.rowAt(i)
			rt := o.(*widget.RichText)
			if !ok {
				rt.Segments = nil
				rt.Refresh()
				return
			}
			rt.Segments = logSegments(row)
			rt.Refresh()
		},
	)
	u.logLists = append(u.logLists, list)
	return list
}

func (u *ui) rowAt(i int) (logRow, bool) {
	u.logMu.Lock()
	defer u.logMu.Unlock()
	if i < 0 || i >= len(u.logShown) {
		return logRow{}, false
	}
	return u.logRows[u.logShown[i]], true
}

// logSegments turns a row into a coloured line: a fixed width level tag, the
// message with the long folder prefixes replaced by short labels, and how many
// times it happened.
func logSegments(row logRow) []widget.RichTextSegment {
	tag := "      "
	color := theme.ColorNameForeground
	switch row.level {
	case levelWarn:
		tag = "WARN  "
		color = theme.ColorNameWarning
	case levelError:
		tag = "ERROR "
		color = theme.ColorNameError
	}

	text := shortenPaths(row.text)
	if row.count > 1 {
		text = fmt.Sprintf("%s   (%d times)", text, row.count)
	}

	return []widget.RichTextSegment{
		&widget.TextSegment{
			Text:  tag,
			Style: widget.RichTextStyle{Inline: true, ColorName: color, TextStyle: fyne.TextStyle{Bold: true, Monospace: true}},
		},
		&widget.TextSegment{
			Text:  text,
			Style: widget.RichTextStyle{Inline: true, ColorName: color},
		},
	}
}

func (u *ui) appendLog(row int, level logLevel, text string, count int) {
	u.logMu.Lock()
	for len(u.logRows) <= row {
		u.logRows = append(u.logRows, logRow{})
	}
	isNew := u.logRows[row].text == ""
	u.logRows[row] = logRow{level: level, text: text, count: count}
	if isNew && level >= u.logFilter {
		u.logShown = append(u.logShown, row)
	}
	if len(u.logRows) > maxLogRows {
		u.logMu.Unlock()
		return
	}
	u.logMu.Unlock()

	fyne.Do(func() {
		for _, l := range u.logLists {
			l.Refresh()
		}
		if isNew && len(u.logLists) > 0 {
			u.logLists[len(u.logLists)-1].ScrollToBottom()
		}
	})
}

func (u *ui) rebuildLogView() {
	u.logMu.Lock()
	u.logShown = u.logShown[:0]
	for i, r := range u.logRows {
		if r.text != "" && r.level >= u.logFilter {
			u.logShown = append(u.logShown, i)
		}
	}
	u.logMu.Unlock()

	for _, l := range u.logLists {
		l.Refresh()
	}
}

func (u *ui) clearLog() {
	resetLog()
	u.logMu.Lock()
	u.logRows = nil
	u.logShown = nil
	u.logMu.Unlock()
	for _, l := range u.logLists {
		l.Refresh()
	}
}

// detachLog moves the log into a window of its own and takes it out of the
// main window, so the settings get the whole height.
func (u *ui) detachLog() {
	if u.logWin != nil {
		u.logWin.RequestFocus()
		return
	}

	w := u.app.NewWindow("TreeSnap log")
	list := u.newLogList()
	w.SetContent(container.NewBorder(u.buildLogControls(false), nil, nil, nil, list))
	w.Resize(fyne.NewSize(900, 520))
	w.CenterOnScreen()

	w.SetOnClosed(func() {
		u.dropLogList(list)
		u.logWin = nil
		// Put the log back into the main window.
		u.layout()
	})

	u.logWin = w
	u.layout()
	w.Show()
}

func (u *ui) dropLogList(list *widget.List) {
	for i, l := range u.logLists {
		if l == list {
			u.logLists = append(u.logLists[:i], u.logLists[i+1:]...)
			return
		}
	}
}

// ------------------------------------------------------------- selections

// appendingFocusFiles reports whether the next pick is added to the selection
// instead of replacing it. That is the case once a tree is selected and shared
// branches are on, since the file defining them has to be added next to it.
func (u *ui) appendingFocusFiles() bool {
	return cfg.IncludeShared && len(cfg.FocusTreePaths) > 0
}

func (u *ui) selectFocusFiles() {
	title := "National focus file(s)"
	appending := u.appendingFocusFiles()
	if appending {
		title = "File that defines the shared focuses"
	}

	askForFiles(u, title, []string{"txt"}, cfg.LastFocusDir, true, func(files []string) {
		if len(files) == 0 {
			return
		}
		if appending {
			for _, f := range files {
				if !containsString(cfg.FocusTreePaths, f) {
					cfg.FocusTreePaths = append(cfg.FocusTreePaths, f)
				}
			}
		} else {
			cfg.FocusTreePaths = files
		}
		cfg.LastFocusDir = filepath.Dir(files[0])
		cfg.save()
		u.refreshFocus()
	})
}

func (u *ui) selectGameFolder() {
	askForFolder(u, "Hearts of Iron IV folder", cfg.GamePath, func(dir string) {
		if dir == "" {
			return
		}
		if !dirExists(filepath.Join(dir, "interface")) {
			dialog.ShowError(errors.New("that folder has no interface subfolder, it does not look like a HOI4 installation"), u.win)
			return
		}
		cfg.GamePath = dir
		cfg.save()
		u.refreshGame()
	})
}

func (u *ui) addModFolder() {
	start := ""
	if len(cfg.ModPaths) > 0 {
		start = filepath.Dir(cfg.ModPaths[len(cfg.ModPaths)-1])
	}
	askForFolder(u, "Dependency mod folder", start, func(dir string) {
		if dir == "" || containsString(cfg.ModPaths, dir) {
			return
		}
		cfg.ModPaths = append(cfg.ModPaths, dir)
		cfg.save()
		u.refreshMods()
	})
}

func (u *ui) selectOutputFolder() {
	askForFolder(u, "Where should the images go?", cfg.outputDir(), func(dir string) {
		if dir == "" {
			return
		}
		cfg.OutputDir = dir
		cfg.save()
		u.refreshOutput()
	})
}

// dialogResult brings a picker's answer back onto the Fyne goroutine.
func (u *ui) dialogResult(err error, ok func()) {
	fyne.Do(func() {
		if err != nil {
			if !errors.Is(err, errDialogCancelled) {
				dialog.ShowError(err, u.win)
			}
			return
		}
		if ok != nil {
			ok()
		}
	})
}

// showDLCWindow lets the user say which DLCs count as owned.
func (u *ui) showDLCWindow() {
	w := u.app.NewWindow("DLCs")

	names := knownDLCs()
	checks := make([]*widget.Check, 0, len(names))
	items := container.NewVBox()
	for _, name := range names {
		name := name
		c := widget.NewCheck(dlcDisplayName(name), func(on bool) {
			setDLCEnabled(name, on)
			cfg.save()
		})
		c.SetChecked(dlcEnabled(name))
		checks = append(checks, c)
		items.Add(c)
	}

	setAll := func(on bool) {
		for _, c := range checks {
			c.SetChecked(on)
		}
	}

	head := hintLabel("A branch behind \"has_dlc\" is drawn only when that DLC is ticked, and a " +
		"branch hidden by a DLC you do not own is drawn when it is not. Only the DLCs that gate " +
		"focus branches are listed; any others a mod uses are added after a run.")

	buttons := container.NewHBox(
		widget.NewButton("Select all", func() { setAll(true) }),
		widget.NewButton("Select none", func() { setAll(false) }),
		widget.NewButton("Close", func() { w.Close() }),
	)

	w.SetContent(container.NewBorder(head, buttons, nil, nil, container.NewVScroll(items)))
	w.Resize(fyne.NewSize(460, 560))
	w.CenterOnScreen()
	w.Show()
}

// ---------------------------------------------------------------- refresh

func (u *ui) refreshAll() {
	u.refreshFocus()
	u.refreshGame()
	u.refreshMods()
	u.refreshOutput()

	u.langSelect.SetSelected(languageLabel(cfg.Language))
	u.sharedChk.SetChecked(cfg.IncludeShared)
	u.scriptedChk.SetChecked(cfg.ResolveScripted)
	u.colorChk.SetChecked(cfg.ColoredText)
	u.linesChk.SetChecked(cfg.DisableLines)
	u.allowChk.SetChecked(cfg.FilterAllowBranch)
	u.refreshDLCButton()
}

func (u *ui) refreshDLCButton() {
	if u.dlcBtn == nil {
		return
	}
	if cfg.FilterAllowBranch {
		u.dlcBtn.Enable()
	} else {
		u.dlcBtn.Disable()
	}
}

func (u *ui) refreshFocus() {
	switch {
	case len(cfg.FocusTreePaths) == 0:
		setPathLabel(u.focusValue, "")

	case len(cfg.FocusTreePaths) == 1:
		setPathLabel(u.focusValue, cfg.FocusTreePaths[0])

	case cfg.IncludeShared:
		// The first file is the tree, the rest only contribute shared focuses.
		lines := []string{"tree:   " + cfg.FocusTreePaths[0]}
		for _, p := range cfg.FocusTreePaths[1:] {
			lines = append(lines, "shared: "+p)
		}
		setPathLabel(u.focusValue, strings.Join(lines, "\n"))

	default:
		setPathLabel(u.focusValue, fmt.Sprintf("%d files, one image each:\n%s",
			len(cfg.FocusTreePaths), strings.Join(cfg.FocusTreePaths, "\n")))
	}

	if u.focusBtn != nil {
		if u.appendingFocusFiles() {
			u.focusBtn.SetText("Add")
			u.focusBtn.SetIcon(theme.ContentAddIcon())
		} else {
			u.focusBtn.SetText("Select")
			u.focusBtn.SetIcon(theme.FileIcon())
		}
	}
}

func (u *ui) refreshGame() {
	setPathLabel(u.gameValue, cfg.GamePath)
}

func (u *ui) refreshOutput() {
	if cfg.OutputDir == "" {
		u.outValue.TextStyle = fyne.TextStyle{Italic: true}
		u.outValue.SetText(binPath + "  (next to the program)")
		u.outValue.Refresh()
		return
	}
	setPathLabel(u.outValue, cfg.OutputDir)
}

func (u *ui) refreshMods() {
	u.modBox.RemoveAll()

	if len(cfg.ModPaths) == 0 {
		l := widget.NewLabel("Not selected")
		l.TextStyle = fyne.TextStyle{Italic: true}
		u.modBox.Add(l)
		u.modBox.Refresh()
		return
	}

	for i, p := range cfg.ModPaths {
		index := i
		label := widget.NewLabel(p)
		label.Truncation = fyne.TextTruncateEllipsis
		remove := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
			u.removeMod(index)
		})
		u.modBox.Add(container.NewBorder(nil, nil, nil, remove, label))
	}
	u.modBox.Refresh()
}

func (u *ui) removeMod(i int) {
	if i < 0 || i >= len(cfg.ModPaths) {
		return
	}
	cfg.ModPaths = append(cfg.ModPaths[:i], cfg.ModPaths[i+1:]...)
	cfg.save()
	u.refreshMods()
}

// --------------------------------------------------------------- generate

func (u *ui) generate() {
	if u.running {
		return
	}

	if len(cfg.FocusTreePaths) == 0 {
		dialog.ShowError(errors.New("select a focus tree file first"), u.win)
		return
	}
	if cfg.GamePath == "" {
		dialog.ShowError(errors.New("select the Hearts of Iron IV folder first"), u.win)
		return
	}

	u.running = true
	u.genBtn.Disable()
	u.clearLog()
	u.pBar.SetValue(0)
	u.pBar.Show()
	u.status.SetText("Starting")
	cfg.save()

	go func() {
		files, err := generateAll(func(v float64, status string) {
			fyne.Do(func() {
				u.pBar.SetValue(v)
				u.status.SetText(status)
			})
		})

		logPath, logErr := writeLogFile(cfg.outputDir())
		if logErr != nil {
			fmt.Println("log file could not be written:", logErr)
		}
		warnings, errs := logSummary()

		fyne.Do(func() {
			u.running = false
			u.genBtn.Enable()
			u.pBar.Hide()
			u.pBar.SetValue(0)

			if err != nil {
				u.status.SetText("Failed: " + err.Error())
				dialog.ShowError(err, u.win)
				return
			}

			summary := fmt.Sprintf("%d image(s) saved to\n%s", len(files), cfg.outputDir())
			if warnings > 0 || errs > 0 {
				summary += fmt.Sprintf("\n\n%d warning(s) and %d error(s) were logged.", warnings, errs)
				if logPath != "" {
					summary += "\nFull log: " + logPath
				}
			}
			u.status.SetText(fmt.Sprintf("Done. %d image(s), %d warning(s), %d error(s)", len(files), warnings, errs))
			dialog.ShowInformation("Finished", summary, u.win)
		})
	}()
}

// ----------------------------------------------------------------- pieces

// hintLabel is the small explanatory line under an option.
func hintLabel(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	l.TextStyle = fyne.TextStyle{Italic: true}
	return l
}

func newPathLabel() *widget.Label {
	l := widget.NewLabel("")
	l.Wrapping = fyne.TextWrapBreak
	return l
}

func setPathLabel(l *widget.Label, value string) {
	if value == "" {
		l.TextStyle = fyne.TextStyle{Italic: true}
		l.SetText("Not selected")
	} else {
		l.TextStyle = fyne.TextStyle{}
		l.SetText(value)
	}
	l.Refresh()
}

func pathRow(title string, value *widget.Label, buttons ...fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(
		container.NewBorder(nil, nil,
			widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			container.NewHBox(buttons...),
		),
		value,
	)
}

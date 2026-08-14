package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// The tool used to stop at the first problem it met: a missing sprite, an
// unparsable line, a font it could not load. Every one of those popped an error
// window and abandoned the image.
//
// Everything now goes through this log instead. Only errors that make it
// impossible to continue at all reach fatalf; the rest are collected, shown in
// the window and written to hoi4treesnap.log next to the binary.

type logLevel int

const (
	levelInfo logLevel = iota
	levelWarn
	levelError
)

func (l logLevel) String() string {
	switch l {
	case levelWarn:
		return "WARN"
	case levelError:
		return "ERROR"
	}
	return "INFO"
}

type logEntry struct {
	Level logLevel
	Text  string
	Count int
	Row   int
}

var (
	logMu      sync.Mutex
	logEntries []*logEntry
	logIndex   = map[string]*logEntry{}
	logSink    func(row int, level logLevel, text string, count int)
	logCounts  [3]int
)

// setLogSink installs the callback the UI uses to mirror the log live. It is
// called with the row the message belongs to, so a repeat updates the line it
// is already on instead of adding another one.
func setLogSink(f func(row int, level logLevel, text string, count int)) {
	logMu.Lock()
	logSink = f
	// Anything logged before the window existed, such as a saved folder that
	// has since been moved, would otherwise never be seen.
	early := make([]logEntry, 0, len(logEntries))
	for _, e := range logEntries {
		early = append(early, *e)
	}
	logMu.Unlock()

	if f == nil {
		return
	}
	for _, e := range early {
		f(e.Row, e.Level, e.Text, e.Count)
	}
}

func resetLog() {
	logMu.Lock()
	logEntries = nil
	logIndex = map[string]*logEntry{}
	logCounts = [3]int{}
	logMu.Unlock()
}

// logMessage records one message. Identical messages are folded together with
// a counter so that a sprite missing from two hundred focuses stays readable.
func logMessage(level logLevel, text string) {
	logMu.Lock()
	logCounts[level]++
	e, seen := logIndex[text]
	if seen {
		e.Count++
	} else {
		e = &logEntry{Level: level, Text: text, Count: 1, Row: len(logEntries)}
		logIndex[text] = e
		logEntries = append(logEntries, e)
	}
	sink := logSink
	row, count := e.Row, e.Count
	logMu.Unlock()

	if !seen {
		fmt.Printf("[%s] %s\n", level, text)
	}
	if sink != nil && (!seen || worthRefreshing(count)) {
		sink(row, level, text, count)
	}
}

// worthRefreshing keeps the counter on a repeated line moving without
// redrawing the list for every single occurrence.
func worthRefreshing(count int) bool {
	switch {
	case count < 10:
		return true
	case count < 100:
		return count%10 == 0
	default:
		return count%100 == 0
	}
}

// Log lines carry full paths so the log file stays useful on its own, but in
// the window those swallow the whole line. These aliases are set up when a run
// starts and only used for display.
var (
	pathAliasMu sync.RWMutex
	pathAliases [][2]string
)

func setPathAliases(pairs [][2]string) {
	pathAliasMu.Lock()
	pathAliases = pairs
	pathAliasMu.Unlock()
}

// shortenPaths replaces the game and mod folders with a short label.
func shortenPaths(s string) string {
	pathAliasMu.RLock()
	defer pathAliasMu.RUnlock()
	for _, p := range pathAliases {
		s = strings.ReplaceAll(s, p[0], p[1])
	}
	return s
}

func infof(format string, args ...interface{}) {
	logMessage(levelInfo, fmt.Sprintf(format, args...))
}

func warnf(format string, args ...interface{}) {
	logMessage(levelWarn, fmt.Sprintf(format, args...))
}

func errorf(format string, args ...interface{}) {
	logMessage(levelError, fmt.Sprintf(format, args...))
}

// logSummary returns counts of warnings and errors recorded so far.
func logSummary() (warnings, errors int) {
	logMu.Lock()
	defer logMu.Unlock()
	return logCounts[levelWarn], logCounts[levelError]
}

// writeLogFile dumps the collected messages next to the binary. Errors first,
// then warnings, then info, each with the number of times it happened.
func writeLogFile(dir string) (string, error) {
	logMu.Lock()
	entries := make([]*logEntry, len(logEntries))
	copy(entries, logEntries)
	logMu.Unlock()

	if len(entries) == 0 {
		return "", nil
	}

	ordered := make([]*logEntry, len(entries))
	copy(ordered, entries)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Level > ordered[j].Level
	})

	var b strings.Builder
	fmt.Fprintf(&b, "hoi4treesnap log - %s\r\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "%s\r\n\r\n", strings.Repeat("=", 60))
	for _, e := range ordered {
		line := e.Text
		if e.Count > 1 {
			line = fmt.Sprintf("%s  (x%d)", line, e.Count)
		}
		fmt.Fprintf(&b, "[%s] %s\r\n", e.Level, line)
	}

	path := filepath.Join(dir, "hoi4treesnap.log")
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return "", err
	}
	return path, nil
}

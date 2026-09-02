package ui

import (
	"fmt"
	"time"
)

// date_format.go is the single home for turning timestamps and durations into the
// strings catclip displays. Keeping every date/time/duration rule here means there
// is one place to read, change, and test them, and one set of helpers for all
// callers — the `--recent` picker, the `--metadata` report, `--verbose` timing, the
// large-copy bundle writer, and future features.
//
// Every function below is a pure transform of its time inputs using only the
// standard library (no catclip types), so this file depends on nothing else in the
// package and can be called from anywhere without risk of an import cycle.

// formatFinderModifiedLabel renders a file's modified time as a Finder-inspired
// relative label. It is the `--recent` picker's cutoff-row format, and it encodes
// recency by *omitting* detail — the current year is dropped, and dates within the
// past week show a weekday name:
//
//	today      -> "Today at 3:04 PM"
//	yesterday  -> "Yesterday at 3:04 PM"
//	< 7 days   -> "Monday at 3:04 PM"      (same year)
//	this year  -> "Jan 2 at 3:04 PM"       (year omitted)
//	older      -> "Jan 2, 2006 at 3:04 PM"
//
// This is intentionally NOT byte-identical to macOS Finder: real Finder shows no
// weekday tier and always prints the year. The weekday + year-omission here are a
// deliberate, richer recency cue for the `--recent` UI. The `--metadata` report uses
// the stricter real-Finder spec instead (always year, no weekday) — that formatter
// lives below.
func formatFinderModifiedLabel(now, modTime time.Time) string {
	if modTime.IsZero() {
		return "unknown date"
	}

	loc := now.Location()
	if loc == nil {
		loc = time.Local
	}
	now = now.In(loc)
	modTime = modTime.In(loc)

	switch {
	case sameCalendarDay(now, modTime):
		return "Today at " + modTime.Format("3:04 PM")
	case sameCalendarDay(now.AddDate(0, 0, -1), modTime):
		return "Yesterday at " + modTime.Format("3:04 PM")
	case now.Year() == modTime.Year() && now.Sub(modTime) < 7*24*time.Hour:
		return modTime.Format("Monday at 3:04 PM")
	case now.Year() == modTime.Year():
		return modTime.Format("Jan 2 at 3:04 PM")
	default:
		return modTime.Format("Jan 2, 2006 at 3:04 PM")
	}
}

// formatFinderModifiedSpec renders a file's modified time as the *strict* macOS
// Finder "Date Modified" spec, used by the `--metadata` report. Unlike
// formatFinderModifiedLabel it adds no recency embellishment — no weekday tier and
// the year is always shown:
//
//	today      -> "Today at 3:04 PM"
//	yesterday  -> "Yesterday at 3:04 PM"
//	otherwise  -> "Jan 2, 2006 at 3:04 PM"   (abbreviated month, year always)
//
// This is exactly what Finder's column shows, which is also why it suits agents:
// "Today"/"Yesterday" self-describe (catclip computes them from now), and every
// older entry is a full, unambiguous date the agent reads or compares without
// decoding a convention or knowing "now".
func formatFinderModifiedSpec(now, modTime time.Time) string {
	if modTime.IsZero() {
		return "unknown date"
	}

	loc := now.Location()
	if loc == nil {
		loc = time.Local
	}
	now = now.In(loc)
	modTime = modTime.In(loc)

	switch {
	case sameCalendarDay(now, modTime):
		return "Today at " + modTime.Format("3:04 PM")
	case sameCalendarDay(now.AddDate(0, 0, -1), modTime):
		return "Yesterday at " + modTime.Format("3:04 PM")
	default:
		return modTime.Format("Jan 2, 2006 at 3:04 PM")
	}
}

// sameCalendarDay reports whether a and b fall on the same calendar day, compared
// in each value's own location. Helper for the relative-date formatters above.
func sameCalendarDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// formatRecentAge renders a file's modified time as an elapsed-*duration* label,
// used in the `--recent` picker's preview pane. Unlike formatFinderModifiedLabel
// (which is clock-relative) this counts time since the file changed:
//
//	< 1 minute -> "just now"   (also when modTime is in the future)
//	< 1 hour   -> "5m ago"
//	< 1 day    -> "3h ago"
//	< 1 week   -> "2d ago"
//	< 30 days  -> "1w ago"
//	< 1 year   -> "4mo ago"
//	otherwise  -> "2y ago"
//
// The `--metadata` report deliberately avoids this "… ago" style (Finder has no
// elapsed-time form); it is specific to the `--recent` preview.
func formatRecentAge(now, modTime time.Time) string {
	if modTime.IsZero() {
		return "unknown"
	}

	if now.Before(modTime) {
		return "just now"
	}

	d := now.Sub(modTime)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dw ago", int(d/(7*24*time.Hour)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d/(30*24*time.Hour)))
	default:
		return fmt.Sprintf("%dy ago", int(d/(365*24*time.Hour)))
	}
}

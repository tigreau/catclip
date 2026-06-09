package ui

import (
	"testing"
	"time"
)

// TestFormatFinderModifiedSpec pins the strict Finder spec used by the --preview
// table: Today / Yesterday / absolute-with-year, no weekday tier, no year omission
// — exactly what Finder's Date Modified column shows (verified against screenshots
// for now = Wed 2026-05-27).
func TestFormatFinderModifiedSpec(t *testing.T) {
	now := time.Date(2026, 5, 27, 15, 45, 0, 0, time.UTC)
	cases := []struct {
		name    string
		modTime time.Time
		want    string
	}{
		{"today", time.Date(2026, 5, 27, 9, 30, 0, 0, time.UTC), "Today at 9:30 AM"},
		{"yesterday", time.Date(2026, 5, 26, 23, 41, 0, 0, time.UTC), "Yesterday at 11:41 PM"},
		{"two days ago is absolute, not a weekday", time.Date(2026, 5, 25, 14, 1, 0, 0, time.UTC), "May 25, 2026 at 2:01 PM"},
		{"five days ago is absolute, not a weekday", time.Date(2026, 5, 22, 18, 42, 0, 0, time.UTC), "May 22, 2026 at 6:42 PM"},
		{"older keeps the year", time.Date(2026, 3, 11, 2, 17, 0, 0, time.UTC), "Mar 11, 2026 at 2:17 AM"},
		{"prior year", time.Date(2025, 11, 18, 20, 31, 0, 0, time.UTC), "Nov 18, 2025 at 8:31 PM"},
		{"zero is unknown", time.Time{}, "unknown date"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatFinderModifiedSpec(now, tc.modTime)
			if got != tc.want {
				t.Errorf("formatFinderModifiedSpec = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFormatFinderModifiedSpecNoWeekday is the explicit guard for the screenshot
// finding: every day in the 2..6-day window renders as an absolute date, never a
// weekday name (the --recent label tier does weekdays; the --preview spec does not).
func TestFormatFinderModifiedSpecNoWeekday(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	weekdays := []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}
	for back := 2; back <= 6; back++ {
		got := formatFinderModifiedSpec(now, now.AddDate(0, 0, -back))
		for _, wd := range weekdays {
			if got == wd+" at 12:00 PM" || (len(got) >= len(wd) && got[:len(wd)] == wd) {
				t.Errorf("%d days back rendered a weekday (%q); --preview spec must use an absolute date", back, got)
			}
		}
	}
}

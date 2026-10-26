package ui

import (
	"fmt"
	"io"
	"strings"

	"github.com/tigreau/catclip/internal/platform"
	renderpkg "github.com/tigreau/catclip/internal/render"
)

// WriteMetadataReportWithPalette styles the existing structured report without
// buffering it or revisiting files. addedBytes excludes source-provided escapes
// and lets the sink retain plain-payload size accounting.
func WriteMetadataReportWithPalette(w io.Writer, report *MetadataReport, colors platform.Palette) (addedBytes int64, err error) {
	if colors.Reset == "" {
		return 0, WriteMetadataReport(w, report)
	}
	style := &metadataStyle{colors: colors}
	err = writeMetadataReport(w, report, style)
	return style.addedBytes, err
}

type metadataStyle struct {
	colors     platform.Palette
	addedBytes int64
}

func (s *metadataStyle) text(value, color string) string {
	if s == nil || color == "" || value == "" {
		return value
	}
	s.addedBytes += int64(len(color) + len(s.colors.Reset))
	return color + value + s.colors.Reset
}

func metadataStyleFrom(styles []*metadataStyle) *metadataStyle {
	if len(styles) == 0 {
		return nil
	}
	return styles[0]
}

// Style a whole generated line, preserving the existing format and byte order.
// This helper only runs on the terminal path; plain formatting stays direct.
func (s *metadataStyle) line(w io.Writer, color, format string, args ...any) error {
	line := fmt.Sprintf(format, args...)
	_, err := io.WriteString(w, s.text(line, color))
	return err
}

func (s *metadataStyle) gitColor(status string) string {
	switch {
	case strings.Contains(status, "D"):
		return s.colors.Err
	case strings.Contains(status, "M"), strings.Contains(status, "R"), strings.Contains(status, "C"):
		return s.colors.Warn
	case strings.Contains(status, "A"), status == "S":
		return s.colors.OK
	case strings.Contains(status, "?"):
		return s.colors.Git
	default:
		return s.colors.Dim
	}
}

func (s *metadataStyle) size(value string, bytes int64) string {
	color := s.colors.Dim
	if bytes >= renderpkg.SizeErrorBytes {
		color = s.colors.Err
	} else if bytes >= renderpkg.SizeWarnBytes {
		color = s.colors.Warn
	}
	return s.text(value, color)
}

func (s *metadataStyle) path(value string) string {
	split := strings.LastIndexAny(value, "/\\") + 1
	return s.text(value[:split], s.colors.Dim) + s.text(value[split:], s.colors.Bold)
}

func (s *metadataStyle) modified(value string) string {
	color := s.colors.Dim
	// The report already captures local calendar-day labels at build time.
	if value == "Today" || strings.HasPrefix(value, "Today at ") {
		color = s.colors.Bold + s.colors.Prompt
	} else if value == "Yesterday" || strings.HasPrefix(value, "Yesterday at ") {
		color = s.colors.Prompt
	}
	return s.text(value, color)
}

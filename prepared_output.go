package catclip

import (
	"bytes"
	"fmt"
	"os"
	"strings"
)

// preparedFileUnit is the post-dedupe output contract shared by preview/report
// and final emit. Payload holds fully prepared output for diff/snippet modes.
// Full-file units keep Payload nil so emit can continue streaming from disk.
type preparedFileUnit struct {
	Entry     fileEntry
	Payload   []byte
	BodyBytes int64
}

func prepareFileUnits(gitCtx gitContext, entries []fileEntry) ([]preparedFileUnit, error) {
	if len(entries) == 0 {
		return nil, nil
	}

	units := make([]preparedFileUnit, 0, len(entries))
	for _, entry := range entries {
		unit, keep, err := prepareFileUnit(gitCtx, entry)
		if err != nil {
			return nil, err
		}
		if !keep {
			continue
		}
		units = append(units, unit)
	}
	return units, nil
}

func prepareFileUnit(gitCtx gitContext, entry fileEntry) (preparedFileUnit, bool, error) {
	unit := preparedFileUnit{Entry: entry}

	switch entry.Mode {
	case entryModeDiff:
		payload, bodyBytes, keep, err := buildPreparedDiffPayload(gitCtx, entry)
		if err != nil {
			return preparedFileUnit{}, false, err
		}
		if !keep {
			return preparedFileUnit{}, false, nil
		}
		unit.Payload = payload
		unit.BodyBytes = bodyBytes
		return unit, true, nil
	case entryModeSnippet:
		payload, bodyBytes, err := buildPreparedSnippetPayload(entry)
		if err != nil {
			return preparedFileUnit{}, false, err
		}
		if len(payload) == 0 {
			return preparedFileUnit{}, false, nil
		}
		unit.Payload = payload
		unit.BodyBytes = bodyBytes
		return unit, true, nil
	default:
		bodyBytes, err := fileBodySize(entry.AbsPath)
		if err != nil {
			return preparedFileUnit{}, false, err
		}
		unit.BodyBytes = bodyBytes
		return unit, true, nil
	}
}

func buildPreparedSnippetPayload(entry fileEntry) ([]byte, int64, error) {
	snapshot, err := loadTextSnapshot(entry.AbsPath, entry.RelPath)
	if err != nil {
		return nil, 0, err
	}
	snippet, err := resolveSnippetFromSnapshot(snapshot, entry.SnippetPattern)
	if err != nil {
		return nil, 0, err
	}
	if len(snippet.Ranges) == 0 {
		return nil, 0, nil
	}

	var payload bytes.Buffer
	var bodyBytes int64
	for _, r := range snippet.Ranges {
		if _, err := fmt.Fprintf(&payload, "<file path=\"%s\" snippet=\"%d-%d\">\n", entry.RelPath, r.Start, r.End); err != nil {
			return nil, 0, err
		}
		for i := r.Start - 1; i < r.End; i++ {
			if _, err := payload.WriteString(snippet.Lines[i]); err != nil {
				return nil, 0, err
			}
			if err := payload.WriteByte('\n'); err != nil {
				return nil, 0, err
			}
			bodyBytes += int64(len(snippet.Lines[i]) + 1)
		}
		if _, err := payload.Write(fileCloseTag); err != nil {
			return nil, 0, err
		}
	}
	return payload.Bytes(), bodyBytes, nil
}

func buildPreparedDiffPayload(gitCtx gitContext, entry fileEntry) ([]byte, int64, bool, error) {
	diffOutput, diffType, tracked, err := diffEntryOutput(gitCtx, entry)
	if err != nil {
		return nil, 0, false, err
	}
	if !tracked {
		snapshot, err := loadBodySnapshot(entry.AbsPath, entry.RelPath)
		if err != nil {
			return nil, 0, false, err
		}
		payload, bodyBytes := buildWrappedPayload(entry.RelPath, "untracked", snapshot.RawBytes)
		return payload, bodyBytes, true, nil
	}
	if strings.TrimSpace(diffOutput) == "" {
		return nil, 0, false, nil
	}

	body := []byte(diffOutput)
	payload, bodyBytes := buildWrappedPayload(entry.RelPath, diffType, body)
	return payload, bodyBytes, true, nil
}

func buildWrappedPayload(relPath, typeAttr string, body []byte) ([]byte, int64) {
	payload := make([]byte, 0, len(body)+len(relPath)+len(typeAttr)+32)
	payload = append(payload, buildFileOpenTag(relPath, typeAttr)...)
	payload = append(payload, body...)
	if len(body) > 0 && body[len(body)-1] != '\n' {
		payload = append(payload, fileCloseTagWithNewline...)
		return payload, int64(len(body) + 1)
	}
	payload = append(payload, fileCloseTag...)
	return payload, int64(len(body))
}

func fileBodySize(absPath string) (int64, error) {
	info, err := os.Lstat(absPath)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

package output

import (
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// nativeTextClipboard writes text to the system clipboard without a child
// process. Set by the Windows build (text_clipboard_windows.go); nil
// elsewhere, where the platform's clipboard command is used instead.
var nativeTextClipboard func(payload []byte) error

// NativeClipboardSinkName names the native Windows clipboard write in
// --verbose timings.
const NativeClipboardSinkName = "Windows clipboard API"

func useNativeTextClipboard(plat string) bool {
	return plat == "windows" && nativeTextClipboard != nil
}

// clipboardEncoder converts a UTF-8 payload to the UTF-16 text Windows
// programs expect on the clipboard, in chunks, without copying the whole
// payload: LF becomes CRLF (as clip.exe produced; existing CRLFs are kept)
// and each invalid byte becomes U+FFFD. Copying 153 MB of Linux sources went
// from a 2.5 GB peak with whole-text conversions to one UTF-16 copy.
type clipboardEncoder struct {
	src    []byte
	pos    int
	prevCR bool
}

// fill writes UTF-16 units into dst and returns how many it wrote; 0 means
// the payload is done. dst must hold at least 2 units.
func (e *clipboardEncoder) fill(dst []uint16) int {
	n := 0
	for e.pos < len(e.src) && len(dst)-n >= 2 {
		b := e.src[e.pos]
		if b < utf8.RuneSelf {
			if b == '\n' && !e.prevCR {
				dst[n] = '\r'
				n++
			}
			dst[n] = uint16(b)
			n++
			e.prevCR = b == '\r'
			e.pos++
			continue
		}
		r, size := utf8.DecodeRune(e.src[e.pos:])
		e.pos += size
		e.prevCR = false
		if r >= 0x10000 {
			r1, r2 := utf16.EncodeRune(r)
			dst[n], dst[n+1] = uint16(r1), uint16(r2)
			n += 2
			continue
		}
		dst[n] = uint16(r) // utf8.RuneError (U+FFFD) for an invalid byte
		n++
	}
	return n
}

// clipboardUTF16Len counts the units clipboardEncoder will produce.
func clipboardUTF16Len(payload []byte) int {
	e := clipboardEncoder{src: payload}
	var scratch [4096]uint16
	total := 0
	for {
		n := e.fill(scratch[:])
		if n == 0 {
			return total
		}
		total += n
	}
}

func emitNativeTextClipboard(payload []byte, generateDuration time.Duration) (EmitStats, error) {
	finalizeStarted := time.Now()
	err := nativeTextClipboard(payload)
	finalizeDuration := time.Since(finalizeStarted)
	if err != nil {
		return EmitStats{}, err
	}
	return EmitStats{
		PayloadBytes:         int64(len(payload)),
		GenerateDuration:     generateDuration,
		SinkFinalizeDuration: finalizeDuration,
		SinkName:             NativeClipboardSinkName,
	}, nil
}

//go:build !windows

package platform

import (
	"errors"
	"strings"
)

func splitWindowsEditorCommand(command string) ([]string, error) {
	var args []string
	for i := 0; i < len(command); {
		for i < len(command) && isWindowsEditorSpace(command[i]) {
			i++
		}
		if i >= len(command) {
			break
		}

		var arg strings.Builder
		inQuotes := false
		for i < len(command) {
			switch command[i] {
			case ' ', '\t':
				if !inQuotes {
					goto endArg
				}
				arg.WriteByte(command[i])
				i++
			case '"':
				inQuotes = !inQuotes
				i++
			case '\\':
				start := i
				for i < len(command) && command[i] == '\\' {
					i++
				}
				slashes := i - start
				if i < len(command) && command[i] == '"' {
					arg.WriteString(strings.Repeat(`\`, slashes/2))
					if slashes%2 == 0 {
						inQuotes = !inQuotes
					} else {
						arg.WriteByte('"')
					}
					i++
					continue
				}
				arg.WriteString(strings.Repeat(`\`, slashes))
			default:
				arg.WriteByte(command[i])
				i++
			}
		}

	endArg:
		if inQuotes {
			return nil, errors.New("unclosed quote")
		}
		args = append(args, arg.String())
	}
	return args, nil
}

func isWindowsEditorSpace(b byte) bool {
	return b == ' ' || b == '\t'
}

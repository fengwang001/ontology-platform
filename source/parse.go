package source

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrLineIncomplete  = errors.New("config file: incomplete line")
	ErrKeyIncomplete   = errors.New("config file: incomplete key")
	ErrValueIncomplete = errors.New("config file: incomplete value")
)

// terminator is the sentinel line that must end every config file so that
// truncation is always detectable.
const terminator = "."

// ParseFile parses a config file of "key = value" lines terminated by a
// lone "." line. Any truncation of a well-formed file yields a decidable
// error carrying a 1-based line number.
func ParseFile(layer Layer, data []byte) ([]Entry, error) {
	text := string(data)
	lines := strings.Split(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		last := lines[len(lines)-1]
		lineNo := len(lines)
		if strings.Contains(last, "=") {
			return nil, fmt.Errorf("line %d: %w", lineNo, ErrValueIncomplete)
		}
		return nil, fmt.Errorf("line %d: %w", lineNo, ErrKeyIncomplete)
	}
	lines = lines[:len(lines)-1] // drop empty tail after final newline
	if len(lines) == 0 || lines[len(lines)-1] != terminator {
		return nil, fmt.Errorf("line %d: %w", len(lines)+1, ErrLineIncomplete)
	}
	lines = lines[:len(lines)-1] // drop terminator
	out := make([]Entry, 0, len(lines))
	for i, line := range lines {
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("line %d: %w", i+1, ErrKeyIncomplete)
		}
		key := strings.TrimSpace(kv[0])
		if err := CheckKey(key); err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		out = append(out, Entry{Key: key, Value: strings.TrimSpace(kv[1]), Layer: layer})
	}
	return out, nil
}

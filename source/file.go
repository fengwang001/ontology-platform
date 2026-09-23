package source

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrLineIncomplete 表示末行有部分值但缺少换行终止。
	ErrLineIncomplete = errors.New("source: line incomplete")
	// ErrKeyIncomplete 表示末行在 '=' 之前被截断。
	ErrKeyIncomplete = errors.New("source: key incomplete")
	// ErrValueIncomplete 表示末行在 '=' 之后没有任何值字节。
	ErrValueIncomplete = errors.New("source: value incomplete")
)

// ParseFile 解析 key = value 行格式。完整行必须以 '\n' 结尾；
// 末行缺少换行时按内容分类为键/值/行不完整，并给出行号。
func ParseFile(content []byte) ([]Entry, error) {
	var entries []Entry
	lineNo := 0
	rest := content
	for len(rest) > 0 {
		lineNo++
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			return nil, classifyTruncated(rest, lineNo)
		}
		entry, ok, err := parseLine(string(rest[:i]), lineNo)
		if err != nil {
			return nil, err
		}
		if ok {
			entries = append(entries, entry)
		}
		rest = rest[i+1:]
	}
	return entries, nil
}

// classifyTruncated 对未以换行结尾的末行分类。
func classifyTruncated(tail []byte, lineNo int) error {
	s := strings.TrimSpace(string(tail))
	before, after, found := strings.Cut(s, "=")
	if !found || strings.TrimSpace(before) == "" {
		return fmt.Errorf("%w: line %d", ErrKeyIncomplete, lineNo)
	}
	if strings.TrimSpace(after) == "" {
		return fmt.Errorf("%w: line %d", ErrValueIncomplete, lineNo)
	}
	return fmt.Errorf("%w: line %d", ErrLineIncomplete, lineNo)
}

func parseLine(line string, lineNo int) (Entry, bool, error) {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "#") {
		return Entry{}, false, nil
	}
	key, value, found := strings.Cut(s, "=")
	if !found {
		return Entry{}, false, fmt.Errorf("%w: line %d", ErrKeyIncomplete, lineNo)
	}
	key, err := NormalizeKey(key)
	if err != nil {
		return Entry{}, false, fmt.Errorf("line %d: %w", lineNo, err)
	}
	return Entry{Key: key, Value: strings.TrimSpace(value), Layer: LayerFile}, true, nil
}

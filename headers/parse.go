package headers

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"ontology/fold"
	"ontology/token"
)

// Parse 从字节流解析头部集合。输入必须以 CRLF 空行终止。
// 任何截断或语法错误都返回可判定的 ParseError（含阶段与头部名），
// 且不会返回半截集合。
func Parse(data []byte, cfg *Config) (*Set, error) {
	s := New(cfg)
	if lim := s.cfg.Limits; lim.MaxBytes > 0 && len(data) > lim.MaxBytes {
		return nil, fmt.Errorf("%w: %d > %d", ErrTooLarge, len(data), lim.MaxBytes)
	}
	lines, err := splitLines(data)
	if err != nil {
		return nil, err
	}
	unfolded, err := fold.Unfold(lines)
	if err != nil {
		return nil, &ParseError{Stage: StageFold, Offset: 0, Err: err}
	}
	for _, line := range unfolded {
		if err := s.parseLine(line); err != nil {
			return nil, err
		}
	}
	if !bytes.Equal(s.bytesLocked(), data) {
		s.normalized = true
	}
	return s, nil
}

// splitLines 按 CRLF 切行，最后一行必须是空行（终止符）。
// 截断时按已读到的内容判定失败阶段。
func splitLines(data []byte) ([]string, error) {
	var lines []string
	rest := data
	offset := 0
	for {
		if len(rest) == 0 {
			return nil, &ParseError{Stage: StageEnd, Offset: offset,
				Err: errors.New("missing terminating blank line")}
		}
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			return nil, &ParseError{Stage: stageOf(rest), Offset: offset,
				Err: errors.New("input truncated")}
		}
		line := rest[:i]
		if len(line) == 0 || line[len(line)-1] != '\r' {
			return nil, &ParseError{Stage: stageOf(line), Offset: offset,
				Err: errors.New("bare LF without CR")}
		}
		line = line[:len(line)-1]
		if len(line) == 0 {
			if i+1 != len(rest) {
				return nil, &ParseError{Stage: StageEnd, Offset: offset + i + 1,
					Err: errors.New("trailing data after terminator")}
			}
			return lines, nil
		}
		lines = append(lines, string(line))
		offset += i + 1
		rest = rest[i+1:]
	}
}

// stageOf 根据一行的已读内容判定截断发生在哪个阶段。
func stageOf(line []byte) Stage {
	switch {
	case len(line) == 0:
		return StageEnd
	case line[0] == ' ' || line[0] == '\t':
		return StageFold
	case bytes.IndexByte(line, ':') >= 0:
		return StageValue
	default:
		return StageName
	}
}

// parseLine 解析一行已展开的 "Name: value"，校验并入库。
// 调用方不持有锁（仅 Parse 在建库期间使用）。
func (s *Set) parseLine(line string) error {
	colon := strings.IndexByte(line, ':')
	if colon < 0 {
		return &ParseError{Stage: StageColon, Err: fmt.Errorf("missing colon in %q", line)}
	}
	name := line[:colon]
	if strings.ContainsAny(name, " \t") {
		return &ParseError{Stage: StageName, Header: name,
			Err: errors.New("whitespace between name and colon")}
	}
	if !token.IsValidName(name) {
		return &ParseError{Stage: StageName, Header: name, Err: ErrInvalidName}
	}
	value := strings.Trim(line[colon+1:], " \t")
	if !token.IsValidValue(value) {
		return &ParseError{Stage: StageValue, Header: name, Err: ErrForbiddenByte}
	}
	if token.HasForbiddenEscape(value) {
		return &ParseError{Stage: StageValue, Header: name,
			Err: fmt.Errorf("%w: percent-escape of forbidden byte", ErrForbiddenByte)}
	}
	if err := s.validate(name, value, 1); err != nil {
		return err
	}
	canonical := token.Canonical(name)
	s.entries = append(s.entries, entry{name: canonical, value: value})
	s.index[canonical] = append(s.index[canonical], len(s.entries)-1)
	return nil
}

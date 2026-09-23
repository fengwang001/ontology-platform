package headers

import (
	"errors"
	"fmt"
	"strings"

	"ontology/fold"
	"ontology/policy"
	"ontology/token"
)

// 四类资源上限错误，彼此可判定（errors.Is）。
var (
	ErrTooManyHeaders = errors.New("headers: too many headers")
	ErrNameTooLong    = errors.New("headers: header name too long")
	ErrValueTooLong   = errors.New("headers: header value too long")
	ErrInputTooLarge  = errors.New("headers: input too large")
)

// ErrNameColonSpace 表示名字与冒号之间存在空白（走私攻击面），一律拒绝。
var ErrNameColonSpace = errors.New("headers: whitespace between name and colon")

// Stage 标识解析失败时所处的阶段。
type Stage int

const (
	StageName  Stage = iota // 解析名字
	StageColon              // 期待冒号
	StageValue              // 解析值
	StageFold               // 折行/续行
	StageEnd                // 终止空行
)

func (st Stage) String() string {
	switch st {
	case StageName:
		return "name"
	case StageColon:
		return "colon"
	case StageValue:
		return "value"
	case StageFold:
		return "fold"
	default:
		return "end"
	}
}

// ParseError 是可判定的解析错误：指出第几个头部、哪个阶段失败。
type ParseError struct {
	Stage Stage // 失败阶段
	Index int   // 第几个头部（0 起）
	Line  int   // 行号（1 起）
	Err   error // 底层错误（上限/非法字符等），可为 nil
	Msg   string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("headers: parse failed at header %d line %d stage %s: %s",
		e.Index, e.Line, e.Stage, e.Msg)
}

// Unwrap 暴露底层错误，便于 errors.Is 判定上限与注入类错误。
func (e *ParseError) Unwrap() error { return e.Err }

// Parse 解析一个完整头部块（以空行终止）为新集合。
func Parse(data []byte, reg *policy.Registry, cfg Config) (*Set, error) {
	s := New(reg, cfg)
	if err := s.Parse(data); err != nil {
		return nil, err
	}
	return s, nil
}

// Parse 把字节流解析进现有集合。解析在本地构建器上完成，
// 全部成功才一次性提交；任何错误都不改变集合已有状态。
func (s *Set) Parse(data []byte) error {
	if len(data) > s.cfg.MaxBytes {
		return &ParseError{Stage: StageEnd, Err: ErrInputTooLarge, Msg: "input exceeds byte limit"}
	}
	b := &builder{set: s, base: len(s.ents)}
	if err := b.run(string(data)); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range b.ents {
		s.index[keyOf(e.name)] = append(s.index[keyOf(e.name)], len(s.ents))
		s.ents = append(s.ents, e)
	}
	if b.norm {
		s.norm = true
	}
	return nil
}

// builder 在提交前累积解析结果。
type builder struct {
	set  *Set
	base int // 提交前已有条数（用于条数上限）
	ents []entry
	norm bool
}

func (b *builder) run(input string) error {
	pos, lineNo := 0, 0
	var rawName, afterColon string
	var conts []string
	have := false
	flush := func() error {
		if !have {
			return nil
		}
		have = false
		return b.commit(rawName, afterColon, conts, lineNo)
	}
	for {
		if pos >= len(input) {
			return &ParseError{Stage: StageEnd, Index: b.count(), Line: lineNo + 1,
				Msg: "unexpected EOF: missing terminating blank line"}
		}
		nl := strings.IndexByte(input[pos:], '\n')
		if nl < 0 {
			st := StageName
			if rest := input[pos:]; rest == "\r" || rest == "" {
				st = StageEnd
			} else if fold.IsContinuation(rest) {
				st = StageFold
			}
			return &ParseError{Stage: st, Index: b.count(), Line: lineNo + 1,
				Msg: "truncated line: no line terminator"}
		}
		line := input[pos : pos+nl]
		pos += nl + 1
		lineNo++
		if strings.HasSuffix(line, "\r") {
			line = line[:len(line)-1]
		} else {
			b.norm = true // LF 行尾被规范为 CRLF
		}
		if strings.ContainsRune(line, '\r') {
			return &ParseError{Stage: StageValue, Index: b.count(), Line: lineNo,
				Err: token.ErrIllegalValue, Msg: "bare CR inside line"}
		}
		if line == "" {
			break // 终止空行
		}
		if fold.IsContinuation(line) {
			if !have {
				return &ParseError{Stage: StageFold, Index: b.count(), Line: lineNo,
					Msg: "first line is a continuation"}
			}
			conts = append(conts, line)
			b.norm = true
			continue
		}
		if err := flush(); err != nil {
			return err
		}
		name, value, err := splitHead(line, b.count(), lineNo)
		if err != nil {
			return err
		}
		rawName, afterColon, conts, have = name, value, conts[:0], true
	}
	if err := flush(); err != nil {
		return err
	}
	if pos < len(input) {
		return &ParseError{Stage: StageEnd, Index: b.count(), Line: lineNo + 1,
			Msg: "trailing data after terminating blank line"}
	}
	return nil
}

func (b *builder) count() int { return b.base + len(b.ents) }

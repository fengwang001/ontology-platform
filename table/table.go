// Package table 把词法事件组装成记录与表头，并做列数一致性检查。
package table

import (
	"ontology/cell"
	"ontology/lexer"
)

// ErrKind 是表层错误（列数不一致、记录数超限）。
type ErrKind int

const (
	ErrColumnCount ErrKind = iota + 1
	ErrTooManyRecords
)

// Error 带位置信息。
type Error struct {
	Kind   ErrKind
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string { return "csv table error" }

var (
	sCol  = &Error{Kind: ErrColumnCount}
	sMany = &Error{Kind: ErrTooManyRecords}
)

// Sentinel 返回表层哨兵。
func Sentinel(k ErrKind) error {
	if k == ErrColumnCount {
		return sCol
	}
	return sMany
}

// Is 支持 errors.Is。
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Kind == e.Kind
}

// Config 为解析配置，0 表示不限。
type Config struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// Table 是组装结果。Header 为第一条记录；Records 含全部记录（首条即表头）。
type Table struct {
	Header  []cell.Cell
	Records [][]cell.Cell
	Width   int
}

// B 是增量事件组装器。
type B struct {
	cfg     Config
	row     []cell.Cell
	Header  []cell.Cell
	Records [][]cell.Cell
	Width   int
	dead    error
	lex     *lexer.L
}

// NewBuilder 创建组装器。
func NewBuilder(cfg Config) *B {
	b := &B{cfg: cfg}
	b.lex = lexer.New(b.onEvent, lexer.Limits{MaxFieldBytes: cfg.MaxFieldBytes, MaxFields: cfg.MaxFields})
	return b
}

func (b *B) onEvent(ev lexer.Event) error {
	switch ev.Kind {
	case lexer.EvField:
		b.row = append(b.row, ev.Cell)
	case lexer.EvRecord:
		r := b.row
		b.row = nil
		if b.Width == 0 {
			b.Width = len(r)
			b.Header = r
		} else if len(r) != b.Width {
			end := r[len(r)-1].End
			return &Error{Kind: ErrColumnCount, Offset: end, Record: len(b.Records) + 1, Field: len(r)}
		}
		if b.cfg.MaxRecords > 0 && len(b.Records) >= b.cfg.MaxRecords {
			c := r[len(r)-1]
			return &Error{Kind: ErrTooManyRecords, Offset: c.End, Record: len(b.Records) + 1, Field: len(r)}
		}
		b.Records = append(b.Records, r)
	}
	return nil
}

// Apply 供 par 拼接后投喂全局事件。
func (b *B) Apply(ev lexer.Event) error {
	if b.dead != nil {
		return b.dead
	}
	if err := b.onEvent(ev); err != nil {
		b.dead = err
		return err
	}
	return nil
}

// Feed 喂入字节。
func (b *B) Feed(p []byte) error {
	if b.dead != nil {
		return b.dead
	}
	err := b.lex.Feed(p)
	if err != nil {
		b.dead = err
	}
	return err
}

// Close 收尾。
func (b *B) Close() error {
	if b.dead != nil {
		return b.dead
	}
	err := b.lex.Close()
	if err != nil {
		b.dead = err
	}
	return err
}

// BytesSeen 返回状态机处理字节数。
func (b *B) BytesSeen() int { return b.lex.BytesSeen() }

// Table 返回已组装的表快照。
func (b *B) Table() *Table {
	return &Table{Header: b.Header, Records: b.Records, Width: b.Width}
}

// Parse 一次性流式解析。
func Parse(p []byte, cfg Config) (*Table, error) {
	b := NewBuilder(cfg)
	if err := b.Feed(p); err != nil {
		return b.Table(), err
	}
	if err := b.Close(); err != nil {
		return b.Table(), err
	}
	return b.Table(), nil
}

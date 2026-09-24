// Package parse 把原始字节解析成记录；坏记录计数但不中断管线。
package parse

import (
	"strconv"
	"strings"

	"ontology/internal/stage"
)

// Rec 是一条解析后的记录：空键合法，Val 必须是整数。
type Rec struct {
	Key string
	Val int64
}

// Parser 是无状态解析阶段的包装，并累计坏记录数。
type Parser struct {
	bad int64
}

// New 构造解析器。
func New() *Parser { return &Parser{} }

// Bad 返回累计坏记录条数。
func (p *Parser) Bad() int64 { return p.bad }

// Work 是 stage 工作函数：屏障原样透传；缺 "=" 或 value 非整数为坏记录。
func (p *Parser) Work(in stage.Msg[[]byte], emit func(stage.Msg[Rec])) error {
	if in.Barrier {
		emit(stage.Msg[Rec]{Seq: in.Seq, Barrier: true})
		return nil
	}
	key, raw, ok := strings.Cut(string(in.V), "=")
	val, err := strconv.ParseInt(raw, 10, 64)
	if !ok || err != nil {
		p.bad++
		return nil
	}
	emit(stage.Msg[Rec]{Seq: in.Seq, V: Rec{Key: key, Val: val}})
	return nil
}

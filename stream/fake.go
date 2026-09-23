package stream

import "ontology/row"

// Fake 是可注入故障的内存假流。行序列由调用方给定（可配置分组大小与顺序）。
// 若 FailAfter>=0，则成功读出 FailAfter 行之后返回 ErrStream。
type Fake struct {
	rows      []row.Row
	failAfter int // -1 表示不注入
	pos       int
	lastKey   string
	read      int
}

// NewFake 构造假流；failAfter 传 -1 表示不注入故障。
func NewFake(rows []row.Row, failAfter int) *Fake {
	return &Fake{rows: rows, failAfter: failAfter}
}

// Next 返回下一行；到达末尾返回 (zero,false,nil)。
func (f *Fake) Next() (row.Row, bool, error) {
	if f.pos < len(f.rows) {
		if f.failAfter >= 0 && f.read >= f.failAfter {
			return row.Row{}, false, ErrStream
		}
		r := f.rows[f.pos]
		f.pos++
		f.read++
		f.lastKey = r.Key
		return r, true, nil
	}
	return row.Row{}, false, nil
}

// Close 对内存假流无副作用。
func (f *Fake) Close() error { return nil }

// ConsumedKey 返回最近一次成功读出的连接键。
func (f *Fake) ConsumedKey() string { return f.lastKey }

// Index 返回已成功读出的行数。
func (f *Fake) Index() int { return f.read }

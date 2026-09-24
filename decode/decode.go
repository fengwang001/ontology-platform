// Package decode 校验 CDC 事件并按列 ID 把任意历史版本解码到当前读 schema。
package decode

import (
	"errors"
	"sync/atomic"

	"ontology/schema"
)

// Event 是一条 CDC 变更事件，Values 按 Version 对应版本的列顺序排列。
type Event struct {
	Version int
	Values  []string
}

var (
	ErrVersionNotFound = errors.New("decode: unknown event version")
	ErrValueCount      = errors.New("decode: value count mismatch")
)

// Decoder 并发安全；checked 记录最近一次 Decode 检查过的列条目个数。
type Decoder struct {
	h       *schema.History
	checked atomic.Int64
}

func New(h *schema.History) *Decoder { return &Decoder{h: h} }

// Decode 把事件解码到当前读 schema：同 ID 取事件值，否则取该列默认值。
func (d *Decoder) Decode(ev Event) ([]string, error) {
	read, src, ok := d.h.Snapshot(ev.Version)
	if !ok {
		return nil, ErrVersionNotFound
	}
	if len(ev.Values) != len(src.Cols) {
		return nil, ErrValueCount
	}
	var n int64
	out := make([]string, len(read.Cols))
	for i, c := range read.Cols {
		n++
		if p, hit := src.Pos(c.ID); hit {
			n++
			out[i] = ev.Values[p]
		} else {
			out[i] = c.Default
		}
	}
	d.checked.Store(n)
	return out, nil
}

// DecodeBatch 批量解码；任一事件被拒则整批失败，不返回部分结果。
func (d *Decoder) DecodeBatch(evs []Event) ([][]string, error) {
	out := make([][]string, len(evs))
	for i, ev := range evs {
		row, err := d.Decode(ev)
		if err != nil {
			return nil, err
		}
		out[i] = row
	}
	return out, nil
}

// CheckedWithin 报告最近一次 Decode 的检查个数是否不超过 limit（不暴露具体数值）。
func (d *Decoder) CheckedWithin(limit int64) bool { return d.checked.Load() <= limit }

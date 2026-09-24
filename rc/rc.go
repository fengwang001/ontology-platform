// Package rc 实现 read_committed 读取：按 [from, LSO) 取记录，
// 过滤中止事务与控制标记，返回 next=LSO。
package rc

import (
	"errors"

	"ontology/txlog"
)

var ErrInvalidFrom = errors.New("rc: 非法的读取起点")

type Reader struct {
	l *txlog.Log
}

func New(l *txlog.Log) *Reader { return &Reader{l: l} }

// Fetch 返回位点在 [from, LSO) 内、所属事务以 Commit 结束的数据记录
// （按位点升序，控制标记绝不返回），以及下次读取起点 next=LSO。
func (r *Reader) Fetch(from int) ([]txlog.Record, int, error) {
	lso := r.l.LSO()
	if from < 0 || from > lso {
		return nil, 0, ErrInvalidFrom
	}
	out := make([]txlog.Record, 0, lso-from)
	for _, rec := range r.l.Scan(from, lso) {
		if rec.Kind == txlog.Data && rec.Committed {
			out = append(out, rec)
		}
	}
	return out, lso, nil
}

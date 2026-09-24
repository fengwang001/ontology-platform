// Package wal 提供 WAL 记录、合法性校验、按 LSN 回收与顺序读取。不依赖其他包。
package wal

import "errors"

type Kind int

const (
	Begin Kind = iota
	Change
	Commit
	Abort
)

type Record struct {
	LSN  int64
	Kind Kind
	Xid  string
	Data string
}

// Rec 构造一条 WAL 记录。
func Rec(lsn int64, k Kind, xid, data string) Record {
	return Record{LSN: lsn, Kind: k, Xid: xid, Data: data}
}

var (
	ErrLSNOrder   = errors.New("wal: LSN 非严格递增")
	ErrXidOpen    = errors.New("wal: Begin 的 Xid 已在进行中")
	ErrXidNotOpen = errors.New("wal: Xid 未在进行中")
)

// WAL 只保存未回收的记录（recs 按 LSN 升序），并跟踪进行中的 Xid。
type WAL struct {
	recs []Record
	last int64 // 已追加的最大 LSN；初始 0 表示还没有任何记录
	open map[string]bool
}

func New() *WAL { return &WAL{open: map[string]bool{}} }

// check 在 (last, open) 快照上校验一条记录，返回校验后的新状态。
func check(rec Record, last int64, open map[string]bool) error {
	if rec.LSN <= last {
		return ErrLSNOrder
	}
	switch rec.Kind {
	case Begin:
		if open[rec.Xid] {
			return ErrXidOpen
		}
	case Change, Commit, Abort:
		if !open[rec.Xid] {
			return ErrXidNotOpen
		}
	}
	return nil
}

func apply(rec Record, open map[string]bool) {
	switch rec.Kind {
	case Begin:
		open[rec.Xid] = true
	case Commit, Abort:
		delete(open, rec.Xid)
	}
}

// Validate 在当前状态上整体校验一批记录，不改变任何状态。
func (w *WAL) Validate(recs ...Record) error {
	open := make(map[string]bool, len(w.open)+len(recs))
	for x := range w.open {
		open[x] = true
	}
	last := w.last
	for _, r := range recs {
		if err := check(r, last, open); err != nil {
			return err
		}
		apply(r, open)
		last = r.LSN
	}
	return nil
}

// Append 整体校验整批记录，任一非法则全部不生效。
func (w *WAL) Append(recs ...Record) error {
	if err := w.Validate(recs...); err != nil {
		return err
	}
	for _, r := range recs {
		apply(r, w.open)
		w.last = r.LSN
	}
	w.recs = append(w.recs, recs...)
	return nil
}

// Reclaim 回收 LSN < before 的记录；before 单调不降才有效。
func (w *WAL) Reclaim(before int64) {
	i := 0
	for i < len(w.recs) && w.recs[i].LSN < before {
		i++
	}
	w.recs = append([]Record(nil), w.recs[i:]...)
}

// Records 返回全部未回收记录（按 LSN 升序）的副本。
func (w *WAL) Records() []Record {
	return append([]Record(nil), w.recs...)
}

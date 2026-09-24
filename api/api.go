// Package api 对外门面：组合 wal 与 chunk，提供全部入口与自检。依赖 chunk。
package api

import (
	"errors"
	"slices"

	"ontology/chunk"
	"ontology/wal"
)

// Out 是一条下游输出：+(Key,Val) 或 -(Key)。Entry 是变更日志条目。
type (
	Out   = chunk.Out
	Entry = wal.Entry
)

const (
	Upsert = wal.Upsert
	Delete = wal.Delete
)

// 可判定的哨兵错误，互不相同。
var (
	ErrBadRange  = chunk.ErrBadRange
	ErrOverlap   = chunk.ErrOverlap
	ErrPhase     = chunk.ErrPhase
	ErrViewLimit = chunk.ErrViewLimit
)

// API 是对外门面。Append 与 chunk 操作、Poll 可并发，View/SelfCheck 可并发。
type API struct {
	log *wal.Log
	mgr *chunk.Manager
}

func New(maxRows int) *API {
	l := wal.New()
	return &API{log: l, mgr: chunk.New(l, maxRows)}
}

func (a *API) Append(es ...Entry) {
	for _, e := range es {
		a.log.Append(e)
	}
}

func (a *API) BeginChunk(lo, hi int64) error { return a.mgr.BeginChunk(lo, hi) }
func (a *API) ReadChunk() error              { return a.mgr.ReadChunk() }
func (a *API) EndChunk() ([]Out, error)      { return a.mgr.EndChunk() }
func (a *API) Poll() ([]Out, error)          { return a.mgr.Poll() }
func (a *API) View() map[int64]string        { return a.mgr.View() }

// checkReject 不变量 4：四类哨兵错误可判定，被拒后状态不变且可继续使用。
func checkReject() error {
	x := New(2)
	x.Append(Entry{Op: Upsert, Key: 1, Val: "a"}, Entry{Op: Upsert, Key: 2, Val: "b"},
		Entry{Op: Upsert, Key: 3, Val: "c"})
	end := func() error { _, err := x.EndChunk(); return err }
	if !errors.Is(x.BeginChunk(5, 5), ErrBadRange) ||
		!errors.Is(x.ReadChunk(), ErrPhase) ||
		x.BeginChunk(0, 10) != nil ||
		!errors.Is(x.BeginChunk(20, 30), ErrPhase) ||
		!errors.Is(end(), ErrPhase) ||
		x.ReadChunk() != nil ||
		!errors.Is(end(), ErrViewLimit) {
		return errors.New("哨兵错误不符")
	}
	if len(x.View()) != 0 {
		return errors.New("被拒的 EndChunk 改动了视图")
	}
	x.Append(Entry{Op: Delete, Key: 3})
	if _, err := x.EndChunk(); err != nil { // 删掉一行后重试成功
		return errors.New("被拒后无法继续")
	}
	if !errors.Is(x.BeginChunk(5, 15), ErrOverlap) {
		return errors.New("缺 ErrOverlap")
	}
	y := New(2) // 被拒的 Poll 不推进处理位置
	y.Append(Entry{Op: Upsert, Key: 1, Val: "a"})
	if y.BeginChunk(0, 5) != nil || y.ReadChunk() != nil {
		return errors.New("内部序列失败")
	}
	if _, err := y.EndChunk(); err != nil {
		return err
	}
	y.Append(Entry{Op: Upsert, Key: 2, Val: "b"}, Entry{Op: Upsert, Key: 3, Val: "c"})
	if _, err := y.Poll(); !errors.Is(err, ErrViewLimit) {
		return errors.New("缺 Poll ErrViewLimit")
	}
	y.Append(Entry{Op: Delete, Key: 3})
	o, err := y.Poll()
	want := []Out{{Op: Upsert, Key: 2, Val: "b"}, {Op: Upsert, Key: 3, Val: "c"}, {Op: Delete, Key: 3}}
	if err != nil || !slices.Equal(o, want) {
		return errors.New("被拒的 Poll 推进了处理位置")
	}
	return nil
}

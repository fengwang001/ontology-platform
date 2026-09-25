// Package log 实现 append-only 审计日志：Seq 分配、顺序约束、Verify、Affected。
// 它只依赖 ent。
package log

import (
	"errors"
	"sync"

	"ontology/ent"
)

// 哨兵错误：四类被拒绝的 Append，互不相同、可判定。
var (
	ErrNegativeTS   = errors.New("audit: TS must be >= 0")
	ErrEmptyWho     = errors.New("audit: Who must be non-empty")
	ErrEmptyOp      = errors.New("audit: Op must be non-empty")
	ErrTSOutOfOrder = errors.New("audit: TS must be non-decreasing")
)

// Log 是只可追加的哈希链审计日志，状态全部在进程内存中。
type Log struct {
	mu      sync.Mutex
	entries []ent.Entry
	head    [32]byte // 缓存链头哈希，Append 以 O(1) 接续
	reads   int      // 非导出：最近一次 Append 为取前驱哈希读取的已有条目数
}

// New 返回含创世条目的日志。
func New() *Log {
	g := ent.Genesis()
	return &Log{entries: []ent.Entry{g}, head: g.Hash}
}

// FromSnapshot 从一份条目快照（可能来自外部存储、可能已被篡改）重建日志，
// 以便随后调用 Verify/Affected 做审计检验。它不修改既有日志，也不做哈希预检。
func FromSnapshot(es []ent.Entry) *Log {
	l := &Log{entries: append([]ent.Entry(nil), es...)}
	if n := len(l.entries); n > 0 {
		l.head = l.entries[n-1].Hash
	}
	return l
}

// Append 校验并追加一条；自动分配 Seq，返回其值。
// 全部校验先于任何状态变更，失败不留痕。
func (l *Log) Append(ts int64, who, op string) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.reads = 0
	switch {
	case ts < 0:
		return 0, ErrNegativeTS
	case who == "":
		return 0, ErrEmptyWho
	case op == "":
		return 0, ErrEmptyOp
	case ts < l.entries[len(l.entries)-1].TS:
		return 0, ErrTSOutOfOrder
	}

	// 前驱哈希直接取缓存的链头：读取已有条目 0 个，与链长无关。
	seq := int64(len(l.entries))
	e := ent.Entry{Seq: seq, TS: ts, Who: who, Op: op}
	e.Hash = ent.NextHash(l.head, e)
	l.entries = append(l.entries, e)
	l.head = e.Hash
	return seq, nil
}

// Entries 返回全部条目的防御性副本。
func (l *Log) Entries() []ent.Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]ent.Entry(nil), l.entries...)
}

// Verify 从创世条目起以「重算哈希」连续传播，返回首个失配条目的 Seq；
// 全部一致返回 -1。绝不使用后一条的存储哈希做比对。
func (l *Log) Verify() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.entries) == 0 || l.entries[0].Hash != ent.GenesisHash() {
		return 0
	}
	expected := l.entries[0].Hash // 创世哈希是固定锚点，不参与 NextHash
	for _, e := range l.entries[1:] {
		recalc := ent.NextHash(expected, e)
		if e.Hash != recalc {
			return e.Seq
		}
		expected = recalc // 传播重算值（非存储值），才能追出被污染的后继
	}
	return -1
}

// Affected 返回 [seq, 末尾] 这一连续区间内不可信条目的 Seq；seq 越界返回 nil。
func (l *Log) Affected(seq int64) []int64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	if seq < 0 || seq >= int64(len(l.entries)) {
		return nil
	}
	out := make([]int64, 0, int64(len(l.entries))-seq)
	for s := seq; s < int64(len(l.entries)); s++ {
		out = append(out, s)
	}
	return out
}

// SelfCheck 用内置操作序列核验顺序、重算、哈希链、留痕与链头 O(1) 接续。
// 它只返回通过与否，绝不暴露非导出计数器 reads 的数值。
func (l *Log) SelfCheck() error {
	type step struct {
		ts      int64
		who, op string
		wantSeq int64 // 接受时期望 Seq；拒绝时为 -1
		wantErr error
	}
	steps := []step{
		{10, "A", "put", 1, nil}, {12, "A", "get", 2, nil},
		{12, "B", "del", 3, nil}, {9, "A", "put", -1, ErrTSOutOfOrder},
		{15, "B", "put", 4, nil}, {15, "", "get", -1, ErrEmptyWho},
		{20, "A", "del", 5, nil},
	}
	c := New()
	for _, s := range steps {
		seq, err := c.Append(s.ts, s.who, s.op)
		if !errors.Is(err, s.wantErr) || (err == nil && seq != s.wantSeq) {
			return errors.New("audit selfcheck: seven-step sequence mismatch")
		}
	}
	if c.Verify() != -1 || len(c.Entries()) != 6 {
		return errors.New("audit selfcheck: chain not intact")
	}
	for _, m := range []int{100, 1000, 10000} {
		b := New()
		for i := 0; i < m; i++ {
			if _, err := b.Append(int64(i), "w", "op"); err != nil {
				return errors.New("audit selfcheck: bulk append failed")
			}
		}
		if _, err := b.Append(int64(m), "w", "op"); err != nil || b.reads > 1 {
			return errors.New("audit selfcheck: head reads not O(1)")
		}
	}
	return nil
}

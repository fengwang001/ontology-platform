// Package txlog 实现事务型分区日志：记录追加、事务归属、HW 推进与 LSO 维护。
package txlog

import (
	"errors"
	"sync"
)

var (
	ErrInvalidRecord = errors.New("txlog: pid 必须为正整数")
	ErrNoOpenTxn     = errors.New("txlog: 该生产者没有进行中的事务")
	ErrInvalidHW     = errors.New("txlog: 非法的高水位")
)

type Kind int

const (
	Data Kind = iota
	Commit
	Abort
)

// Record 是 Scan 返回的只读视图；Committed 仅对 Data 有意义：
// 所属事务已以 Commit 定论且控制标记位点 < HW。
type Record struct {
	Off       int
	Pid       int
	Val       string
	Kind      Kind
	Committed bool
}

type entry struct {
	pid  int
	val  string
	kind Kind
	txn  int // 所属事务下标；控制标记为 -1
}

type txn struct {
	first     int  // 事务首位点
	ctrl      int  // 控制标记位点，-1 表示进行中
	committed bool // 控制标记为 Commit
}

// Log 是单分区事务日志。所有方法可并发调用。
type Log struct {
	mu      sync.RWMutex
	recs    []entry
	txns    []txn // 按首位点递增（首位点即创建时的追加位点）
	open    map[int]int
	head    int // txns 中首个可能未决的下标，之前的均已定论
	hw      int
	lso     int
	checked int // 最近一次 AdvanceHW 为确定 LSO 检查过的事务个数
}

func New() *Log { return &Log{open: map[int]int{}} }

func (l *Log) AppendData(pid int, val string) (int, error) {
	if pid <= 0 {
		return 0, ErrInvalidRecord
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	off := len(l.recs)
	t, ok := l.open[pid]
	if !ok {
		t = len(l.txns)
		l.txns = append(l.txns, txn{first: off, ctrl: -1})
		l.open[pid] = t
	}
	l.recs = append(l.recs, entry{pid: pid, val: val, kind: Data, txn: t})
	return off, nil
}

func (l *Log) appendControl(pid int, kind Kind) (int, error) {
	if pid <= 0 {
		return 0, ErrInvalidRecord
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := l.open[pid]
	if !ok {
		return 0, ErrNoOpenTxn
	}
	off := len(l.recs)
	l.recs = append(l.recs, entry{pid: pid, kind: kind, txn: -1})
	l.txns[t].ctrl = off
	l.txns[t].committed = kind == Commit
	delete(l.open, pid)
	return off, nil
}

func (l *Log) AppendCommit(pid int) (int, error) { return l.appendControl(pid, Commit) }

func (l *Log) AppendAbort(pid int) (int, error) { return l.appendControl(pid, Abort) }

// AdvanceHW 把高水位推进到 h，并重算 LSO。
// 未决事务按首位点有序：从队首弹出已定论者后，队首即首位点最小的未决事务。
func (l *Log) AdvanceHW(h int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if h < l.hw || h > len(l.recs) {
		return ErrInvalidHW
	}
	l.hw = h
	l.checked = 0
	for l.head < len(l.txns) {
		t := &l.txns[l.head]
		l.checked++
		if t.ctrl >= 0 && t.ctrl < h {
			l.head++
			continue
		}
		break
	}
	l.lso = h
	if l.head < len(l.txns) && l.txns[l.head].first < h {
		l.lso = l.txns[l.head].first
	}
	return nil
}

func (l *Log) HW() int  { l.mu.RLock(); defer l.mu.RUnlock(); return l.hw }
func (l *Log) LSO() int { l.mu.RLock(); defer l.mu.RUnlock(); return l.lso }

// Scan 返回 [from, to) 的记录视图；调用方保证 0 <= from <= to <= 日志末端。
func (l *Log) Scan(from, to int) []Record {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Record, 0, to-from)
	for i := from; i < to; i++ {
		e := l.recs[i]
		r := Record{Off: i, Pid: e.pid, Val: e.val, Kind: e.kind}
		if e.kind == Data {
			t := l.txns[e.txn]
			r.Committed = t.ctrl >= 0 && t.ctrl < l.hw && t.committed
		}
		out = append(out, r)
	}
	return out
}

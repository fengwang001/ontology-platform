// Package repl 实现 Raft leader 的日志复制簿记：matchIndex / nextIndex / commitIndex。
package repl

import (
	"errors"
	"sort"
	"sync"

	"ontology/log"
)

// 四类可判定故障，互不相同的哨兵错误。
var (
	ErrFollowerRange  = errors.New("repl: follower index out of range")
	ErrAppendTerm     = errors.New("repl: append term is not the current term")
	ErrElectTerm      = errors.New("repl: elect term not greater than current term")
	ErrReplicateRange = errors.New("repl: replicate index out of range")
)

// Leader 是节点 1 的簿记状态，follower 编号 2..n。
type Leader struct {
	mu          sync.RWMutex
	n           int
	term        int
	lg          *log.Log
	matchIndex  []int // 按下标 f 访问，1 号（leader 自己）不用
	nextIndex   []int
	commitIndex int
	lastReads   int // 最近一次 CommitIndex 计算读取的日志条目数（非导出，不进公开接口）
}

// New 建 n 节点集群的 leader，当前任期 term，日志预置为任期 terms 的条目。
func New(n, term int, terms ...int) *Leader {
	if n < 1 {
		n = 1
	}
	l := &Leader{n: n, term: term, lg: log.New(terms...),
		matchIndex: make([]int, n+1), nextIndex: make([]int, n+1)}
	for f := 2; f <= n; f++ {
		l.nextIndex[f] = 1
	}
	return l
}

// Append 追加一条当前任期的条目；任期不符则整体失败。
func (l *Leader) Append(term int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if term != l.term {
		return ErrAppendTerm
	}
	l.lg.Append(term)
	return nil
}

// Replicate 处理 follower f 的回报：ok 时其日志已与 leader 前 r 条一致；
// 拒绝时 r 是回报的最后下标（hint），matchIndex 不变。
func (l *Leader) Replicate(f int, ok bool, r int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if f < 2 || f > l.n {
		return ErrFollowerRange
	}
	if r < 0 || r > l.lg.Len() {
		return ErrReplicateRange
	}
	if ok {
		l.matchIndex[f] = r
	}
	l.nextIndex[f] = r + 1
	return nil
}

// Elect 在任期 t（必须大于当前任期）重新当选：重置簿记，commitIndex 不动。
func (l *Leader) Elect(t int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t <= l.term {
		return ErrElectTerm
	}
	l.term = t
	for f := 2; f <= l.n; f++ {
		l.matchIndex[f] = 0
		l.nextIndex[f] = l.lg.Len() + 1
	}
	return nil
}

// CommitIndex 推进并返回 commitIndex。
func (l *Leader) CommitIndex() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.advance()
	return l.commitIndex
}

// advance 对 n 个 match 值（leader 自己算 Len()）排序取多数派阈值下标，
// 只查那一条条目的 term，读取次数记入 lastReads。调用方须持写锁。
func (l *Leader) advance() {
	vals := make([]int, 0, l.n)
	vals = append(vals, l.lg.Len())
	for f := 2; f <= l.n; f++ {
		vals = append(vals, l.matchIndex[f])
	}
	sort.Sort(sort.Reverse(sort.IntSlice(vals)))
	cand := vals[l.n/2] // 多数派 = n/2+1，阈值是第 n/2+1 大（0 基下标 n/2）
	reads := 0
	if cand > l.commitIndex && cand >= 1 {
		reads++
		if l.lg.Term(cand) == l.term {
			l.commitIndex = cand
		}
	}
	l.lastReads = reads
}

func (l *Leader) MatchIndex(f int) int { l.mu.RLock(); defer l.mu.RUnlock(); return l.matchIndex[f] }
func (l *Leader) NextIndex(f int) int  { l.mu.RLock(); defer l.mu.RUnlock(); return l.nextIndex[f] }
func (l *Leader) Len() int             { l.mu.RLock(); defer l.mu.RUnlock(); return l.lg.Len() }
func (l *Leader) Term(i int) int       { l.mu.RLock(); defer l.mu.RUnlock(); return l.lg.Term(i) }
func (l *Leader) CurrentTerm() int     { l.mu.RLock(); defer l.mu.RUnlock(); return l.term }

// CheckCommitReads 内部构造多档规模的集群，判定 CommitIndex 的日志读取次数
// 不随日志长度增长；只返回结论，不暴露计数器数值。
func CheckCommitReads() bool {
	for _, m := range []int{100, 1000, 10000} {
		terms := make([]int, m)
		for i := range terms {
			terms[i] = 1
		}
		l := New(3, 1, terms...)
		if l.Replicate(2, true, m) != nil || l.Replicate(3, true, m) != nil {
			return false
		}
		if l.CommitIndex() != m || l.lastReads > 2 {
			return false
		}
	}
	return true
}

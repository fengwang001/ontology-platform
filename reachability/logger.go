package reachability

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// logger 负责把输入、可达集合与判定依据写入日志。
//
// 为保证“同一输入序列反复运行得到完全相同的输出”，日志不含时间戳，
// 只使用单调递增的操作序号；集合一律按字典序输出。
type logger struct {
	mu  sync.Mutex
	w   io.Writer
	seq int64
}

// newLogger 创建日志器；w 为 nil 时所有输出被丢弃。
func newLogger(w io.Writer) *logger {
	if w == nil {
		w = io.Discard
	}
	return &logger{w: w}
}

func (l *logger) logf(format string, args ...any) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	fmt.Fprintf(l.w, "#%d %s\n", l.seq, fmt.Sprintf(format, args...))
}

// logRejected 记录被拒绝的输入及其可区分原因。
func (l *logger) logRejected(op, from, to string, n uint64, err error) {
	l.logf("REJECT input=%s edge=%q->%q n=%d reason=%s detail=%q | state unchanged",
		op, from, to, n, kindOf(err), err.Error())
}

// logChange 记录一次成功变更的输入、判定依据与操作后的完整可达集合。
func (l *logger) logChange(r *ChangeResult) {
	added := sortedWitnesses(r.AddedPairs)
	removed := sortPairs(append([]Pair(nil), r.RemovedPairs...))
	restored := sortedWitnesses(r.RestoredPairs)

	l.logf("ACCEPT input=%s edge=%q->%q multiplicity=%d->%d structure_changed=%t",
		r.Op, r.Edge.From, r.Edge.To, r.MultiplicityBefore, r.MultiplicityAfter, r.StructureChanged)
	for _, w := range added {
		l.logf("  + added    %q->%q via %s", w.From, w.To, formatPath(w.Path))
	}
	for _, p := range removed {
		l.logf("  - removed  %q->%q (no alternate path after edge deletion)", p.From, p.To)
	}
	for _, w := range restored {
		l.logf("  ~ restored %q->%q via %s", w.From, w.To, formatPath(w.Path))
	}
	l.logf("  reachability_after=%s", formatPairs(r.After.Pairs()))
}

// logReachable 记录一次可达判定输入与结论及其依据。
func (l *logger) logReachable(from, to string, ok bool, witness []string) {
	basis := "no path of length >= 1"
	if ok {
		basis = "path " + formatPath(witness)
	}
	l.logf("QUERY input=reachable edge=%q->%q result=%t basis={%s}", from, to, ok, basis)
}

func kindOf(err error) ErrorKind {
	if e, ok := err.(*OpError); ok {
		return e.Kind
	}
	return ErrorKind("unknown")
}

func formatPath(path []string) string {
	quoted := make([]string, len(path))
	for i, v := range path {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(quoted, "->")
}

func formatPairs(pairs []Pair) string {
	if len(pairs) == 0 {
		return "{}"
	}
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = fmt.Sprintf("%q->%q", p.From, p.To)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func sortPairs(ps []Pair) []Pair {
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].From != ps[j].From {
			return ps[i].From < ps[j].From
		}
		return ps[i].To < ps[j].To
	})
	return ps
}

func sortedWitnesses(ws []PairWitness) []PairWitness {
	sort.Slice(ws, func(i, j int) bool {
		if ws[i].From != ws[j].From {
			return ws[i].From < ws[j].From
		}
		return ws[i].To < ws[j].To
	})
	return ws
}

package mvcc

import (
	"fmt"
	"strings"
)

// naive 是按规则逐条直译的朴素参考实现，用于与 Engine 对拍。
// 它不做任何性能优化：根、后代、有效状态都靠全表扫描现算。
type naive struct {
	status   map[int]string // "running" | "subcommitted" | "committed" | "aborted"
	parent   map[int]int    // 0 表示顶层
	tuples   map[string]*naiveTuple
	snaps    map[int]map[int]bool
	nextSnap int
	maxCmd   map[int]int // 根事务号 -> 该树已用最大命令号
}

type naiveTuple struct {
	xmin, cmin int
	xmax, cmax int
	hasXmax    bool
}

func newNaive() *naive {
	return &naive{
		status:   make(map[int]string),
		parent:   make(map[int]int),
		tuples:   make(map[string]*naiveTuple),
		snaps:    make(map[int]map[int]bool),
		nextSnap: 1,
		maxCmd:   make(map[int]int),
	}
}

func (n *naive) rootOf(x int) int {
	for n.parent[x] != 0 {
		x = n.parent[x]
	}
	return x
}

func (n *naive) isAncestor(anc, x int) bool {
	for x != 0 {
		if x == anc {
			return true
		}
		x = n.parent[x]
	}
	return false
}

func (n *naive) effective(x int) string {
	if n.status[x] == "aborted" {
		return "aborted"
	}
	if (n.status[x] == "subcommitted" || n.status[x] == "committed") &&
		n.status[n.rootOf(x)] == "committed" {
		return "committed"
	}
	return "active"
}

func (n *naive) hasRunningDescendant(x int) bool {
	for id, st := range n.status {
		if id != x && st == "running" && n.isAncestor(x, id) {
			return true
		}
	}
	return false
}

func (n *naive) Begin(x int) error {
	if x <= 0 {
		return ErrTxNotPositive
	}
	if _, ok := n.status[x]; ok {
		return ErrTxExists
	}
	n.status[x] = "running"
	n.parent[x] = 0
	return nil
}

func (n *naive) BeginSub(x, p int) error {
	if x <= 0 {
		return ErrTxNotPositive
	}
	if _, ok := n.status[x]; ok {
		return ErrTxExists
	}
	if _, ok := n.status[p]; !ok {
		return ErrTxNotFound
	}
	if n.status[p] != "running" {
		return ErrTxNotRunning
	}
	n.status[x] = "running"
	n.parent[x] = p
	return nil
}

func (n *naive) commitCheck(x int) error {
	if _, ok := n.status[x]; !ok {
		return ErrTxNotFound
	}
	if n.status[x] != "running" {
		return ErrTxNotRunning
	}
	return nil
}

func (n *naive) CommitSub(x int) error {
	if err := n.commitCheck(x); err != nil {
		return err
	}
	if n.parent[x] == 0 {
		return ErrTxTypeMismatch
	}
	if n.hasRunningDescendant(x) {
		return ErrTxRunningDescendants
	}
	n.status[x] = "subcommitted"
	return nil
}

func (n *naive) Commit(x int) error {
	if err := n.commitCheck(x); err != nil {
		return err
	}
	if n.parent[x] != 0 {
		return ErrTxTypeMismatch
	}
	if n.hasRunningDescendant(x) {
		return ErrTxRunningDescendants
	}
	n.status[x] = "committed"
	return nil
}

func (n *naive) Abort(x int) error {
	if err := n.commitCheck(x); err != nil {
		return err
	}
	for id := range n.status {
		if id == x || n.isAncestor(x, id) {
			n.status[id] = "aborted"
		}
	}
	return nil
}

func (n *naive) Snapshot() int {
	committed := make(map[int]bool)
	for id, st := range n.status {
		if n.parent[id] == 0 && st == "committed" {
			committed[id] = true
		}
	}
	id := n.nextSnap
	n.nextSnap++
	n.snaps[id] = committed
	return id
}

func (n *naive) writeCheck(x, c int) (int, error) {
	if _, ok := n.status[x]; !ok {
		return 0, ErrTxNotFound
	}
	if n.status[x] != "running" {
		return 0, ErrTxNotRunning
	}
	if c < 0 {
		return 0, ErrNegativeCommand
	}
	root := n.rootOf(x)
	if c < n.maxCmd[root] {
		return 0, ErrCommandOrder
	}
	return root, nil
}

func (n *naive) Insert(t string, x, c int) error {
	root, err := n.writeCheck(x, c)
	if err != nil {
		return err
	}
	if _, ok := n.tuples[t]; ok {
		return ErrTupleExists
	}
	n.tuples[t] = &naiveTuple{xmin: x, cmin: c}
	n.maxCmd[root] = c
	return nil
}

func (n *naive) Delete(t string, x, c int) error {
	root, err := n.writeCheck(x, c)
	if err != nil {
		return err
	}
	tv, ok := n.tuples[t]
	if !ok {
		return ErrTupleNotFound
	}
	if tv.hasXmax && n.effective(tv.xmax) != "aborted" {
		return ErrTupleAlreadyDeleted
	}
	tv.xmax, tv.cmax, tv.hasXmax = x, c, true
	n.maxCmd[root] = c
	return nil
}

// seen 返回效果是否被看到及判定依据。
func (n *naive) seen(x, c, s, y, cy int) (bool, string) {
	eff := n.effective(y)
	if n.rootOf(y) == n.rootOf(x) {
		ok := eff != "aborted" && cy < c
		rel := ">="
		if cy < c {
			rel = "<"
		}
		return ok, fmt.Sprintf("y=%d 与 x=%d 同根, 有效状态=%s, cy=%d %s c=%d => 被看到=%v",
			y, x, eff, cy, rel, c, ok)
	}
	inSnap := n.snaps[s][n.rootOf(y)]
	ok := eff == "committed" && inSnap
	return ok, fmt.Sprintf("y=%d 与 x=%d 不同根, 有效状态=%s, 根=%d 在快照%d中=%v => 被看到=%v",
		y, x, eff, n.rootOf(y), s, inSnap, ok)
}

func (n *naive) Visible(t string, x, c, s int) (bool, error, string) {
	if _, ok := n.status[x]; !ok {
		return false, ErrTxNotFound, ""
	}
	if n.status[x] != "running" {
		return false, ErrTxNotRunning, ""
	}
	if c < 0 {
		return false, ErrNegativeCommand, ""
	}
	if _, ok := n.snaps[s]; !ok {
		return false, ErrSnapshotUnknown, ""
	}
	tv, ok := n.tuples[t]
	if !ok {
		return false, ErrTupleNotFound, ""
	}
	var basis strings.Builder
	xminSeen, b1 := n.seen(x, c, s, tv.xmin, tv.cmin)
	fmt.Fprintf(&basis, "xmin 效果(tx=%d,c=%d): %s; ", tv.xmin, tv.cmin, b1)
	xmaxSeen := false
	if tv.hasXmax {
		var b2 string
		xmaxSeen, b2 = n.seen(x, c, s, tv.xmax, tv.cmax)
		fmt.Fprintf(&basis, "xmax 效果(tx=%d,c=%d): %s; ", tv.xmax, tv.cmax, b2)
	} else {
		basis.WriteString("无删除标记, xmax 视为未被看到; ")
	}
	vis := xminSeen && !xmaxSeen
	fmt.Fprintf(&basis, "xmin被看到=%v 且 xmax被看到=%v => 可见=%v", xminSeen, xmaxSeen, vis)
	return vis, nil, basis.String()
}

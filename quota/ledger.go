// Package quota 实现带改名转移、字节预留与原子批处理的目录树配额账本。
package quota

import (
	"fmt"
	"sync"
)

// RootID 根目录编号。
const RootID = 0

// node 目录树节点。目录维护子树聚合（bytes/entries/reserved）与限额；
// 文件仅有 size。所有聚合字段恒等于从整棵树重新累加的结果。
type node struct {
	dir      bool
	size     int64 // 仅文件：字节数
	parent   int   // 根为 -1
	children map[int]struct{}

	r int64 // 仅目录：自身预留

	bytes    int64 // 仅目录：子树文件字节和
	entries  int64 // 仅目录：子树条目数（不含自身）
	reserved int64 // 仅目录：子树预留和 R（含自身 r）

	byteLimit  int64 // -1 表示无限制
	entryLimit int64 // -1 表示无限制
}

// state 账本全部可变状态，Batch 通过整体克隆实现回滚。
type state struct {
	nodes  map[int]*node
	nextID int
}

func newState() *state {
	root := &node{
		dir:        true,
		parent:     -1,
		children:   make(map[int]struct{}),
		byteLimit:  -1,
		entryLimit: -1,
	}
	return &state{nodes: map[int]*node{RootID: root}, nextID: 1}
}

func (s *state) clone() *state {
	ns := &state{nodes: make(map[int]*node, len(s.nodes)), nextID: s.nextID}
	for id, n := range s.nodes {
		c := *n
		if n.children != nil {
			c.children = make(map[int]struct{}, len(n.children))
			for k := range n.children {
				c.children[k] = struct{}{}
			}
		}
		ns.nodes[id] = &c
	}
	return ns
}

// Ledger 目录树配额账本。所有方法可并发调用，效果等价于某个串行顺序。
type Ledger struct {
	mu sync.RWMutex
	st *state
}

// NewLedger 返回只有根目录（编号 0）的空账本。
func NewLedger() *Ledger {
	return &Ledger{st: newState()}
}

// Mkdir 在目录 p 下创建目录，返回新节点编号。
func (l *Ledger) Mkdir(p int) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st.mkdir(p)
}

// AddFile 在目录 p 下创建大小为 size 的文件，返回新节点编号。
// 先用 p 的自身预留抵扣：c=min(size, r_p)，E 净增 size-c。
func (l *Ledger) AddFile(p int, size int64) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st.addFile(p, size)
}

// SetQuota 设置目录 d 的字节（针对有效字节 E）与条目数限额，-1 表示无限制。
func (l *Ledger) SetQuota(d int, b, n int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st.setQuota(d, b, n)
}

// Reserve 为目录 d 增加 b 字节的自身预留。
func (l *Ledger) Reserve(d int, b int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st.reserve(d, b)
}

// Release 释放目录 d 的 b 字节自身预留，要求 b 不大于 r_d。
func (l *Ledger) Release(d int, b int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st.release(d, b)
}

// Resize 把文件 f 的大小改为 s，只影响字节，不使用预留；增量不大于 0 恒允许。
func (l *Ledger) Resize(f int, s int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st.resize(f, s)
}

// Remove 删除文件或空目录并退还用量，被删目录的自身预留一并退还。
func (l *Ledger) Remove(x int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st.remove(x)
}

// Rename 把节点 x 整体移到目录 p 之下，只核对新旧祖先链的差异部分。
func (l *Ledger) Rename(x, p int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st.rename(x, p)
}

// Usage 返回目录 d 的（子树字节， 子树条目数， 子树预留 R）。
func (l *Ledger) Usage(d int) (bytes, entries, reserved int64, err error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	n, ok := l.st.nodes[d]
	if !ok {
		return 0, 0, 0, notFoundErr(d)
	}
	if !n.dir {
		return 0, 0, 0, typeMismatchErr("usage of file node %d", d)
	}
	return n.bytes, n.entries, n.reserved, nil
}

// effective 返回目录的有效字节 E = 子树字节 + 子树预留 R。
func (n *node) effective() int64 { return n.bytes + n.reserved }

// checkChain 自近到远检查 start 及其全部祖先：同一目录先字节（有效字节）后条目数，
// 只报第一个违规。dE 为有效字节增量，dEntries 为条目数增量。
func (s *state) checkChain(start int, dE, dEntries int64) error {
	for id := start; id != -1; id = s.nodes[id].parent {
		n := s.nodes[id]
		if n.byteLimit >= 0 && n.effective()+dE > n.byteLimit {
			return &QuotaError{Byte: true, Dir: id, Limit: n.byteLimit, Used: n.effective(), Delta: dE}
		}
		if n.entryLimit >= 0 && n.entries+dEntries > n.entryLimit {
			return &QuotaError{Dir: id, Limit: n.entryLimit, Used: n.entries, Delta: dEntries}
		}
	}
	return nil
}

// applyChain 把聚合增量应用到 start 及其全部祖先。
func (s *state) applyChain(start int, dBytes, dEntries, dReserved int64) {
	for id := start; id != -1; id = s.nodes[id].parent {
		n := s.nodes[id]
		n.bytes += dBytes
		n.entries += dEntries
		n.reserved += dReserved
	}
}

// chainSet 收集 start 及其全部祖先的编号。
func (s *state) chainSet(start int) map[int]bool {
	set := make(map[int]bool)
	for id := start; id != -1; id = s.nodes[id].parent {
		set[id] = true
	}
	return set
}

func (s *state) mkdir(p int) (int, error) {
	pn, ok := s.nodes[p]
	if !ok {
		return 0, notFoundErr(p)
	}
	if !pn.dir {
		return 0, typeMismatchErr("mkdir parent %d is a file", p)
	}
	if err := s.checkChain(p, 0, 1); err != nil {
		return 0, err
	}
	id := s.nextID
	s.nextID++
	s.nodes[id] = &node{
		dir:        true,
		parent:     p,
		children:   make(map[int]struct{}),
		byteLimit:  -1,
		entryLimit: -1,
	}
	pn.children[id] = struct{}{}
	s.applyChain(p, 0, 1, 0)
	return id, nil
}

func (s *state) addFile(p int, size int64) (int, error) {
	if size < 0 {
		return 0, invalidArgErr("negative file size %d", size)
	}
	pn, ok := s.nodes[p]
	if !ok {
		return 0, notFoundErr(p)
	}
	if !pn.dir {
		return 0, typeMismatchErr("addfile parent %d is a file", p)
	}
	c := min(size, pn.r)
	if err := s.checkChain(p, size-c, 1); err != nil {
		return 0, err
	}
	id := s.nextID
	s.nextID++
	s.nodes[id] = &node{dir: false, size: size, parent: p}
	pn.children[id] = struct{}{}
	pn.r -= c
	s.applyChain(p, size, 1, -c)
	return id, nil
}

func (s *state) setQuota(d int, b, n int64) error {
	if b < -1 || n < -1 {
		return invalidArgErr("quota limits must be >= -1, got (%d, %d)", b, n)
	}
	dn, ok := s.nodes[d]
	if !ok {
		return notFoundErr(d)
	}
	if !dn.dir {
		return typeMismatchErr("setquota on file node %d", d)
	}
	if b >= 0 && b < dn.effective() {
		return &BelowUsageError{Dir: d, Byte: true, Limit: b, Used: dn.effective()}
	}
	if n >= 0 && n < dn.entries {
		return &BelowUsageError{Dir: d, Byte: false, Limit: n, Used: dn.entries}
	}
	dn.byteLimit = b
	dn.entryLimit = n
	return nil
}

func (s *state) reserve(d int, b int64) error {
	if b <= 0 {
		return invalidArgErr("reserve amount must be > 0, got %d", b)
	}
	dn, ok := s.nodes[d]
	if !ok {
		return notFoundErr(d)
	}
	if !dn.dir {
		return typeMismatchErr("reserve on file node %d", d)
	}
	if err := s.checkChain(d, b, 0); err != nil {
		return err
	}
	dn.r += b
	s.applyChain(d, 0, 0, b)
	return nil
}

func (s *state) release(d int, b int64) error {
	if b <= 0 {
		return invalidArgErr("release amount must be > 0, got %d", b)
	}
	dn, ok := s.nodes[d]
	if !ok {
		return notFoundErr(d)
	}
	if !dn.dir {
		return typeMismatchErr("release on file node %d", d)
	}
	if b > dn.r {
		return fmt.Errorf("%w: release %d > reserved %d on dir %d",
			ErrInsufficientReservation, b, dn.r, d)
	}
	dn.r -= b
	s.applyChain(d, 0, 0, -b)
	return nil
}

func (s *state) resize(f int, size int64) error {
	if size < 0 {
		return invalidArgErr("negative file size %d", size)
	}
	fn, ok := s.nodes[f]
	if !ok {
		return notFoundErr(f)
	}
	if fn.dir {
		return typeMismatchErr("resize on dir node %d", f)
	}
	delta := size - fn.size
	if delta > 0 {
		if err := s.checkChain(fn.parent, delta, 0); err != nil {
			return err
		}
	}
	fn.size = size
	s.applyChain(fn.parent, delta, 0, 0)
	return nil
}

func (s *state) remove(x int) error {
	xn, ok := s.nodes[x]
	if !ok {
		return notFoundErr(x)
	}
	if x == RootID {
		return fmt.Errorf("%w: remove", ErrRoot)
	}
	if xn.dir && len(xn.children) > 0 {
		return fmt.Errorf("%w: dir %d has %d children", ErrNotEmpty, x, len(xn.children))
	}
	var dBytes, dReserved int64
	if xn.dir {
		dReserved = xn.r
	} else {
		dBytes = xn.size
	}
	pn := s.nodes[xn.parent]
	delete(pn.children, x)
	delete(s.nodes, x)
	s.applyChain(xn.parent, -dBytes, -1, -dReserved)
	return nil
}

func (s *state) rename(x, p int) error {
	xn, ok := s.nodes[x]
	if !ok {
		return notFoundErr(x)
	}
	pn, ok := s.nodes[p]
	if !ok {
		return notFoundErr(p)
	}
	if !pn.dir {
		return typeMismatchErr("rename target parent %d is a file", p)
	}
	if x == RootID {
		return fmt.Errorf("%w: rename", ErrRoot)
	}
	if xn.dir {
		for id := p; id != -1; id = s.nodes[id].parent {
			if id == x {
				return fmt.Errorf("%w: dir %d into node %d", ErrCycle, x, p)
			}
		}
	}
	if xn.parent == p {
		return nil // p 恰为当前父目录：成功且什么都不改
	}
	var dBytes, dReserved, dEntries int64
	if xn.dir {
		dBytes = xn.bytes
		dReserved = xn.reserved
		dEntries = xn.entries + 1
	} else {
		dBytes = xn.size
		dEntries = 1
	}
	dE := dBytes + dReserved

	oldSet := s.chainSet(xn.parent)
	newSet := s.chainSet(p)

	// 只核对「p 及其祖先」中不在旧链里的目录，自近到远。
	for id := p; id != -1; id = s.nodes[id].parent {
		if oldSet[id] {
			continue
		}
		n := s.nodes[id]
		if n.byteLimit >= 0 && n.effective()+dE > n.byteLimit {
			return &QuotaError{Byte: true, Dir: id, Limit: n.byteLimit, Used: n.effective(), Delta: dE}
		}
		if n.entryLimit >= 0 && n.entries+dEntries > n.entryLimit {
			return &QuotaError{Dir: id, Limit: n.entryLimit, Used: n.entries, Delta: dEntries}
		}
	}

	// 旧链独有：退还；新链独有：计入；共有目录不变。
	for id := xn.parent; id != -1; id = s.nodes[id].parent {
		if !newSet[id] {
			n := s.nodes[id]
			n.bytes -= dBytes
			n.entries -= dEntries
			n.reserved -= dReserved
		}
	}
	for id := p; id != -1; id = s.nodes[id].parent {
		if !oldSet[id] {
			n := s.nodes[id]
			n.bytes += dBytes
			n.entries += dEntries
			n.reserved += dReserved
		}
	}

	delete(s.nodes[xn.parent].children, x)
	pn.children[x] = struct{}{}
	xn.parent = p
	return nil
}

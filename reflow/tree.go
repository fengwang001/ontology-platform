package reflow

import (
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

// Logger 用于打印输入、输出与判定依据，可为 nil。
type Logger interface {
	Printf(format string, args ...any)
}

type node struct {
	id                  NodeID
	parent              *node
	children            []*node
	isolated            bool
	width, height       Mode
	padX, padY          int64
	w, h                int64
	selfDirty, subDirty bool
	lastSeenGen         uint64
}

func newNode(id NodeID, wm, hm Mode, px, py int64, isolated bool) *node {
	return &node{id: id, width: wm, height: hm, padX: px, padY: py, isolated: isolated}
}

// Kernel 是布局失效/重排内核，所有方法可被并发调用。
type Kernel struct {
	mu         *rmutex
	root       *node
	nodes      map[NodeID]*node
	nextID     NodeID
	boundaries map[*node]struct{}
	inCommit   bool
	gen        uint64
	pending    bool
	stats      Stats

	Log         Logger
	ReentryHook func()
}

// rmutex 是可识别同 goroutine 重入的互斥量：
// 用容量 1 的通道串行化不同 goroutine；同一 goroutine 再次 Lock
// 立即成功并加深重入计数，使提交钩子中的重入调用可以进入并被检测。
type rmutex struct {
	held  chan struct{}
	owner atomic.Int64
	depth atomic.Int32
}

func newRMutex() *rmutex {
	m := &rmutex{}
	m.held = make(chan struct{}, 1)
	m.owner.Store(-1)
	return m
}

func (m *rmutex) Lock() {
	gid := goroutineID()
	if m.owner.Load() == gid {
		m.depth.Add(1)
		return
	}
	m.held <- struct{}{}
	m.depth.Store(1)
	m.owner.Store(gid)
}

func (m *rmutex) Unlock() {
	if m.depth.Add(-1) == 0 {
		m.owner.Store(-1)
		<-m.held
	}
}

func goroutineID() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	s := strings.TrimPrefix(string(buf[:n]), "goroutine ")
	if sp := strings.IndexByte(s, ' '); sp >= 0 {
		if id, err := strconv.ParseInt(s[:sp], 10, 64); err == nil {
			return id
		}
	}
	return -1
}

// NewKernel 创建仅含根节点的内核（根始终视为布局边界）。
func NewKernel(width, height Mode, padX, padY int64) (*Kernel, error) {
	const op = "NewKernel"
	if err := validateGeom(op, width, height, padX, padY); err != nil {
		return nil, err
	}
	k := &Kernel{
		mu:         newRMutex(),
		nodes:      make(map[NodeID]*node),
		boundaries: make(map[*node]struct{}),
		nextID:     1,
	}
	k.root = newNode(0, width, height, padX, padY, false)
	k.nodes[0] = k.root
	k.root.compute()
	k.logf("%s -> root size=(%d,%d)", op, k.root.w, k.root.h)
	return k, nil
}

func (k *Kernel) Root() NodeID { return 0 }

func validateGeom(op string, wm, hm Mode, px, py int64) error {
	switch wm.Kind {
	case ModeAuto:
	case ModeFixed:
		if wm.Value < 0 {
			return kerr(KindInvalidArg, op, "负的固定宽度 %d", wm.Value)
		}
	default:
		return kerr(KindInvalidArg, op, "未知宽度模式 %d", wm.Kind)
	}
	switch hm.Kind {
	case ModeAuto:
	case ModeFixed:
		if hm.Value < 0 {
			return kerr(KindInvalidArg, op, "负的固定高度 %d", hm.Value)
		}
	default:
		return kerr(KindInvalidArg, op, "未知高度模式 %d", hm.Kind)
	}
	if px < 0 || py < 0 {
		return kerr(KindInvalidArg, op, "负内边距 (%d,%d)", px, py)
	}
	return nil
}

func (k *Kernel) NewNode(width, height Mode, padX, padY int64, isolated bool) (NodeID, error) {
	const op = "NewNode"
	if err := validateGeom(op, width, height, padX, padY); err != nil {
		return 0, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	id := k.nextID
	k.nextID++
	n := newNode(id, width, height, padX, padY, isolated)
	k.nodes[id] = n
	n.compute()
	k.logf("%s -> id=%d size=(%d,%d) isolated=%v", op, id, n.w, n.h, isolated)
	return id, nil
}

func (k *Kernel) mustNode(op string, id NodeID) (*node, error) {
	n, ok := k.nodes[id]
	if !ok {
		return nil, kerr(KindNodeNotFound, op, "节点 %d 不存在", id)
	}
	return n, nil
}

func (k *Kernel) SetWidth(id NodeID, m Mode) error {
	const op = "SetWidth"
	if err := validateGeom(op, m, Auto(), 0, 0); err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	n, err := k.mustNode(op, id)
	if err != nil {
		return err
	}
	if n.width == m {
		k.logf("%s id=%d 同值，不标脏", op, id)
		return nil
	}
	wasBoundary := n.isBoundaryFor(k.root)
	n.width = m
	k.logf("%s id=%d -> kind=%d value=%d", op, id, m.Kind, m.Value)
	k.afterAttrChange(n, wasBoundary)
	return nil
}

func (k *Kernel) SetHeight(id NodeID, m Mode) error {
	const op = "SetHeight"
	if err := validateGeom(op, Auto(), m, 0, 0); err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	n, err := k.mustNode(op, id)
	if err != nil {
		return err
	}
	if n.height == m {
		k.logf("%s id=%d 同值，不标脏", op, id)
		return nil
	}
	wasBoundary := n.isBoundaryFor(k.root)
	n.height = m
	k.logf("%s id=%d -> kind=%d value=%d", op, id, m.Kind, m.Value)
	k.afterAttrChange(n, wasBoundary)
	return nil
}

func (k *Kernel) SetPadding(id NodeID, x, y int64) error {
	const op = "SetPadding"
	if x < 0 || y < 0 {
		return kerr(KindInvalidArg, op, "负内边距 (%d,%d)", x, y)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	n, err := k.mustNode(op, id)
	if err != nil {
		return err
	}
	if n.padX == x && n.padY == y {
		k.logf("%s id=%d 同值，不标脏", op, id)
		return nil
	}
	n.padX, n.padY = x, y
	k.logf("%s id=%d -> (%d,%d)", op, id, x, y)
	k.markSelf(n, op)
	return nil
}

func (k *Kernel) SetIsolated(id NodeID, isolated bool) error {
	const op = "SetIsolated"
	k.mu.Lock()
	defer k.mu.Unlock()
	n, err := k.mustNode(op, id)
	if err != nil {
		return err
	}
	if n.isolated == isolated {
		k.logf("%s id=%d 同值，不标脏", op, id)
		return nil
	}
	wasBoundary := n.isBoundaryFor(k.root)
	n.isolated = isolated
	k.logf("%s id=%d -> %v", op, id, isolated)
	k.afterAttrChange(n, wasBoundary)
	return nil
}

// afterAttrChange：属性修改使节点自身脏；若因此失去边界身份，
// 原本封在其内部的子树脏标记须沿祖先继续上传；获得边界身份不撤回。
func (k *Kernel) afterAttrChange(n *node, wasBoundary bool) {
	const op = "attr"
	k.markSelf(n, op)
	nowBoundary := n.isBoundaryFor(k.root)
	if wasBoundary && !nowBoundary {
		delete(k.boundaries, n)
		if n.subDirty {
			k.logf("节点 %d 失去边界身份，子树脏继续上传", n.id)
			k.propagateSubDirty(n.parent, op)
		}
	}
	if !wasBoundary && nowBoundary {
		k.boundaries[n] = struct{}{}
		k.logf("节点 %d 获得边界身份，已上传标记不撤回", n.id)
	}
}

func (k *Kernel) Insert(parentID, childID NodeID, index int) error {
	const op = "Insert"
	if index < 0 {
		return kerr(KindInvalidArg, op, "负下标 %d", index)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	p, err := k.mustNode(op, parentID)
	if err != nil {
		return err
	}
	c, err := k.mustNode(op, childID)
	if err != nil {
		return err
	}
	if index > len(p.children) {
		return kerr(KindInvalidArg, op, "下标 %d 越界（当前子数 %d）", index, len(p.children))
	}
	if c == p {
		return kerr(KindInvalidArg, op, "节点不能作为自身子孙")
	}
	if isAncestor(c, p) {
		return kerr(KindInvalidArg, op, "不能把祖先 %d 插入自身子孙 %d", childID, parentID)
	}
	if c.parent != nil {
		return kerr(KindConflict, op, "节点 %d 已有父节点 %d", childID, c.parent.id)
	}
	p.children = append(p.children, nil)
	copy(p.children[index+1:], p.children[index:])
	p.children[index] = c
	c.parent = p
	k.logf("%s child=%d -> parent=%d[%d]", op, childID, parentID, index)
	k.markStructureChange(p, op)
	k.reattachDirty(c, op)
	return nil
}

func (k *Kernel) Remove(id NodeID) error {
	const op = "Remove"
	k.mu.Lock()
	defer k.mu.Unlock()
	n, err := k.mustNode(op, id)
	if err != nil {
		return err
	}
	if n == k.root {
		return kerr(KindConflict, op, "不能移除根节点")
	}
	if n.parent == nil {
		return kerr(KindConflict, op, "节点 %d 已处于摘除状态", id)
	}
	old := n.parent
	idx := childIndex(old, n)
	old.children = append(old.children[:idx], old.children[idx+1:]...)
	n.parent = nil
	k.logf("%s id=%d from parent=%d（摘下子树脏标记保留）", op, id, old.id)
	k.markStructureChange(old, op)
	if !n.isBoundaryFor(k.root) {
		delete(k.boundaries, n)
	}
	return nil
}

func (k *Kernel) Move(childID, newParentID NodeID, index int) error {
	const op = "Move"
	if index < 0 {
		return kerr(KindInvalidArg, op, "负下标 %d", index)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	c, err := k.mustNode(op, childID)
	if err != nil {
		return err
	}
	p, err := k.mustNode(op, newParentID)
	if err != nil {
		return err
	}
	if c == k.root {
		return kerr(KindConflict, op, "不能移动根节点")
	}
	if c == p {
		return kerr(KindInvalidArg, op, "节点不能作为自身子孙")
	}
	if isAncestor(c, p) {
		return kerr(KindInvalidArg, op, "不能把节点 %d 移入自身子孙 %d", childID, newParentID)
	}
	if c.parent == nil {
		return kerr(KindConflict, op, "节点 %d 已摘除，移动请用 Insert", childID)
	}
	if c.parent == p {
		return kerr(KindConflict, op, "节点 %d 已在父节点 %d 下", childID, newParentID)
	}
	if index > len(p.children) {
		return kerr(KindInvalidArg, op, "下标 %d 越界（当前子数 %d）", index, len(p.children))
	}
	old := c.parent
	oldIdx := childIndex(old, c)
	old.children = append(old.children[:oldIdx], old.children[oldIdx+1:]...)
	p.children = append(p.children, nil)
	copy(p.children[index+1:], p.children[index:])
	p.children[index] = c
	c.parent = p
	k.logf("%s id=%d: parent=%d -> parent=%d[%d]（被移动子树标记不变）", op, childID, old.id, newParentID, index)
	k.markStructureChange(old, op)
	k.markStructureChange(p, op)
	k.reattachDirty(c, op)
	return nil
}

// reattachDirty：挂上/移入一棵子树后，子树内部未被边界封住的
// 脏标记须沿新位置向最近布局边界重新传播。
func (k *Kernel) reattachDirty(c *node, op string) {
	if c.subDirty && !c.isBoundaryFor(k.root) {
		k.logf("子树 %d 内含脏且顶不是边界，按新位置重新传播", c.id)
		k.propagateSubDirty(c.parent, op)
	}
}

func childIndex(p, c *node) int {
	for i, ch := range p.children {
		if ch == c {
			return i
		}
	}
	return -1
}

func isAncestor(maybeAnc, n *node) bool {
	for p := n; p != nil; p = p.parent {
		if p == maybeAnc {
			return true
		}
	}
	return false
}

func (k *Kernel) Commit() ([]Change, error) {
	const op = "Commit"
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.inCommit {
		return nil, kerr(KindReentrantCommit, op, "提交过程中重入提交")
	}
	k.inCommit = true
	defer func() { k.inCommit = false }()
	k.stats.Commits++
	changes := k.commitLocked(op)
	if k.ReentryHook != nil {
		k.ReentryHook()
	}
	return changes, nil
}

func (k *Kernel) Size(id NodeID) (Size, error) {
	const op = "Size"
	k.mu.Lock()
	defer k.mu.Unlock()
	n, err := k.mustNode(op, id)
	if err != nil {
		return Size{}, err
	}
	if k.pending {
		k.logf("%s id=%d 存在未提交脏标记，隐式提交", op, id)
		k.stats.Commits++
		k.commitLocked("implicitCommit")
	}
	return Size{W: n.w, H: n.h}, nil
}

func (k *Kernel) StatsSnapshot() Stats {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.stats
}

func (k *Kernel) logf(format string, args ...any) {
	if k.Log != nil {
		k.Log.Printf(format, args...)
	}
}

package quota

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// 朴素模型：不维护任何增量聚合，每次查询都从头累加整棵树，
// 用于对照 Ledger 的增量实现。

type nnode struct {
	dir      bool
	size     int64
	parent   int
	r        int64
	bl, nl   int64
	children map[int]bool
}

type naive struct {
	nodes map[int]*nnode
	next  int
}

func newNaive() *naive {
	return &naive{
		nodes: map[int]*nnode{
			RootID: {dir: true, parent: -1, bl: -1, nl: -1, children: map[int]bool{}},
		},
		next: 1,
	}
}

func (nv *naive) clone() *naive {
	c := &naive{nodes: make(map[int]*nnode, len(nv.nodes)), next: nv.next}
	for id, n := range nv.nodes {
		m := *n
		if n.children != nil {
			m.children = make(map[int]bool, len(n.children))
			for k := range n.children {
				m.children[k] = true
			}
		}
		c.nodes[id] = &m
	}
	return c
}

// usage 从头累加：返回 d 的（子树字节， 子树条目数， 子树预留 R）。
func (nv *naive) usage(d int) (bytes, entries, reserved int64) {
	for id, n := range nv.nodes {
		if id == d {
			continue
		}
		for cur := id; cur != -1; cur = nv.nodes[cur].parent {
			if cur == d {
				entries++
				if n.dir {
					reserved += n.r
				} else {
					bytes += n.size
				}
				break
			}
		}
	}
	reserved += nv.nodes[d].r
	return bytes, entries, reserved
}

// checkChain 自近到远、同目录先字节后条目数核对。
func (nv *naive) checkChain(start int, dE, dEntries int64) error {
	for id := start; id != -1; id = nv.nodes[id].parent {
		n := nv.nodes[id]
		b, e, r := nv.usage(id)
		if n.bl >= 0 && b+r+dE > n.bl {
			return &QuotaError{Byte: true, Dir: id, Limit: n.bl, Used: b + r, Delta: dE}
		}
		if n.nl >= 0 && e+dEntries > n.nl {
			return &QuotaError{Dir: id, Limit: n.nl, Used: e, Delta: dEntries}
		}
	}
	return nil
}

func (nv *naive) mkdir(p int) (int, error) {
	pn, ok := nv.nodes[p]
	if !ok {
		return 0, notFoundErr(p)
	}
	if !pn.dir {
		return 0, typeMismatchErr("mkdir parent %d is a file", p)
	}
	if err := nv.checkChain(p, 0, 1); err != nil {
		return 0, err
	}
	id := nv.next
	nv.next++
	nv.nodes[id] = &nnode{dir: true, parent: p, bl: -1, nl: -1, children: map[int]bool{}}
	pn.children[id] = true
	return id, nil
}

func (nv *naive) addFile(p int, size int64) (int, error) {
	if size < 0 {
		return 0, invalidArgErr("negative file size %d", size)
	}
	pn, ok := nv.nodes[p]
	if !ok {
		return 0, notFoundErr(p)
	}
	if !pn.dir {
		return 0, typeMismatchErr("addfile parent %d is a file", p)
	}
	c := min(size, pn.r)
	if err := nv.checkChain(p, size-c, 1); err != nil {
		return 0, err
	}
	id := nv.next
	nv.next++
	nv.nodes[id] = &nnode{dir: false, size: size, parent: p}
	pn.children[id] = true
	pn.r -= c
	return id, nil
}

func (nv *naive) setQuota(d int, b, n int64) error {
	if b < -1 || n < -1 {
		return invalidArgErr("quota limits must be >= -1, got (%d, %d)", b, n)
	}
	dn, ok := nv.nodes[d]
	if !ok {
		return notFoundErr(d)
	}
	if !dn.dir {
		return typeMismatchErr("setquota on file node %d", d)
	}
	ub, ue, ur := nv.usage(d)
	if b >= 0 && b < ub+ur {
		return &BelowUsageError{Dir: d, Byte: true, Limit: b, Used: ub + ur}
	}
	if n >= 0 && n < ue {
		return &BelowUsageError{Dir: d, Byte: false, Limit: n, Used: ue}
	}
	dn.bl = b
	dn.nl = n
	return nil
}

func (nv *naive) reserve(d int, b int64) error {
	if b <= 0 {
		return invalidArgErr("reserve amount must be > 0, got %d", b)
	}
	dn, ok := nv.nodes[d]
	if !ok {
		return notFoundErr(d)
	}
	if !dn.dir {
		return typeMismatchErr("reserve on file node %d", d)
	}
	if err := nv.checkChain(d, b, 0); err != nil {
		return err
	}
	dn.r += b
	return nil
}

func (nv *naive) release(d int, b int64) error {
	if b <= 0 {
		return invalidArgErr("release amount must be > 0, got %d", b)
	}
	dn, ok := nv.nodes[d]
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
	return nil
}

func (nv *naive) resize(f int, size int64) error {
	if size < 0 {
		return invalidArgErr("negative file size %d", size)
	}
	fn, ok := nv.nodes[f]
	if !ok {
		return notFoundErr(f)
	}
	if fn.dir {
		return typeMismatchErr("resize on dir node %d", f)
	}
	delta := size - fn.size
	if delta > 0 {
		if err := nv.checkChain(fn.parent, delta, 0); err != nil {
			return err
		}
	}
	fn.size = size
	return nil
}

func (nv *naive) remove(x int) error {
	xn, ok := nv.nodes[x]
	if !ok {
		return notFoundErr(x)
	}
	if x == RootID {
		return fmt.Errorf("%w: remove", ErrRoot)
	}
	if xn.dir && len(xn.children) > 0 {
		return fmt.Errorf("%w: dir %d has %d children", ErrNotEmpty, x, len(xn.children))
	}
	delete(nv.nodes[xn.parent].children, x)
	delete(nv.nodes, x)
	return nil
}

func (nv *naive) rename(x, p int) error {
	xn, ok := nv.nodes[x]
	if !ok {
		return notFoundErr(x)
	}
	pn, ok := nv.nodes[p]
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
		for id := p; id != -1; id = nv.nodes[id].parent {
			if id == x {
				return fmt.Errorf("%w: dir %d into node %d", ErrCycle, x, p)
			}
		}
	}
	if xn.parent == p {
		return nil
	}
	// 差异链核对：x 的有效字节与条目增量。
	var dE, dEntries int64
	if xn.dir {
		b, e, r := nv.usage(x)
		dE = b + r
		dEntries = e + 1
	} else {
		dE = xn.size
		dEntries = 1
	}
	oldSet := map[int]bool{}
	for id := xn.parent; id != -1; id = nv.nodes[id].parent {
		oldSet[id] = true
	}
	for id := p; id != -1; id = nv.nodes[id].parent {
		if oldSet[id] {
			continue
		}
		n := nv.nodes[id]
		b, e, r := nv.usage(id)
		if n.bl >= 0 && b+r+dE > n.bl {
			return &QuotaError{Byte: true, Dir: id, Limit: n.bl, Used: b + r, Delta: dE}
		}
		if n.nl >= 0 && e+dEntries > n.nl {
			return &QuotaError{Dir: id, Limit: n.nl, Used: e, Delta: dEntries}
		}
	}
	delete(nv.nodes[xn.parent].children, x)
	pn.children[x] = true
	xn.parent = p
	return nil
}

func (nv *naive) apply(op Op) error {
	var err error
	switch op.Kind {
	case KindMkdir:
		_, err = nv.mkdir(op.X)
	case KindAddFile:
		_, err = nv.addFile(op.X, op.Size)
	case KindResize:
		err = nv.resize(op.X, op.Size)
	case KindRemove:
		err = nv.remove(op.X)
	case KindRename:
		err = nv.rename(op.X, op.P)
	case KindReserve:
		err = nv.reserve(op.X, op.Size)
	case KindRelease:
		err = nv.release(op.X, op.Size)
	case KindSetQuota:
		err = nv.setQuota(op.X, op.Size, op.N)
	default:
		err = invalidArgErr("unknown op kind %d", op.Kind)
	}
	return err
}

func (nv *naive) batch(ops []Op) (int, error) {
	if len(ops) == 0 {
		return -1, invalidArgErr("empty batch")
	}
	c := nv.clone()
	for i, op := range ops {
		if err := c.apply(op); err != nil {
			return i, &BatchError{Index: i, Err: err}
		}
	}
	*nv = *c
	return -1, nil
}

// 错误等价：所有哨兵分类一致，配额违规目录一致，批处理下标一致。
func sameErr(a, b error) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	sentinels := []error{
		ErrInvalidArgument, ErrNotFound, ErrTypeMismatch, ErrRoot, ErrNotEmpty,
		ErrCycle, ErrInsufficientReservation, ErrByteQuotaExceeded,
		ErrEntryQuotaExceeded, ErrBelowUsage, ErrBatchFailed,
	}
	for _, s := range sentinels {
		if errors.Is(a, s) != errors.Is(b, s) {
			return false
		}
	}
	var qa, qb *QuotaError
	if errors.As(a, &qa) != errors.As(b, &qb) {
		return false
	}
	if qa != nil && (qa.Dir != qb.Dir || qa.Byte != qb.Byte) {
		return false
	}
	var ba, bb *BatchError
	if errors.As(a, &ba) != errors.As(b, &bb) {
		return false
	}
	if ba != nil && ba.Index != bb.Index {
		return false
	}
	return true
}

func sortedIDs(nv *naive) []int {
	ids := make([]int, 0, len(nv.nodes))
	for id := range nv.nodes {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func (nv *naive) dirs() []int {
	var out []int
	for id, n := range nv.nodes {
		if n.dir {
			out = append(out, id)
		}
	}
	sort.Ints(out)
	return out
}

func (nv *naive) files() []int {
	var out []int
	for id, n := range nv.nodes {
		if !n.dir {
			out = append(out, id)
		}
	}
	sort.Ints(out)
	return out
}

// pickDir 选取一个目录编号：优先已存在的目录，偶尔取批内预测编号，偶尔取可能不存在的编号。
func pickDir(rng *rand.Rand, nv *naive, predicted []int) int {
	dirs := nv.dirs()
	switch r := rng.Intn(100); {
	case r < 10:
		return rng.Intn(nv.next + len(predicted) + 3)
	case r < 30 && len(predicted) > 0:
		return predicted[rng.Intn(len(predicted))]
	default:
		return dirs[rng.Intn(len(dirs))]
	}
}

// pickNode 选取任意节点编号（含文件与根）。
func pickNode(rng *rand.Rand, nv *naive, predicted []int) int {
	ids := sortedIDs(nv)
	switch r := rng.Intn(100); {
	case r < 10:
		return rng.Intn(nv.next + len(predicted) + 3)
	case r < 30 && len(predicted) > 0:
		return predicted[rng.Intn(len(predicted))]
	default:
		return ids[rng.Intn(len(ids))]
	}
}

func randSize(rng *rand.Rand) int64 {
	if rng.Intn(100) < 5 {
		return -1 - rng.Int63n(3) // 非法：负数
	}
	return rng.Int63n(60)
}

func randAmount(rng *rand.Rand) int64 {
	if rng.Intn(100) < 5 {
		return -rng.Int63n(3) // 非法：<= 0
	}
	return 1 + rng.Int63n(50)
}

func randLimit(rng *rand.Rand) int64 {
	switch r := rng.Intn(100); {
	case r < 3:
		return -2 // 非法：小于 -1
	case r < 35:
		return -1
	default:
		return rng.Int63n(80)
	}
}

// genOp 生成一个随机操作；predicted 为批内前序新建节点的预测编号。
func genOp(rng *rand.Rand, nv *naive, predicted []int) Op {
	switch r := rng.Intn(100); {
	case r < 15:
		return OpMkdir(pickDir(rng, nv, predicted))
	case r < 35:
		return OpAddFile(pickDir(rng, nv, predicted), randSize(rng))
	case r < 45:
		return OpReserve(pickDir(rng, nv, predicted), randAmount(rng))
	case r < 53:
		return OpRelease(pickDir(rng, nv, predicted), randAmount(rng))
	case r < 61:
		files := nv.files()
		if len(files) == 0 || rng.Intn(100) < 10 {
			return OpResize(rng.Intn(nv.next+3), randSize(rng))
		}
		return OpResize(files[rng.Intn(len(files))], randSize(rng))
	case r < 69:
		return OpRemove(pickNode(rng, nv, predicted))
	case r < 79:
		return OpRename(pickNode(rng, nv, predicted), pickDir(rng, nv, predicted))
	default:
		return OpSetQuota(pickDir(rng, nv, predicted), randLimit(rng), randLimit(rng))
	}
}

// checkState 对照 Ledger 与朴素模型的全部状态。
func checkState(t *testing.T, l *Ledger, nv *naive) {
	t.Helper()
	if len(l.st.nodes) != len(nv.nodes) {
		t.Fatalf("node count: ledger=%d naive=%d", len(l.st.nodes), len(nv.nodes))
	}
	if l.st.nextID != nv.next {
		t.Fatalf("nextID: ledger=%d naive=%d", l.st.nextID, nv.next)
	}
	for _, id := range sortedIDs(nv) {
		n := nv.nodes[id]
		if !n.dir {
			continue
		}
		b, e, r, err := l.Usage(id)
		if err != nil {
			t.Fatalf("Usage(%d): %v", id, err)
		}
		nb, ne, nr := nv.usage(id)
		if b != nb || e != ne || r != nr {
			t.Fatalf("Usage(%d): ledger=(%d,%d,%d) naive=(%d,%d,%d)",
				id, b, e, r, nb, ne, nr)
		}
	}
}

// TestRandomAgainstNaive 2000 组随机操作序列（含随机 Batch）与朴素模型对照。
// 日志打印每步的输入、输出与判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 1))
		l := NewLedger()
		nv := newNaive()
		steps := 10 + rng.Intn(25)
		for step := 0; step < steps; step++ {
			if rng.Intn(100) < 15 {
				runRandomBatch(t, seq, step, rng, l, nv)
			} else {
				op := genOp(rng, nv, nil)
				runSingleOp(t, seq, step, l, nv, op)
			}
			checkState(t, l, nv)
		}
	}
}

func runSingleOp(t *testing.T, seq, step int, l *Ledger, nv *naive, op Op) {
	t.Helper()
	var lErr, nErr error
	var lID, nID int
	switch op.Kind {
	case KindMkdir:
		lID, lErr = l.Mkdir(op.X)
		nID, nErr = nv.mkdir(op.X)
	case KindAddFile:
		lID, lErr = l.AddFile(op.X, op.Size)
		nID, nErr = nv.addFile(op.X, op.Size)
	case KindResize:
		lErr, nErr = l.Resize(op.X, op.Size), nv.resize(op.X, op.Size)
	case KindRemove:
		lErr, nErr = l.Remove(op.X), nv.remove(op.X)
	case KindRename:
		lErr, nErr = l.Rename(op.X, op.P), nv.rename(op.X, op.P)
	case KindReserve:
		lErr, nErr = l.Reserve(op.X, op.Size), nv.reserve(op.X, op.Size)
	case KindRelease:
		lErr, nErr = l.Release(op.X, op.Size), nv.release(op.X, op.Size)
	case KindSetQuota:
		lErr, nErr = l.SetQuota(op.X, op.Size, op.N), nv.setQuota(op.X, op.Size, op.N)
	}
	t.Logf("seq=%d step=%d op=%s -> id=%d err=%v", seq, step, op, lID, lErr)
	if !sameErr(lErr, nErr) {
		t.Fatalf("seq=%d step=%d op=%s: ledger err=%v, naive err=%v",
			seq, step, op, lErr, nErr)
	}
	if lErr == nil && (op.Kind == KindMkdir || op.Kind == KindAddFile) && lID != nID {
		t.Fatalf("seq=%d step=%d op=%s: ledger id=%d, naive id=%d",
			seq, step, op, lID, nID)
	}
}

func runRandomBatch(t *testing.T, seq, step int, rng *rand.Rand, l *Ledger, nv *naive) {
	t.Helper()
	k := 2 + rng.Intn(5)
	ops := make([]Op, 0, k)
	var predicted []int
	nextPredicted := nv.next
	for i := 0; i < k; i++ {
		op := genOp(rng, nv, predicted)
		if op.Kind == KindMkdir || op.Kind == KindAddFile {
			predicted = append(predicted, nextPredicted)
			nextPredicted++
		}
		ops = append(ops, op)
	}
	lIdx, lErr := l.Batch(ops)
	nIdx, nErr := nv.batch(ops)
	t.Logf("seq=%d step=%d batch=%v -> idx=%d err=%v", seq, step, ops, lIdx, lErr)
	if lIdx != nIdx {
		t.Fatalf("seq=%d step=%d batch=%v: ledger idx=%d, naive idx=%d",
			seq, step, ops, lIdx, nIdx)
	}
	if !sameErr(lErr, nErr) {
		t.Fatalf("seq=%d step=%d batch=%v: ledger err=%v, naive err=%v",
			seq, step, ops, lErr, nErr)
	}
	if lErr != nil {
		if !errors.Is(lErr, ErrBatchFailed) {
			t.Fatalf("seq=%d step=%d: batch error is not ErrBatchFailed: %v", seq, step, lErr)
		}
	}
}

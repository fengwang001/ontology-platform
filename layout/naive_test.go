package layout_test

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"

	"ontology/layout"
)

var naiveSeed = flag.Int64("naive-seed", 20261001, "seed for naive model differential tests")

// ---- 朴素参考模型：独立按题意从头实现，不依赖被测代码的任何内部函数 ----

type naiveFile struct {
	mode   layout.Mode
	size   int64
	xattrs []layout.Xattr // 按名字升序
}

type naiveWorld struct {
	a, x, bs, p int64
	files       map[int]*naiveFile
	next        int
}

type naivePlaced struct {
	locs     []layout.Location
	extBytes int64
	extKey   []byte
	external bool
	dataUsed int64
}

func nRoundup4(n int) int { return (n + 3) &^ 3 }

func nXattrSize(name, value []byte) int {
	return 4 + nRoundup4(len(name)) + nRoundup4(len(value))
}

func nCeilDiv(a, b int64) int64 {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

func (w *naiveWorld) place(mode layout.Mode, size int64, xs []layout.Xattr) naivePlaced {
	left := w.a
	if mode == layout.Inline {
		left = w.a - size
	}
	out := naivePlaced{locs: make([]layout.Location, len(xs))}
	i := 0
	for ; i < len(xs); i++ {
		need := int64(nXattrSize(xs[i].Name, xs[i].Value))
		if need <= left {
			out.locs[i] = layout.InInode
			left -= need
			continue
		}
		break
	}
	var buf bytes.Buffer
	for ; i < len(xs); i++ {
		out.locs[i] = layout.External
		out.extBytes += int64(nXattrSize(xs[i].Name, xs[i].Value))
		var h [4]byte
		binary.BigEndian.PutUint32(h[:], uint32(len(xs[i].Name)))
		buf.Write(h[:])
		buf.Write(xs[i].Name)
		binary.BigEndian.PutUint32(h[:], uint32(len(xs[i].Value)))
		buf.Write(h[:])
		buf.Write(xs[i].Value)
	}
	out.external = out.extBytes > 0
	out.extKey = buf.Bytes()
	if mode == layout.Block {
		out.dataUsed = nCeilDiv(size, w.bs)
	}
	return out
}

func (w *naiveWorld) legal(mode layout.Mode, size int64, xs []layout.Xattr) (naivePlaced, bool) {
	pl := w.place(mode, size, xs)
	return pl, pl.extBytes <= w.x
}

func (w *naiveWorld) clone() map[int]*naiveFile {
	c := make(map[int]*naiveFile, len(w.files))
	for id, f := range w.files {
		nf := &naiveFile{mode: f.mode, size: f.size, xattrs: make([]layout.Xattr, len(f.xattrs))}
		for i, xx := range f.xattrs {
			nf.xattrs[i] = layout.Xattr{Name: append([]byte(nil), xx.Name...), Value: append([]byte(nil), xx.Value...)}
		}
		c[id] = nf
	}
	return c
}

func (w *naiveWorld) poolOf(c map[int]*naiveFile) int64 {
	var total int64
	exts := map[string]struct{}{}
	for _, f := range c {
		pl, ok := w.legal(f.mode, f.size, f.xattrs)
		if !ok {
			panic("naive illegal state")
		}
		total += pl.dataUsed
		if pl.external {
			exts[string(pl.extKey)] = struct{}{}
		}
	}
	return total + int64(len(exts))
}

func (w *naiveWorld) pool() int64 { return w.poolOf(w.files) }

func (w *naiveWorld) extID(target int) int {
	f := w.files[target]
	pl, _ := w.legal(f.mode, f.size, f.xattrs)
	if !pl.external {
		return 0
	}
	id := 0
	for oid, of := range w.files {
		op, _ := w.legal(of.mode, of.size, of.xattrs)
		if op.external && bytes.Equal(op.extKey, pl.extKey) && (id == 0 || oid < id) {
			id = oid
		}
	}
	return id
}

func setSorted(xs []layout.Xattr) []layout.Xattr {
	sort.Slice(xs, func(i, j int) bool { return bytes.Compare(xs[i].Name, xs[j].Name) < 0 })
	return xs
}

// 期望错误类别；空串表示成功。
func errCat(err error) string {
	switch {
	case err == nil:
		return ""
	case errorIs(err, layout.ErrInvalidArgument):
		return "INVALID"
	case errorIs(err, layout.ErrNotFound):
		return "NOTFOUND"
	case errorIs(err, layout.ErrNoXattr):
		return "NOXATTR"
	case errorIs(err, layout.ErrXattrTooLarge):
		return "TOOBIG"
	case errorIs(err, layout.ErrPoolFull):
		return "POOLFULL"
	default:
		return "OTHER:" + err.Error()
	}
}

func errorIs(err, target error) bool {
	type iser interface{ Is(error) bool }
	for err != nil {
		if err == target {
			return true
		}
		if ei, ok := err.(iser); ok && ei.Is(target) {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

type stepResult struct {
	cat   string
	id    int
	value []byte
	stat  *layout.StatInfo
	pool  int64
}

// runOne 在候选世界上模拟一个操作，返回期望错误类别；成功时候选世界已被修改。
// 输入 desc 仅用于日志。
func (w *naiveWorld) expect(op string, f int, size int64, name, value []byte, cand map[int]*naiveFile, newID int) string {
	switch op {
	case "create":
		cand[newID] = &naiveFile{mode: layout.Inline}
		return ""
	case "resize":
		if size < 0 || size > (1<<40) {
			return "INVALID"
		}
		file, ok := cand[f]
		if !ok {
			return "NOTFOUND"
		}
		newMode := file.mode
		if file.mode == layout.Inline {
			if size <= w.a {
				if _, ok := w.legal(layout.Inline, size, file.xattrs); ok {
					newMode = layout.Inline
				} else {
					newMode = layout.Block
				}
			} else {
				newMode = layout.Block
			}
		} else if size == 0 {
			newMode = layout.Inline
		}
		if _, ok := w.legal(newMode, size, file.xattrs); !ok {
			return "TOOBIG"
		}
		savedMode, savedSize := file.mode, file.size
		file.mode, file.size = newMode, size
		if w.poolOf(cand) > w.p {
			file.mode, file.size = savedMode, savedSize
			return "POOLFULL"
		}
		return ""
	case "setxattr":
		if len(name) < 1 || len(name) > 255 || len(value) > 4096 {
			return "INVALID"
		}
		file, ok := cand[f]
		if !ok {
			return "NOTFOUND"
		}
		nx := make([]layout.Xattr, len(file.xattrs))
		copy(nx, file.xattrs)
		found := false
		for i := range nx {
			if bytes.Equal(nx[i].Name, name) {
				nx[i].Value = append([]byte(nil), value...)
				found = true
				break
			}
		}
		if !found {
			nx = append(nx, layout.Xattr{Name: append([]byte(nil), name...), Value: append([]byte(nil), value...)})
			nx = setSorted(nx)
		}
		newMode := file.mode
		if _, ok := w.legal(newMode, file.size, nx); !ok && newMode == layout.Inline {
			newMode = layout.Block
		}
		if _, ok := w.legal(newMode, file.size, nx); !ok {
			return "TOOBIG"
		}
		saved := *file
		file.mode, file.xattrs = newMode, nx
		if w.poolOf(cand) > w.p {
			*file = saved
			return "POOLFULL"
		}
		return ""
	case "removexattr":
		if len(name) < 1 || len(name) > 255 {
			return "INVALID"
		}
		file, ok := cand[f]
		if !ok {
			return "NOTFOUND"
		}
		idx := -1
		for i := range file.xattrs {
			if bytes.Equal(file.xattrs[i].Name, name) {
				idx = i
				break
			}
		}
		if idx < 0 {
			return "NOXATTR"
		}
		nx := append(file.xattrs[:idx:idx], file.xattrs[idx+1:]...)
		newMode := file.mode
		if _, ok := w.legal(newMode, file.size, nx); !ok && newMode == layout.Inline {
			newMode = layout.Block
		}
		if _, ok := w.legal(newMode, file.size, nx); !ok {
			return "TOOBIG"
		}
		saved := *file
		file.mode, file.xattrs = newMode, nx
		if w.poolOf(cand) > w.p {
			*file = saved
			return "POOLFULL"
		}
		return ""
	case "getxattr":
		if len(name) < 1 || len(name) > 255 {
			return "INVALID"
		}
		file, ok := cand[f]
		if !ok {
			return "NOTFOUND"
		}
		for _, xx := range file.xattrs {
			if bytes.Equal(xx.Name, name) {
				return ""
			}
		}
		return "NOXATTR"
	case "clone":
		if _, ok := cand[f]; !ok {
			return "NOTFOUND"
		}
		src := cand[f]
		cp := &naiveFile{mode: src.mode, size: src.size, xattrs: make([]layout.Xattr, len(src.xattrs))}
		for i, xx := range src.xattrs {
			cp.xattrs[i] = layout.Xattr{Name: append([]byte(nil), xx.Name...), Value: append([]byte(nil), xx.Value...)}
		}
		cand[newID] = cp
		if w.poolOf(cand) > w.p {
			delete(cand, newID)
			return "POOLFULL"
		}
		return ""
	case "stat":
		if _, ok := cand[f]; !ok {
			return "NOTFOUND"
		}
		return ""
	}
	return "INVALID"
}

type op struct {
	kind  string
	f     int
	size  int64
	name  []byte
	value []byte
}

type outcome struct {
	cat   string
	id    int
	value []byte
	pool  int64
	stat  *layout.StatInfo
}

func statDigest(s *layout.StatInfo) string {
	if s == nil {
		return "<nil>"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "mode=%d size=%d data=%d ext=%d", s.Mode, s.Size, s.DataUsed, s.ExtID)
	for _, x := range s.Xattrs {
		fmt.Fprintf(&b, "|%s:%d", x.Name, x.Location)
	}
	return b.String()
}

func execOp(m *layout.Manager, o op) outcome {
	r := outcome{}
	switch o.kind {
	case "create":
		r.id, _ = m.Create()
	case "resize":
		r.cat = errCat(m.Resize(o.f, o.size))
	case "setxattr":
		r.cat = errCat(m.SetXattr(o.f, o.name, o.value))
	case "removexattr":
		r.cat = errCat(m.RemoveXattr(o.f, o.name))
	case "getxattr":
		v, e := m.GetXattr(o.f, o.name)
		r.value, r.cat = v, errCat(e)
	case "clone":
		id, e := m.Clone(o.f)
		r.id, r.cat = id, errCat(e)
	case "stat":
		s, e := m.Stat(o.f)
		r.stat, r.cat = s, errCat(e)
	}
	r.pool = m.PoolUsed()
	return r
}

func (w *naiveWorld) expectOp(o op, newID int) (string, []byte, *layout.StatInfo) {
	cand := w.clone()
	cat := w.expect(o.kind, o.f, o.size, o.name, o.value, cand, newID)
	if cat != "" {
		return cat, nil, nil
	}
	w.files = cand
	var gotVal []byte
	var gotStat *layout.StatInfo
	switch o.kind {
	case "create", "clone":
		w.next = newID + 1
	case "getxattr":
		for _, xx := range w.files[o.f].xattrs {
			if bytes.Equal(xx.Name, o.name) {
				gotVal = append([]byte(nil), xx.Value...)
			}
		}
	case "stat":
		f := w.files[o.f]
		pl, _ := w.legal(f.mode, f.size, f.xattrs)
		gotStat = &layout.StatInfo{
			Mode: f.mode, Size: f.size, DataUsed: pl.dataUsed, ExtID: w.extID(o.f),
			Xattrs: make([]layout.XattrLoc, len(f.xattrs)),
		}
		for i, xx := range f.xattrs {
			gotStat.Xattrs[i] = layout.XattrLoc{Name: append([]byte(nil), xx.Name...), Location: pl.locs[i]}
		}
	}
	return "", gotVal, gotStat
}

func pickBytes(rng *rand.Rand, pool [][]byte) []byte {
	return pool[rng.Intn(len(pool))]
}

func genOps(rng *rand.Rand, n int) []op {
	names := [][]byte{
		[]byte("a"), []byte("b"), []byte("d"), []byte("m"),
		bytes.Repeat([]byte("n"), 3), bytes.Repeat([]byte("z"), 255),
		[]byte(""), bytes.Repeat([]byte("q"), 256),
	}
	mkVal := func() []byte {
		lens := []int{0, 1, 3, 4, 5, 7, 8, 9, 12, 16, 20, 32, 60}
		return bytes.Repeat([]byte{byte('A' + rng.Intn(26))}, lens[rng.Intn(len(lens))])
	}
	ops := make([]op, 0, n)
	nextID := 1
	live := []int{}
	for i := 0; i < n; i++ {
		roll := rng.Intn(100)
		switch {
		case roll < 14:
			ops = append(ops, op{kind: "create"})
			live = append(live, nextID)
			nextID++
		case roll < 30 && len(live) > 0:
			sizes := []int64{0, 1, 3, 4, 5, 8, 12, 16, 17, 20, 1 << 40, -1, 1<<40 + 1}
			ops = append(ops, op{kind: "resize", f: live[rng.Intn(len(live))], size: sizes[rng.Intn(len(sizes))]})
		case roll < 58 && len(live) > 0:
			o := op{kind: "setxattr", f: live[rng.Intn(len(live))], name: pickBytes(rng, names)}
			if rng.Intn(20) == 0 {
				o.value = make([]byte, 4097)
			} else {
				o.value = mkVal()
			}
			ops = append(ops, o)
		case roll < 68 && len(live) > 0:
			ops = append(ops, op{kind: "removexattr", f: live[rng.Intn(len(live))], name: pickBytes(rng, names)})
		case roll < 78 && len(live) > 0:
			ops = append(ops, op{kind: "getxattr", f: live[rng.Intn(len(live))], name: pickBytes(rng, names)})
		case roll < 90 && len(live) > 0:
			ops = append(ops, op{kind: "clone", f: live[rng.Intn(len(live))]})
			live = append(live, nextID)
			nextID++
		case len(live) > 0:
			target := live[rng.Intn(len(live))]
			if rng.Intn(5) == 0 {
				target = 999
			}
			ops = append(ops, op{kind: "stat", f: target})
		default:
			ops = append(ops, op{kind: "stat", f: 999})
		}
	}
	return ops
}

func runSequence(t *testing.T, A, X, Bs, P int64, ops []op, tag string, seed int64) []outcome {
	t.Helper()
	m := layout.New(A, X, Bs, P)
	w := &naiveWorld{a: A, x: X, bs: Bs, p: P, files: map[int]*naiveFile{}, next: 1}
	results := make([]outcome, len(ops))
	var logb strings.Builder
	fmt.Fprintf(&logb, "[%s seed=%d params A=%d X=%d Bs=%d P=%d]\n", tag, seed, A, X, Bs, P)
	for i, o := range ops {
		newID := w.next
		expCat, expVal, expStat := w.expectOp(o, newID)
		got := execOp(m, o)
		results[i] = got
		expPool := w.pool()
		fmt.Fprintf(&logb, "step %d op=%s f=%d size=%d name=%q vlen=%d => cat=%s id=%d pool(actual=%d naive=%d)",
			i, o.kind, o.f, o.size, o.name, len(o.value), got.cat, got.id, got.pool, expPool)
		fail := got.cat != expCat
		if o.kind == "create" || o.kind == "clone" {
			if expCat == "" && got.id != newID {
				fail = true
			}
			fmt.Fprintf(&logb, " [want id=%d]", newID)
		}
		if o.kind == "getxattr" && expCat == "" && !bytes.Equal(got.value, expVal) {
			fail = true
		}
		if o.kind == "stat" {
			if expCat == "" && statDigest(got.stat) != statDigest(expStat) {
				fail = true
			}
			fmt.Fprintf(&logb, " [actual %s] [naive %s]", statDigest(got.stat), statDigest(expStat))
		}
		if got.pool != expPool || got.pool > P {
			fail = true
		}
		for id := range w.files {
			s, err := m.Stat(id)
			if err != nil {
				fail = true
				fmt.Fprintf(&logb, " [stat(%d) err=%v]", id, err)
				break
			}
			nf := w.files[id]
			pl, _ := w.legal(nf.mode, nf.size, nf.xattrs)
			ns := &layout.StatInfo{
				Mode: nf.mode, Size: nf.size, DataUsed: pl.dataUsed, ExtID: w.extID(id),
				Xattrs: make([]layout.XattrLoc, len(nf.xattrs)),
			}
			for j, xx := range nf.xattrs {
				ns.Xattrs[j] = layout.XattrLoc{Name: xx.Name, Location: pl.locs[j]}
			}
			if statDigest(s) != statDigest(ns) {
				fail = true
				fmt.Fprintf(&logb, " [cross-check f=%d actual %s != naive %s]", id, statDigest(s), statDigest(ns))
				break
			}
		}
		if fail {
			fmt.Fprintf(&logb, " => MISMATCH\n")
			t.Logf("%s", logb.String())
			t.Fatalf("[%s] differential mismatch at step %d (seed=%d)", tag, i, seed)
		}
		fmt.Fprintf(&logb, " => ok\n")
	}
	t.Logf("%s", logb.String())
	return results
}

// TestNaiveDifferential：2000 组随机序列与朴素模型逐步对照，并在第二个管理器上
// 原样重放校验完全相同的可复现性。-v 时打印每组每步的输入、输出与判定依据。
func TestNaiveDifferential(t *testing.T) {
	if !testing.Verbose() {
		t.Logf("differential test: add -v to print every input/output/decision")
	}
	const sequences, steps = 2000, 40
	rootRng := rand.New(rand.NewSource(*naiveSeed))
	for seq := 0; seq < sequences; seq++ {
		seed := rootRng.Int63()
		rng := rand.New(rand.NewSource(seed))
		A := int64(1 + rng.Intn(20))
		X := int64(1 + rng.Intn(48))
		Bs := int64(1 + rng.Intn(8))
		P := int64(1 + rng.Intn(14))
		ops := genOps(rng, steps)
		first := runSequence(t, A, X, Bs, P, ops, "first", seed)
		m2 := layout.New(A, X, Bs, P)
		for i, o := range ops {
			got := execOp(m2, o)
			if got.cat != first[i].cat || got.id != first[i].id || got.pool != first[i].pool ||
				!bytes.Equal(got.value, first[i].value) || statDigest(got.stat) != statDigest(first[i].stat) {
				t.Fatalf("replay mismatch seq=%d step=%d seed=%d", seq, i, seed)
			}
		}
	}
}

// TestConcurrentSmoke：高并发混合操作，校验全局不变量；配合 -race 检测竞态。
func TestConcurrentSmoke(t *testing.T) {
	m := layout.New(12, 40, 4, 30)
	base, err := m.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetXattr(base, []byte("a"), []byte("v1")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 12; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g) + 777))
			mine := []int{base}
			for i := 0; i < 300; i++ {
				f := mine[rng.Intn(len(mine))]
				switch rng.Intn(8) {
				case 0:
					if id, e := m.Create(); e == nil {
						mine = append(mine, id)
					}
				case 1:
					_ = m.Resize(f, int64(rng.Intn(24)))
				case 2:
					_ = m.SetXattr(f, []byte("a"), bytes.Repeat([]byte("x"), rng.Intn(40)))
				case 3:
					_ = m.SetXattr(f, []byte("b"), []byte("y"))
				case 4:
					_ = m.RemoveXattr(f, []byte("a"))
				case 5:
					_, _ = m.GetXattr(f, []byte("a"))
				case 6:
					if id, e := m.Clone(f); e == nil {
						mine = append(mine, id)
					}
				default:
					_, _ = m.Stat(f)
				}
			}
		}(g)
	}
	wg.Wait()
	if used := m.PoolUsed(); used > 30 {
		t.Fatalf("pool invariant violated: %d", used)
	}
}

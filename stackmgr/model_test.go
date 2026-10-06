package stackmgr

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// naiveModel 是独立朴素模型：每个协程一开始就分配 MaxStackSize 大小的
// 固定内存，从不搬迁；帧以逻辑（帧序号, 槽位）编址，帧序号单调且不复用，
// 因而悬垂判定与真实系统一致。它用来对照“搬迁”这一优化不改变任何
// 可观察结果：槽位内容与指针最终目标。
type naiveModel struct {
	cfg     Config
	mem     map[int]map[slotKey]Value // coID -> 槽 -> 值
	frames  map[int][]modelFrame
	nextFID map[int]int64
	quota   int // 朴素模型：所有协程恒占 MaxStackSize
}

type slotKey struct {
	fid  int64
	slot int
}

type modelFrame struct {
	id    int64
	slots int
}

func newNaiveModel(cfg Config) *naiveModel {
	return &naiveModel{
		cfg:     cfg,
		mem:     map[int]map[slotKey]Value{},
		frames:  map[int][]modelFrame{},
		nextFID: map[int]int64{},
	}
}

func (n *naiveModel) spawn(id int) error {
	if n.quota+n.cfg.MaxStackSize > n.cfg.TotalQuota {
		return errf(ClassQuota, "naive: quota")
	}
	n.quota += n.cfg.MaxStackSize
	n.mem[id] = map[slotKey]Value{}
	n.frames[id] = nil
	n.nextFID[id] = 1
	return nil
}

func (n *naiveModel) used(co int) int {
	u := 0
	for _, f := range n.frames[co] {
		u += f.slots
	}
	return u
}

func (n *naiveModel) push(co, slots int) error {
	if _, ok := n.mem[co]; !ok {
		return errf(ClassUndefined, "naive: co")
	}
	if slots <= 0 {
		return errf(ClassParameter, "naive: param")
	}
	if n.used(co)+slots > n.cfg.MaxStackSize {
		return errf(ClassStackOverflow, "naive: overflow")
	}
	fid := n.nextFID[co]
	n.nextFID[co]++
	n.frames[co] = append(n.frames[co], modelFrame{id: fid, slots: slots})
	return nil
}

func (n *naiveModel) pop(co int) error {
	fs, ok := n.frames[co]
	if !ok {
		return errf(ClassUndefined, "naive: co")
	}
	if len(fs) == 0 {
		return errf(ClassParameter, "naive: empty")
	}
	top := fs[len(fs)-1]
	for s := 0; s < top.slots; s++ {
		delete(n.mem[co], slotKey{top.id, s})
	}
	n.frames[co] = fs[:len(fs)-1]
	return nil
}

func (n *naiveModel) addr(co, fi, slot int) (Pointer, error) {
	fs, ok := n.frames[co]
	if !ok {
		return Pointer{}, errf(ClassUndefined, "naive: co")
	}
	if fi < 0 {
		fi += len(fs)
	}
	if fi < 0 || fi >= len(fs) {
		return Pointer{}, errf(ClassUndefined, "naive: frame")
	}
	if slot < 0 || slot >= fs[fi].slots {
		return Pointer{}, errf(ClassUndefined, "naive: slot")
	}
	return Pointer{coID: co, frameID: fs[fi].id, slot: slot}, nil
}

func (n *naiveModel) writeInt(co, fi, slot int, v int64) error {
	p, err := n.addr(co, fi, slot)
	if err != nil {
		return err
	}
	n.mem[co][slotKey{p.frameID, slot}] = IntValue(v)
	return nil
}

func (n *naiveModel) writePtr(co int, p Pointer) error {
	fs, ok := n.frames[co]
	if !ok {
		return errf(ClassUndefined, "naive: co")
	}
	// 拒绝次序：目标槽位未定义先于指针自身的悬垂/跨栈。
	if len(fs) == 0 {
		return errf(ClassUndefined, "naive: no target frame")
	}
	target := fs[len(fs)-1]
	if p.slot < 0 {
		return errf(ClassParameter, "naive: param")
	}
	if p.coID != co {
		return errf(ClassCrossStack, "naive: cross")
	}
	var aliveFrame modelFrame
	alive := false
	for _, f := range fs {
		if f.id == p.frameID {
			alive, aliveFrame = true, f
		}
	}
	if !alive {
		return errf(ClassDangling, "naive: dangling")
	}
	if p.slot >= aliveFrame.slots {
		return errf(ClassUndefined, "naive: slot")
	}
	n.mem[co][slotKey{target.id, 0}] = PtrValue(p)
	return nil
}

func (n *naiveModel) readInt(co int, p Pointer) (int64, error) {
	fs, ok := n.frames[co]
	if !ok {
		return 0, errf(ClassUndefined, "naive: co")
	}
	if p.slot < 0 {
		return 0, errf(ClassParameter, "naive: param")
	}
	if p.coID != co {
		return 0, errf(ClassCrossStack, "naive: cross")
	}
	alive := -1
	for i, f := range fs {
		if f.id == p.frameID {
			alive = i
		}
	}
	if alive < 0 {
		return 0, errf(ClassDangling, "naive: dangling")
	}
	if p.slot >= fs[alive].slots {
		return 0, errf(ClassUndefined, "naive: slot")
	}
	v := n.mem[co][slotKey{p.frameID, p.slot}]
	if iv, ok := v.Int(); ok {
		return iv, nil
	}
	return 0, errf(ClassParameter, "naive: not int")
}

// op 是一条随机操作。
type op struct {
	kind string
	co   int
	fi   int
	slot int
	val  int64
	p    Pointer
	hasP bool
}

func TestDifferentialRandomSequence(t *testing.T) {
	for _, seed := range []int64{1, 20261006, 777, 424242, 987654321} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runDifferential(t, seed)
		})
	}
}

func runDifferential(t *testing.T, seed int64) {
	cfg := Config{BaseSize: 2, GrowthFactor: 2, MaxStackSize: 16, TotalQuota: 2048,
		ShrinkRatio: ShrinkRatio{1, 4}}
	const numCo = 3

	var log strings.Builder
	logStep := func(format string, args ...any) {
		fmt.Fprintf(&log, format+"\n", args...)
	}

	rng := rand.New(rand.NewSource(seed))
	real, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	model := newNaiveModel(cfg)

	// 记录每个协程“活着的、指向本协程”的指针，用于随机读写与跨栈写入。
	livePtrs := map[int][]Pointer{}

	spawnAll := func() {
		for c := 0; c < numCo; c++ {
			if _, e := real.Spawn(); e != nil {
				t.Fatalf("real spawn: %v", e)
			}
			if e := model.spawn(c); e != nil {
				t.Fatalf("model spawn: %v", e)
			}
		}
	}
	spawnAll()

	className := func(e error) string {
		if e == nil {
			return "ok"
		}
		return classOf(e).String()
	}

	genOp := func() op {
		c := rng.Intn(numCo)
		kinds := []string{"push", "pop", "writeint", "writeptr", "addr", "read"}
		k := kinds[rng.Intn(len(kinds))]
		o := op{kind: k, co: c, fi: -1, slot: rng.Intn(4)}
		switch k {
		case "push":
			o.val = int64(1 + rng.Intn(6))
		case "writeint":
			o.val = rng.Int63n(1000)
		case "writeptr":
			// 30% 概率跨协程指针；否则用本协程某存活指针。
			if rng.Intn(10) < 3 {
				other := (c + 1) % numCo
				if ps := livePtrs[other]; len(ps) > 0 {
					o.p = ps[rng.Intn(len(ps))]
					o.hasP = true
				} else {
					o.kind = "writeint"
					o.val = rng.Int63n(1000)
				}
			} else if ps := livePtrs[c]; len(ps) > 0 {
				o.p = ps[rng.Intn(len(ps))]
				o.hasP = true
			} else {
				o.kind = "writeint"
				o.val = rng.Int63n(1000)
			}
		case "read":
			if ps := livePtrs[c]; len(ps) > 0 {
				o.p = ps[rng.Intn(len(ps))]
				o.hasP = true
			} else {
				o.kind = "writeint"
				o.val = rng.Int63n(1000)
			}
		}
		return o
	}

	const steps = 3000
	for step := 0; step < steps; step++ {
		o := genOp()
		var rErr, mErr error
		var rVal int64
		var mVal int64
		var rPtr, mPtr Pointer
		switch o.kind {
		case "push":
			rErr = real.Push(o.co, int(o.val))
			mErr = model.push(o.co, int(o.val))
		case "pop":
			rErr = real.Pop(o.co)
			mErr = model.pop(o.co)
			// 弹帧后把指向已死帧的指针从对照集合移除（两系统判定应一致）。
			livePtrs[o.co] = filterAlive(livePtrs[o.co], model, o.co)
		case "writeint":
			rErr = real.WriteInt(o.co, o.fi, o.slot, o.val)
			mErr = model.writeInt(o.co, o.fi, o.slot, o.val)
		case "writeptr":
			rErr = real.WritePtr(o.co, o.fi, 0, o.p)
			mErr = model.writePtr(o.co, o.p)
		case "addr":
			rPtr, rErr = real.AddrOf(o.co, o.fi, o.slot)
			mPtr, mErr = model.addr(o.co, o.fi, o.slot)
		case "read":
			rVal, rErr = real.ReadInt(o.co, o.p)
			mVal, mErr = model.readInt(o.co, o.p)
		}

		rc, mc := className(rErr), className(mErr)
		basis := "拒绝类别一致"
		match := rc == mc
		if o.kind == "addr" {
			if rc == "ok" && mc == "ok" {
				if ptrTargetEq(rPtr, mPtr) {
					basis = "指针目标一致(协程,帧,槽)"
				} else {
					match = false
					basis = fmt.Sprintf("指针目标不一致 real=(c=%d,f=%d,s=%d) model=(c=%d,f=%d,s=%d)",
						rPtr.coID, rPtr.frameID, rPtr.slot, mPtr.coID, mPtr.frameID, mPtr.slot)
				}
				if match {
					livePtrs[o.co] = append(livePtrs[o.co], rPtr)
				}
			}
		}
		if o.kind == "read" && rc == "ok" && mc == "ok" {
			if rVal == mVal {
				basis = fmt.Sprintf("读值一致=%d", rVal)
			} else {
				match = false
				basis = fmt.Sprintf("读值不一致 real=%d model=%d", rVal, mVal)
			}
		}

		logStep("[%4d] IN  %-8s co=%d fi=%d slot=%d val=%d hasP=%v -> OUT real=%s model=%s | %s",
			step, o.kind, o.co, o.fi, o.slot, o.val, o.hasP, rc, mc, basis)
		if !match {
			t.Fatalf("step %d divergence on %+v: real=%v model=%v | %s\n%s",
				step, o, rErr, mErr, basis, tailLog(&log))
		}
	}

	// 最终：把所有协程弹空后，逐槽位对照最终内容不可能（朴素模型按帧存）；
	// 改为在每个存活指针上对照最终目标与读出值。
	for c := 0; c < numCo; c++ {
		for _, p := range livePtrs[c] {
			rv, re := real.ReadInt(c, p)
			mv, me := model.readInt(c, p)
			basis := "最终悬垂/读值一致"
			if className(re) != className(me) || (re == nil && rv != mv) {
				t.Fatalf("final mismatch co=%d p=(f=%d,s=%d): real(%d,%v) model(%d,%v) %s\n%s",
					c, p.frameID, p.slot, rv, re, mv, me, basis, tailLog(&log))
			}
			logStep("[FIN ] co=%d ptr(f=%d,s=%d) real=(%v,%s) model=(%v,%s) | %s",
				c, p.frameID, p.slot, rv, className(re), mv, className(me), basis)
		}
	}
	t.Log("\n" + log.String())
	t.Logf("differential test finished: %d steps, all outputs matched naive model", steps)
}

func ptrTargetEq(a, b Pointer) bool {
	return a.coID == b.coID && a.frameID == b.frameID && a.slot == b.slot
}

func filterAlive(ps []Pointer, n *naiveModel, co int) []Pointer {
	out := ps[:0]
	for _, p := range ps {
		alive := false
		for _, f := range n.frames[co] {
			if f.id == p.frameID {
				alive = true
			}
		}
		if alive {
			out = append(out, p)
		}
	}
	return out
}

func tailLog(b *strings.Builder) string {
	s := b.String()
	if len(s) > 4000 {
		return s[len(s)-4000:]
	}
	return s
}

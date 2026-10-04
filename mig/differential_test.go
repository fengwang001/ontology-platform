package mig

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// model 是按逻辑键映射表写成的朴素模拟，直接实现题目语义。
type model struct {
	a, b    map[int64]int64
	w       int64
	crashed bool
}

func newModel() *model {
	return &model{a: map[int64]int64{}, b: map[int64]int64{}, w: MinKey}
}

func (mod *model) put(k, v int64) error {
	if k < MinKey || k > MaxKey {
		return ErrInvalid
	}
	if mod.crashed {
		return ErrCrashed
	}
	if k < mod.w {
		mod.b[k] = v
	} else {
		mod.a[k] = v
	}
	return nil
}

func (mod *model) get(k int64) (int64, bool, error) {
	if k < MinKey || k > MaxKey {
		return 0, false, ErrInvalid
	}
	if k < mod.w {
		v, ok := mod.b[k]
		return v, ok, nil
	}
	v, ok := mod.a[k]
	return v, ok, nil
}

func (mod *model) del(k int64) (bool, error) {
	if k < MinKey || k > MaxKey {
		return false, ErrInvalid
	}
	if mod.crashed {
		return false, ErrCrashed
	}
	if k < mod.w {
		_, ok := mod.b[k]
		delete(mod.b, k)
		return ok, nil
	}
	_, ok := mod.a[k]
	delete(mod.a, k)
	return ok, nil
}

func (mod *model) scan(lo, hi int64) ([]Entry, error) {
	if lo < MinKey || hi > MaxKey+1 || lo > hi {
		return nil, ErrInvalid
	}
	var out []Entry
	bHi := min(hi, mod.w)
	for k, v := range mod.b {
		if k >= lo && k < bHi {
			out = append(out, Entry{Key: k, Val: v})
		}
	}
	aLo := max(lo, mod.w)
	for k, v := range mod.a {
		if k >= aLo && k < hi {
			out = append(out, Entry{Key: k, Val: v})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func sortedKeys(m map[int64]int64) []int64 {
	keys := make([]int64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func (mod *model) step(n int) (int, error) {
	if n < 1 || n > 10_000 {
		return 0, ErrInvalid
	}
	if mod.crashed {
		return 0, ErrCrashed
	}
	moved := 0
	for _, k := range sortedKeys(mod.a) {
		if moved >= n {
			break
		}
		mod.b[k] = mod.a[k]
		mod.w = k + 1
		delete(mod.a, k)
		moved++
	}
	return moved, nil
}

func (mod *model) stepPartial(p int) error {
	if p != 1 && p != 2 {
		return ErrInvalid
	}
	if mod.crashed {
		return ErrCrashed
	}
	keys := sortedKeys(mod.a)
	if len(keys) == 0 {
		return ErrDrained
	}
	k := keys[0]
	mod.b[k] = mod.a[k]
	if p == 2 {
		mod.w = k + 1
	}
	mod.crashed = true
	return nil
}

func (mod *model) recover() (int, int, error) {
	if !mod.crashed {
		return 0, 0, ErrNotCrashed
	}
	bDel, aDel := 0, 0
	for k := range mod.b {
		if k >= mod.w {
			delete(mod.b, k)
			bDel++
		}
	}
	for k := range mod.a {
		if k < mod.w {
			delete(mod.a, k)
			aDel++
		}
	}
	mod.crashed = false
	return bDel, aDel, nil
}

func (mod *model) finish() error {
	if mod.crashed {
		return ErrCrashed
	}
	if len(mod.a) > 0 {
		return ErrNotDrained
	}
	mod.w = MaxKey + 1
	return nil
}

type opKind int

const (
	opPut opKind = iota
	opGet
	opDelete
	opScan
	opStep
	opStepPartial
	opRecover
	opFinish
)

type op struct {
	kind   opKind
	k, v   int64
	lo, hi int64
	n, p   int
}

func genKey(r *rand.Rand) int64 {
	switch r.Intn(12) {
	case 0:
		return MinKey
	case 1:
		return MaxKey
	case 2:
		return 0
	case 3:
		return -1
	case 4:
		return MinKey - 1 - r.Int63n(1000)
	case 5:
		return MaxKey + 1 + r.Int63n(1000)
	default:
		return int64(r.Intn(21) - 10)
	}
}

func genBound(r *rand.Rand) int64 {
	if r.Intn(8) == 0 {
		return MaxKey + 1
	}
	return genKey(r)
}

func genOp(r *rand.Rand) op {
	switch w := r.Intn(100); {
	case w < 25:
		v := int64(r.Intn(2000) - 1000)
		if r.Intn(10) == 0 {
			v = r.Int63()
		}
		return op{kind: opPut, k: genKey(r), v: v}
	case w < 40:
		return op{kind: opGet, k: genKey(r)}
	case w < 55:
		return op{kind: opDelete, k: genKey(r)}
	case w < 70:
		return op{kind: opScan, lo: genBound(r), hi: genBound(r)}
	case w < 80:
		ns := []int{0, 1, 2, 3, 7, 10000, 10001, -1}
		return op{kind: opStep, n: ns[r.Intn(len(ns))]}
	case w < 88:
		ps := []int{0, 1, 1, 2, 2, 3}
		return op{kind: opStepPartial, p: ps[r.Intn(len(ps))]}
	case w < 94:
		return op{kind: opRecover}
	default:
		return op{kind: opFinish}
	}
}

func (o op) String() string {
	switch o.kind {
	case opPut:
		return fmt.Sprintf("Put(%d,%d)", o.k, o.v)
	case opGet:
		return fmt.Sprintf("Get(%d)", o.k)
	case opDelete:
		return fmt.Sprintf("Delete(%d)", o.k)
	case opScan:
		return fmt.Sprintf("Scan(%d,%d)", o.lo, o.hi)
	case opStep:
		return fmt.Sprintf("Step(%d)", o.n)
	case opStepPartial:
		return fmt.Sprintf("StepPartial(%d)", o.p)
	case opRecover:
		return "Recover()"
	default:
		return "Finish()"
	}
}

func applyMig(m *Migrator, o op) string {
	switch o.kind {
	case opPut:
		return fmt.Sprintf("err=%v", m.Put(o.k, o.v))
	case opGet:
		v, ok, err := m.Get(o.k)
		return fmt.Sprintf("val=%d ok=%v err=%v", v, ok, err)
	case opDelete:
		ok, err := m.Delete(o.k)
		return fmt.Sprintf("ok=%v err=%v", ok, err)
	case opScan:
		ents, err := m.Scan(o.lo, o.hi)
		return fmt.Sprintf("ents=%v err=%v", ents, err)
	case opStep:
		n, err := m.Step(o.n)
		return fmt.Sprintf("moved=%d err=%v", n, err)
	case opStepPartial:
		return fmt.Sprintf("err=%v", m.StepPartial(o.p))
	case opRecover:
		b, a, err := m.Recover()
		return fmt.Sprintf("bDel=%d aDel=%d err=%v", b, a, err)
	default:
		return fmt.Sprintf("err=%v", m.Finish())
	}
}

func applyModel(mod *model, o op) string {
	switch o.kind {
	case opPut:
		return fmt.Sprintf("err=%v", mod.put(o.k, o.v))
	case opGet:
		v, ok, err := mod.get(o.k)
		return fmt.Sprintf("val=%d ok=%v err=%v", v, ok, err)
	case opDelete:
		ok, err := mod.del(o.k)
		return fmt.Sprintf("ok=%v err=%v", ok, err)
	case opScan:
		ents, err := mod.scan(o.lo, o.hi)
		return fmt.Sprintf("ents=%v err=%v", ents, err)
	case opStep:
		n, err := mod.step(o.n)
		return fmt.Sprintf("moved=%d err=%v", n, err)
	case opStepPartial:
		return fmt.Sprintf("err=%v", mod.stepPartial(o.p))
	case opRecover:
		b, a, err := mod.recover()
		return fmt.Sprintf("bDel=%d aDel=%d err=%v", b, a, err)
	default:
		return fmt.Sprintf("err=%v", mod.finish())
	}
}

// why 依据操作前的模型状态给出判定依据。
func why(mod *model, o op) string {
	switch o.kind {
	case opPut:
		if o.k < MinKey || o.k > MaxKey {
			return "参数非法：k 越界"
		}
		if mod.crashed {
			return "崩溃态拒绝"
		}
		if o.k < mod.w {
			return "k<w，路由 B"
		}
		return "k>=w，路由 A"
	case opGet:
		if o.k < MinKey || o.k > MaxKey {
			return "参数非法：k 越界"
		}
		if o.k < mod.w {
			return "k<w，读 B（崩溃态仍可读）"
		}
		return "k>=w，读 A（崩溃态仍可读）"
	case opDelete:
		if o.k < MinKey || o.k > MaxKey {
			return "参数非法：k 越界"
		}
		if mod.crashed {
			return "崩溃态拒绝"
		}
		if o.k < mod.w {
			return "k<w，删 B"
		}
		return "k>=w，删 A"
	case opScan:
		if o.lo < MinKey || o.hi > MaxKey+1 || o.lo > o.hi {
			return "参数非法：lo/hi 越界或 lo>hi"
		}
		return fmt.Sprintf("B 段 [%d,%d) + A 段 [%d,%d)", o.lo, min(o.hi, mod.w), max(o.lo, mod.w), o.hi)
	case opStep:
		if o.n < 1 || o.n > 10_000 {
			return "参数非法：n 越界"
		}
		if mod.crashed {
			return "崩溃态拒绝"
		}
		return "按逻辑升序搬迁 A 的最小键"
	case opStepPartial:
		if o.p != 1 && o.p != 2 {
			return "参数非法：p 越界"
		}
		if mod.crashed {
			return "崩溃态拒绝"
		}
		if len(mod.a) == 0 {
			return "状态拒绝：ErrDrained"
		}
		return fmt.Sprintf("对逻辑最小键做前 %d 步后崩溃", o.p)
	case opRecover:
		if !mod.crashed {
			return "状态拒绝：ErrNotCrashed"
		}
		return "删 B 中 >=w 与 A 中 <w 的残留"
	default:
		if mod.crashed {
			return "崩溃态拒绝"
		}
		if len(mod.a) > 0 {
			return "状态拒绝：ErrNotDrained"
		}
		return "A 已空，w 置为 MaxKey+1"
	}
}

// 与朴素模拟对拍 2000 组随机序列（含随机崩溃点）；
// 日志打印输入、输出与判定依据；双实例重放验证确定性。
func TestDifferentialRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20261004))
	for seq := 0; seq < 2000; seq++ {
		m1, m2 := New(), New()
		mod := newModel()
		nOps := 5 + rng.Intn(56)
		for i := 0; i < nOps; i++ {
			o := genOp(rng)
			reason := why(mod, o)
			out1 := applyMig(m1, o)
			out2 := applyMig(m2, o)
			outM := applyModel(mod, o)
			t.Logf("seq=%d op=%d in=%s out=%s why=%s", seq, i, o.String(), out1, reason)
			if out1 != out2 {
				t.Fatalf("seq=%d op=%d in=%s：重放不一致 %q vs %q", seq, i, o.String(), out1, out2)
			}
			if out1 != outM {
				t.Fatalf("seq=%d op=%d in=%s：与模型不一致 mig=%q model=%q why=%s", seq, i, o.String(), out1, outM, reason)
			}
			if m1.w != mod.w || m1.crashed != mod.crashed || m1.a.Len() != len(mod.a) || m1.b.Len() != len(mod.b) {
				t.Fatalf("seq=%d op=%d in=%s：内部状态不一致 w=%d/%d crashed=%v/%v a=%d/%d b=%d/%d",
					seq, i, o.String(), m1.w, mod.w, m1.crashed, mod.crashed, m1.a.Len(), len(mod.a), m1.b.Len(), len(mod.b))
			}
		}
		ents, err := m1.Scan(MinKey, MaxKey+1)
		if err != nil {
			t.Fatalf("seq=%d 最终全扫描 err=%v", seq, err)
		}
		entsM, errM := mod.scan(MinKey, MaxKey+1)
		if errM != nil || fmt.Sprint(ents) != fmt.Sprint(entsM) {
			t.Fatalf("seq=%d 最终全扫描不一致 mig=%v model=%v", seq, ents, entsM)
		}
		for _, e := range ents {
			v, ok, _ := m1.Get(e.Key)
			if !ok || v != e.Val {
				t.Fatalf("seq=%d Get(%d)=(%v,%v) 与 Scan 值 %v 不一致", seq, e.Key, v, ok, e.Val)
			}
		}
		var b strings.Builder
		for _, e := range ents {
			fmt.Fprintf(&b, "%d:%d ", e.Key, e.Val)
		}
		t.Logf("seq=%d final w=%d crashed=%v entries=%s", seq, m1.w, m1.crashed, b.String())
	}
}

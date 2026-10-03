package mig

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ontology/keyenc"
)

// op is one recorded action; Log is the "input/output" audit trail.
type op struct {
	name string
	k    int64
	v    int64
	n    int64
	loHi [2]int64
	log  string
}

func sameErr(a, b error) bool {
	return errors.Is(a, b) || errors.Is(b, a)
}

func sameKVs(a, b []KV) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// pickKey stresses sign boundaries and the current watermark neighborhood.
func pickKey(rng *rand.Rand, w int64) int64 {
	switch rng.Intn(6) {
	case 0:
		return -3 + rng.Int63n(7)
	case 1:
		return keyenc.MinKey + rng.Int63n(6)
	case 2:
		return keyenc.MaxKey - rng.Int63n(6)
	case 3:
		d := int64(rng.Intn(5)) - 2
		k := w + d
		if k < keyenc.MinKey {
			k = keyenc.MinKey
		}
		if k > keyenc.MaxKey {
			k = keyenc.MaxKey
		}
		return k
	default:
		return rng.Int63n(21) - 10
	}
}

func runRandomSequence(t *testing.T, seed int64, length int) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	h := NewHybrid()
	m := newModel()
	ops := make([]op, 0, length)

	var lastStepMoved, lastStepProbed int

	failf := func(format string, args ...any) {
		var sb strings.Builder
		for _, o := range ops {
			sb.WriteString("  " + o.log + "\n")
		}
		t.Fatalf("seed=%d 对拍失败: %s\n操作日志(输入/输出/判定):\n%s",
			seed, fmt.Sprintf(format, args...), sb.String())
	}

	for i := 0; i < length; i++ {
		k := pickKey(rng, m.w)
		v := rng.Int63n(2_000_000_001) - 1_000_000_000
		o := op{k: k, v: v}

		// Inject some deliberately invalid arguments (~8%).
		bad := rng.Intn(12) == 0
		switch {
		case bad && rng.Intn(3) == 0:
			o.k = keyenc.MaxKey + 1 + rng.Int63n(3)
			bad = true
		case bad:
			v = 1_000_000_000 + 1 + rng.Int63n(5)
		}

		switch rng.Intn(10) {
		case 0, 1, 2: // Put
			o.name = "Put"
			he := h.Put(k, v)
			me := m.put(k, v)
			o.log = fmt.Sprintf("Put(k=%d,v=%d) -> err=%v", k, v, he)
			if !sameErr(he, me) {
				ops = append(ops, o)
				failf("Put err hybrid=%v model=%v", he, me)
			}
		case 3: // Delete
			o.name = "Delete"
			hb, he := h.Delete(k)
			mb, me := m.del(k)
			o.log = fmt.Sprintf("Delete(k=%d) -> (%v,%v)", k, hb, he)
			if !sameErr(he, me) || hb != mb {
				ops = append(ops, o)
				failf("Delete hybrid=(%v,%v) model=(%v,%v)", hb, he, mb, me)
			}
		case 4: // Get
			o.name = "Get"
			hv, hok, he := h.Get(k)
			mv, mok, me := m.get(k)
			o.log = fmt.Sprintf("Get(k=%d) -> (%d,%v,%v)", k, hv, hok, he)
			if !sameErr(he, me) || hv != mv || hok != mok {
				ops = append(ops, o)
				failf("Get hybrid=(%d,%v,%v) model=(%d,%v,%v)", hv, hok, he, mv, mok, me)
			}
		case 5: // Step (favor small n, occasionally max)
			n := 1 + rng.Intn(6)
			if rng.Intn(10) == 0 {
				n = 10_000
			}
			if bad {
				n = 10_001
			}
			o.name = "Step"
			hm, he := h.Step(n)
			mm, me := m.step(n)
			lastStepMoved, lastStepProbed, _ = h.countersStep()
			o.log = fmt.Sprintf("Step(n=%d) -> moved=%d err=%v [探测=%d, 判定:%s]",
				n, hm, he, lastStepProbed, probeVerdict(lastStepMoved, lastStepProbed))
			if !sameErr(he, me) || hm != mm {
				ops = append(ops, o)
				failf("Step hybrid=(%d,%v) model=(%d,%v)", hm, he, mm, me)
			}
			if hm > 0 && lastStepProbed > 2*hm {
				ops = append(ops, o)
				failf("Step probe budget violated: moved=%d probed=%d", hm, lastStepProbed)
			}
		case 6: // StepPartial: random crash point
			p := 1 + rng.Intn(2)
			if bad {
				p = 3
			}
			o.name = "StepPartial"
			he := h.StepPartial(p)
			me := m.stepPartial(p)
			o.log = fmt.Sprintf("StepPartial(p=%d) -> err=%v crashed=%v w=%d",
				p, he, h.Crashed(), h.Watermark())
			if !sameErr(he, me) || h.Crashed() != m.crashed || h.Watermark() != m.w {
				ops = append(ops, o)
				failf("StepPartial hybrid=(%v,c=%v,w=%d) model=(%v,c=%v,w=%d)",
					he, h.Crashed(), h.Watermark(), me, m.crashed, m.w)
			}
		case 7: // Recover
			o.name = "Recover"
			hb, ha, he := h.Recover()
			mb2, ma2, me := m.recover()
			o.log = fmt.Sprintf("Recover() -> (B删=%d,A删=%d,err=%v)", hb, ha, he)
			if !sameErr(he, me) || h.Crashed() != m.crashed || h.Watermark() != m.w ||
				(he == nil && (hb != mb2 || ha != ma2)) {
				ops = append(ops, o)
				failf("Recover hybrid=(%d,%d,%v,c=%v,w=%d) model=(%d,%d,%v)",
					hb, ha, he, h.Crashed(), h.Watermark(), mb2, ma2, me)
			}
			if he == nil {
				mb, ma, _, _ := h.snapshotCounts()
				o.log += fmt.Sprintf("; 恢复后残留判定 B(>=w)=%d A(<w)=%d (须均为0)", mb, ma)
				if mb != 0 || ma != 0 {
					ops = append(ops, o)
					failf("residue after Recover: B=%d A=%d", mb, ma)
				}
			}
		case 8: // Finish
			o.name = "Finish"
			he := h.Finish()
			me := m.finish()
			o.log = fmt.Sprintf("Finish() -> err=%v w=%d", he, h.Watermark())
			if !sameErr(he, me) || h.Watermark() != m.w {
				ops = append(ops, o)
				failf("Finish hybrid=(%v,w=%d) model=(%v,w=%d)", he, h.Watermark(), me, m.w)
			}
		default: // Scan, including boundary lo/hi around 0 and w
			var lo, hi int64
			if rng.Intn(3) == 0 {
				bounds := []int64{keyenc.MinKey, m.w, m.w - 1, -1, 0, 1, keyenc.MaxKey + 1}
				lo = bounds[rng.Intn(len(bounds))]
				hi = bounds[rng.Intn(len(bounds))]
			} else {
				lo = keyenc.MinKey + rng.Int63n(21) - 10
				hi = keyenc.MinKey + rng.Int63n(21) - 10
				if lo < keyenc.MinKey {
					lo = keyenc.MinKey
				}
				if hi < keyenc.MinKey {
					hi = keyenc.MinKey
				}
			}
			if bad {
				lo = keyenc.MinKey - 1
			}
			o.loHi = [2]int64{lo, hi}
			hs, he := h.Scan(lo, hi)
			ms, me := m.scan(lo, hi)
			phys, logicalTotal, lastLogical, _, _ := h.counters()
			verdict := "物理产出==返回条数"
			if phys != logicalTotal {
				verdict = fmt.Sprintf("违反: 物理%d != 逻辑%d", phys, logicalTotal)
			}
			o.log = fmt.Sprintf("Scan(lo=%d,hi=%d) -> %d 条=%v err=%v [%s]",
				lo, hi, lastLogical, hs, he, verdict)
			if !sameErr(he, me) || !sameKVs(hs, ms) {
				ops = append(ops, o)
				failf("Scan hybrid=%v,%v\n model=%v,%v", hs, he, ms, me)
			}
			if phys != logicalTotal {
				ops = append(ops, o)
				failf("physical output count %d != logical %d", phys, logicalTotal)
			}
		}
		ops = append(ops, o)

		// Periodic full-view equivalence check (Scan vs per-key Get too).
		if i%11 == 10 {
			full, _ := h.Scan(keyenc.MinKey, keyenc.MaxKey+1)
			mfull, _ := m.scan(keyenc.MinKey, keyenc.MaxKey+1)
			if !sameKVs(full, mfull) || !reflect.DeepEqual(scanMap(full), m.data) {
				failf("full snapshot hybrid=%v model=%v", full, mfull)
			}
			for k, v := range m.data {
				gv, ok, _ := h.Get(k)
				if !ok || gv != v {
					failf("Scan/Get disagreement at k=%d: %d,%v vs %d", k, gv, ok, v)
				}
			}
		}
	}

	// Final full-view equivalence.
	full, _ := h.Scan(keyenc.MinKey, keyenc.MaxKey+1)
	mfull, _ := m.scan(keyenc.MinKey, keyenc.MaxKey+1)
	if !sameKVs(full, mfull) {
		failf("final snapshot hybrid=%v model=%v", full, mfull)
	}
}

func TestRandomSequencesAgainstNaiveModel(t *testing.T) {
	const sequences = 2000
	for s := 0; s < sequences; s++ {
		seed := int64(1_000_000 + s*7919)
		t.Run(fmt.Sprintf("seq%d", s), func(t *testing.T) {
			runRandomSequence(t, seed, 60)
		})
	}
}

func TestConcurrentOperationsSerializable(t *testing.T) {
	h := NewHybrid()
	for _, k := range []int64{-5, -1, 0, 3, 7} {
		if err := h.Put(k, k); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 200; i++ {
				k := rng.Int63n(21) - 10
				switch rng.Intn(4) {
				case 0:
					_ = h.Put(k, rng.Int63n(100))
				case 1:
					_, _, _ = h.Get(k)
				case 2:
					_, _ = h.Scan(-10, 11)
				case 3:
					_, _ = h.Step(1 + rng.Intn(3))
				}
			}
		}(g)
	}
	wg.Wait()
	full, err := h.Scan(keyenc.MinKey, keyenc.MaxKey+1)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(full); i++ {
		if full[i-1].Key >= full[i].Key {
			t.Fatalf("scan not strictly ascending: %v", full)
		}
	}
	phys, logical, _, _, _ := h.counters()
	if phys != logical {
		t.Fatalf("physical %d != logical %d", phys, logical)
	}
}

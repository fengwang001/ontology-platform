package gengc

import (
	"fmt"
	"math/rand"
	"testing"
)

// effState / naiveState 是差分对照用的完整状态指纹：
// 存活集合、每对象所在区/年龄/晋升次数/字段、根集合、记忆集、全部计数器。
type effState struct {
	objects map[uint64]string
	roots   map[uint64]struct{}
	rs      map[uint64]struct{}
	stats   Stats
}

func fingerprintEff(h *Heap) effState {
	s := effState{
		objects: make(map[uint64]string),
		roots:   make(map[uint64]struct{}),
		rs:      make(map[uint64]struct{}),
		stats:   h.Stats(),
	}
	for id := uint64(1); id < h.nextID; id++ {
		if o, ok := h.Object(id); ok {
			s.objects[id] = fmt.Sprintf("%s age=%d prom=%d fields=%v",
				o.Gen(), o.YoungGCs(), o.Promotions(), o.fields)
		}
	}
	for id := range h.roots {
		s.roots[id] = struct{}{}
	}
	for id := range h.rs.members {
		s.rs[id] = struct{}{}
	}
	return s
}

func fingerprintNaive(n *naiveHeap) effState {
	s := effState{
		objects: make(map[uint64]string),
		roots:   make(map[uint64]struct{}),
		rs:      make(map[uint64]struct{}),
		stats: Stats{
			YoungObjects:   n.youngCount(),
			OldObjects:     n.oldCount(),
			Promotions:     n.promotions,
			YoungGCs:       n.youngGCs,
			OldGCs:         n.oldGCs,
			RememberedSize: len(n.rs),
			BarrierEntries: n.barrierEntries,
		},
	}
	for id, o := range n.objects {
		s.objects[id] = fmt.Sprintf("%s age=%d prom=%d fields=%v",
			o.gen, o.youngGCs, o.promotions, o.fields)
	}
	for id := range n.roots {
		s.roots[id] = struct{}{}
	}
	for id := range n.rs {
		s.rs[id] = struct{}{}
	}
	return s
}

func sameSet(a, b map[uint64]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func (s effState) equal(t effState) (bool, string) {
	if len(s.objects) != len(t.objects) {
		return false, fmt.Sprintf("object count %d vs %d", len(s.objects), len(t.objects))
	}
	for id, sv := range s.objects {
		if tv, ok := t.objects[id]; !ok {
			return false, fmt.Sprintf("object %d missing in naive", id)
		} else if sv != tv {
			return false, fmt.Sprintf("object %d: eff(%s) != naive(%s)", id, sv, tv)
		}
	}
	if !sameSet(s.roots, t.roots) {
		return false, "root sets differ"
	}
	if !sameSet(s.rs, t.rs) {
		return false, fmt.Sprintf("remembered sets differ: eff=%v naive=%v", s.rs, t.rs)
	}
	if s.stats != t.stats {
		return false, fmt.Sprintf("stats eff=%+v naive=%+v", s.stats, t.stats)
	}
	return true, ""
}

// TestRandomDifferential 对随机操作序列做实现与朴素模型的差分：
// 每步打印输入、双方实际输出与判定依据；每次回收后比较存活集合与所在区。
func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			cfg := Config{
				YoungCapacity:    1 + rng.Intn(6),
				OldCapacity:      1 + rng.Intn(6),
				PromoteThreshold: 1 + rng.Intn(3),
			}
			eff := New(cfg)
			nai := newNaive(cfg)

			t.Logf("config: youngCap=%d oldCap=%d threshold=%d",
				cfg.YoungCapacity, cfg.OldCapacity, cfg.PromoteThreshold)

			for step := 0; step < 300; step++ {
				op := rng.Intn(10)
				var input string
				var effOut, naiOut string
				wasGC := false

				switch op {
				case 0, 1, 2: // 分配
					nf := rng.Intn(4)
					input = fmt.Sprintf("alloc(%d)", nf)
					id1, e1 := eff.Allocate(nf)
					id2, e2 := nai.allocate(nf)
					effOut = fmt.Sprintf("id=%d err=%v", id1, e1)
					naiOut = fmt.Sprintf("id=%d err=%v", id2, e2)
					if (e1 == nil) != (e2 == nil) || errKind(e1) != errKind(e2) ||
						(e1 == nil && id1 != id2) {
						t.Fatalf("step %d %s: eff[%s] naive[%s]", step, input, effOut, naiOut)
					}
				case 3, 4, 5: // 写引用（含越界、悬垂、未定义、清空）
					maxID := eff.nextID
					if maxID < 2 {
						maxID = 2
					}
					src := uint64(1 + rng.Intn(int(maxID)+2))
					field := rng.Intn(6) - 1 // -1..4，可能越界
					dst := uint64(0)
					switch rng.Intn(3) {
					case 0:
						dst = 0
					case 1:
						dst = uint64(1 + rng.Intn(int(maxID)+2))
					}
					input = fmt.Sprintf("set(%d,%d,%d)", src, field, dst)
					e1 := eff.SetField(src, field, dst)
					e2 := nai.setField(src, field, dst)
					effOut = fmt.Sprintf("err=%v", e1)
					naiOut = fmt.Sprintf("err=%v", e2)
					if errKind(e1) != errKind(e2) {
						t.Fatalf("step %d %s: eff[%s] naive[%s]", step, input, effOut, naiOut)
					}
				case 6: // 读
					src := uint64(1 + rng.Intn(int(eff.nextID)+2))
					field := rng.Intn(5) - 1
					input = fmt.Sprintf("get(%d,%d)", src, field)
					v1, e1 := eff.GetField(src, field)
					v2, e2 := nai.getField(src, field)
					effOut = fmt.Sprintf("val=%d err=%v", v1, e1)
					naiOut = fmt.Sprintf("val=%d err=%v", v2, e2)
					if v1 != v2 || errKind(e1) != errKind(e2) {
						t.Fatalf("step %d %s: eff[%s] naive[%s]", step, input, effOut, naiOut)
					}
				case 7: // 根增删
					id := uint64(1 + rng.Intn(int(eff.nextID)+1))
					if rng.Intn(2) == 0 {
						input = fmt.Sprintf("addRoot(%d)", id)
						e1 := eff.AddRoot(id)
						e2 := nai.addRoot(id)
						effOut = fmt.Sprintf("err=%v", e1)
						naiOut = fmt.Sprintf("err=%v", e2)
						if errKind(e1) != errKind(e2) {
							t.Fatalf("step %d %s: eff[%s] naive[%s]", step, input, effOut, naiOut)
						}
					} else {
						input = fmt.Sprintf("removeRoot(%d)", id)
						eff.RemoveRoot(id)
						nai.removeRoot(id)
						effOut, naiOut = "ok", "ok"
					}
				case 8: // 年轻区回收
					input = "collectYoung"
					eff.CollectYoung()
					nai.collectYoung()
					effOut, naiOut = "gc", "gc"
					wasGC = true
				case 9: // 年老区回收（仅显式）
					input = "collectOld"
					eff.CollectOld()
					nai.collectOld()
					effOut, naiOut = "gc", "gc"
					wasGC = true
				}

				fs, ns := fingerprintEff(eff), fingerprintNaive(nai)
				if ok, why := fs.equal(ns); !ok {
					t.Fatalf("step %d %s: state mismatch: %s\neff=%+v\nnaive=%+v",
						step, input, why, fs, ns)
				}
				basis := "states equal (objects/generations/ages/promotions/roots/rs/stats)"
				if wasGC {
					basis = "post-GC live sets, generations, RS rebuilt and stats equal naive full-scan model"
				}
				t.Logf("step %3d | %-18s | eff: %-22s | naive: %-22s | %s",
					step, input, effOut, naiOut, basis)
			}
		})
	}
}

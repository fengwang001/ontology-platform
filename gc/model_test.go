package gc

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// naiveModel 是独立的朴素参照实现：不用记忆集，每次年轻区回收都从根集合
// 出发扫描全部对象（含年老区）判定年轻对象存活；晋升与回收规则与 Runtime 一致。
type naiveObject struct {
	gen    generation
	fields []ObjID
	age    int
	alive  bool
}

type naiveModel struct {
	objs      map[ObjID]*naiveObject
	nextID    ObjID
	roots     map[ObjID]struct{}
	youngN    int
	oldN      int
	youngCap  int
	oldCap    int
	threshold int
}

func newNaive(youngCap, oldCap, threshold int) *naiveModel {
	return &naiveModel{
		objs:      make(map[ObjID]*naiveObject),
		roots:     make(map[ObjID]struct{}),
		youngCap:  youngCap,
		oldCap:    oldCap,
		threshold: threshold,
	}
}

func (m *naiveModel) alloc(numFields int) (ObjID, error) {
	if numFields < 0 {
		return NilObj, ErrBadArgument
	}
	if m.youngN >= m.youngCap {
		m.minorGC()
		if m.youngN >= m.youngCap {
			return NilObj, ErrOutOfMemory
		}
	}
	m.nextID++
	m.objs[m.nextID] = &naiveObject{gen: genYoung, fields: make([]ObjID, numFields), alive: true}
	m.youngN++
	return m.nextID, nil
}

func (m *naiveModel) write(srcID ObjID, field int, dstID ObjID) error {
	src, ok := m.objs[srcID]
	if !ok {
		return ErrUndefined
	}
	var dst *naiveObject
	if dstID != NilObj {
		dst, ok = m.objs[dstID]
		if !ok {
			return ErrUndefined
		}
	}
	if field < 0 || field >= len(src.fields) {
		return ErrBadArgument
	}
	if !src.alive || (dst != nil && !dst.alive) {
		return ErrDangling
	}
	src.fields[field] = dstID
	return nil
}

func (m *naiveModel) addRoot(id ObjID) error {
	o, ok := m.objs[id]
	if !ok {
		return ErrUndefined
	}
	if !o.alive {
		return ErrDangling
	}
	m.roots[id] = struct{}{}
	return nil
}

func (m *naiveModel) removeRoot(id ObjID) error {
	if _, ok := m.objs[id]; !ok {
		return ErrUndefined
	}
	delete(m.roots, id)
	return nil
}

func (m *naiveModel) markFromRoots() map[ObjID]struct{} {
	marked := make(map[ObjID]struct{})
	var mark func(id ObjID)
	mark = func(id ObjID) {
		o, ok := m.objs[id]
		if !ok || !o.alive {
			return
		}
		if _, dup := marked[id]; dup {
			return
		}
		marked[id] = struct{}{}
		for _, f := range o.fields {
			mark(f)
		}
	}
	for id := range m.roots {
		mark(id)
	}
	return marked
}

// minorGC 朴素地扫描全部对象：年轻对象只要从根可达（哪怕途经年老对象）即存活。
func (m *naiveModel) minorGC() {
	marked := m.markFromRoots()
	var youngs []ObjID
	for id, o := range m.objs {
		if o.alive && o.gen == genYoung {
			youngs = append(youngs, id)
		}
	}
	sort.Slice(youngs, func(i, j int) bool { return youngs[i] < youngs[j] })
	for _, id := range youngs {
		o := m.objs[id]
		if _, live := marked[id]; !live {
			o.alive = false
			m.youngN--
			continue
		}
		o.age++
		if o.age >= m.threshold {
			if m.oldN < m.oldCap {
				o.gen = genOld
				m.youngN--
				m.oldN++
			} else {
				o.age = m.threshold
			}
		}
	}
}

func (m *naiveModel) majorGC() {
	marked := m.markFromRoots()
	for id, o := range m.objs {
		if !o.alive || o.gen != genOld {
			continue
		}
		if _, live := marked[id]; !live {
			o.alive = false
			m.oldN--
		}
	}
}

// rationale 给出一条操作的判定依据，用于日志。
func rationale(err error, accepted string) string {
	switch {
	case err == nil:
		return "accepted: " + accepted
	case err == ErrUndefined:
		return "rejected: undefined object (priority: undefined > argument > dangling > oom)"
	case err == ErrBadArgument:
		return "rejected: field index out of range (priority: undefined > argument > dangling > oom)"
	case err == ErrDangling:
		return "rejected: dangling reference to collected object (priority: undefined > argument > dangling > oom)"
	case err == ErrOutOfMemory:
		return "rejected: young space still full after auto minor GC"
	default:
		return "unexpected error"
	}
}

// compareState 在每次回收后比较两个实现的存活集合与各对象所在区。
func compareState(t *testing.T, step int, rt *Runtime, m *naiveModel, maxID ObjID) {
	t.Helper()
	for id := ObjID(1); id <= maxID; id++ {
		ro, rok := rt.objs[id]
		no, nok := m.objs[id]
		if rok != nok {
			t.Fatalf("step %d: object %d existence diverges: runtime=%v naive=%v", step, id, rok, nok)
		}
		if !rok {
			continue
		}
		if ro.alive != no.alive {
			t.Fatalf("step %d: object %d liveness diverges: runtime=%v naive=%v", step, id, ro.alive, no.alive)
		}
		if ro.alive && ro.gen != no.gen {
			t.Fatalf("step %d: object %d generation diverges: runtime=%v naive=%v", step, id, ro.gen, no.gen)
		}
	}
	if rt.youngN != m.youngN || rt.oldN != m.oldN {
		t.Fatalf("step %d: space usage diverges: runtime=(%d,%d) naive=(%d,%d)",
			step, rt.youngN, rt.oldN, m.youngN, m.oldN)
	}
}

// TestRandomizedAgainstNaiveModel 对随机操作序列逐步对照记忆集实现与朴素模型，
// 日志打印每条输入、实际输出与判定依据。
func TestRandomizedAgainstNaiveModel(t *testing.T) {
	for _, seed := range []int64{7, 42, 2026} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			rt := New(24, 24, 3)
			naive := newNaive(24, 24, 3)
			var maxID ObjID

			pick := func() ObjID {
				return ObjID(rng.Intn(int(maxID) + 3)) // 含 NilObj 与未定义 id
			}

			for step := 0; step < 600; step++ {
				switch op := rng.Intn(100); {
				case op < 28: // 分配
					nf := rng.Intn(4)
					idR, errR := rt.Alloc(nf)
					idN, errN := naive.alloc(nf)
					t.Logf("step %03d Alloc(fields=%d) -> id=%d err=%v | %s", step, nf, idR, errR,
						rationale(errR, "object allocated in young space"))
					if errR != errN || idR != idN {
						t.Fatalf("step %d: alloc diverges: runtime=(%d,%v) naive=(%d,%v)", step, idR, errR, idN, errN)
					}
					if errR == nil && idR > maxID {
						maxID = idR
					}
				case op < 55: // 写引用
					src, dst := pick(), pick()
					field := rng.Intn(5) - 1 // 覆盖越界字段
					errR := rt.Write(src, field, dst)
					errN := naive.write(src, field, dst)
					t.Logf("step %03d Write(src=%d, field=%d, dst=%d) -> %v | %s", step, src, field, dst, errR,
						rationale(errR, "field stored; old->young write registers source in remembered set atomically"))
					if errR != errN {
						t.Fatalf("step %d: write diverges: runtime=%v naive=%v", step, errR, errN)
					}
				case op < 65: // 读引用
					src, field := pick(), rng.Intn(5)-1
					vR, errR := rt.Read(src, field)
					t.Logf("step %03d Read(src=%d, field=%d) -> (%d, %v) | %s", step, src, field, vR, errR,
						rationale(errR, "field value returned"))
					_ = vR
				case op < 78: // 加根
					id := pick()
					errR := rt.AddRoot(id)
					errN := naive.addRoot(id)
					t.Logf("step %03d AddRoot(%d) -> %v | %s", step, id, errR,
						rationale(errR, "object added to root set"))
					if errR != errN {
						t.Fatalf("step %d: addroot diverges: runtime=%v naive=%v", step, errR, errN)
					}
				case op < 86: // 删根
					id := pick()
					errR := rt.RemoveRoot(id)
					errN := naive.removeRoot(id)
					t.Logf("step %03d RemoveRoot(%d) -> %v | %s", step, id, errR,
						rationale(errR, "object removed from root set; liveness decided at next GC"))
					if errR != errN {
						t.Fatalf("step %d: removeroot diverges: runtime=%v naive=%v", step, errR, errN)
					}
				case op < 97: // 年轻区回收
					rt.MinorGC()
					naive.minorGC()
					s := rt.Stats()
					t.Logf("step %03d MinorGC -> young=%d old=%d remset=%d promotions=%d | roots+remembered-set mark, sweep young, promote at age>=%d",
						step, s.YoungObjects, s.OldObjects, s.RememberedSize, s.Promotions, 3)
					compareState(t, step, rt, naive, maxID)
				default: // 年老区回收
					rt.MajorGC()
					naive.majorGC()
					s := rt.Stats()
					t.Logf("step %03d MajorGC -> young=%d old=%d remset=%d | full-heap mark from roots, sweep old only",
						step, s.YoungObjects, s.OldObjects, s.RememberedSize)
					compareState(t, step, rt, naive, maxID)
				}
			}
		})
	}
}

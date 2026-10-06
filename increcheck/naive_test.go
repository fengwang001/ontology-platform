package increcheck

import (
	"math/rand"
	"sort"
	"testing"
)

// naive_model_test.go：独立朴素模型。
// 每次编辑后丢弃全部缓存，按固定点迭代对所有现存声明做全量重检，
// 得到每个声明签名/实现检查的确定结果，作为增量调度的对照基准。

type naiveModel struct {
	src map[string]Declaration
}

func newNaive() *naiveModel { return &naiveModel{src: map[string]Declaration{}} }

func (m *naiveModel) add(id string, d Declaration) { m.src[id] = d }
func (m *naiveModel) sig(id string) (string, bool) {
	d, ok := m.src[id]
	return d.SigText, ok
}

// sigStatus 以带记忆化的 DFS 求错误态：
// 不存在 -> 错误；当前 DFS 栈上重新遇到（循环）-> 先假定无错误，
// 若循环成员另有错误路径则沿「污染」第二次扫描修正。
// 为简单确定，直接迭代错误集合到固定点，初始集合含所有
// 「直接引用不存在标识」的声明，再沿边传播。
func (m *naiveModel) sigStatus(id string) (bool, bool) {
	if _, ok := m.src[id]; !ok {
		return false, true
	}
	bad := map[string]bool{}
	for id, d := range m.src {
		for _, ref := range parseRefs(d.SigText) {
			if _, ok := m.src[ref]; !ok {
				bad[id] = true
				break
			}
		}
	}
	for {
		next := map[string]bool{}
		for k := range bad {
			next[k] = true
		}
		for id, d := range m.src {
			for _, ref := range parseRefs(d.SigText) {
				if bad[ref] {
					next[id] = true
					break
				}
			}
		}
		if mapEqual(next, bad) {
			bad = next
			break
		}
		bad = next
	}
	return true, bad[id]
}

func (m *naiveModel) implStatus(id string) bool {
	d, ok := m.src[id]
	if !ok {
		return true
	}
	_, sigErr := m.sigStatus(id)
	if sigErr {
		return true
	}
	for _, ref := range parseRefs(d.ImplText) {
		if ref == id {
			continue
		}
		_, e := m.sigStatus(ref)
		if e {
			return true
		}
	}
	return false
}

func mapEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// randomOp 是一次随机编辑。
type randomOp struct {
	kind string // add editSig editImpl delete
	id   string
	sig  string
	impl string
}

func TestNaiveModelEquivalence(t *testing.T) {
	const iterations = 400
	const opsPer = 60
	rng := rand.New(rand.NewSource(20261006))
	ids := []string{"a", "b", "c", "d", "e"}
	sigPool := []string{"", "$a", "$b", "$c", "$a $b", "$b $d", "$d", "e($e)", "$x"}
	implPool := []string{"body", "$a", "$c", "$d $e", "$b", "$a $e"}

	for it := 0; it < iterations; it++ {
		s := NewScheduler()
		m := newNaive()
		exists := map[string]bool{}
		for _, id := range ids {
			d := Declaration{SigText: pick(rng, sigPool), ImplText: pick(rng, implPool)}
			_ = s.Add(id, d)
			m.add(id, d)
			exists[id] = true
		}
		for step := 0; step < opsPer; step++ {
			var lastOp randomOp
			op := randomOp{
				id:   pick(rng, ids),
				sig:  pick(rng, sigPool),
				impl: pick(rng, implPool),
			}
			op.kind = pickKind(rng, exists[op.id])
			lastOp = op
			applyNaiveOp(m, exists, op)
			applySchedOp(s, op) // 空操作/不存在等错误两边都允许

			// 每个编辑后比较所有标识的最终检查结果。
			all := append([]string{}, ids...)
			sort.Strings(all)
			for _, id := range all {
				se := s.SigResult(id)
				present, sigErr := m.sigStatus(id)
				if se.Result.Present != present {
					t.Fatalf("iter=%d step=%d id=%s present inc=%v naive=%v",
						it, step, id, se.Result.Present, present)
				}
				if !present {
					// 不存在的声明：增量模型结果 absent 且无错误码，
					// 朴素模型报未定义；两者对「引用它」的下游效果一致，
					// 这里只要求版本已单调推进（墓碑）。
					if se.Result.Err != ErrNone {
						t.Fatalf("iter=%d step=%d id=%s absent entry must carry no error", it, step, id)
					}
					continue
				}
				if present {
					d := m.src[id]
					if se.Result.Text != d.SigText {
						t.Fatalf("iter=%d step=%d id=%s text inc=%q naive=%q",
							it, step, id, se.Result.Text, d.SigText)
					}
				}
				if (se.Result.Err != ErrNone) != sigErr {
					t.Fatalf("iter=%d step=%d id=%s sigErr inc=%v(%v) naive=%v op=%+v cExists=%v",
						it, step, id, se.Result.Err != ErrNone, se.Result, sigErr, lastOp, exists)
				}
				ie := s.ImplResult(id)
				implErr := m.implStatus(id)
				if (ie.Result.Err != ErrNone) != implErr {
					t.Fatalf("iter=%d step=%d id=%s implErr inc=%v naive=%v",
						it, step, id, ie.Result.Err != ErrNone, implErr)
				}
				// 结果与依据必须来自同一瞬间：依据版本必须与当前登记一致。
				for dep, ver := range ie.Basis {
					if s.reg.sigVersion(dep) != ver {
						t.Fatalf("iter=%d step=%d impl basis mismatch %s:%d", it, step, dep, ver)
					}
				}
				for dep, ver := range se.Basis {
					if s.reg.sigVersion(dep) != ver {
						t.Fatalf("iter=%d step=%d sig basis mismatch %s:%d", it, step, dep, ver)
					}
				}
			}
		}
	}
}

func pick(rng *rand.Rand, pool []string) string { return pool[rng.Intn(len(pool))] }

func pickKind(rng *rand.Rand, exists bool) string {
	if !exists {
		return "add"
	}
	return []string{"add", "editSig", "editImpl", "delete"}[rng.Intn(4)]
}

func applyNaiveOp(m *naiveModel, exists map[string]bool, op randomOp) {
	switch op.kind {
	case "add":
		if exists[op.id] {
			return
		}
		m.add(op.id, Declaration{SigText: op.sig, ImplText: op.impl})
		exists[op.id] = true
	case "editSig":
		if d, ok := m.src[op.id]; ok && d.SigText != op.sig {
			d.SigText = op.sig
			m.src[op.id] = d
		}
	case "editImpl":
		if d, ok := m.src[op.id]; ok && d.ImplText != op.impl {
			d.ImplText = op.impl
			m.src[op.id] = d
		}
	case "delete":
		if exists[op.id] {
			delete(m.src, op.id)
			exists[op.id] = false
		}
	}
}

func applySchedOp(s *Scheduler, op randomOp) {
	switch op.kind {
	case "add":
		_ = s.Add(op.id, Declaration{SigText: op.sig, ImplText: op.impl})
	case "editSig":
		_ = s.EditSig(op.id, op.sig)
	case "editImpl":
		_ = s.EditImpl(op.id, op.impl)
	case "delete":
		_ = s.Delete(op.id)
	}
}

package growstack

import (
	"bytes"
	"testing"
)

// naiveModel never relocates: every coroutine starts at its maximum size.
//
// It executes the SAME logical operation sequence and records the final
// content of every slot plus the logical target of every pointer. The real
// subsystem (which grows/shrinks/relocates) must converge to identical
// observable results.

type naiveCell struct {
	kind byte // cellEmpty / cellInt / cellPtr
	ival int64
	ptr  *naivePtr
	dead bool
}

type naivePtr struct {
	co    int64
	frame int64
	slot  int
}

type naiveFrame struct{ id int64 }

type naiveModel struct {
	frameslots int
	maxStack   int
	stacks     map[int64]map[int64][]*naiveCell // co -> frame -> slots
	order      map[int64][]int64                // co -> frame stack
	nextFrame  map[int64]int64
}

func newNaive(slots, maxStack int, cos []int64) *naiveModel {
	m := &naiveModel{frameslots: slots, maxStack: maxStack, stacks: map[int64]map[int64][]*naiveCell{}, order: map[int64][]int64{}, nextFrame: map[int64]int64{}}
	for _, c := range cos {
		m.stacks[c] = map[int64][]*naiveCell{}
		m.nextFrame[c] = 0
	}
	return m
}

func (m *naiveModel) push(co int64) (int64, bool) {
	if len(m.order[co])*m.frameslots+m.frameslots > m.maxStack {
		return 0, false
	}
	id := m.nextFrame[co]
	m.nextFrame[co]++
	cells := make([]*naiveCell, m.frameslots)
	for i := range cells {
		cells[i] = &naiveCell{}
	}
	m.stacks[co][id] = cells
	m.order[co] = append(m.order[co], id)
	return id, true
}

func (m *naiveModel) pop(co int64) {
	ord := m.order[co]
	top := ord[len(ord)-1]
	// Mark every pointer targeting the popped frame dangling (cross-stack
	// pointers never exist in accepted operations).
	for _, frames := range m.stacks {
		for _, cs := range frames {
			for _, cl := range cs {
				if cl.kind == cellPtr && cl.ptr != nil && cl.ptr.co == co && cl.ptr.frame == top {
					cl.dead = true
				}
			}
		}
	}
	delete(m.stacks[co], top)
	m.order[co] = ord[:len(ord)-1]
}

type opKind int

const (
	opPush opKind = iota
	opPop
	opStore
	opMkStore
)

type dOp struct {
	kind  opKind
	co    int64
	frame int64
	slot  int
	val   int64
	dstCO int64
	dstF  int64
	dstS  int
}

func TestNaiveEquivalence(t *testing.T) {
	const coN = 3
	cfg := Config{BaseSize: 4, GrowthMul: 2, MaxPerStack: 64, TotalQuota: coN * 64, FrameSlots: 3, ShrinkRatio: 1.0 / 6.0}
	if _, err := NewRuntime(cfg); err != nil {
		t.Fatal(err)
	}
	for seed := int64(1); seed <= 60; seed++ {
		rnd := rngState(seed)
		var log bytes.Buffer
		r, err := NewRuntime(cfg, WithLogger(logWriter{w: &log}))
		if err != nil {
			t.Fatal(err)
		}
		var cos []int64
		for c := int64(1); c <= coN; c++ {
			cos = append(cos, c)
			if err := r.NewCoroutine(c); err != nil {
				t.Fatal(err)
			}
		}
		nm := newNaive(cfg.FrameSlots, cfg.MaxPerStack, cos)

		// Track handles: store index -> logical target.
		type hkey struct {
			co int64
			f  int64
			s  int
		}
		live := map[hkey]hkey{} // cells holding a same-stack pointer

		for step := 0; step < 220; step++ {
			co := int64(1 + rnd.Uint64()%coN)
			ord := nm.order[co]
			switch rnd.Uint64() % 10 {
			case 0, 1, 2, 3: // push
				_, realErr := r.Push(co)
				_, ok := nm.push(co)
				if ok != (realErr == nil) {
					t.Fatalf("seed=%d step=%d push divergence: naive=%v real=%v\n%s", seed, step, ok, realErr, log.String())
				}
			case 4, 5: // pop
				if len(ord) == 0 {
					continue
				}
				if err := r.Pop(co); err != nil {
					t.Fatalf("seed=%d pop: %v\n%s", seed, err, log.String())
				}
				nm.pop(co)
				// Drop dead pointer registrations.
				for k, v := range live {
					if !nm.frameAlive(v.co, v.f) || !nm.frameAlive(k.co, k.f) {
						delete(live, k)
					}
				}
			case 6, 7: // store int
				if len(ord) == 0 {
					continue
				}
				f := ord[rnd.Uint64()%uint64(len(ord))]
				s := int(rnd.Uint64() % uint64(cfg.FrameSlots))
				v := int64(rnd.Uint64() % 1000)
				if err := r.Store(co, f, s, v); err != nil {
					t.Fatalf("seed=%d store: %v", seed, err)
				}
				nm.stacks[co][f][s].kind = cellInt
				nm.stacks[co][f][s].ival = v
				delete(live, hkey{co, f, s})
			default: // mk pointer to same stack, store it
				if len(ord) == 0 {
					continue
				}
				sf := ord[rnd.Uint64()%uint64(len(ord))]
				tf := ord[rnd.Uint64()%uint64(len(ord))]
				ss := int(rnd.Uint64() % uint64(cfg.FrameSlots))
				ts := int(rnd.Uint64() % uint64(cfg.FrameSlots))
				h, err := r.MkPtr(co, sf, ss, co, tf, ts)
				if err != nil {
					t.Fatalf("seed=%d mkptr: %v", seed, err)
				}
				if err := r.CopyPtr(co, sf, ss, h); err != nil {
					t.Fatalf("seed=%d copyptr: %v", seed, err)
				}
				nm.stacks[co][sf][ss] = &naiveCell{kind: cellPtr, ptr: &naivePtr{co: co, frame: tf, slot: ts}}
				live[hkey{co, sf, ss}] = hkey{co, tf, ts}
			}
		}

		// Compare every live slot.
		for co, frames := range nm.stacks {
			for f, cs := range frames {
				for s, nc := range cs {
					rv, err := r.Load(co, f, s)
					if err != nil {
						t.Fatalf("seed=%d load co=%d f=%d s=%d: %v", seed, co, f, s, err)
					}
					if nc.kind == cellPtr && nc.dead {
						if !rv.IsPointer() || rv.ptr == nil || !rv.ptr.dead {
							t.Fatalf("seed=%d expected dangling co=%d f=%d s=%d got %v", seed, co, f, s, rv)
						}
						continue
					}
					if nc.kind == cellEmpty {
						// Never-written slots have no defined observable
						// value across relocations; skip residual bytes.
						continue
					}
					switch nc.kind {
					case cellEmpty:
					case cellInt:
						if !rv.IsInt() || rv.Int() != nc.ival {
							t.Fatalf("seed=%d mismatch int co=%d f=%d s=%d naive=%d real=%v", seed, co, f, s, nc.ival, rv)
						}
					case cellPtr:
						if !rv.IsPointer() || rv.ptr == nil {
							t.Fatalf("seed=%d expected pointer co=%d f=%d s=%d", seed, co, f, s)
						}
						if rv.ptr.targetCO != nc.ptr.co || rv.ptr.frame != nc.ptr.frame || rv.ptr.slot != nc.ptr.slot {
							t.Fatalf("seed=%d pointer target mismatch co=%d f=%d s=%d naive=(%d,%d,%d) real=(%d,%d,%d)",
								seed, co, f, s, nc.ptr.co, nc.ptr.frame, nc.ptr.slot,
								rv.ptr.targetCO, rv.ptr.frame, rv.ptr.slot)
						}
						if nc.dead != rv.ptr.dead {
							t.Fatalf("seed=%d dangling mismatch: naive=%v real=%v", seed, nc.dead, rv.ptr.dead)
						}
					}
				}
			}
		}
		t.Logf("seed=%d accepted log lines=%d", seed, bytes.Count(log.Bytes(), []byte{'\n'}))
	}
}

func (m *naiveModel) frameAlive(co, f int64) bool {
	for _, id := range m.order[co] {
		if id == f {
			return true
		}
	}
	return false
}

type rngState uint64

func (s *rngState) next() uint64 {
	x := uint64(*s)
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	*s = rngState(x)
	return x
}

func (s *rngState) Uint64() uint64 { return s.next() }

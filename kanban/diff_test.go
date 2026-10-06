package kanban_test

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"ontology/kanban"
	"ontology/naive"
)

// 共享操作字母表。
const (
	opAdd = iota
	opMove
	opReopen
	opOwner
	opAddDep
	opRemDep
	opSetLimit
	numOps
)

type op struct {
	kind                int
	id, id2, owner      string
	to, ver, col, limit int
	exp                 bool
	now                 int64
}

func (o op) String() string {
	names := []string{"Add", "Move", "Reopen", "Owner", "AddDep", "RemDep", "SetLimit"}
	return fmt.Sprintf("%s(id=%s id2=%s to=%d ver=%d exp=%t owner=%q col=%d lim=%d now=%d)",
		names[o.kind], o.id, o.id2, o.to, o.ver, o.exp, o.owner, o.col, o.limit, o.now)
}

type seedCard struct {
	id    string
	owner string
}

type scenario struct {
	ncols  int
	limits []int
	g      int
	seed   int64
	seeds  []seedCard
	ops    []op
}

func colNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("C%d", i)
	}
	return out
}

// 生成器状态：ids/ver/poolNext 会随“两边一致接受”推进。
type gen struct {
	rng      *rand.Rand
	ids      []string
	ver      map[string]int
	poolNext int
	owners   []string
	ncols, g int
	limits   []int
}

func (g *gen) pick() string { return g.ids[g.rng.Intn(len(g.ids))] }

func (g *gen) verOf(id string) int {
	v := g.ver[id]
	if g.rng.Intn(6) == 0 { // ~17% 坏版本
		return v + 1 + g.rng.Intn(3)
	}
	return v
}

func (g *gen) nextNow(cur int64) int64 {
	if cur > 0 && g.rng.Intn(12) == 0 {
		return cur - int64(1+g.rng.Intn(3))
	}
	return cur + int64(g.rng.Intn(4))
}

func (g *gen) makeOp(cur int64) op {
	o := op{kind: g.rng.Intn(numOps), now: g.nextNow(cur)}
	switch o.kind {
	case opAdd:
		o.id = fmt.Sprintf("c%d", g.poolNext)
		o.owner = g.owners[g.rng.Intn(len(g.owners))]
		if g.rng.Intn(15) == 0 {
			o.owner = ""
		}
	case opMove:
		o.id, o.to, o.ver, o.exp = g.pick(), g.rng.Intn(g.ncols), g.verOf(""), false
		o.ver = g.verOf(o.id)
		o.exp = g.rng.Intn(4) == 0
	case opReopen:
		o.id, o.ver = g.pick(), g.verOf(g.pick())
		// 上面两次 pick 不一致，重做一次保证同一 id
		o.id = g.pick()
		o.ver = g.verOf(o.id)
	case opOwner:
		o.id = g.pick()
		o.owner = g.owners[g.rng.Intn(len(g.owners))]
		if g.rng.Intn(15) == 0 {
			o.owner = ""
		}
		o.ver = g.verOf(o.id)
	case opAddDep, opRemDep:
		o.id, o.id2 = g.pick(), g.pick()
		for o.id2 == o.id {
			o.id2 = g.pick()
		}
		o.ver = g.verOf(o.id)
	case opSetLimit:
		o.col = 1 + g.rng.Intn(g.ncols-2)
		o.limit = g.rng.Intn(4)
	}
	return o
}

func buildScenario(seq, seqLen int) *scenario {
	seed := int64(seq + 1)
	rng := rand.New(rand.NewSource(seed))
	ncols := 3 + rng.Intn(6)
	limits := make([]int, ncols)
	for i := 1; i < ncols-1; i++ {
		limits[i] = 1 + rng.Intn(3)
	}
	g := 1 + rng.Intn(4)
	owners := []string{"alice", "bob", "carol"}

	pool := 4 + rng.Intn(8)
	sc := &scenario{ncols: ncols, limits: limits, g: g, seed: seed}
	var now int64
	gg := &gen{rng: rng, owners: owners, ncols: ncols, g: g, limits: limits, ver: map[string]int{}}
	for i := 0; i < pool; i++ {
		now++
		owner := owners[rng.Intn(len(owners))]
		id := fmt.Sprintf("c%d", i)
		sc.seeds = append(sc.seeds, seedCard{id, owner})
		gg.ids = append(gg.ids, id)
		gg.ver[id] = 1
		gg.poolNext++
	}
	for step := 0; step < seqLen; step++ {
		o := gg.makeOp(now)
		sc.ops = append(sc.ops, o)
		if o.kind != opSetLimit && o.now > now {
			now = o.now // 乐观推进；实际序列固定，回放时以真实接受为准
		}
		if o.kind == opSetLimit && o.now > now {
			now = o.now
		}
	}
	return sc
}

func (sc *scenario) initReal(t *testing.T) (*kanban.Board, int64) {
	t.Helper()
	real, err := kanban.NewBoard(kanban.Config{Columns: colNames(sc.ncols), Limits: sc.limits, OwnerLimit: sc.g})
	if err != nil {
		t.Fatal(err)
	}
	var now int64
	for _, s := range sc.seeds {
		now++
		if _, err := real.AddCard(s.id, s.owner, now); err != nil {
			t.Fatal(err)
		}
	}
	return real, now
}

func (sc *scenario) initNaive(t *testing.T) (*naive.Model, int64) {
	t.Helper()
	ref, err := naive.New(colNames(sc.ncols), sc.limits, sc.g)
	if err != nil {
		t.Fatal(err)
	}
	var now int64
	for _, s := range sc.seeds {
		now++
		if out := ref.AddCard(s.id, s.owner, now); !out.Accepted {
			t.Fatalf("naive seed: %s", out.Code)
		}
	}
	return ref, now
}

func codeOfReal(err error) (string, string) {
	if err == nil {
		return "", "accepted"
	}
	if e, ok := err.(*kanban.Error); ok {
		return string(e.Code), e.Error()
	}
	return "non-kanban-error", err.Error()
}

func applyReal(b *kanban.Board, o op) (string, string) {
	switch o.kind {
	case opAdd:
		_, e := b.AddCard(o.id, o.owner, o.now)
		return codeOfReal(e)
	case opMove:
		_, e := b.Move("u", o.id, o.to, o.ver, o.exp, o.now)
		return codeOfReal(e)
	case opReopen:
		_, e := b.Reopen("u", o.id, o.ver, o.now)
		return codeOfReal(e)
	case opOwner:
		_, e := b.ChangeOwner("u", o.id, o.owner, o.ver, o.now)
		return codeOfReal(e)
	case opAddDep:
		return codeOfReal(b.AddDep("u", o.id, o.id2, o.ver, o.now))
	case opRemDep:
		return codeOfReal(b.RemoveDep("u", o.id, o.id2, o.ver, o.now))
	case opSetLimit:
		return codeOfReal(b.SetColumnLimit(o.col, o.limit, o.now))
	}
	return "?", ""
}

func applyNaive(m *naive.Model, o op) string {
	var out naive.Outcome
	switch o.kind {
	case opAdd:
		out = m.AddCard(o.id, o.owner, o.now)
	case opMove:
		out = m.Move(o.id, o.to, o.ver, o.exp, o.now)
	case opReopen:
		out = m.Reopen(o.id, o.ver, o.now)
	case opOwner:
		out = m.ChangeOwner(o.id, o.owner, o.ver, o.now)
	case opAddDep:
		out = m.AddDep(o.id, o.id2, o.ver, o.now)
	case opRemDep:
		out = m.RemoveDep(o.id, o.id2, o.ver, o.now)
	case opSetLimit:
		out = m.SetLimit(o.col, o.limit, o.now)
	}
	if out.Accepted {
		return ""
	}
	return string(out.Code)
}

func fullState(b *kanban.Board) string {
	parts := make([]string, 0)
	for id := range b.AllCards() {
		c, _ := b.GetCard(id)
		ps := append([]string(nil), c.Prereqs...)
		sort.Strings(ps)
		parts = append(parts, fmt.Sprintf("%s|%s|c%d|v%d|e%d|%s",
			c.ID, c.Owner, c.Column, c.Version, boolI(c.Expedited), strings.Join(ps, ",")))
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

func fullStateNaive(m *naive.Model) string { return m.DumpState() }

func boolI(b bool) int {
	if b {
		return 1
	}
	return 0
}

func advance(cur int64, o op, accepted bool) int64 {
	if accepted && o.now > cur {
		return o.now
	}
	return cur
}

// TestDifferential1500：1500 组随机序列与独立朴素模型逐步对照。
func TestDifferential1500(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	const sequences, seqLen = 1500, 80
	var log bytes.Buffer
	var mu sync.Mutex
	rejects, accepts := 0, 0

	for seq := 0; seq < sequences; seq++ {
		sc := buildScenario(seq, seqLen)
		real, nowR := sc.initReal(t)
		ref, nowN := sc.initNaive(t)
		if nowR != nowN {
			t.Fatal("init now mismatch")
		}
		now := nowR

		// 感知版本/ID池（两边一致接受时推进）。
		ids := make([]string, 0, len(sc.seeds))
		ver := map[string]int{}
		poolNext := 0
		for _, s := range sc.seeds {
			ids = append(ids, s.id)
			ver[s.id] = 1
			poolNext++
		}

		for step := range sc.ops {
			o := sc.ops[step]
			if o.kind == opAdd {
				o.id = fmt.Sprintf("c%d", poolNext)
			}
			// 用感知版本替换生成期版本，保证高接受率；坏版本注入保留。
			if o.kind != opAdd && o.kind != opSetLimit && o.ver > 0 {
				// 生成器已按概率注入坏版本：以当前感知版本为基准重建注入偏移。
				// 为保持简单与确定：30% 概率给错版本。
				if step%3 == 0 {
					o.ver = ver[o.id] + 2
				} else {
					o.ver = ver[o.id]
				}
			}
			rc, why := applyReal(real, o)
			nc := applyNaive(ref, o)
			mu.Lock()
			fmt.Fprintf(&log, "seq=%d step=%d %s real=%q naive=%q | %s\n", seq, step, o, rc, nc, why)
			mu.Unlock()
			if rc != nc {
				t.Fatalf("seed=%d step=%d code mismatch\nop: %s\nreal=%s naive=%s\nrealState: %s\nnaiveState: %s",
					sc.seed, step, o, rc, nc, fullState(real), fullStateNaive(ref))
			}
			accepted := rc == ""
			if accepted {
				accepts++
				switch o.kind {
				case opAdd:
					ids = append(ids, o.id)
					ver[o.id] = 1
					poolNext++
				case opMove, opReopen, opOwner, opAddDep, opRemDep:
					ver[o.id]++
				}
			} else {
				rejects++
			}
			now = advance(now, o, accepted)
			if err := real.CheckInvariants(); err != nil {
				t.Fatalf("seed=%d step=%d invariants: %v\nop=%s\nstate=%s", sc.seed, step, err, o, fullState(real))
			}
		}
		if fullState(real) != fullStateNaive(ref) {
			t.Fatalf("seed=%d final state divergence\nreal : %s\nnaive: %s", sc.seed, fullState(real), fullStateNaive(ref))
		}
	}
	t.Logf("differential: sequences=%d accepts=%d rejects=%d", sequences, accepts, rejects)
	if err := os.WriteFile("diff_trace.log", log.Bytes(), 0o644); err != nil {
		t.Logf("write trace: %v", err)
	}
}

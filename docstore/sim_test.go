package docstore

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"ontology/shardmap"
	"ontology/slot"
)

type simRec struct {
	routing string
	body    string
}

type naiveModel struct {
	n, r, p int
	h       slot.HashFunc
	blocked bool
	shards  map[int]map[string]simRec
}

func newNaive(n, r, p int, h slot.HashFunc) *naiveModel {
	m := &naiveModel{n: n, r: r, p: p, h: h, shards: map[int]map[string]simRec{}}
	for i := 0; i < n; i++ {
		m.shards[i] = map[string]simRec{}
	}
	return m
}

func (m *naiveModel) locate(id, routing string) (int, string) {
	eff := routing
	if eff == "" {
		eff = id
	}
	pa := slot.Params{N: m.n, R: m.r, P: m.p, H: m.h}
	return pa.Shard([]byte(id), []byte(routing)), eff
}

func (m *naiveModel) counts() []int {
	out := make([]int, m.n)
	for i := 0; i < m.n; i++ {
		out[i] = len(m.shards[i])
	}
	return out
}

func (m *naiveModel) reshard(n2 int) (string, bool) {
	next := map[int]map[string]simRec{}
	for i := 0; i < n2; i++ {
		next[i] = map[string]simRec{}
	}
	pa := slot.Params{N: n2, R: m.r, P: m.p, H: m.h}
	var conflicts []string
	for _, docs := range m.shards {
		for id, rec := range docs {
			target := pa.Shard([]byte(id), []byte(rec.routing))
			if _, ok := next[target][id]; ok {
				conflicts = append(conflicts, id)
				continue
			}
			next[target][id] = rec
		}
	}
	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return conflicts[0], false
	}
	m.shards = next
	m.n = n2
	return "", true
}

type simOp struct {
	kind              string
	id, routing, body string
	n2                int
	on                bool
}

func (m *naiveModel) exec(op simOp) (wantErr string, counts []int, search []int, conflict string) {
	badID := len(op.id) < 1 || len(op.id) > 512
	badRT := len(op.routing) > 512
	switch op.kind {
	case "put":
		if badID || badRT {
			return "invalid", nil, nil, ""
		}
		if m.blocked {
			return "readonly", nil, nil, ""
		}
		if m.p > 1 && op.routing == "" {
			return "missingrouting", nil, nil, ""
		}
		s, eff := m.locate(op.id, op.routing)
		m.shards[s][op.id] = simRec{routing: eff, body: op.body}
	case "get":
		if badID || badRT {
			return "invalid", nil, nil, ""
		}
		if m.p > 1 && op.routing == "" {
			return "missingrouting", nil, nil, ""
		}
		s, _ := m.locate(op.id, op.routing)
		if _, ok := m.shards[s][op.id]; !ok {
			return "notfound", nil, nil, ""
		}
	case "delete":
		if badID || badRT {
			return "invalid", nil, nil, ""
		}
		if m.blocked {
			return "readonly", nil, nil, ""
		}
		if m.p > 1 && op.routing == "" {
			return "missingrouting", nil, nil, ""
		}
		s, _ := m.locate(op.id, op.routing)
		if _, ok := m.shards[s][op.id]; !ok {
			return "notfound", nil, nil, ""
		}
		delete(m.shards[s], op.id)
	case "block":
		m.blocked = op.on
	case "split":
		if op.n2 < 1 || op.n2 > 1024 {
			return "invalid", nil, nil, ""
		}
		if !m.blocked {
			return "notblocked", nil, nil, ""
		}
		if op.n2 <= m.n || op.n2%m.n != 0 || m.r%op.n2 != 0 {
			return "notsplittable", nil, nil, ""
		}
		if _, ok := m.reshard(op.n2); !ok {
			return "conflict", nil, nil, ""
		}
	case "shrink":
		if op.n2 < 1 || op.n2 > 1024 {
			return "invalid", nil, nil, ""
		}
		if !m.blocked {
			return "notblocked", nil, nil, ""
		}
		if op.n2 >= m.n || m.n%op.n2 != 0 {
			return "notshrinkable", nil, nil, ""
		}
		if m.p > 1 && op.n2 <= m.p {
			return "notshrinkable", nil, nil, ""
		}
		cid, ok := m.reshard(op.n2)
		if !ok {
			return "conflict", nil, nil, cid
		}
	case "count":
		return "", m.counts(), nil, ""
	case "search":
		if op.routing == "" {
			return "missingrouting", nil, nil, ""
		}
		found, _ := slot.SearchShards(slot.Params{N: m.n, R: m.r, P: m.p, H: m.h}, []byte(op.routing))
		return "", nil, found, ""
	}
	return "", nil, nil, ""
}

var errNameMap = map[error]string{
	shardmap.ErrInvalidArgument: "invalid",
	shardmap.ErrNotFound:        "notfound",
	shardmap.ErrReadOnly:        "readonly",
	shardmap.ErrNotWriteBlocked: "notblocked",
	shardmap.ErrNotSplittable:   "notsplittable",
	shardmap.ErrNotShrinkable:   "notshrinkable",
	shardmap.ErrIDConflict:      "conflict",
	shardmap.ErrMissingRouting:  "missingrouting",
	shardmap.ErrDocumentMissing: "notfound",
}

func errName(err error) string {
	for sentinel, name := range errNameMap {
		if errors.Is(err, sentinel) {
			return name
		}
	}
	return ""
}

func sliceEq(a, b []int) bool {
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

func chooseSetup(rng *rand.Rand) (int, int, int) {
	rr := []int{8, 16, 32}[rng.Intn(3)]
	var divisors []int
	for x := 1; x <= rr; x++ {
		if rr%x == 0 {
			divisors = append(divisors, x)
		}
	}
	n := divisors[rng.Intn(len(divisors))]
	p := 1
	if n > 2 && rng.Intn(2) == 0 {
		p = 2 + rng.Intn(n-2)
	}
	return n, rr, p
}

func genSplitN2(rng *rand.Rand, m *naiveModel) int {
	if rng.Intn(3) == 0 {
		return []int{0, 1025, m.n}[rng.Intn(3)]
	}
	var cands []int
	for x := m.n + 1; x <= m.r; x++ {
		if x%m.n == 0 && m.r%x == 0 {
			cands = append(cands, x)
		}
	}
	if len(cands) == 0 {
		return m.n
	}
	return cands[rng.Intn(len(cands))]
}

func genShrinkN2(rng *rand.Rand, m *naiveModel) int {
	if rng.Intn(4) == 0 {
		return []int{0, 1025, m.n}[rng.Intn(3)]
	}
	var cands []int
	for x := 1; x < m.n; x++ {
		if m.n%x == 0 {
			cands = append(cands, x)
		}
	}
	if len(cands) == 0 {
		return m.n
	}
	return cands[rng.Intn(len(cands))]
}

func describeBasis(op simOp, want string) string {
	return fmt.Sprintf("判定: kind=%s 预期分类=%s (N2=%d)", op.kind, want, op.n2)
}

func assertInvariants(t *testing.T, name string, store *Store, m *naiveModel, seq, step int) {
	t.Helper()
	realCounts, err := store.Count(name)
	if err != nil {
		t.Fatalf("seq=%d step=%d count: %v", seq, step, err)
	}
	if !sliceEq(realCounts, m.counts()) {
		t.Fatalf("seq=%d step=%d counts real=%v model=%v", seq, step, realCounts, m.counts())
	}
	// 逐条比对所在分片与内容
	st, err := shardmap.Snapshot(name)
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	d := store.indexes[name]
	if d == nil {
		store.mu.Unlock()
		if sumCounts(m.counts()) != 0 {
			t.Fatalf("seq=%d step=%d model has docs but real store has no data", seq, step)
		}
		return
	}
	d.mu.Lock()
	pa := st.Params()
	for shardIdx, docs := range d.shards {
		for id, rec := range docs {
			wantShard := pa.Shard([]byte(id), []byte(rec.routing))
			if wantShard != shardIdx {
				d.mu.Unlock()
				store.mu.Unlock()
				t.Fatalf("seq=%d step=%d doc %q/%q on shard %d but locates to %d",
					seq, step, id, rec.routing, shardIdx, wantShard)
			}
			mrec, ok := m.shards[shardIdx][id]
			if !ok {
				d.mu.Unlock()
				store.mu.Unlock()
				t.Fatalf("seq=%d step=%d doc %q missing in model shard %d", seq, step, id, shardIdx)
			}
			if mrec.routing != string(rec.routing) || mrec.body != string(rec.body) {
				d.mu.Unlock()
				store.mu.Unlock()
				t.Fatalf("seq=%d step=%d doc %q mismatch real=%+v model=%+v",
					seq, step, id, rec, mrec)
			}
			// 必在 SearchShards(其 routing) 之内
			found, _ := slot.SearchShards(pa, rec.routing)
			member := false
			for _, s := range found {
				if s == shardIdx {
					member = true
				}
			}
			if !member {
				d.mu.Unlock()
				store.mu.Unlock()
				t.Fatalf("seq=%d step=%d shard %d not in SearchShards(%q)=%v",
					seq, step, shardIdx, rec.routing, found)
			}
		}
	}
	d.mu.Unlock()
	store.mu.Unlock()
}

func TestRandomSimulation(t *testing.T) {
	const sequences = 1500
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq) + 1))
		n0, r0, p0 := chooseSetup(rng)
		hmod := uint32(3 + rng.Intn(9))
		h := smallHash(hmod)
		name := fmt.Sprintf("sim%d", seq)
		store := New()
		if err := shardmap.CreateIndex(name, n0, r0, p0, h); err != nil {
			t.Fatalf("seq %d create: %v", seq, err)
		}
		model := newNaive(n0, r0, p0, h)

		const opsPerSeq = 24
		for step := 0; step < opsPerSeq; step++ {
			op := simOp{
				id:      fmt.Sprintf("id%d", rng.Intn(6)),
				routing: fmt.Sprintf("rt%d", rng.Intn(6)),
				body:    fmt.Sprintf("body%d", rng.Intn(5)),
			}
			switch rng.Intn(10) {
			case 0:
				op.kind = "get"
			case 1:
				op.kind = "delete"
			case 2:
				op.kind = "block"
				op.on = rng.Intn(2) == 0
			case 3:
				op.kind = "split"
				op.n2 = genSplitN2(rng, model)
			case 4:
				op.kind = "shrink"
				op.n2 = genShrinkN2(rng, model)
			case 5:
				op.kind = "count"
			case 6:
				op.kind = "search"
				if rng.Intn(4) == 0 {
					op.routing = ""
				}
			case 7:
				op.kind = "put"
				if p0 > 1 && rng.Intn(3) == 0 {
					op.routing = ""
				}
			case 8:
				op.kind = "put"
				if rng.Intn(6) == 0 {
					op.id = ""
				}
			default:
				op.kind = "put"
			}

			var gotErr error
			var gotCounts, gotSearch []int
			var conflictID string
			switch op.kind {
			case "put":
				gotErr = store.Put(name, []byte(op.id), []byte(op.routing), []byte(op.body))
			case "get":
				_, gotErr = store.Get(name, []byte(op.id), []byte(op.routing))
			case "delete":
				gotErr = store.Delete(name, []byte(op.id), []byte(op.routing))
			case "block":
				gotErr = shardmap.SetWriteBlock(name, op.on)
			case "split":
				gotErr = shardmap.Split(name, op.n2)
			case "shrink":
				gotErr = shardmap.Shrink(name, op.n2)
				var ce *shardmap.IDConflictError
				if errors.As(gotErr, &ce) {
					conflictID = string(ce.ID)
				}
			case "count":
				gotCounts, gotErr = store.Count(name)
			case "search":
				gotSearch, gotErr = shardmap.SearchShards(name, []byte(op.routing))
			}

			wantErr, wantCounts, wantSearch, wantConflict := model.exec(op)
			t.Logf("seq=%d step=%d 输入 op=%s id=%q rt=%q n2=%d on=%v | 输出 real=%s model=%s counts=%v search=%v conflict=%q | %s",
				seq, step, op.kind, op.id, op.routing, op.n2, op.on,
				errName(gotErr), wantErr, gotCounts, gotSearch, conflictID,
				describeBasis(op, wantErr))

			if errName(gotErr) != wantErr {
				t.Fatalf("seq=%d step=%d %+v real=%v(%s) model=%s",
					seq, step, op, gotErr, errName(gotErr), wantErr)
			}
			if op.kind == "shrink" && wantErr == "conflict" && conflictID != wantConflict {
				t.Fatalf("seq=%d conflict id=%q model=%q", seq, conflictID, wantConflict)
			}
			if op.kind == "count" && !sliceEq(gotCounts, wantCounts) {
				t.Fatalf("seq=%d counts real=%v model=%v", seq, gotCounts, wantCounts)
			}
			if op.kind == "search" && wantErr == "" && !sliceEq(gotSearch, wantSearch) {
				t.Fatalf("seq=%d search real=%v model=%v", seq, gotSearch, wantSearch)
			}
			assertInvariants(t, name, store, model, seq, step)
		}
	}
}

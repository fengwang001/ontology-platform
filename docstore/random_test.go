package docstore

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/shardmap"
	"ontology/slot"
)

// ---------- 朴素参考模型：按规格独立重写，不调用实现的定位逻辑 ----------

type refDoc struct {
	id, routing, body string
}

type refModel struct {
	n, r, p int
	blocked bool
	shards  []map[string]refDoc
}

var activeHashTable = map[string]uint32{}

func refHash(b string) uint32 {
	if v, ok := activeHashTable[b]; ok {
		return v
	}
	return uint32(len(b)) + 7
}

func newRefModel(n, r, p int) *refModel {
	m := &refModel{n: n, r: r, p: p, shards: make([]map[string]refDoc, n)}
	for i := range m.shards {
		m.shards[i] = map[string]refDoc{}
	}
	return m
}

func refLocate(m *refModel, id, routing string) int {
	eff := routing
	if eff == "" {
		eff = id
	}
	hr := refHash(eff)
	hid := refHash(id)
	var s int
	if m.p == 1 {
		s = int(uint64(hr) % uint64(m.r))
	} else {
		s = int((uint64(hr) + uint64(hid%uint32(m.p))) % uint64(m.r))
	}
	return s / (m.r / m.n)
}

func (m *refModel) put(id, routing, body string) (error, string) {
	if m.blocked {
		return ErrIndexReadOnly, "write block is on"
	}
	if routing == "" && m.p > 1 {
		return ErrMissingRouting, "P>1 needs explicit routing"
	}
	eff := routing
	if eff == "" {
		eff = id
	}
	sh := refLocate(m, id, eff)
	m.shards[sh][id] = refDoc{id: id, routing: eff, body: body}
	return nil, fmt.Sprintf("effective=%q -> shard %d", eff, sh)
}

func (m *refModel) get(id, routing string) (string, error, string) {
	if routing == "" && m.p > 1 {
		return "", ErrMissingRouting, "P>1 needs explicit routing"
	}
	eff := routing
	if eff == "" {
		eff = id
	}
	sh := refLocate(m, id, eff)
	if d, ok := m.shards[sh][id]; ok {
		return d.body, nil, fmt.Sprintf("touched shard %d only: hit", sh)
	}
	return "", ErrDocumentNotFound, fmt.Sprintf("touched shard %d only: miss", sh)
}

func (m *refModel) del(id, routing string) (error, string) {
	if m.blocked {
		return ErrIndexReadOnly, "write block is on"
	}
	if routing == "" && m.p > 1 {
		return ErrMissingRouting, "P>1 needs explicit routing"
	}
	eff := routing
	if eff == "" {
		eff = id
	}
	sh := refLocate(m, id, eff)
	if _, ok := m.shards[sh][id]; !ok {
		return ErrDocumentNotFound, fmt.Sprintf("only shard %d checked: miss", sh)
	}
	delete(m.shards[sh], id)
	return nil, fmt.Sprintf("removed from shard %d", sh)
}

func (m *refModel) search(routing string) ([]int, error, string) {
	if routing == "" {
		return nil, ErrMissingRouting, "routing required"
	}
	hr := refHash(routing)
	set := map[int]struct{}{}
	for off := 0; off < m.p; off++ {
		s := int((uint64(hr) + uint64(off)) % uint64(m.r))
		set[s/(m.r/m.n)] = struct{}{}
	}
	out := make([]int, 0, len(set))
	for sh := range set {
		out = append(out, sh)
	}
	sort.Ints(out)
	return out, nil, fmt.Sprintf("h(%q)=%d, %d slots -> shards %v", routing, hr, m.p, out)
}

// resize 返回 (错误, 判定依据, 冲突 id)。任何拒绝都不改模型。
func (m *refModel) resize(n2 int, shrink bool) (error, string, string) {
	if !m.blocked {
		return ErrWriteBlockRequired, "resize requires write block", ""
	}
	if n2 < 1 || n2 > 1024 {
		return ErrInvalidParam, "N2 out of [1,1024]", ""
	}
	if shrink {
		if n2 >= m.n || m.n%n2 != 0 || (m.p > 1 && n2 <= m.p) {
			return ErrCannotShrink, fmt.Sprintf("shrink %d->%d rejected (R=%d,P=%d)", m.n, n2, m.r, m.p), ""
		}
	} else {
		if n2 <= m.n || n2%m.n != 0 || m.r%n2 != 0 {
			return ErrCannotSplit, fmt.Sprintf("split %d->%d rejected (R=%d)", m.n, n2, m.r), ""
		}
	}
	candidate := &refModel{n: n2, r: m.r, p: m.p}
	next := make([]map[string]refDoc, n2)
	for i := range next {
		next[i] = map[string]refDoc{}
	}
	conflict := ""
	for _, sh := range m.shards {
		for _, d := range sh {
			target := refLocate(candidate, d.id, d.routing)
			if ex, ok := next[target][d.id]; ok && ex.routing != d.routing {
				id := d.id
				if ex.id < id {
					id = ex.id
				}
				if conflict == "" || id < conflict {
					conflict = id
				}
			}
			next[target][d.id] = d
		}
	}
	if conflict != "" {
		return ErrIDConflict, "same id with different routing merges, state unchanged", conflict
	}
	m.shards = next
	m.n = n2
	return nil, fmt.Sprintf("redistributed to N=%d (%d shards)", n2, n2), ""
}

func (m *refModel) counts() []int {
	out := make([]int, len(m.shards))
	for i, sh := range m.shards {
		out[i] = len(sh)
	}
	return out
}

func errClass(err error) error {
	for _, target := range []error{
		ErrInvalidParam, ErrIndexNotFound, ErrIndexExists, ErrIndexReadOnly,
		ErrWriteBlockRequired, ErrMissingRouting, ErrCannotSplit, ErrCannotShrink,
		ErrIDConflict, ErrDocumentNotFound,
	} {
		if errors.Is(err, target) {
			return target
		}
	}
	return nil
}

func intSliceEqual(a, b []int) bool {
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

func optBytes(s string) []byte {
	if s == "" {
		return nil
	}
	return []byte(s)
}

func divisors(n int) []int {
	var out []int
	for d := 1; d < n; d++ {
		if n%d == 0 {
			out = append(out, d)
		}
	}
	return out
}

var _ = strings.TrimSpace
var _ = shardmap.ErrInvalidParam

type opKind int

const (
	opPut opKind = iota
	opGet
	opDelete
	opSearch
	opBlock
	opUnblock
	opSplit
	opShrink
	opCount
	opCountEnd
)

// TestRandomAgainstNaiveModel 用 1500 组随机序列逐步对照朴素模型。
func TestRandomAgainstNaiveModel(t *testing.T) {
	t.Log("running 1500 random sequences against the naive simulator")
	for seed := int64(0); seed < 1500; seed++ {
		runOneRandomSequence(t, seed, fmt.Sprintf("rand-%d", seed))
	}
}

type testLogger interface {
	Helper()
	Fatalf(format string, args ...any)
	Logf(format string, args ...any)
}

func runOneRandomSequence(t testLogger, seed int64, name string) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))

	// 小值域注入哈希：哈希值落在 0..hashMod-1，刻意制造碰撞。
	hashMod := 1 + int(rng.Int63n(8))
	ids := []string{"i0", "i1", "i2", "i3", "i4", "i5"}
	rtgs := []string{"r0", "r1", "r2", "r3"}
	table := map[string]uint32{}
	for _, x := range append(append([]string{}, ids...), rtgs...) {
		table[x] = uint32(rng.Int63n(int64(hashMod)))
	}
	activeHashTable = table

	type cfg struct{ n, r, p int }
	var validCfgs []cfg
	for n := 1; n <= 16; n++ {
		for r := n; r <= 64; r += n {
			for p := 1; p < n; p++ {
				validCfgs = append(validCfgs, cfg{n, r, p})
			}
		}
	}
	c := validCfgs[rng.Intn(len(validCfgs))]
	ref := newRefModel(c.n, c.r, c.p)

	var log []string
	log = append(log, fmt.Sprintf("seed=%d N=%d R=%d P=%d hashMod=%d table=%v",
		seed, c.n, c.r, c.p, hashMod, table))
	fail := func(msg string) {
		log = append(log, "MISMATCH: "+msg)
		t.Fatalf("seed %d failed\n%s", seed, strings.Join(log, "\n"))
	}

	if err := CreateIndex(name, c.n, c.r, c.p,
		shardmap.WithHash(func(b []byte) uint32 { return table[string(b)] })); err != nil {
		fail("create: " + err.Error())
	}

	steps := 30 + rng.Intn(25)
	for step := 0; step < steps; step++ {
		kind := opKind(rng.Intn(int(opCountEnd)))
		id := ids[rng.Intn(len(ids))]
		rt := rtgs[rng.Intn(len(rtgs))]
		if rng.Intn(4) == 0 {
			rt = ""
		}
		var in, why, out string

		switch kind {
		case opPut:
			body := fmt.Sprintf("body-%d-%d", seed, step)
			in = fmt.Sprintf("Put(id=%q,routing=%q)", id, rt)
			gotErr := Put(name, []byte(id), optBytes(rt), []byte(body))
			refErr, reason := ref.put(id, rt, body)
			why, out = reason, fmt.Sprintf("err=%v", gotErr)
			if errClass(gotErr) != errClass(refErr) {
				fail(fmt.Sprintf("%s: got %v want %v | %s", in, gotErr, refErr, why))
			}
		case opGet:
			in = fmt.Sprintf("Get(id=%q,routing=%q)", id, rt)
			gbody, gotErr := Get(name, []byte(id), optBytes(rt))
			rbody, refErr, reason := ref.get(id, rt)
			why, out = reason, fmt.Sprintf("body=%q err=%v", string(gbody), gotErr)
			if errClass(gotErr) != errClass(refErr) || (gotErr == nil && string(gbody) != rbody) {
				fail(fmt.Sprintf("%s: got %q/%v want %q/%v | %s", in, gbody, gotErr, rbody, refErr, why))
			}
			if gotErr == nil {
				ts, td := readTouched(name)
				if ts != 1 || td != 1 {
					fail(fmt.Sprintf("%s: touched shards=%d docs=%d want 1/1", in, ts, td))
				}
			}
		case opDelete:
			in = fmt.Sprintf("Delete(id=%q,routing=%q)", id, rt)
			gotErr := Delete(name, []byte(id), optBytes(rt))
			refErr, reason := ref.del(id, rt)
			why, out = reason, fmt.Sprintf("err=%v", gotErr)
			if errClass(gotErr) != errClass(refErr) {
				fail(fmt.Sprintf("%s: got %v want %v | %s", in, gotErr, refErr, why))
			}
		case opSearch:
			in = fmt.Sprintf("SearchShards(routing=%q)", rt)
			got, gotErr := SearchShards(name, optBytes(rt))
			rshards, refErr, reason := ref.search(rt)
			why, out = reason, fmt.Sprintf("shards=%v err=%v", got, gotErr)
			if errClass(gotErr) != errClass(refErr) || (gotErr == nil && !intSliceEqual(got, rshards)) {
				fail(fmt.Sprintf("%s: got %v/%v want %v/%v | %s", in, got, gotErr, rshards, refErr, why))
			}
		case opBlock:
			in, why, out = "SetWriteBlock(true)", "block writes, allow resize", "ok"
			if err := SetWriteBlock(name, true); err != nil {
				fail(in + ": " + err.Error())
			}
			ref.blocked = true
		case opUnblock:
			in, why, out = "SetWriteBlock(false)", "writes allowed again", "ok"
			if err := SetWriteBlock(name, false); err != nil {
				fail(in + ": " + err.Error())
			}
			ref.blocked = false
		case opSplit:
			n2 := ref.n * (1 + rng.Intn(4))
			in = fmt.Sprintf("Split(N2=%d)", n2)
			gotErr := Split(name, n2)
			refErr, reason, conflict := ref.resize(n2, false)
			why, out = reason, fmt.Sprintf("err=%v", gotErr)
			if errClass(gotErr) != errClass(refErr) || conflictOf(gotErr) != conflict {
				fail(fmt.Sprintf("%s: got %v(%q) want %v(%q) | %s",
					in, gotErr, conflictOf(gotErr), refErr, conflict, why))
			}
		case opShrink:
			n2 := 1
			if ref.n > 1 {
				divs := divisors(ref.n)
				n2 = divs[rng.Intn(len(divs))]
			}
			in = fmt.Sprintf("Shrink(N2=%d)", n2)
			gotErr := Shrink(name, n2)
			refErr, reason, conflict := ref.resize(n2, true)
			why, out = reason, fmt.Sprintf("err=%v", gotErr)
			if errClass(gotErr) != errClass(refErr) || conflictOf(gotErr) != conflict {
				fail(fmt.Sprintf("%s: got %v(%q) want %v(%q) | %s",
					in, gotErr, conflictOf(gotErr), refErr, conflict, why))
			}
		case opCount:
			in, why = "Count()", "per-shard counts compared"
			got, gotErr := Count(name)
			if gotErr != nil {
				fail(in + ": " + gotErr.Error())
			}
			out = fmt.Sprintf("counts=%v", got)
			if !intSliceEqual(got, ref.counts()) {
				fail(fmt.Sprintf("%s: got %v want %v", in, got, ref.counts()))
			}
			sum := 0
			for _, c := range got {
				sum += c
			}
			refSum := 0
			for _, c := range ref.counts() {
				refSum += c
			}
			if sum != refSum {
				fail(fmt.Sprintf("total %d != ref total %d", sum, refSum))
			}
		}

		// 不变量：每条文档所在分片等于按当前 N 的定位，且在 SearchShards 内。
		if kind != opCount {
			storesMu.Lock()
			st := stores[name]
			storesMu.Unlock()
			st.mu.Lock()
			for sh, shardDocs := range st.shards {
				for id, d := range shardDocs {
					snap := st.meta.Snapshot()
					if want := locate(snap, []byte(id), d.routing); want != sh {
						st.mu.Unlock()
						fail(fmt.Sprintf("doc %q at shard %d but locate says %d (N=%d)", id, sh, want, snap.N))
					}
					targets, serr := searchShardsLocked(st, d.routing)
					if serr != nil {
						st.mu.Unlock()
						fail(fmt.Sprintf("doc routing %q search: %v", d.routing, serr))
					}
					if !containsInt(targets, sh) {
						st.mu.Unlock()
						fail(fmt.Sprintf("doc %q shard %d not in search %v", id, sh, targets))
					}
				}
			}
			st.mu.Unlock()
		}
		t.Logf("seed=%d step=%d input=%s output=%s why=%s", seed, step, in, out, why)
	}
}

func readTouched(name string) (int, int) {
	storesMu.Lock()
	st := stores[name]
	storesMu.Unlock()
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.touchedShards, st.touchedDocs
}

func searchShardsLocked(st *store, routing []byte) ([]int, error) {
	snap := st.meta.Snapshot()
	hr := snap.Hash(routing)
	set := map[int]struct{}{}
	for _, s := range slot.SearchSlots(hr, snap.R, snap.P) {
		set[slot.Shard(s, snap.N, snap.R)] = struct{}{}
	}
	out := make([]int, 0, len(set))
	for sh := range set {
		out = append(out, sh)
	}
	sort.Ints(out)
	return out, nil
}

func conflictOf(err error) string {
	if err == nil || !errors.Is(err, ErrIDConflict) {
		return ""
	}
	s := err.Error()
	const prefix = ": "
	idx := strings.Index(s, prefix)
	if idx < 0 {
		return ""
	}
	return s[idx+len(prefix):]
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// TestTouchedIndependentOfTotal 对照 100 与 100000 条文档：Get 始终只触 1 分片至多 1 记录。
func TestTouchedIndependentOfTotal(t *testing.T) {
	for _, total := range []int{100, 100000} {
		name := fmt.Sprintf("touched-%d", total)
		freshIndex(t, name, 32, 1024, 1, nil)
		for i := 0; i < total; i++ {
			id := []byte(fmt.Sprintf("doc-%06d", i))
			if err := Put(name, id, id, []byte("payload")); err != nil {
				t.Fatalf("put %d: %v", i, err)
			}
		}
		probe := []byte("doc-000042")
		if _, err := Get(name, probe, probe); err != nil {
			t.Fatalf("get: %v", err)
		}
		ts, td := readTouched(name)
		if ts != 1 || td != 1 {
			t.Fatalf("total=%d touched shards=%d docs=%d want 1/1", total, ts, td)
		}
		// miss 时同样只触一个分片、0 条记录。
		missing := []byte("does-not-exist")
		if _, err := Get(name, missing, missing); !errors.Is(err, ErrDocumentNotFound) {
			t.Fatalf("missing get: %v", err)
		}
		ts, td = readTouched(name)
		if ts != 1 || td != 0 {
			t.Fatalf("miss total=%d touched shards=%d docs=%d want 1/0", total, ts, td)
		}
		t.Logf("total=%d: Get touched 1 shard, docs=%d/0 regardless of index size", total, 1)
	}
}

// TestDeterministicReplay 同一种子重放两次，所有输出一致（由 RNG 确定性保证）。
func TestDeterministicReplay(t *testing.T) {
	var sa, sb2 strings.Builder
	runOneRandomSequence(&recorderT{t: t, sb: &sa}, 42, "replay-42-a")
	runOneRandomSequence(&recorderT{t: t, sb: &sb2}, 42, "replay-42-b")
	a, b := sa.String(), sb2.String()
	if a != b {
		t.Fatal("replay of the same seed produced different results")
	}
}

type recorderT struct {
	t  *testing.T
	sb *strings.Builder
}

func (r *recorderT) Helper()                           {}
func (r *recorderT) Fatalf(format string, args ...any) { r.t.Fatalf(format, args...) }
func (r *recorderT) Logf(format string, args ...any)   { fmt.Fprintf(r.sb, format+"\n", args...) }

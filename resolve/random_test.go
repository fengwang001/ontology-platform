package resolve

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"testing"
)

type randPoint struct{ k, w int64 }

type randRec struct {
	id      string
	boot, k int64
	arrival int
	emitted bool
}

type randBoot struct {
	points    []randPoint
	pending   []*randRec
	sealed    bool
	synced    bool
	estimated bool
	start     int64
	maxK      int64
}

type naive struct {
	pmax    int
	boots   map[int64]*randBoot
	maxBoot int64
	pending int
	arrival int
	log     []string
	out     []wantEmit
}

func newNaive(pmax int) *naive {
	return &naive{pmax: pmax, boots: map[int64]*randBoot{}}
}

func (m *naive) boot(id int64) *randBoot {
	b := m.boots[id]
	if b == nil {
		b = &randBoot{}
		m.boots[id] = b
	}
	return b
}

func (m *naive) valid(kind string, boot, k, w int64) error {
	if boot < 1 || boot > 1_000_000 || k < 0 || k > 1_000_000_000 {
		return ErrInvalid
	}
	if kind == "sync" && (w < 0 || w > 1_000_000_000_000_000) {
		return ErrInvalid
	}
	if m.maxBoot != 0 && boot < m.maxBoot {
		return ErrSealed
	}
	return nil
}

func (m *naive) pointErr(b *randBoot, k, w int64) error {
	pos := sort.Search(len(b.points), func(i int) bool { return b.points[i].k >= k })
	if pos < len(b.points) && b.points[pos].k == k {
		return ErrDupSync
	}
	if pos > 0 && b.points[pos-1].w >= w {
		return ErrSkew
	}
	if pos < len(b.points) && w >= b.points[pos].w {
		return ErrSkew
	}
	return nil
}

func (m *naive) addPoint(b *randBoot, k, w int64) {
	pos := sort.Search(len(b.points), func(i int) bool { return b.points[i].k >= k })
	b.points = append(b.points, randPoint{})
	copy(b.points[pos+1:], b.points[pos:])
	b.points[pos] = randPoint{k, w}
}

func (m *naive) pointAt(b *randBoot, k int64, above bool) (randPoint, bool) {
	if above {
		pos := sort.Search(len(b.points), func(i int) bool { return b.points[i].k >= k })
		if pos == len(b.points) {
			return randPoint{}, false
		}
		return b.points[pos], true
	}
	pos := sort.Search(len(b.points), func(i int) bool { return b.points[i].k > k })
	if pos == 0 {
		return randPoint{}, false
	}
	return b.points[pos-1], true
}

func naiveInterp(p, n randPoint, k int64) int64 {
	x := new(big.Int).Mul(big.NewInt(k-p.k), big.NewInt(n.w-p.w))
	x.Quo(x, big.NewInt(n.k-p.k))
	return p.w + x.Int64()
}

func (m *naive) sortPending(list []*randRec) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].k != list[j].k {
			return list[i].k < list[j].k
		}
		return list[i].arrival < list[j].arrival
	})
}

func (m *naive) emit(rec *randRec, wall int64, src Source, why string) {
	rec.emitted = true
	m.out = append(m.out, wantEmit{rec.id, wall, src})
	m.log = append(m.log, fmt.Sprintf("EMIT %s b=%d k=%d wall=%d src=%s because=%s", rec.id, rec.boot, rec.k, wall, src, why))
}

func (m *naive) resolveNow(b *randBoot, rec *randRec) (Emit, bool) {
	if len(b.points) == 0 {
		return Emit{}, false
	}
	p, hasP := m.pointAt(b, rec.k, false)
	n, hasN := m.pointAt(b, rec.k, true)
	if hasN && n.k == rec.k {
		return Emit{Wall: n.w, Source: Interp}, true
	}
	if hasP && hasN && p.k != n.k {
		return Emit{Wall: naiveInterp(p, n, rec.k), Source: Interp}, true
	}
	if !hasP && hasN {
		return Emit{Wall: n.w - (n.k - rec.k), Source: Back}, true
	}
	return Emit{}, false
}

func (m *naive) seal(old int64) []wantEmit {
	if old == 0 {
		return nil
	}
	before := len(m.out)
	b := m.boot(old)
	b.sealed = true
	if len(b.points) == 0 {
		if len(b.pending) > 0 {
			m.sortPending(b.pending)
			b.maxK = b.pending[len(b.pending)-1].k
		}
		return nil
	}
	last := b.points[len(b.points)-1]
	m.sortPending(b.pending)
	rem := b.pending
	b.pending = nil
	m.pending -= len(rem)
	for _, rec := range rem {
		m.emit(rec, last.w+(rec.k-last.k), Fwd, "sealed boot has last sync point")
	}
	return m.out[before:]
}

func afterSealPending(m *naive, old int64) int {
	if old == 0 {
		return 0
	}
	b := m.boots[old]
	if b == nil || len(b.points) == 0 {
		return m.pending
	}
	return m.pending - len(b.pending)
}

func (m *naive) estimate(boot, nextStart int64) {
	var ids []int64
	for id := range m.boots {
		if id < boot {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	next := nextStart
	for i := len(ids) - 1; i >= 0; i-- {
		b := m.boots[ids[i]]
		if len(b.points) > 0 || b.estimated || !b.sealed {
			break
		}
		start := next - 1 - b.maxK
		b.start, b.estimated, next = start, true, start
		m.sortPending(b.pending)
		rem := b.pending
		b.pending = nil
		m.pending -= len(rem)
		for _, rec := range rem {
			m.emit(rec, start+rec.k, Est, "chained estimated boot start")
		}
	}
}

func (m *naive) record(boot, k int64, id string) error {
	if err := m.valid("record", boot, k, 0); err != nil {
		m.log = append(m.log, fmt.Sprintf("RECORD %s REJECT %s", id, err))
		return err
	}
	var sealed []wantEmit
	if boot > m.maxBoot {
		if afterSealPending(m, m.maxBoot) >= m.pmax {
			m.log = append(m.log, fmt.Sprintf("RECORD %s REJECT %s after-seal pending is full", id, ErrFull))
			return ErrFull
		}
	}
	rec := &randRec{id: id, boot: boot, k: k, arrival: m.arrival}
	m.arrival++
	var b *randBoot
	if boot == m.maxBoot {
		b = m.boot(boot)
		if e, ok := m.resolveNow(b, rec); ok {
			m.log = append(m.log, fmt.Sprintf("RECORD %s direct-resolve", id))
			m.emit(rec, e.Wall, e.Source, "current sync set at arrival")
			return nil
		}
		if m.pending >= m.pmax {
			m.log = append(m.log, fmt.Sprintf("RECORD %s REJECT %s current pending is full", id, ErrFull))
			return ErrFull
		}
	}
	if boot > m.maxBoot {
		sealed = m.seal(m.maxBoot)
		m.maxBoot = boot
		b = m.boot(boot)
	}
	b.pending = append(b.pending, rec)
	m.pending++
	if !b.sealed && rec.k > b.maxK {
		b.maxK = rec.k
	}
	m.log = append(m.log, fmt.Sprintf("RECORD %s held; sealEmit=%d totalPending=%d", id, len(sealed), m.pending))
	return nil
}

func (m *naive) sync(boot, k, w int64) error {
	if err := m.valid("sync", boot, k, w); err != nil {
		m.log = append(m.log, fmt.Sprintf("SYNC b=%d k=%d w=%d REJECT %s", boot, k, w, err))
		return err
	}
	if boot > m.maxBoot {
		if afterSealPending(m, m.maxBoot) >= m.pmax {
			m.log = append(m.log, fmt.Sprintf("SYNC b=%d k=%d REJECT %s", boot, k, ErrFull))
			return ErrFull
		}
	}
	var sealed []wantEmit
	if boot > m.maxBoot {
		sealed = m.seal(m.maxBoot)
		m.maxBoot = boot
	}
	b := m.boots[boot]
	if b == nil {
		b = &randBoot{}
	}
	if err := m.pointErr(b, k, w); err != nil {
		m.log = append(m.log, fmt.Sprintf("SYNC b=%d k=%d w=%d REJECT %s; no state change", boot, k, w, err))
		if len(sealed) != 0 {
			panic("sealed before point validation")
		}
		return err
	}
	if _, exists := m.boots[boot]; !exists {
		m.boots[boot] = b
	}
	m.addPoint(b, k, w)
	first := !b.synced
	b.synced = true
	if first {
		b.start = w - k
	}
	m.sortPending(b.pending)
	var rem []*randRec
	for len(b.pending) > 0 && b.pending[0].k <= k {
		rem = append(rem, b.pending[0])
		b.pending = b.pending[1:]
	}
	m.pending -= len(rem)
	for _, rec := range rem {
		e, ok := m.resolveNow(b, rec)
		if !ok {
			panic("released record unresolved")
		}
		m.emit(rec, e.Wall, e.Source, "sync released pending prefix")
	}
	if first {
		m.estimate(boot, b.start)
	}
	m.log = append(m.log, fmt.Sprintf("SYNC b=%d k=%d w=%d accepted first=%t released=%d", boot, k, w, first, len(rem)))
	return nil
}

type randOp struct {
	kind       byte
	boot, k, w int64
	id         string
}

func TestRandomAgainstNaive(t *testing.T) {
	for seed := int64(1); seed <= 1500; seed++ {
		rng := rand.NewPCG(uint64(seed), uint64(seed*7+1))
		random := rand.New(rng)
		pmax := 3 + random.IntN(12)
		resolver, err := New(pmax)
		if err != nil {
			t.Fatal(err)
		}
		resolver.Register(1)
		model := newNaive(pmax)
		var ops []randOp
		recN := 0
		currentBoot := int64(1)
		for step := 0; step < 45; step++ {
			boot := int64(1 + random.IntN(6))
			if random.IntN(6) == 0 {
				boot = currentBoot
			}
			if boot > currentBoot {
				currentBoot = boot
			}
			k := int64(random.IntN(80))
			if random.IntN(4) == 0 {
				record := randOp{kind: 's', boot: boot, k: k, w: int64(random.IntN(10000)), id: fmt.Sprintf("seed%d-op%d", seed, step)}
				ops = append(ops, record)
				continue
			}
			recN++
			ops = append(ops, randOp{kind: 'r', boot: boot, k: k, id: fmt.Sprintf("s%d-%d", seed, recN)})
		}
		var actual []wantEmit
		var actualErrs []error
		var modelErrs []error
		for opIndex := range ops {
			in := ops[opIndex]
			var got []Emit
			var gotErr error
			var modelErr error
			if in.kind == 'r' {
				got, gotErr = resolver.Record(1, in.boot, in.k, in.id)
				modelErr = model.record(in.boot, in.k, in.id)
			} else {
				got, gotErr = resolver.Sync(1, in.boot, in.k, in.w)
				modelErr = model.sync(in.boot, in.k, in.w)
			}
			actualErrs = append(actualErrs, gotErr)
			modelErrs = append(modelErrs, modelErr)
			for _, e := range got {
				actual = append(actual, wantEmit{e.Record.Payload.(string), e.Wall, e.Source})
			}
		}
		for opIndex := range ops {
			if !errors.Is(actualErrs[opIndex], modelErrs[opIndex]) {
				t.Fatalf("seed=%d op=%d %+v actual err=%v naive err=%v", seed, opIndex, ops[opIndex], actualErrs[opIndex], modelErrs[opIndex])
			}
		}
		if model.pending > pmax {
			t.Fatalf("seed %d pending %d > pmax %d", seed, model.pending, pmax)
		}
		actualPending := resolver.getPendingForTest(1)
		bufferPending := 0
		resolver.mu.Lock()
		for _, b := range resolver.dev[1].boots {
			bufferPending += b.pending.Len()
		}
		resolver.mu.Unlock()
		if actualPending > pmax || actualPending != bufferPending {
			t.Fatalf("seed=%d actualPending=%d bufferPending=%d pmax=%d", seed, actualPending, bufferPending, pmax)
		}
		if fmt.Sprint(actual) != fmt.Sprint(model.out) {
			var b strings.Builder
			b.WriteString(fmt.Sprintf("seed=%d pmax=%d\nops:\n", seed, pmax))
			for index, in := range ops {
				fmt.Fprintf(&b, "%d: %+v\n", index, in)
			}
			b.WriteString("actual:\n")
			for _, e := range actual {
				fmt.Fprintf(&b, "%+v\n", e)
			}
			b.WriteString("naive:\n")
			for _, e := range model.out {
				fmt.Fprintf(&b, "%+v\n", e)
			}
			b.WriteString("decisions:\n")
			for _, line := range model.log {
				b.WriteString(line)
				b.WriteByte('\n')
			}
			t.Fatal(b.String())
		}
	}
}

func TestNegativeEstimateAndOperationOrder(t *testing.T) {
	rows := runCase(t, 100, []op{
		{"register", 1, 0, 0, 0, "", nil},
		{"record", 1, 1, 10, 0, "a", nil},
		{"record", 1, 2, 100, 0, "b", nil},
		{"sync", 1, 2, 100, 50, "", nil},
	})
	got := flatten(rows)
	want := []wantEmit{{"b", 50, Interp}, {"a", -51, Est}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got=%#v want=%#v", got, want)
	}

	ordered := runCase(t, 100, []op{
		{"register", 3, 0, 0, 0, "", nil},
		{"sync", 3, 1, 100, 1000, "", nil},
		{"record", 3, 1, 100, 0, "oldPending", nil},
		{"record", 3, 1, 200, 0, "oldForward", nil},
		{"record", 3, 2, 10, 0, "middle", nil},
		{"record", 3, 3, 5, 0, "direct", nil},
		{"sync", 3, 3, 20, 100, "", nil},
	})
	sealNames := payloadNames(ordered[3])
	finalNames := payloadNames(ordered[len(ordered)-1])
	wantSeal := []string{"oldForward"}
	wantFinal := []string{"direct", "middle"}
	if fmt.Sprint(sealNames) != fmt.Sprint(wantSeal) || fmt.Sprint(finalNames) != fmt.Sprint(wantFinal) {
		t.Fatalf("seal=%v want=%v; final=%v want=%v", sealNames, wantSeal, finalNames, wantFinal)
	}
}

func payloadNames(row []wantEmit) []string {
	names := make([]string, 0, len(row))
	for _, e := range row {
		names = append(names, e.payload)
	}
	return names
}

func TestConcurrentDeterministicContent(t *testing.T) {
	resolver, _ := New(2000)
	resolver.Register(1)
	if _, err := resolver.Sync(1, 1, 10, 100); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var got []Emit
	for worker := 0; worker < 8; worker++ {
		count := 125
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < count; i++ {
				id := worker*count + i
				out, err := resolver.Record(1, 1, 10, fmt.Sprintf("r-%d", id))
				if err != nil || len(out) != 1 || out[0].Wall != 100 || out[0].Source != Interp {
					t.Errorf("worker=%d i=%d out=%#v err=%v", worker, i, out, err)
					continue
				}
				mu.Lock()
				got = append(got, out...)
				mu.Unlock()
			}
		}(worker)
	}
	wg.Wait()
	if len(got) != 1000 || resolver.getPendingForTest(1) != 0 {
		t.Fatalf("emitted=%d pending=%d", len(got), resolver.getPendingForTest(1))
	}
}

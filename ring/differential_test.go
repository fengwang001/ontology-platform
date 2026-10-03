package ring

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/change"
)

type dOpKind int

const dPut dOpKind = 0
const dDelete dOpKind = 1

type dOp struct {
	kind  dOpKind
	value int64
}

type dTriple struct {
	ts     int64
	origin int
	seq    int64
}

type dEntry struct {
	triple dTriple
	value  int64
	del    bool
	exists bool
}

type dChange struct {
	origin int
	seq    int64
	ts     int64
	key    string
	op     dOp
}

type dSite struct {
	entries map[string]dEntry
	clock   int64
	seq     int64
}

type dLink struct {
	up    bool
	queue []dChange
}

type dModel struct {
	n       int
	writers map[int]bool
	sites   []dSite
	floors  [][]int64
	links   [][]dLink
	stats   Stats
}

func newDModel(n int, writers []int) *dModel {
	writerSet := make(map[int]bool)
	for _, site := range writers {
		writerSet[site] = true
	}
	sites := make([]dSite, n+1)
	for idx := range sites {
		sites[idx] = dSite{entries: make(map[string]dEntry)}
	}
	floors := make([][]int64, n+1)
	for idx := range floors {
		floors[idx] = make([]int64, n+1)
	}
	links := make([][]dLink, n+1)
	for from := range links {
		links[from] = make([]dLink, n+1)
		for to := range links[from] {
			links[from][to].up = adjacent(n, from, to)
		}
	}
	return &dModel{n: n, writers: writerSet, sites: sites, floors: floors, links: links}
}

func dGreater(a, b dTriple) bool {
	if a.ts != b.ts {
		return a.ts > b.ts
	}
	if a.origin != b.origin {
		return a.origin > b.origin
	}
	return a.seq > b.seq
}

func (m *dModel) write(site int, key []byte, op change.Op, now int64) (WriteOutcome, error) {
	if site < 1 || site > m.n {
		return 0, ErrInvalidArgument
	}
	if len(key) == 0 || len(key) > 32 || !validOp(op) || now < 0 || now > 1_000_000_000_000 {
		return 0, ErrInvalidArgument
	}
	if !m.writers[site] {
		return 0, ErrNotWritable
	}
	writer := &m.sites[site]
	if now < writer.clock {
		return 0, ErrClockRollback
	}
	writer.seq++
	seq := writer.seq
	writer.clock = now
	m.floors[site][site] = seq
	generated := dChange{origin: site, seq: seq, ts: now, key: string(key), op: fromChangeOp(op)}
	outcome := WriteLost
	if m.apply(site, generated) {
		outcome = WriteApplied
	}
	for _, neighbor := range neighbors(m.n, site) {
		m.enqueue(site, neighbor, generated)
		m.stats.Enqueued++
	}
	return outcome, nil
}

func (m *dModel) deliver(from, to int) (DeliverOutcome, error) {
	if from < 1 || from > m.n || to < 1 || to > m.n || !adjacent(m.n, from, to) {
		return 0, ErrInvalidArgument
	}
	if !m.links[from][to].up {
		return 0, ErrDown
	}
	if len(m.links[from][to].queue) == 0 {
		return 0, ErrEmpty
	}
	generated := m.links[from][to].queue[0]
	m.links[from][to].queue = m.links[from][to].queue[1:]
	m.stats.Dequeued++
	floor := m.floors[to][generated.origin]
	if generated.seq <= floor {
		m.stats.Dup++
		return DeliverDup, nil
	}
	if generated.seq != floor+1 {
		panic("differential: sequence gap")
	}
	m.floors[to][generated.origin] = generated.seq
	outcome := DeliverLost
	if m.apply(to, generated) {
		outcome = DeliverApplied
	}
	m.enqueue(to, otherNeighbor(m.n, to, from), generated)
	m.stats.Forwarded++
	m.stats.Enqueued++
	return outcome, nil
}

func (m *dModel) apply(site int, generated dChange) bool {
	current, exists := m.sites[site].entries[generated.key]
	incoming := dEntry{
		triple: dTriple{ts: generated.ts, origin: generated.origin, seq: generated.seq},
		exists: true,
		del:    generated.op.kind == dDelete,
		value:  generated.op.value,
	}
	if exists && !dGreater(incoming.triple, current.triple) {
		return false
	}
	m.sites[site].entries[generated.key] = incoming
	return true
}

func (m *dModel) enqueue(from, to int, generated dChange) {
	m.links[from][to].queue = append(m.links[from][to].queue, generated)
}

func fromChangeOp(op change.Op) dOp {
	switch value := op.(type) {
	case change.Put:
		return dOp{kind: dPut, value: value.Value}
	case change.Delete:
		return dOp{kind: dDelete}
	default:
		panic("invalid op")
	}
}

type dAction struct {
	name string
	run  func(*Ring, *dModel) (string, error)
}

func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 2000-case differential test in short mode")
	}
	rng := rand.New(rand.NewSource(1332))
	const cases = 2000
	for iteration := 0; iteration < cases; iteration++ {
		n := 3 + rng.Intn(6)
		permuted := rng.Perm(n)
		writerCount := 1 + rng.Intn(n)
		writers := make([]int, 0, writerCount)
		for idx := 0; idx < writerCount; idx++ {
			writers = append(writers, permuted[idx]+1)
		}

		actual, err := New(n, writers)
		if err != nil {
			t.Fatal(err)
		}
		expected := newDModel(n, writers)
		clocks := make([]int64, n+1)
		actions := make([]dAction, 0, 24+rng.Intn(40))

		for step := 0; step < cap(actions); step++ {
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4, 5:
				site := writers[rng.Intn(len(writers))]
				clocks[site] += int64(rng.Intn(5))
				now := clocks[site]
				key := []byte(fmt.Sprintf("k%d", rng.Intn(5)))
				op := change.Put{Value: rng.Int63n(20) - 10}
				if rng.Intn(3) == 0 {
					op = change.Put{}
					if rng.Intn(2) == 0 {
						op = change.Put{Value: rng.Int63n(20) - 10}
					} else {
						key = []byte(fmt.Sprintf("d%d", rng.Intn(4)))
					}
				}
				var finalOp change.Op = op
				if rng.Intn(5) == 0 {
					finalOp = change.Del
				}
				name := fmt.Sprintf("Write(%d,%q,%v,%d)", site, key, finalOp, now)
				actions = append(actions, dAction{name: name, run: func(r *Ring, m *dModel) (string, error) {
					a, e1 := r.Write(site, key, finalOp, now)
					b, e2 := m.write(site, key, finalOp, now)
					return fmt.Sprintf("actual=(%v,%v) expected=(%v,%v)", a, e1, b, e2), compareWrite(a, b, e1, e2)
				}})
			case 6, 7, 8:
				from := 1 + rng.Intn(n)
				to := neighbors(n, from)[rng.Intn(2)]
				name := fmt.Sprintf("Deliver(%d,%d)", from, to)
				actions = append(actions, dAction{name: name, run: func(r *Ring, m *dModel) (string, error) {
					a, e1 := r.Deliver(from, to)
					b, e2 := m.deliver(from, to)
					return fmt.Sprintf("actual=(%v,%v) expected=(%v,%v)", a, e1, b, e2), compareDeliver(a, b, e1, e2)
				}})
			case 9:
				site := 1 + rng.Intn(n)
				key := []byte(fmt.Sprintf("k%d", rng.Intn(5)))
				name := fmt.Sprintf("Get(%d,%q)", site, key)
				actions = append(actions, dAction{name: name, run: func(r *Ring, m *dModel) (string, error) {
					entry, ok, err := r.Get(site, key)
					return fmt.Sprintf("entry=%+v exists=%v err=%v", entry, ok, err), nil
				}})
			}
		}

		for step, action := range actions {
			detail, err := action.run(actual, expected)
			t.Logf("case=%d step=%d input=%s output=%s basis=naive-rule-model", iteration, step, action.name, detail)
			if err != nil {
				t.Fatalf("case %d step %d %s: %v", iteration, step, action.name, err)
			}
			assertModelsEqual(t, actual, expected, iteration, step)
		}

		drainAll(t, actual)
		drainDModel(t, expected)
		assertModelsEqual(t, actual, expected, iteration, len(actions))
		assertConvergence(t, actual)
		writes := totalWrites(expected)
		if actual.stats.Dup != int64(2*writes) {
			t.Fatalf("case %d dup=%d, want %d", iteration, actual.stats.Dup, 2*writes)
		}
		if actual.stats.Dequeued != int64((n+1)*writes) {
			t.Fatalf("case %d dequeued=%d, want %d", iteration, actual.stats.Dequeued, (n+1)*writes)
		}
	}
}

func compareWrite(a, b WriteOutcome, actualErr, expectedErr error) error {
	if err := compareError(actualErr, expectedErr); err != nil {
		return err
	}
	if actualErr == nil && a != b {
		return fmt.Errorf("write outcome %v != %v", a, b)
	}
	return nil
}

func compareDeliver(a, b DeliverOutcome, actualErr, expectedErr error) error {
	if err := compareError(actualErr, expectedErr); err != nil {
		return err
	}
	if actualErr == nil && a != b {
		return fmt.Errorf("deliver outcome %v != %v", a, b)
	}
	return nil
}

func compareError(a, b error) error {
	if (a == nil) != (b == nil) {
		return fmt.Errorf("error %v != %v", a, b)
	}
	if a != nil && a.Error() != b.Error() {
		return fmt.Errorf("error %v != %v", a, b)
	}
	return nil
}

func drainDModel(t *testing.T, m *dModel) (dequeued int, dup int) {
	t.Helper()
	for progress := true; progress; {
		progress = false
		for from := 1; from <= m.n; from++ {
			for _, to := range neighbors(m.n, from) {
				if len(m.links[from][to].queue) == 0 {
					continue
				}
				outcome, err := m.deliver(from, to)
				if err != nil {
					t.Fatal(err)
				}
				dequeued++
				progress = true
				if outcome == DeliverDup {
					dup++
				}
			}
		}
	}
	return dequeued, dup
}

func totalWrites(m *dModel) int {
	var total int
	for site := 1; site <= m.n; site++ {
		total += int(m.sites[site].seq)
	}
	return total
}

func assertConvergence(t *testing.T, r *Ring) {
	t.Helper()
	for origin := 1; origin <= r.n; origin++ {
		want, err := r.Floor(origin, origin)
		if err != nil {
			t.Fatal(err)
		}
		for site := 1; site <= r.n; site++ {
			got, err := r.Floor(site, origin)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("floor site=%d origin=%d got=%d want=%d", site, origin, got, want)
			}
		}
	}
}

func assertModelsEqual(t *testing.T, r *Ring, m *dModel, iteration, step int) {
	t.Helper()
	if r.stats != m.stats {
		t.Fatalf("case %d step %d stats actual=%+v expected=%+v", iteration, step, r.stats, m.stats)
	}
	if r.n != m.n {
		t.Fatalf("case %d step %d n differs", iteration, step)
	}
	for site := 1; site <= r.n; site++ {
		actualSite := r.sites[site]
		expectedSite := m.sites[site]
		if actualSite.clock != expectedSite.clock || actualSite.writeQ != expectedSite.seq {
			t.Fatalf("case %d step %d site %d metadata actual=(clock=%d q=%d) expected=(clock=%d q=%d)",
				iteration, step, site, actualSite.clock, actualSite.writeQ, expectedSite.clock, expectedSite.seq)
		}
		for origin := 1; origin <= r.n; origin++ {
			if r.floors.Floor(site, origin) != m.floors[site][origin] {
				t.Fatalf("case %d step %d floor site=%d origin=%d actual=%d expected=%d",
					iteration, step, site, origin, r.floors.Floor(site, origin), m.floors[site][origin])
			}
		}
		keys := make(map[string]bool)
		for key := range actualSite.table.Records() {
			keys[key] = true
		}
		for key := range expectedSite.entries {
			keys[key] = true
		}
		for key := range keys {
			actualEntry, actualOK := actualSite.table.Get([]byte(key))
			expectedEntry, expectedOK := expectedSite.entries[key]
			if actualOK != expectedOK {
				t.Fatalf("case %d step %d site=%d key=%q existence actual=%v expected=%v",
					iteration, step, site, key, actualOK, expectedOK)
			}
			if actualOK && (actualEntry.Value != expectedEntry.value ||
				actualEntry.Delete != expectedEntry.del ||
				actualEntry.Exists != expectedEntry.exists ||
				actualEntry.Triple.TS != expectedEntry.triple.ts ||
				actualEntry.Triple.Origin != expectedEntry.triple.origin ||
				actualEntry.Triple.Seq != expectedEntry.triple.seq) {
				t.Fatalf("case %d step %d site=%d key=%q entry actual=%+v expected=%+v",
					iteration, step, site, key, actualEntry, expectedEntry)
			}
		}
	}
	for from := 1; from <= r.n; from++ {
		for _, to := range neighbors(r.n, from) {
			actualLink := &r.links[from][to]
			expectedLink := &m.links[from][to]
			if actualLink.up != expectedLink.up {
				t.Fatalf("case %d step %d link %d->%d up actual=%v expected=%v",
					iteration, step, from, to, actualLink.up, expectedLink.up)
			}
			if len(actualLink.queue) != len(expectedLink.queue) {
				t.Fatalf("case %d step %d link %d->%d length actual=%d expected=%d",
					iteration, step, from, to, len(actualLink.queue), len(expectedLink.queue))
			}
			for idx := range actualLink.queue {
				if !sameAsDChange(actualLink.queue[idx], expectedLink.queue[idx]) {
					t.Fatalf("case %d step %d link %d->%d position %d actual=%+v expected=%+v",
						iteration, step, from, to, idx, actualLink.queue[idx], expectedLink.queue[idx])
				}
			}
		}
	}
}

func sameAsDChange(actual change.Change, expected dChange) bool {
	return actual.Origin == expected.origin &&
		actual.Seq == expected.seq &&
		actual.TS == expected.ts &&
		string(actual.Key) == expected.key &&
		sameOpAsDOp(actual.Op, expected.op)
}

func sameOpAsDOp(actual change.Op, expected dOp) bool {
	switch value := actual.(type) {
	case change.Put:
		return expected.kind == dPut && value.Value == expected.value
	case change.Delete:
		return expected.kind == dDelete
	default:
		return false
	}
}

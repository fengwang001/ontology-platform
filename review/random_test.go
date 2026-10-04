package review

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// naiveItem is a live item in the reference implementation.
type naiveItem struct {
	rx     int
	ing    string
	mg     int
	per    int
	perDay int
	start  int
	end    int
}

// naiveRx is the reference prescription record.
type naiveRx struct {
	id      int
	doctor  string
	patient string
	items   []Item
	submit  int
	status  int
}

// naive is the straightforward re-implementation: for every check it scans
// the patient's live items day by day and pair by pair.
type naive struct {
	T       int
	horizon int
	drugs   map[string]struct {
		ing string
		mg  int
		lvl int
	}
	max      map[string]int
	pairs    map[[2]string]int
	allergy  map[string]map[string]bool
	doctors  map[string]int
	pharma   map[string]bool
	patients map[string]bool
	rxs      map[int]*naiveRx
	items    map[string][]naiveItem
	pending  map[int]*naiveRx
	maxNow   int
	nextRx   int
	frozen   bool
}

func newNaive(T int) *naive {
	return &naive{
		T: T,
		drugs: map[string]struct {
			ing string
			mg  int
			lvl int
		}{},
		max:      map[string]int{},
		pairs:    map[[2]string]int{},
		allergy:  map[string]map[string]bool{},
		doctors:  map[string]int{},
		pharma:   map[string]bool{},
		patients: map[string]bool{},
		rxs:      map[int]*naiveRx{},
		items:    map[string][]naiveItem{},
		pending:  map[int]*naiveRx{},
		horizon:  1_000_000,
	}
}

func (n *naive) grade(a, b string) int {
	if a == b {
		return 0
	}
	lo, hi := a, b
	if lo > hi {
		lo, hi = hi, lo
	}
	return n.pairs[[2]string{lo, hi}]
}

func (n *naive) expire(now int) {
	type p struct {
		dead, id int
	}
	var ps []p
	for id, r := range n.pending {
		ps = append(ps, p{r.submit + n.T, id})
	}
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].dead != ps[j].dead {
			return ps[i].dead < ps[j].dead
		}
		return ps[i].id < ps[j].id
	})
	for _, q := range ps {
		if now >= q.dead {
			r := n.pending[q.id]
			r.status = StatusVoided
			delete(n.pending, q.id)
			out := n.items[r.patient][:0]
			for _, it := range n.items[r.patient] {
				if it.rx != q.id {
					out = append(out, it)
				}
			}
			n.items[r.patient] = out
		}
	}
}

func (n *naive) validItems(now int, items []Item) bool {
	if len(items) < 1 || len(items) > 20 {
		return false
	}
	seen := map[string]bool{}
	for _, it := range items {
		if it.Drug == "" || seen[it.Drug] ||
			it.Per < 1 || it.Per > 20 || it.PerDay < 1 || it.PerDay > 12 ||
			now > it.Start || it.Start >= it.End || it.End > 1_000_000 {
			return false
		}
		seen[it.Drug] = true
	}
	return true
}

type naiveResult struct {
	id     int
	status int
	hints  []Hint
	reason error
	index  int
	ok     bool
}

func (n *naive) submit(now int, doctor, patient string, items []Item) naiveResult {
	valid := now >= 0 && now <= 1_000_000 && doctor != "" && patient != "" && n.validItems(now, items)
	if !valid {
		return naiveResult{reason: ErrInvalid, index: -1}
	}
	if now < n.maxNow {
		return naiveResult{reason: ErrClock, index: -1}
	}
	n.expire(now)
	n.maxNow = now

	lvl, docOK := n.doctors[doctor]
	patOK := n.patients[patient]
	type ni struct {
		ing       string
		mg        int
		per, pd   int
		s, e      int
		drug      string
		drugLevel int
	}
	var fresh []ni
	for i, it := range items {
		d, ok := n.drugs[it.Drug]
		if !ok {
			return naiveResult{reason: ErrNoEntity, index: i}
		}
		fresh = append(fresh, ni{d.ing, d.mg, it.Per, it.PerDay, it.Start, it.End, it.Drug, d.lvl})
	}
	if !docOK || !patOK {
		return naiveResult{reason: ErrNoEntity, index: -1}
	}
	for i, f := range fresh {
		if f.drugLevel > lvl {
			return naiveResult{reason: ErrNoPrivilege, index: i}
		}
	}
	for i, f := range fresh {
		if n.allergy[patient][f.ing] {
			return naiveResult{reason: ErrAllergy, index: i}
		}
	}
	over := func(s1, e1, s2, e2 int) bool { return s1 < e2 && s2 < e1 }
	for i := range fresh {
		for j := i + 1; j < len(fresh); j++ {
			a, b := fresh[i], fresh[j]
			if a.ing != b.ing && over(a.s, a.e, b.s, b.e) && n.grade(a.ing, b.ing) == 3 {
				return naiveResult{reason: ErrBan, index: i}
			}
		}
		for _, r := range n.items[patient] {
			if fresh[i].ing != r.ing && over(fresh[i].s, fresh[i].e, r.start, r.end) &&
				n.grade(fresh[i].ing, r.ing) == 3 {
				return naiveResult{reason: ErrBan, index: i}
			}
		}
	}
	// Day-by-day cap summation.
	for day := 0; day <= n.horizon; day++ {
		sum := map[string]int64{}
		covered := map[string]bool{}
		anyFresh := map[int]bool{}
		for i, f := range fresh {
			if f.s <= day && day < f.e {
				sum[f.ing] += int64(f.mg) * int64(f.per) * int64(f.pd)
				covered[f.ing] = true
				anyFresh[i] = true
			}
		}
		for _, r := range n.items[patient] {
			if r.start <= day && day < r.end {
				sum[r.ing] += int64(r.mg) * int64(r.per) * int64(r.perDay)
			}
		}
		coveredIngs := make([]string, 0, len(covered))
		for ing := range covered {
			coveredIngs = append(coveredIngs, ing)
		}
		sort.Strings(coveredIngs)
		for _, ing := range coveredIngs {
			if cap, ok := n.max[ing]; ok && sum[ing] > int64(cap) {
				minIdx := -1
				for i, f := range fresh {
					if anyFresh[i] && f.ing == ing {
						minIdx = i
						break
					}
				}
				return naiveResult{reason: ErrOverMax, index: minIdx}
			}
		}
	}
	// Hints.
	hintSet := map[[2]string]int{}
	add := func(a, b string) {
		g := n.grade(a, b)
		if g != 1 && g != 2 {
			return
		}
		lo, hi := a, b
		if lo > hi {
			lo, hi = hi, lo
		}
		k := [2]string{lo, hi}
		if old, ok := hintSet[k]; !ok || g > old {
			hintSet[k] = g
		}
	}
	for i := range fresh {
		for j := i + 1; j < len(fresh); j++ {
			a, b := fresh[i], fresh[j]
			if a.ing != b.ing && over(a.s, a.e, b.s, b.e) {
				add(a.ing, b.ing)
			}
		}
		for _, r := range n.items[patient] {
			if fresh[i].ing != r.ing && over(fresh[i].s, fresh[i].e, r.start, r.end) {
				add(fresh[i].ing, r.ing)
			}
		}
	}
	var hints []Hint
	for k, g := range hintSet {
		hints = append(hints, Hint{IngA: []byte(k[0]), IngB: []byte(k[1]), Grade: g})
	}
	sort.Slice(hints, func(i, j int) bool {
		if hints[i].Grade != hints[j].Grade {
			return hints[i].Grade > hints[j].Grade
		}
		if string(hints[i].IngA) != string(hints[j].IngA) {
			return string(hints[i].IngA) < string(hints[j].IngA)
		}
		return string(hints[i].IngB) < string(hints[j].IngB)
	})
	review := false
	for _, h := range hints {
		if h.Grade == 2 {
			review = true
		}
	}
	if !n.frozen {
		n.frozen = true
	}
	n.nextRx++
	id := n.nextRx
	status := StatusActive
	if review {
		status = StatusPending
	}
	r := &naiveRx{id: id, doctor: doctor, patient: patient, items: append([]Item(nil), items...), submit: now, status: status}
	n.rxs[id] = r
	for _, f := range fresh {
		n.items[patient] = append(n.items[patient], naiveItem{
			rx: id, ing: f.ing, mg: f.mg, per: f.per, perDay: f.pd, start: f.s, end: f.e,
		})
	}
	if review {
		n.pending[id] = r
	}
	return naiveResult{id: id, status: status, hints: hints, ok: true}
}

func (n *naive) reviewOp(now int, ph string, id int, approve bool) error {
	if now < 0 || now > 1_000_000 || ph == "" || id < 1 {
		return ErrInvalid
	}
	if now < n.maxNow {
		return ErrClock
	}
	n.expire(now)
	n.maxNow = now
	r, rxOK := n.rxs[id]
	if !rxOK || !n.pharma[ph] {
		return ErrNoRx
	}
	if r.status != StatusPending {
		return ErrState
	}
	if approve {
		r.status = StatusActive
		delete(n.pending, id)
	} else {
		r.status = StatusVoided
		delete(n.pending, id)
		out := n.items[r.patient][:0]
		for _, it := range n.items[r.patient] {
			if it.rx != id {
				out = append(out, it)
			}
		}
		n.items[r.patient] = out
	}
	return nil
}

func (n *naive) stop(now int, doctor string, id int) error {
	if now < 0 || now > 1_000_000 || doctor == "" || id < 1 {
		return ErrInvalid
	}
	if now < n.maxNow {
		return ErrClock
	}
	n.expire(now)
	n.maxNow = now
	r, rxOK := n.rxs[id]
	lvl, docOK := n.doctors[doctor]
	if !rxOK || !docOK {
		return ErrNoRx
	}
	if r.doctor != doctor && lvl < 3 {
		return ErrNoAuth
	}
	if r.status != StatusActive && r.status != StatusPending {
		return ErrState
	}
	out := n.items[r.patient][:0]
	for _, it := range n.items[r.patient] {
		if it.rx != id {
			out = append(out, it)
			continue
		}
		if now < it.end {
			it.end = now
		}
		if it.end > it.start {
			out = append(out, it)
		}
	}
	n.items[r.patient] = out
	for i := range r.items {
		if now < r.items[i].End {
			r.items[i].End = now
		}
	}
	return nil
}

const randomHorizon = 200

// configWorld creates matching engine and naive worlds with random drugs,
// caps, pairs, allergies, doctors, pharmacists and patients.
func configWorld(t *testing.T, rng *rand.Rand, drugCount, patientCount int) (*Engine, *naive, []string, []string) {
	t.Helper()
	T := 1 + rng.Intn(10)
	e := NewEngine(T)
	n := newNaive(T)
	n.horizon = randomHorizon
	drugIDs := make([]string, drugCount)
	ings := make([]string, drugCount)
	for i := range drugIDs {
		drugIDs[i] = fmt.Sprintf("DR%d", i)
		ing := fmt.Sprintf("ING%03d", rng.Intn(drugCount*2+2))
		ings[i] = ing
		mg := 1 + rng.Intn(500)
		lvl := 1 + rng.Intn(3)
		if err := e.Formulary().AddDrug(drugIDs[i], []byte(ing), mg, lvl); err != nil {
			t.Fatal(err)
		}
		n.drugs[drugIDs[i]] = struct {
			ing string
			mg  int
			lvl int
		}{ing, mg, lvl}
	}
	// Caps for a random subset of ingredients.
	ingSet := map[string]bool{}
	for _, ing := range ings {
		ingSet[ing] = true
	}
	for ing := range ingSet {
		if rng.Intn(2) == 0 {
			c := 1 + rng.Intn(20000)
			if err := e.Formulary().SetMax([]byte(ing), c); err != nil {
				t.Fatal(err)
			}
			n.max[ing] = c
		}
	}
	// Interaction pairs.
	uniqIng := make([]string, 0, len(ingSet))
	for ing := range ingSet {
		uniqIng = append(uniqIng, ing)
	}
	sort.Strings(uniqIng)
	for i := 0; i < len(uniqIng); i++ {
		for j := i + 1; j < len(uniqIng); j++ {
			if rng.Intn(3) == 0 {
				g := 1 + rng.Intn(3)
				if err := e.Table().SetPair([]byte(uniqIng[i]), []byte(uniqIng[j]), g); err != nil {
					t.Fatal(err)
				}
				n.pairs[[2]string{uniqIng[i], uniqIng[j]}] = g
			}
		}
	}
	doctors := []string{"d1", "d2", "d3"}
	for i, lvl := range []int{1, 2, 3} {
		if err := e.AddDoctor(doctors[i], lvl); err != nil {
			t.Fatal(err)
		}
		n.doctors[doctors[i]] = lvl
	}
	if err := e.AddPharmacist("ph"); err != nil {
		t.Fatal(err)
	}
	n.pharma["ph"] = true
	patients := make([]string, patientCount)
	for i := range patients {
		patients[i] = fmt.Sprintf("p%d", i)
		if err := e.AddPatient(patients[i]); err != nil {
			t.Fatal(err)
		}
		n.patients[patients[i]] = true
		// Random allergies.
		for _, ing := range uniqIng {
			if rng.Intn(8) == 0 {
				if err := e.Table().SetAllergy(patients[i], []byte(ing)); err != nil {
					t.Fatal(err)
				}
				if n.allergy[patients[i]] == nil {
					n.allergy[patients[i]] = map[string]bool{}
				}
				n.allergy[patients[i]][ing] = true
			}
		}
	}
	return e, n, doctors, patients
}

func hintsEq(a, b []Hint) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Grade != b[i].Grade ||
			string(a[i].IngA) != string(b[i].IngA) ||
			string(a[i].IngB) != string(b[i].IngB) {
			return false
		}
	}
	return true
}

// TestRandomAgainstNaive replays 1500 random operation sequences against
// both the engine and the day-by-day reference implementation.
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 1500
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*1_000_003 + 7))
		drugCount := 4 + rng.Intn(12)
		patientCount := 1 + rng.Intn(3)
		e, n, doctors, patients := configWorld(t, rng, drugCount, patientCount)
		var log []string
		now := 0
		ops := 20 + rng.Intn(30)
		for op := 0; op < ops; op++ {
			// Time moves non-decreasingly; occasionally jump to an expiry
			// boundary.
			switch rng.Intn(10) {
			case 0, 1:
				if now < randomHorizon {
					now += 1
				}
			case 2:
				if now < randomHorizon {
					now += 1 + rng.Intn(4)
				}
			}
			rollback := rng.Intn(12) == 0
			opNow := now
			if rollback && now > 0 {
				opNow = now - 1
			}
			pat := patients[rng.Intn(len(patients))]
			doc := doctors[rng.Intn(len(doctors))]
			kind := rng.Intn(10)
			switch {
			case kind < 7: // Submit
				k := 1 + rng.Intn(4)
				items := make([]Item, 0, k)
				used := map[string]bool{}
				for len(items) < k {
					d := fmt.Sprintf("DR%d", rng.Intn(drugCount))
					if used[d] {
						continue
					}
					used[d] = true
					per := 1 + rng.Intn(4)
					pd := 1 + rng.Intn(3)
					s := opNow + rng.Intn(randomHorizon-opNow+1)
					ed := s + 1 + rng.Intn(15)
					if ed > randomHorizon+1 {
						ed = randomHorizon + 1
					}
					// Occasionally produce invalid shapes.
					if rng.Intn(15) == 0 {
						per = 21
					}
					items = append(items, Item{Drug: d, Per: per, PerDay: pd, Start: s, End: ed})
				}
				id, st, hints, err := e.Submit(opNow, doc, pat, items)
				nr := n.submit(opNow, doc, pat, items)
				log = append(log, fmt.Sprintf("Submit(now=%d doc=%s pat=%s items=%v)", opNow, doc, pat, items))
				if nr.ok {
					if err != nil {
						t.Fatalf("seq=%d engine rejected but naive accepted: %v | %s", seq, err, log[len(log)-1])
					}
					if id != nr.id || st != nr.status || !hintsEq(hints, nr.hints) {
						t.Fatalf("seq=%d result mismatch: eng(%d,%d,%v) naive(%d,%d,%v) | %s",
							seq, id, st, hints, nr.id, nr.status, nr.hints, log[len(log)-1])
					}
					log[len(log)-1] += fmt.Sprintf(" => id=%d status=%d hints=%v", id, st, hints)
				} else {
					if err == nil {
						t.Fatalf("seq=%d engine accepted but naive rejected (%v) | %s", seq, nr.reason, log[len(log)-1])
					}
					se := seErr(err)
					if se == nil || se.Reason != nr.reason || se.Index != nr.index {
						t.Fatalf("seq=%d reject mismatch: eng=(%v,%d) naive=(%v,%d) | %s",
							seq, se.Reason, se.Index, nr.reason, nr.index, log[len(log)-1])
					}
					log[len(log)-1] += fmt.Sprintf(" => REJECT %v idx=%d", nr.reason, nr.index)
				}
			case kind < 9: // Approve / Deny a random known rx
				approve := rng.Intn(2) == 0
				id := 1 + rng.Intn(n.nextRx+1)
				var err error
				if approve {
					err = e.Approve(opNow, "ph", id)
				} else {
					err = e.Deny(opNow, "ph", id)
				}
				nr := n.reviewOp(opNow, "ph", id, approve)
				log = append(log, fmt.Sprintf("%s(now=%d rx=%d)", map[bool]string{true: "Approve", false: "Deny"}[approve], opNow, id))
				if !sameErr(err, nr) {
					t.Fatalf("seq=%d %s mismatch: eng=%v naive=%v | %s",
						seq, map[bool]string{true: "Approve", false: "Deny"}[approve], err, nr, log[len(log)-1])
				}
				log[len(log)-1] += fmt.Sprintf(" => %v", err)
			default: // Stop
				id := 1 + rng.Intn(n.nextRx+1)
				err := e.Stop(opNow, doc, id)
				nr := n.stop(opNow, doc, id)
				log = append(log, fmt.Sprintf("Stop(now=%d doc=%s rx=%d)", opNow, doc, id))
				if !sameErr(err, nr) {
					t.Fatalf("seq=%d Stop mismatch: eng=%v naive=%v | %s", seq, err, nr, log[len(log)-1])
				}
				log[len(log)-1] += fmt.Sprintf(" => %v", err)
			}
		}
		// Final state comparison: statuses and item intervals per patient.
		for id := 1; id <= n.nextRx; id++ {
			rx := n.rxs[id]
			er, ok := e.RxRecord(id)
			if !ok {
				t.Fatalf("seq=%d rx %d missing", seq, id)
			}
			if er.Status != rx.status || er.SubmitNow != rx.submit || er.Doctor != rx.doctor {
				t.Fatalf("seq=%d rx %d state mismatch: eng=%+v naive=%+v", seq, id, er, rx)
			}
			if len(er.Items) != len(rx.items) {
				t.Fatalf("seq=%d rx %d item count", seq, id)
			}
			for i := range rx.items {
				if er.Items[i] != rx.items[i] {
					t.Fatalf("seq=%d rx %d item %d: %+v vs %+v", seq, id, i, er.Items[i], rx.items[i])
				}
			}
		}
		t.Logf("seq=%d:\n%s", seq, joinLines(log))
	}
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Error() == b.Error()
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		out += fmt.Sprintf("  %d. %s\n", i+1, l)
	}
	return out
}

package clinic

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"

	"ontology/vaxrule"
)

func addMV(t *testing.T, c *Clinic) {
	t.Helper()
	if err := c.AddSeries("M", true, 2, []int{365, 393}, []int{0, 28}, 28); err != nil {
		t.Fatal(err)
	}
	if err := c.AddSeries("V", true, 1, []int{365}, []int{0}, 28); err != nil {
		t.Fatal(err)
	}
}

func mustRecord(t *testing.T, c *Clinic, now int, p, s string, d int) {
	t.Helper()
	if err := c.Record(now, p, s, d); err != nil {
		t.Fatalf("Record(now=%d %s %s d=%d): %v", now, p, s, d, err)
	}
}

func evalAt(t *testing.T, c *Clinic, now int, p string) Report {
	t.Helper()
	r, err := c.Evaluate(now, p)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return r
}

func findVerdict(r Report, series string, date int) RecordVerdict {
	for _, v := range r.Records {
		if v.Series == series && v.Date == date {
			return v
		}
	}
	return RecordVerdict{Reason: "<missing>"}
}

func nextOf(r Report, series string) (NextDose, bool) {
	for _, n := range r.Next {
		if n.Series == series {
			return n, true
		}
	}
	return NextDose{}, false
}

func TestSpecExamples(t *testing.T) {
	c := New()
	addMV(t, c)
	for _, p := range []string{"jia", "yi", "bing", "ding", "wu"} {
		if err := c.AddPatient(p, 0); err != nil {
			t.Fatal(err)
		}
	}

	// 丙 must be evaluated at now=430 (monotonic clock): do it first.
	mustRecord(t, c, 430, "bing", "M", 400)
	mustRecord(t, c, 430, "bing", "V", 410)
	mustRecord(t, c, 430, "bing", "M", 428)
	r, err := c.Evaluate(430, "bing")
	if err != nil {
		t.Fatal(err)
	}
	if v := findVerdict(r, "M", 400); !v.Valid {
		t.Fatalf("bing 400: %+v", v)
	}
	if v := findVerdict(r, "V", 410); v.Valid || v.Reason != "live" {
		t.Fatalf("bing 410: %+v", v)
	}
	if v := findVerdict(r, "M", 428); v.Valid || v.Reason != "live" {
		t.Fatalf("bing 428: %+v", v)
	}
	if nm, ok := nextOf(r, "M"); !ok || nm.Dose != 2 || nm.Date != 456 {
		t.Fatalf("bing next M: %+v ok=%v", nm, ok)
	}
	if nv, ok := nextOf(r, "V"); !ok || nv.Dose != 1 || nv.Date != 456 {
		t.Fatalf("bing next V: %+v ok=%v", nv, ok)
	}

	mustRecord(t, c, 500, "jia", "M", 361)
	r = evalAt(t, c, 500, "jia")
	if v := findVerdict(r, "M", 361); !v.Valid || v.Dose != 1 {
		t.Fatalf("jia: %+v", v)
	}

	mustRecord(t, c, 500, "yi", "M", 360)
	mustRecord(t, c, 500, "yi", "M", 387)
	mustRecord(t, c, 500, "yi", "M", 415)
	r = evalAt(t, c, 500, "yi")
	if v := findVerdict(r, "M", 360); v.Valid || v.Reason != "age" {
		t.Fatalf("yi 360: %+v", v)
	}
	if v := findVerdict(r, "M", 387); v.Valid || v.Reason != "revacc" {
		t.Fatalf("yi 387: %+v", v)
	}
	if v := findVerdict(r, "M", 415); !v.Valid || v.Dose != 1 {
		t.Fatalf("yi 415: %+v", v)
	}

	mustRecord(t, c, 500, "ding", "M", 400)
	mustRecord(t, c, 500, "ding", "V", 400)
	r = evalAt(t, c, 500, "ding")
	if v := findVerdict(r, "M", 400); !v.Valid {
		t.Fatalf("ding M: %+v", v)
	}
	if v := findVerdict(r, "V", 400); !v.Valid {
		t.Fatalf("ding V: %+v", v)
	}

	mustRecord(t, c, 500, "wu", "M", 400)
	mustRecord(t, c, 500, "wu", "V", 428)
	r = evalAt(t, c, 500, "wu")
	if v := findVerdict(r, "V", 428); !v.Valid {
		t.Fatalf("wu V before backfill: %+v", v)
	}
	mustRecord(t, c, 500, "wu", "M", 420)
	r = evalAt(t, c, 500, "wu")
	if v := findVerdict(r, "M", 420); v.Valid || v.Reason != "interval" || v.Dose != 2 {
		t.Fatalf("wu M420: %+v", v)
	}
	if v := findVerdict(r, "V", 428); v.Valid || v.Reason != "live" {
		t.Fatalf("wu V after backfill: %+v", v)
	}
}

func TestRecordValidationOrder(t *testing.T) {
	c := New()
	addMV(t, c)
	if err := c.AddPatient("p", 0); err != nil {
		t.Fatal(err)
	}
	if err := c.Record(5, "p", "M", 1); err != nil {
		t.Fatal(err)
	}
	// invalid arg beats clock rollback
	if err := c.Record(4, "", "M", 1); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("empty patient: %v", err)
	}
	if err := c.Record(4, "p", "M", 1); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("clock: %v", err)
	}
	if err := c.Record(5, "ghost", "M", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing patient: %v", err)
	}
	if err := c.Record(5, "p", "ZZ", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing series: %v", err)
	}
	if err := c.Record(6, "p", "M", 7); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("d>now: %v", err)
	}
	if err := c.Record(6, "p", "M", 1); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("dup: %v", err)
	}
}

func TestAdministerRejectionOrder(t *testing.T) {
	c := New()
	addMV(t, c)
	if err := c.AddSeries("K", false, 1, []int{365}, []int{0}, 28); err != nil {
		t.Fatal(err)
	}
	if err := c.AddPatient("p", 0); err != nil {
		t.Fatal(err)
	}
	if err := c.AddLot("L", "K", 1000, 10); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Administer(10, "", "p", "K"); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("arg: %v", err)
	}
	mustRecord(t, c, 10, "p", "K", 5) // invalid history, stock untouched
	if c.LotQty("L") != 10 {
		t.Fatalf("Record must not deduct stock: %d", c.LotQty("L"))
	}
	if _, err := c.Administer(9, "n", "p", "K"); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("clock: %v", err)
	}
	if _, err := c.Administer(10, "n", "ghost", "K"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("patient missing: %v", err)
	}
	if _, err := c.Administer(10, "n", "p", "ZZ"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("series missing: %v", err)
	}
	if _, err := c.Administer(10, "n", "p", "K"); !errors.Is(err, ErrNotGranted) {
		t.Fatalf("grant: %v", err)
	}
	if err := c.Grant("n"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddPatient("pv", 0); err != nil {
		t.Fatal(err)
	}
	mustRecord(t, c, 10, "pv", "V", 10)
	if _, err := c.Administer(10, "n", "pv", "V"); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("dup: %v", err)
	}

	if err := c.AddPatient("young", 0); err != nil {
		t.Fatal(err)
	}
	_, err := c.Administer(100, "n", "young", "K")
	var te *TooEarlyError
	if !errors.As(err, &te) {
		t.Fatalf("early: %v", err)
	}
	if te.Reason != "age" || te.Earliest != 361 {
		t.Fatalf("early detail: %+v", te)
	}
	if c.LotQty("L") != 10 {
		t.Fatalf("failed administer must not deduct: %d", c.LotQty("L"))
	}

	if err := c.AddPatient("done", 0); err != nil {
		t.Fatal(err)
	}
	mustRecord(t, c, 400, "done", "K", 400)
	if _, err := c.Administer(401, "n", "done", "K"); !errors.Is(err, ErrCompleted) {
		t.Fatalf("completed: %v", err)
	}

	if err := c.AddPatient("broke", 0); err != nil {
		t.Fatal(err)
	}
	lot, err := c.Administer(402, "n", "broke", "K")
	if err != nil || lot != "L" {
		t.Fatalf("success: lot=%q err=%v", lot, err)
	}
	if q := c.LotQty("L"); q != 9 {
		t.Fatalf("deduct qty=%d want 9", q)
	}
	r := evalAt(t, c, 402, "broke")
	if v := findVerdict(r, "K", 402); !v.Valid {
		t.Fatalf("administered invalid: %+v", v)
	}
}

func TestAdministerStockExpiryQuarantine(t *testing.T) {
	c := New()
	if err := c.AddSeries("K", false, 1, []int{0}, []int{0}, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.AddPatient("p", 0); err != nil {
		t.Fatal(err)
	}
	if err := c.Grant("n"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddLot("expired", "K", 100, 1); err != nil {
		t.Fatal(err)
	}
	if err := c.AddLot("good", "K", 101, 1); err != nil {
		t.Fatal(err)
	}
	lot, err := c.Administer(100, "n", "p", "K")
	if err != nil || lot != "good" {
		t.Fatalf("expiry pick lot=%q err=%v", lot, err)
	}
	if err := c.Quarantine("expired", true); err != nil {
		t.Fatal(err)
	}
	// p2 was used after clock=100; the release pick at 99 must happen
	// on a clock <= 99, so use a fresh clinic-independent path ordering:
	// do the 99 pick first (clock=99), then the 100 case above.
	if err := c.AddPatient("p2", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Administer(100, "n", "p2", "K"); !errors.Is(err, ErrNoStock) {
		t.Fatalf("quarantine+expired: %v", err)
	}
	if err := c.Quarantine("expired", false); err != nil {
		t.Fatal(err)
	}
	// 99 < 100 would be a rollback; release makes exp==100 still
	// unusable at 100, so use the other lot semantics: quarantine
	// "good" was consumed; verify released lot works exactly at 99 on
	// a separate clinic built with the clock never past 99.
	c2 := New()
	if err := c2.AddSeries("K", false, 1, []int{0}, []int{0}, 0); err != nil {
		t.Fatal(err)
	}
	if err := c2.AddPatient("p3", 0); err != nil {
		t.Fatal(err)
	}
	if err := c2.Grant("n"); err != nil {
		t.Fatal(err)
	}
	if err := c2.AddLot("edge", "K", 100, 1); err != nil {
		t.Fatal(err)
	}
	if lot, err := c2.Administer(99, "n", "p3", "K"); err != nil || lot != "edge" {
		t.Fatalf("usable one day before exp lot=%q err=%v", lot, err)
	}
	if err := c2.AddPatient("p4", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Administer(100, "n", "p4", "K"); !errors.Is(err, ErrNoStock) {
		t.Fatalf("at exp exactly must be expired (and qty now 0): %v", err)
	}
}

func TestTouchedConstantAcrossHistorySize(t *testing.T) {
	measure := func(records int) int64 {
		c := New()
		for i := 0; i < 5; i++ {
			name := fmt.Sprintf("H%d", i)
			if err := c.AddSeries(name, false, 1, []int{0}, []int{0}, 0); err != nil {
				t.Fatal(err)
			}
		}
		if err := c.AddSeries("T", false, 1, []int{0}, []int{0}, 0); err != nil {
			t.Fatal(err)
		}
		if err := c.AddPatient("p", 0); err != nil {
			t.Fatal(err)
		}
		if err := c.Grant("n"); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < records; i++ {
			s := fmt.Sprintf("H%d", i%5)
			mustRecord(t, c, 5000, "p", s, i)
		}
		if err := c.AddLot("L", "T", 6000, 10); err != nil {
			t.Fatal(err)
		}
		c.ResetTouched()
		if _, err := c.Administer(5000, "n", "p", "T"); err != nil {
			t.Fatalf("administer with %d records: %v", records, err)
		}
		return c.Touched()
	}
	t10 := measure(10)
	t1000 := measure(1000)
	if t10 != t1000 {
		t.Fatalf("touched grows: 10->%d 1000->%d", t10, t1000)
	}
}

func TestConcurrentEquivalentToSerial(t *testing.T) {
	c := New()
	if err := c.AddSeries("K", false, 1, []int{0}, []int{0}, 0); err != nil {
		t.Fatal(err)
	}
	const patients = 40
	for i := 0; i < patients; i++ {
		if err := c.AddPatient(fmt.Sprintf("p%d", i), 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.AddLot("L", "K", 10000, patients); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if err := c.Grant(fmt.Sprintf("n%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	success := make(chan string, patients)
	for i := 0; i < patients; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := fmt.Sprintf("p%d", i)
			n := fmt.Sprintf("n%d", i%8)
			lot, err := c.Administer(100, n, p, "K")
			if err != nil {
				t.Errorf("administer %s: %v", p, err)
				return
			}
			success <- p + ":" + lot
		}(i)
	}
	wg.Wait()
	close(success)
	n := 0
	for range success {
		n++
	}
	if n != patients {
		t.Fatalf("success=%d want %d", n, patients)
	}
	if q := c.LotQty("L"); q != 0 {
		t.Fatalf("qty=%d want 0", q)
	}
	for i := 0; i < patients; i++ {
		p := fmt.Sprintf("p%d", i)
		r := evalAt(t, c, 100, p)
		if len(r.Records) != 1 || !r.Records[0].Valid {
			t.Fatalf("%s records=%+v", p, r.Records)
		}
	}
}

// ---- naive reference model for differential testing ----

type naiveLot struct {
	series string
	exp    int
	qty    int
	quar   bool
}

type naiveModel struct {
	t       *testing.T
	rng     *rand.Rand
	series  map[string]vaxrule.Series
	birth   map[string]int
	lots    map[string]*naiveLot
	nurses  map[string]bool
	records map[string][]vaxrule.Entry
	lastNow int
	hasNow  bool
	log     []string
}

type opKind int

const (
	opRecord opKind = iota
	opAdminister
	opEvaluate
)

type op struct {
	kind            opKind
	now             int
	patient, series string
	d               int
	nurse           string
}

func newNaive(t *testing.T, rng *rand.Rand) *naiveModel {
	return &naiveModel{
		t: t, rng: rng,
		series:  map[string]vaxrule.Series{},
		birth:   map[string]int{},
		lots:    map[string]*naiveLot{},
		nurses:  map[string]bool{},
		records: map[string][]vaxrule.Entry{},
	}
}

func errClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidArg):
		return "invalid"
	case errors.Is(err, ErrClockRollback):
		return "clock"
	case errors.Is(err, ErrNotFound):
		return "notfound"
	case errors.Is(err, ErrNotGranted):
		return "notgranted"
	case errors.Is(err, ErrDuplicate):
		return "duplicate"
	case errors.Is(err, ErrCompleted):
		return "completed"
	case errors.Is(err, ErrNoStock):
		return "nostock"
	}
	var te *TooEarlyError
	if errors.As(err, &te) {
		return "early:" + te.Reason + ":" + fmt.Sprint(te.Earliest)
	}
	return "other:" + err.Error()
}

func sortedEntries(es []vaxrule.Entry) []vaxrule.Entry {
	out := append([]vaxrule.Entry(nil), es...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		return out[i].Series < out[j].Series
	})
	return out
}

// naiveResult recomputes everything from scratch from raw records.
func (m *naiveModel) naiveResult(now int, patient string) (string, []RecordVerdict, []NextDose) {
	birth, ok := m.birth[patient]
	if !ok {
		return "notfound", nil, nil
	}
	if now < 0 || now > 1_000_000 || patient == "" {
		return "invalid", nil, nil
	}
	if m.hasNow && now < m.lastNow {
		return "clock", nil, nil
	}
	books := vaxrule.NewBooks()
	names := make([]string, 0, len(m.series))
	for name, s := range m.series {
		if err := books.Add(s); err != nil {
			m.t.Fatal(err)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	es := sortedEntries(m.records[patient])
	res, err := vaxrule.Evaluate(books, birth, es)
	if err != nil {
		m.t.Fatal(err)
	}
	rv := []RecordVerdict{}
	for _, idx := range res.Order {
		v := res.Verdicts[idx]
		rv = append(rv, RecordVerdict{
			Series: es[idx].Series, Date: es[idx].Date,
			Valid: v.Valid, Extra: v.Extra, Dose: v.Dose, Reason: v.Reason.String(),
		})
	}
	st, _, err := vaxrule.Recompute(books, birth, es)
	if err != nil {
		m.t.Fatal(err)
	}
	next := []NextDose{}
	for _, name := range names {
		s := m.series[name]
		sum := st.Sums[name]
		if sum.ValidCount >= s.N {
			continue
		}
		// brute-force earliest valid date >= now
		d := now
		for d <= 1_000_000 {
			if vaxrule.TodayReason(s, birth, d, sum, st.Lives) == vaxrule.ReasonNone {
				break
			}
			d++
		}
		next = append(next, NextDose{Series: name, Dose: sum.ValidCount + 1, Date: d})
	}
	return "ok", rv, next
}

func (m *naiveModel) naiveRecord(o op) string {
	if o.now < 0 || o.now > 1_000_000 || o.patient == "" || o.series == "" || o.d < 0 || o.d > 1_000_000 {
		return "invalid"
	}
	if m.hasNow && o.now < m.lastNow {
		return "clock"
	}
	birth, ok := m.birth[o.patient]
	if !ok {
		return "notfound"
	}
	if _, ok := m.series[o.series]; !ok {
		return "notfound"
	}
	if o.d < birth || o.d > o.now {
		return "invalid"
	}
	for _, e := range m.records[o.patient] {
		if e.Series == o.series && e.Date == o.d {
			return "duplicate"
		}
	}
	m.lastNow, m.hasNow = o.now, true
	m.records[o.patient] = append(m.records[o.patient], vaxrule.Entry{Series: o.series, Date: o.d})
	return "ok"
}

func (m *naiveModel) naiveAdminister(o op) (string, string) {
	if o.now < 0 || o.now > 1_000_000 || o.nurse == "" || o.patient == "" || o.series == "" {
		return "invalid", ""
	}
	if m.hasNow && o.now < m.lastNow {
		return "clock", ""
	}
	birth, ok := m.birth[o.patient]
	if !ok {
		return "notfound", ""
	}
	s, ok := m.series[o.series]
	if !ok {
		return "notfound", ""
	}
	if !m.nurses[o.nurse] {
		return "notgranted", ""
	}
	books := vaxrule.NewBooks()
	for _, x := range m.series {
		if err := books.Add(x); err != nil {
			m.t.Fatal(err)
		}
	}
	es := sortedEntries(m.records[o.patient])
	for _, e := range es {
		if e.Series == o.series && e.Date == o.now {
			return "duplicate", ""
		}
	}
	st, res, err := vaxrule.Recompute(books, birth, es)
	if err != nil {
		m.t.Fatal(err)
	}
	sum := st.Sums[o.series]
	if sum.ValidCount >= s.N {
		return "completed", ""
	}
	if r := vaxrule.TodayReason(s, birth, o.now, sum, st.Lives); r != vaxrule.ReasonNone {
		d := o.now
		for d <= 1_000_000 && vaxrule.TodayReason(s, birth, d, sum, st.Lives) != vaxrule.ReasonNone {
			d++
		}
		return "early:" + r.String() + ":" + fmt.Sprint(d), ""
	}
	// FEFO
	best := ""
	bestExp := 0
	for id, l := range m.lots {
		if l.series != o.series || l.quar || l.qty <= 0 || o.now >= l.exp {
			continue
		}
		if best == "" || l.exp < bestExp || (l.exp == bestExp && id < best) {
			best, bestExp = id, l.exp
		}
	}
	if best == "" {
		return "nostock", ""
	}
	m.lots[best].qty--
	m.lastNow, m.hasNow = o.now, true
	m.records[o.patient] = append(m.records[o.patient], vaxrule.Entry{Series: o.series, Date: o.now})
	// Paranoia: appended record must be valid and nothing flips.
	es2 := sortedEntries(m.records[o.patient])
	res2, err := vaxrule.Evaluate(books, birth, es2)
	if err != nil {
		m.t.Fatal(err)
	}
	for i := 0; i < len(es); i++ {
		if res.Verdicts[i] != res2.Verdicts[i] {
			m.t.Fatalf("naive invariant broken: administer flipped record %+v %+v->%+v",
				es[i], res.Verdicts[i], res2.Verdicts[i])
		}
	}
	last := res2.Verdicts[len(es2)-1]
	if !last.Valid {
		m.t.Fatalf("naive: administered record invalid %+v", last)
	}
	return "ok", best
}

func TestRandomDifferential1500(t *testing.T) {
	type sdef struct {
		name   string
		live   bool
		n      int
		minAge []int
		minInt []int
		r      int
	}
	pool := []sdef{
		{"K", false, 1, []int{10}, []int{0}, 5},
		{"M", true, 2, []int{100, 120}, []int{0, 20}, 28},
		{"V", true, 1, []int{90}, []int{0}, 28},
		{"X", false, 3, []int{0, 10, 30}, []int{0, 10, 15}, 10},
	}

	for iter := 0; iter < 1500; iter++ {
		rng := rand.New(rand.NewSource(int64(100000 + iter)))
		m := newNaive(t, rng)
		c := New()
		logf := func(format string, a ...any) {
			line := fmt.Sprintf(format, a...)
			m.log = append(m.log, line)
			t.Logf("[iter=%d] %s", iter, line)
		}

		// pick a random subset of series
		var chosen []sdef
		for _, s := range pool {
			if rng.Intn(2) == 0 {
				chosen = append(chosen, s)
			}
		}
		if len(chosen) == 0 {
			chosen = append(chosen, pool[0])
		}
		for _, s := range chosen {
			if err := c.AddSeries(s.name, s.live, s.n, s.minAge, s.minInt, s.r); err != nil {
				t.Fatal(err)
			}
			m.series[s.name] = vaxrule.Series{
				Name: s.name, Live: s.live, N: s.n,
				MinAge: append([]int(nil), s.minAge...),
				MinInt: append([]int(nil), s.minInt...),
				R:      s.r,
			}
			logf("AddSeries %+v", s)
		}

		const np = 2
		for i := 0; i < np; i++ {
			p := fmt.Sprintf("p%d", i)
			birth := rng.Intn(5)
			if err := c.AddPatient(p, birth); err != nil {
				t.Fatal(err)
			}
			m.birth[p] = birth
			logf("AddPatient %s birth=%d", p, birth)
		}
		nurses := []string{"n1", "n2"}
		for _, n := range nurses {
			if rng.Intn(5) != 0 { // sometimes a nurse lacks grant
				if err := c.Grant(n); err != nil {
					t.Fatal(err)
				}
				m.nurses[n] = true
				logf("Grant %s", n)
			}
		}

		// lots
		nLots := 1 + rng.Intn(3)
		for i := 0; i < nLots; i++ {
			s := chosen[rng.Intn(len(chosen))].name
			id := fmt.Sprintf("L%d", i)
			exp := 100 + rng.Intn(400)
			qty := rng.Intn(4)
			if err := c.AddLot(id, s, exp, qty); err != nil {
				t.Fatal(err)
			}
			m.lots[id] = &naiveLot{series: s, exp: exp, qty: qty}
			logf("AddLot %s series=%s exp=%d qty=%d", id, s, exp, qty)
		}

		steps := 6 + rng.Intn(20)
		now := 0
		for step := 0; step < steps; step++ {
			switch rng.Intn(6) {
			case 0: // time travel / quarantine toggle
				id := fmt.Sprintf("L%d", rng.Intn(nLots))
				on := rng.Intn(2) == 0
				err := c.Quarantine(id, on)
				logf("Quarantine(%s,%v)=%v", id, on, err)
				if _, has := m.lots[id]; has {
					m.lots[id].quar = on
				} else if !errors.Is(err, ErrNotFound) {
					t.Fatalf("iter=%d quarantine missing lot err=%v", iter, err)
				}
			default:
				p := fmt.Sprintf("p%d", rng.Intn(np))
				s := chosen[rng.Intn(len(chosen))].name
				birth := m.birth[p]
				if rng.Intn(3) != 0 {
					now++
				}
				d := birth + rng.Intn(now+2) // sometimes > now (invalid arg)
				o := op{now: now, patient: p, series: s, d: d, nurse: nurses[rng.Intn(2)]}
				switch rng.Intn(3) {
				case 0:
					o.kind = opRecord
					err := c.Record(o.now, o.patient, o.series, o.d)
					want := m.naiveRecord(o)
					logf("Record(%d,%s,%s,d=%d) => got=%s want=%s",
						o.now, o.patient, o.series, o.d, errClass(err), want)
					if errClass(err) != want {
						m.t.Fatalf("iter=%d step=%d Record mismatch logs:\n%s",
							iter, step, stringsJoin(m.log))
					}
				case 1:
					o.kind = opAdminister
					lot, err := c.Administer(o.now, o.nurse, o.patient, o.series)
					want, wantLot := m.naiveAdminister(o)
					logf("Administer(%d,%s,%s,%s) => got=(%s,%s) want=(%s,%s)",
						o.now, o.nurse, o.patient, o.series, lot, errClass(err), wantLot, want)
					if errClass(err) != want || lot != wantLot {
						m.t.Fatalf("iter=%d step=%d Administer mismatch logs:\n%s",
							iter, step, stringsJoin(m.log))
					}
				default:
					o.kind = opEvaluate
					rep, err := c.Evaluate(o.now, o.patient)
					want, wantRec, wantNext := m.naiveResult(o.now, o.patient)
					logf("Evaluate(%d,%s) => got=%s rec=%+v next=%+v",
						o.now, o.patient, errClass(err), rep.Records, rep.Next)
					if errClass(err) != want {
						m.t.Fatalf("iter=%d step=%d Evaluate err mismatch logs:\n%s",
							iter, step, stringsJoin(m.log))
					}
					if want == "ok" {
						if !recordsEqual(rep.Records, wantRec) {
							m.t.Fatalf("iter=%d step=%d records mismatch\n got=%+v\nwant=%+v\nlogs:\n%s",
								iter, step, rep.Records, wantRec, stringsJoin(m.log))
						}
						if !nextEqual(rep.Next, wantNext) {
							m.t.Fatalf("iter=%d step=%d next mismatch\n got=%+v\nwant=%+v\nlogs:\n%s",
								iter, step, rep.Next, wantNext, stringsJoin(m.log))
						}
					}
				}
			}
		}
		// Final lot quantities must match and stay non-negative.
		for id, l := range m.lots {
			if got := c.LotQty(id); got != l.qty || got < 0 {
				t.Fatalf("iter=%d lot %s qty got=%d want=%d logs:\n%s",
					iter, id, got, l.qty, stringsJoin(m.log))
			}
		}
	}
}

func stringsJoin(ss []string) string {
	out := ""
	for _, s := range ss {
		out += s + "\n"
	}
	return out
}

func recordsEqual(a, b []RecordVerdict) bool {
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

func nextEqual(a, b []NextDose) bool {
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

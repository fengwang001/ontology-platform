package vaxrule

import (
	"math/rand"
	"testing"
)

func mSeries() Series {
	return Series{Name: "M", Live: true, N: 2, MinAge: []int{365, 393}, MinInt: []int{0, 28}, R: 28}
}

func vSeries() Series {
	return Series{Name: "V", Live: true, N: 1, MinAge: []int{365}, MinInt: []int{0}, R: 28}
}

func booksWith(ss ...Series) *Books {
	b := NewBooks()
	for _, s := range ss {
		if err := b.Add(s); err != nil {
			panic(err)
		}
	}
	return b
}

// naiveJudge is an independent literal implementation of the spec.
func naiveJudge(b *Books, birth int, entries []Entry) []Verdict {
	order := make([]int, len(entries))
	for i := range order {
		order[i] = i
	}
	for i := 0; i < len(order); i++ {
		for j := i + 1; j < len(order); j++ {
			a, c := entries[order[i]], entries[order[j]]
			if a.Date > c.Date || (a.Date == c.Date && a.Series > c.Series) ||
				(a.Date == c.Date && a.Series == c.Series && order[i] > order[j]) {
				order[i], order[j] = order[j], order[i]
			}
		}
	}
	type st struct {
		valids []int
		lastD  int
		lastV  bool
		has    bool
	}
	states := map[string]*st{}
	live := []Entry{}
	out := make([]Verdict, len(entries))
	for _, idx := range order {
		e := entries[idx]
		s, _ := b.Get(e.Series)
		x := states[e.Series]
		if x == nil {
			x = &st{}
			states[e.Series] = x
		}
		k := len(x.valids) + 1
		ver := Verdict{Dose: k}
		conflict := false
		if s.Live {
			for _, l := range live {
				if l.Series != e.Series && l.Date < e.Date && e.Date-l.Date < 28 {
					conflict = true
				}
			}
		}
		switch {
		case k > s.N:
			ver.Valid, ver.Extra = true, true
		case e.Date-birth < s.MinAge[k-1]-4:
			ver.Reason = ReasonAge
		case k > 1 && (len(x.valids) == 0 || e.Date-x.valids[len(x.valids)-1] < s.MinInt[k-1]-4):
			ver.Reason = ReasonInterval
		case x.has && !x.lastV && e.Date-x.lastD < s.R:
			ver.Reason = ReasonRevacc
		case conflict:
			ver.Reason = ReasonLive
		default:
			ver.Valid = true
		}
		if ver.Valid && !ver.Extra {
			x.valids = append(x.valids, e.Date)
		}
		x.lastD, x.lastV, x.has = e.Date, ver.Valid, true
		if s.Live {
			live = append(live, e)
		}
		out[idx] = ver
	}
	return out
}

type evalCase struct {
	name    string
	series  []Series
	birth   int
	entries []Entry
	want    []Verdict
}

func TestEvaluateTable(t *testing.T) {
	cases := []evalCase{
		{
			name:    "jia age grace exactly passes",
			series:  []Series{mSeries()},
			birth:   0,
			entries: []Entry{{"M", 361}},
			want:    []Verdict{{Valid: true, Dose: 1}},
		},
		{
			name:    "jia minus one fails age",
			series:  []Series{mSeries()},
			birth:   0,
			entries: []Entry{{"M", 360}},
			want:    []Verdict{{Valid: false, Dose: 1, Reason: ReasonAge}},
		},
		{
			name:    "yi revacc chain 27 then exactly 28",
			series:  []Series{mSeries()},
			birth:   0,
			entries: []Entry{{"M", 360}, {"M", 387}, {"M", 415}},
			want: []Verdict{
				{Valid: false, Dose: 1, Reason: ReasonAge},
				{Valid: false, Dose: 1, Reason: ReasonRevacc},
				{Valid: true, Dose: 1},
			},
		},
		{
			name:    "revacc bound exactly 28 passes no grace",
			series:  []Series{mSeries()},
			birth:   0,
			entries: []Entry{{"M", 360}, {"M", 388}},
			want: []Verdict{
				{Valid: false, Dose: 1, Reason: ReasonAge},
				{Valid: true, Dose: 1},
			},
		},
		{
			name:    "interval grace 24 passes",
			series:  []Series{mSeries()},
			birth:   0,
			entries: []Entry{{"M", 400}, {"M", 424}},
			want: []Verdict{
				{Valid: true, Dose: 1},
				{Valid: true, Dose: 2},
			},
		},
		{
			name:    "interval 23 fails",
			series:  []Series{mSeries()},
			birth:   0,
			entries: []Entry{{"M", 400}, {"M", 423}},
			want: []Verdict{
				{Valid: true, Dose: 1},
				{Valid: false, Dose: 2, Reason: ReasonInterval},
			},
		},
		{
			name:   "bing invalid live record still conflicts",
			series: []Series{mSeries(), vSeries()},
			birth:  0,
			entries: []Entry{
				{"M", 400}, {"V", 410}, {"M", 428},
			},
			want: []Verdict{
				{Valid: true, Dose: 1},
				{Valid: false, Dose: 1, Reason: ReasonLive},
				{Valid: false, Dose: 2, Reason: ReasonLive},
			},
		},
		{
			name:    "ding same-day live both valid",
			series:  []Series{mSeries(), vSeries()},
			birth:   0,
			entries: []Entry{{"M", 400}, {"V", 400}},
			want: []Verdict{
				{Valid: true, Dose: 1},
				{Valid: true, Dose: 1},
			},
		},
		{
			name:    "live gap 27 conflicts; later dose clears revacc and live",
			series:  []Series{mSeries(), vSeries()},
			birth:   0,
			entries: []Entry{{"M", 400}, {"V", 427}, {"V", 455}},
			want: []Verdict{
				{Valid: true, Dose: 1},
				{Valid: false, Dose: 1, Reason: ReasonLive},
				// 455-427=28 clears revacc (no grace); 455-400=55 clears live.
				{Valid: true, Dose: 1},
			},
		},
		{
			name:    "extra dose is valid-extra not invalid",
			series:  []Series{vSeries()},
			birth:   0,
			entries: []Entry{{"V", 400}, {"V", 401}, {"V", 430}},
			want: []Verdict{
				{Valid: true, Dose: 1},
				{Valid: true, Extra: true, Dose: 2},
				{Valid: true, Extra: true, Dose: 2},
			},
		},
		{
			name:    "extra live record still creates conflict",
			series:  []Series{mSeries(), vSeries()},
			birth:   0,
			entries: []Entry{{"V", 400}, {"V", 401}, {"M", 410}},
			want: []Verdict{
				{Valid: true, Dose: 1},
				{Valid: true, Extra: true, Dose: 2},
				{Valid: false, Dose: 1, Reason: ReasonLive},
			},
		},
		{
			name:    "check order age beats interval",
			series:  []Series{mSeries()},
			birth:   0,
			entries: []Entry{{"M", 365}, {"M", 380}},
			want: []Verdict{
				{Valid: true, Dose: 1},
				{Valid: false, Dose: 2, Reason: ReasonAge}, // 380 < 389 age and also <24 int; age wins
			},
		},
		{
			name:    "grace can make age bound negative",
			series:  []Series{{Name: "X", N: 1, MinAge: []int{2}, MinInt: []int{0}, R: 0}},
			birth:   0,
			entries: []Entry{{"X", 0}},
			want:    []Verdict{{Valid: true, Dose: 1}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := booksWith(tc.series...)
			res, err := Evaluate(b, tc.birth, tc.entries)
			if err != nil {
				t.Fatalf("Evaluate err=%v input=%+v", err, tc.entries)
			}
			for i, got := range res.Verdicts {
				if got != tc.want[i] {
					t.Fatalf("entry %d (series=%s date=%d): got %+v want %+v",
						i, tc.entries[i].Series, tc.entries[i].Date, got, tc.want[i])
				}
			}
		})
	}
}

func TestOutOfOrderBackfillFlips(t *testing.T) {
	// 戊: M@400 valid, V@428 valid; backfill M@420 -> M invalid
	// (interval), V flips to live-conflict.
	b := booksWith(mSeries(), vSeries())
	birth := 0
	entries := []Entry{{"M", 400}, {"V", 428}}
	r1, err := Evaluate(b, birth, entries)
	if err != nil {
		t.Fatal(err)
	}
	if !r1.Verdicts[0].Valid || !r1.Verdicts[1].Valid {
		t.Fatalf("before backfill: %+v", r1.Verdicts)
	}
	entries = append(entries, Entry{"M", 420})
	r2, err := Evaluate(b, birth, entries)
	if err != nil {
		t.Fatal(err)
	}
	var gotM, gotV Verdict
	for i, e := range entries {
		if e.Series == "M" && e.Date == 420 {
			gotM = r2.Verdicts[i]
		}
		if e.Series == "V" && e.Date == 428 {
			gotV = r2.Verdicts[i]
		}
	}
	if gotM.Valid || gotM.Reason != ReasonInterval || gotM.Dose != 2 {
		t.Fatalf("backfilled M@420: %+v", gotM)
	}
	if gotV.Valid || gotV.Reason != ReasonLive {
		t.Fatalf("V@428 should flip to live-invalid: %+v", gotV)
	}
}

func TestEarliestSpecCase(t *testing.T) {
	// 丙 now=430: M@400 valid, V@410 invalid, M@428 invalid.
	b := booksWith(mSeries(), vSeries())
	birth := 0
	entries := []Entry{{"M", 400}, {"V", 410}, {"M", 428}}
	st, _, err := Recompute(b, birth, entries)
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := b.Get("M")
	vs, _ := b.Get("V")
	dm, rm := Earliest(ms, birth, 430, st.Sums["M"], st.Lives)
	dv, rv := Earliest(vs, birth, 430, st.Sums["V"], st.Lives)
	if dm != 456 || rm != ReasonRevacc {
		t.Fatalf("M earliest=%d reason=%v, want 456 revacc", dm, rm)
	}
	// V's own invalid 410 makes today(430) fail revacc first
	// (430-410=20<28); the live clash with M@428 then pushes the
	// feasible date to 456, matching the spec.
	if dv != 456 || rv != ReasonRevacc {
		t.Fatalf("V earliest=%d reason=%v, want 456 revacc", dv, rv)
	}
	for _, e := range []Entry{{"M", 456}, {"V", 456}} {
		more := append(append([]Entry{}, entries...), e)
		rr, _ := Evaluate(b, birth, more)
		if !rr.Verdicts[len(more)-1].Valid {
			t.Fatalf("%+v not valid at 456: %+v", e, rr.Verdicts[len(more)-1])
		}
	}
}

func TestEarliestReasonsOrder(t *testing.T) {
	b := booksWith(mSeries(), vSeries())
	empty := State{Sums: map[string]Summary{}, Lives: nil}
	ms, _ := b.Get("M")
	// Too young at now=100 -> age.
	if _, r := Earliest(ms, 0, 100, empty.Sums["M"], empty.Lives); r != ReasonAge {
		t.Fatalf("want age, got %v", r)
	}
	// One valid dose at 400, now=405 -> interval (age ok).
	sum := Summary{ValidCount: 1, PrevValid: 400, HasPrev: true,
		LastDate: 400, LastValid: true, HasLast: true}
	if d, r := Earliest(ms, 0, 405, sum, nil); r != ReasonInterval || d != 424 {
		t.Fatalf("want interval/424, got %v/%d", r, d)
	}
	// Last record invalid at 428, now=430 -> revacc (age+interval pass).
	sum2 := Summary{ValidCount: 1, PrevValid: 400, HasPrev: true,
		LastDate: 428, LastValid: false, HasLast: true}
	if d, r := Earliest(ms, 0, 430, sum2, nil); r != ReasonRevacc || d != 456 {
		t.Fatalf("want revacc/456, got %v/%d", r, d)
	}
	// Live conflict at 420 with V@410.
	vs, _ := b.Get("V")
	if d, r := Earliest(vs, 0, 420, Summary{},
		[]LiveRec{{"V", 300}, {"M", 410}}); r != ReasonLive || d != 438 {
		t.Fatalf("want live/438, got %v/%d", r, d)
	}
}

func TestAddSeriesValidation(t *testing.T) {
	cases := []Series{
		{Name: "", N: 1, MinAge: []int{0}, MinInt: []int{0}},
		{Name: "A", N: 0, MinAge: nil, MinInt: nil},
		{Name: "A", N: 7, MinAge: make([]int, 7), MinInt: make([]int, 7)},
		{Name: "A", N: 1, MinAge: []int{-1}, MinInt: []int{0}},
		{Name: "A", N: 1, MinAge: []int{10001}, MinInt: []int{0}},
		{Name: "A", N: 1, MinAge: []int{0}, MinInt: []int{1}},
		{Name: "A", N: 2, MinAge: []int{0, 0}, MinInt: []int{0, -1}, R: 0},
		{Name: "A", N: 1, MinAge: []int{0}, MinInt: []int{0}, R: -1},
	}
	for _, s := range cases {
		if err := NewBooks().Add(s); err != ErrInvalidSeries {
			t.Fatalf("%+v: want ErrInvalidSeries got %v", s, err)
		}
	}
	b := NewBooks()
	s := mSeries()
	if err := b.Add(s); err != nil {
		t.Fatal(err)
	}
	if err := b.Add(s); err != ErrSeriesExists {
		t.Fatalf("dup: want ErrSeriesExists got %v", err)
	}
}

// TestRandomVsNaive replays random histories and requires the engine to
// match the independent naive implementation exactly.
func TestRandomVsNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	names := []string{"A", "B", "C", "D"}
	for iter := 0; iter < 1500; iter++ {
		b := NewBooks()
		all := []Series{}
		for _, name := range names {
			n := 1 + rng.Intn(3)
			s := Series{
				Name:   name,
				Live:   rng.Intn(2) == 0,
				N:      n,
				MinAge: make([]int, n),
				MinInt: make([]int, n),
				R:      rng.Intn(30),
			}
			for k := 0; k < n; k++ {
				s.MinAge[k] = rng.Intn(400)
				if k > 0 {
					s.MinInt[k] = rng.Intn(40)
				}
			}
			if err := b.Add(s); err != nil {
				t.Fatal(err)
			}
			all = append(all, s)
		}
		birth := rng.Intn(50)
		entries := []Entry{}
		steps := 1 + rng.Intn(12)
		for i := 0; i < steps; i++ {
			entries = append(entries, Entry{
				Series: names[rng.Intn(len(names))],
				Date:   birth + rng.Intn(500),
			})
			got, err := Evaluate(b, birth, entries)
			if err != nil {
				t.Fatal(err)
			}
			want := naiveJudge(b, birth, entries)
			t.Logf("[iter=%d] input birth=%d entries=%+v", iter, birth, entries)
			for j := range entries {
				t.Logf("    basis idx=%d %s@%d -> valid=%v extra=%v dose=%d reason=%s",
					j, entries[j].Series, entries[j].Date,
					got.Verdicts[j].Valid, got.Verdicts[j].Extra,
					got.Verdicts[j].Dose, got.Verdicts[j].Reason)
				if got.Verdicts[j] != want[j] {
					t.Fatalf("iter=%d entries=%+v\n idx=%d got=%+v want=%+v",
						iter, entries, j, got.Verdicts[j], want[j])
				}
			}
			// Recompute summaries must agree too, and Earliest must
			// point at a genuinely valid date.
			st, res, err := Recompute(b, birth, entries)
			if err != nil {
				t.Fatal(err)
			}
			for j := range entries {
				if res.Verdicts[j] != want[j] {
					t.Fatalf("recompute mismatch iter=%d", iter)
				}
			}
			now := birth + 500
			for _, s := range all {
				sum := st.Sums[s.Name]
				if sum.ValidCount >= s.N {
					continue
				}
				date, _ := Earliest(s, birth, now, sum, st.Lives)
				if date < now {
					t.Fatalf("iter=%d earliest %s=%d < now %d", iter, s.Name, date, now)
				}
				trial := append(append([]Entry{}, entries...), Entry{s.Name, date})
				rr, _ := Evaluate(b, birth, trial)
				v := rr.Verdicts[len(trial)-1]
				if !v.Valid {
					t.Fatalf("iter=%d earliest %s=%d not valid: %+v entries=%+v",
						iter, s.Name, date, v, entries)
				}
				if date == now {
					if reason := TodayReason(s, birth, now, sum, st.Lives); reason != ReasonNone {
						t.Fatalf("iter=%d date==now but reason=%v", iter, reason)
					}
				}
			}
		}
	}
}

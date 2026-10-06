package contract

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

// op 是可在两个模型上重放的一步操作。
type op struct {
	kind     string
	now      int64
	contract string
	amend    string
	party    int // 0/1
	eff      int64
	changes  map[string]int
	revokes  string
	queryDay int64
	clause   string
}

// runner 对服务模型和朴素模型施加同一操作序列，并比较所有结果。
type runner struct {
	t    *testing.T
	svc  *Service
	nav  *NaiveModel
	logs strings.Builder
	rng  *rand.Rand
	step int
}

func newRunner(t *testing.T, seed int64) *runner {
	svc := NewService()
	svc.SetLogging(true)
	return &runner{t: t, svc: svc, nav: NewNaiveModel(),
		rng: rand.New(rand.NewSource(seed)), logs: strings.Builder{}}
}

func codeOf(err error) ErrorCode {
	if err == nil {
		return 0
	}
	if se, ok := err.(*ServiceError); ok {
		return se.Code
	}
	return -1
}

func (r *runner) apply(o op) {
	r.step++
	var serr, nerr error
	switch o.kind {
	case "create":
		in := CreateContractInput{
			ID: o.contract, Now: o.now, StartDay: 0, ExpiryDay: 100,
			Parties: [2]PartyInput{
				{ID: "A", AuthFrom: 0, AuthUntil: 10000},
				{ID: "B", AuthFrom: 0, AuthUntil: 10000},
			},
			Clauses: map[string]int{
				"PRICE":           100,
				"LOCKED_TERM":     7,
				ClauseAutoRenew:   1,
				ClauseRenewalTerm: 30,
				ClauseNoticeDays:  10,
			},
			LockedClauses: map[string]bool{"LOCKED_TERM": true},
		}
		serr = r.svc.CreateContract(in)
		nerr = r.nav.Create(in)
	case "add":
		in := AddAmendmentInput{ContractID: o.contract, AmendmentID: o.amend,
			Now: o.now, EffectiveDay: o.eff, Changes: o.changes, Revokes: o.revokes}
		serr = r.svc.AddAmendment(in)
		nerr = r.nav.Add(in)
	case "sign":
		pid := []string{"A", "B"}[o.party]
		serr = r.svc.Sign(o.contract, o.amend, pid, o.now)
		nerr = r.nav.Sign(o.contract, o.amend, pid, o.now)
	case "cosign":
		serr = r.svc.LegalCosign(o.contract, o.amend, o.now)
		nerr = r.nav.Cosign(o.contract, o.amend, o.now)
	case "notice":
		pid := []string{"A", "B"}[o.party]
		serr = r.svc.NoticeNonRenewal(o.contract, pid, o.now)
		nerr = r.nav.Notice(o.contract, pid, o.now)
	case "term":
		pid := []string{"A", "B"}[o.party]
		serr = r.svc.AgreeEarlyTermination(o.contract, pid, o.now)
		nerr = r.nav.EarlyTerm(o.contract, pid, o.now)
	}
	line := fmt.Sprintf("step %d %s in={now:%d c:%s m:%s p:%d eff:%d ch:%v rev:%s} -> svc=%s naive=%s",
		r.step, o.kind, o.now, o.contract, o.amend, o.party, o.eff, o.changes, o.revokes,
		codeOf(serr).String(), codeOf(nerr).String())
	fmt.Fprintln(&r.logs, line)
	r.t.Log(line)
	if codeOf(serr) != codeOf(nerr) {
		r.t.Fatalf("op %+v: svc err=%v naive err=%v\nstep log:\n%s\nsvc log:\n%s",
			o, serr, nerr, r.logs.String(), r.svc.SnapshotLog())
	}
}

func (r *runner) check(o op) {
	nc := r.nav.Contract(o.contract)
	if nc == nil {
		if _, err := r.svc.EffectiveValue(o.contract, o.clause, o.queryDay); codeOf(err) != ErrNotFound {
			r.t.Fatalf("missing contract not reported")
		}
		return
	}
	for _, clause := range []string{"PRICE", "LOCKED_TERM", ClauseAutoRenew, ClauseRenewalTerm, ClauseNoticeDays} {
		got, gerr := r.svc.EffectiveValue(o.contract, clause, o.queryDay)
		want := nc.Value(clause, o.queryDay)
		if gerr != nil {
			r.t.Fatalf("svc value error %v", gerr)
		}
		if got.Value != want.Value || got.Kind != want.Kind || got.AmendmentID != want.AmendmentID {
			r.t.Fatalf("day %d clause %s: svc=%+v naive=%+v\nsteps:\n%s\nsvc:\n%s",
				o.queryDay, clause, got, want, r.logs.String(), r.svc.SnapshotLog())
		}
		fmt.Fprintf(&r.logs, "  query day=%d clause=%s => %s %d (%s)\n",
			o.queryDay, clause, want.Kind, want.Value, want.AmendmentID)
	}
	wExpiry, wIn, wRecs := nc.Simulate(o.queryDay)
	gIn, _ := r.svc.InTerm(o.contract, o.queryDay)
	gExpiry, _ := r.svc.CurrentExpiry(o.contract, o.queryDay)
	gRecs, _ := r.svc.Renewals(o.contract, o.queryDay)
	if wExpiry != gExpiry || wIn != gIn || !recsEqual(wRecs, gRecs) {
		r.t.Fatalf("day %d term state: svc(exp=%d in=%v recs=%+v) naive(exp=%d in=%v recs=%+v)\nsteps:\n%s\nsvc:\n%s",
			o.queryDay, gExpiry, gIn, gRecs, wExpiry, wIn, wRecs, r.logs.String(), r.svc.SnapshotLog())
	}
}

func recsEqual(a, b []RenewalRecord) bool {
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

// TestRandomDifferential 大量随机操作序列与朴素模型对照。
func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping differential in short mode")
	}
	for seed := int64(1); seed <= 40; seed++ {
		r := newRunner(t, seed)
		cid := fmt.Sprintf("C%d", seed)
		r.apply(op{kind: "create", contract: cid, now: 0})

		now := int64(0)
		amends := []string{}
		revokable := []string{}
		lockedDone := false
		for step := 0; step < 120; step++ {
			now += int64(r.rng.Intn(4)) // 单调时钟，偶尔停留
			roll := r.rng.Float64()
			switch {
			case roll < 0.45 && len(amends) < 20:
				id := fmt.Sprintf("M%d", len(amends))
				changes := map[string]int{}
				clauses := []string{"PRICE", "LOCKED_TERM", ClauseAutoRenew, ClauseRenewalTerm, ClauseNoticeDays}
				k := 1 + r.rng.Intn(2)
				cp := append([]string(nil), clauses...)
				r.rng.Shuffle(len(cp), func(i, j int) { cp[i], cp[j] = cp[j], cp[i] })
				for _, c := range cp[:k] {
					switch c {
					case ClauseAutoRenew:
						changes[c] = r.rng.Intn(2)
					case ClauseRenewalTerm:
						changes[c] = 1 + r.rng.Intn(25)
					case ClauseNoticeDays:
						changes[c] = r.rng.Intn(15)
					default:
						changes[c] = r.rng.Intn(500)
					}
				}
				eff := now + int64(r.rng.Intn(10))
				r.apply(op{kind: "add", contract: cid, amend: id, now: now,
					eff: eff, changes: changes})
				amends = append(amends, id)
			case roll < 0.72 && len(amends) > 0:
				id := amends[r.rng.Intn(len(amends))]
				p := r.rng.Intn(2)
				r.apply(op{kind: "sign", contract: cid, amend: id, party: p, now: now})
				// 随机补会签。
				if r.rng.Intn(3) == 0 {
					r.apply(op{kind: "cosign", contract: cid, amend: id, now: now})
				}
				if na := r.nav.Contract(cid).amend(id); na != nil && na.effectiveDay(r.nav.Contract(cid)) >= 0 {
					revokable = append(revokable, id)
				}
			case roll < 0.80 && len(revokable) > 0:
				target := revokable[r.rng.Intn(len(revokable))]
				id := fmt.Sprintf("M%d", len(amends))
				r.apply(op{kind: "add", contract: cid, amend: id, now: now,
					eff: now + int64(r.rng.Intn(8)), revokes: target})
				amends = append(amends, id)
			case roll < 0.84 && len(amends) > 0:
				r.apply(op{kind: "cosign", contract: cid,
					amend: amends[r.rng.Intn(len(amends))], now: now})
			case roll < 0.92:
				r.apply(op{kind: "notice", contract: cid, party: r.rng.Intn(2), now: now})
			case r.rng.Intn(6) == 0:
				if r.rng.Intn(6) == 0 {
					r.apply(op{kind: "term", contract: cid, party: r.rng.Intn(2), now: now})
				}
			}
			_ = lockedDone
			// 每步在若干日期查询，含严格历史、当前、远期。
			for _, d := range []int64{0, now, now + 1, 100, 130, 260} {
				r.check(op{kind: "query", contract: cid, now: now, queryDay: d})
			}
		}
	}
}

var _ = sort.IntSlice{}
var _ = sync.Mutex{}

package apf

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The differential test drives the optimized Controller and the deliberately
// naive reference model through identical random operation sequences
// (admits, finishes, config updates, clock advances) and compares every
// observable outcome step by step: error kinds, lease-vs-queue decisions,
// ticket resolutions and per-level occupancy/queue state. Every operation
// is logged with its input, actual output and the state that justifies it.

var (
	diffUsers      = []string{"u0", "u1", "u2", "u3"}
	diffNamespaces = []string{"n0", "n1", "n2"}
	diffVerbs      = []string{"get", "list", "create"}
	diffResources  = []string{"pods", "jobs", "cfg"}
)

func randomSubset(rng *rand.Rand, universe []string, p float64) []string {
	var out []string
	for _, v := range universe {
		if rng.Float64() < p {
			out = append(out, v)
		}
	}
	return out
}

func randomConfig(rng *rand.Rand) Config {
	numLimited := 1 + rng.Intn(3)
	var levels []Level
	for i := 0; i < numLimited; i++ {
		levels = append(levels, Level{
			Name:         fmt.Sprintf("L%d", i),
			Shares:       rng.Intn(4),
			QueueLimit:   1 + rng.Intn(5),
			QueueTimeout: time.Duration(5+rng.Intn(15)) * time.Second,
		})
	}
	if rng.Float64() < 0.3 {
		levels = append(levels, Level{Name: "ex", Exempt: true})
	}
	cfg := Config{TotalSeats: 1 + rng.Intn(8), Levels: levels}
	numRules := 1 + rng.Intn(4)
	for i := 0; i < numRules; i++ {
		l := levels[rng.Intn(len(levels))]
		d := ByUser
		if rng.Float64() < 0.5 {
			d = ByNamespace
		}
		cfg.Rules = append(cfg.Rules, Rule{
			Name:          fmt.Sprintf("r%d", i),
			Precedence:    rng.Intn(3),
			Users:         randomSubset(rng, diffUsers, 0.5),
			Verbs:         randomSubset(rng, diffVerbs, 0.5),
			Resources:     randomSubset(rng, diffResources, 0.5),
			Level:         l.Name,
			DistinguishBy: d,
		})
	}
	return cfg
}

func randomRequest(rng *rand.Rand) Request {
	seats := 1 + rng.Intn(10)
	if rng.Float64() < 0.03 {
		seats = rng.Intn(15) // occasionally invalid
	}
	return Request{
		User:      diffUsers[rng.Intn(len(diffUsers))],
		Namespace: diffNamespaces[rng.Intn(len(diffNamespaces))],
		Verb:      diffVerbs[rng.Intn(len(diffVerbs))],
		Resource:  diffResources[rng.Intn(len(diffResources))],
		Seats:     seats,
	}
}

type leasePair struct {
	real  *Lease
	naive *naiveLease
}

type ticketPair struct {
	real    *Ticket
	naiveID int
}

func kindString(err error) string {
	if err == nil {
		return "ok"
	}
	k, _ := KindOf(err)
	return k.String()
}

// drainRealEvents collects ticket resolutions produced since the last call.
// It returns sorted event strings, the still-pending tickets, and the leases
// granted to resolved tickets keyed by ticket id.
func drainRealEvents(tickets []ticketPair) ([]string, []ticketPair, map[int]*Lease) {
	var events []string
	granted := make(map[int]*Lease)
	remaining := tickets[:0]
	for _, tp := range tickets {
		select {
		case r := <-tp.real.C():
			if r.Err != nil {
				events = append(events, fmt.Sprintf("#%d:%s", tp.naiveID, kindString(r.Err)))
			} else {
				events = append(events, fmt.Sprintf("#%d:lease", tp.naiveID))
				granted[tp.naiveID] = r.Lease
			}
		default:
			remaining = append(remaining, tp)
		}
	}
	sort.Strings(events)
	return events, remaining, granted
}

func naiveEventStrings(events []naiveEvent) []string {
	var out []string
	for _, e := range events {
		if e.err != nil {
			out = append(out, fmt.Sprintf("#%d:%s", e.ticket, kindString(e.err)))
		} else {
			out = append(out, fmt.Sprintf("#%d:lease", e.ticket))
		}
	}
	sort.Strings(out)
	return out
}

func stateSummary(levels []LevelDebug) string {
	var parts []string
	for _, l := range levels {
		if l.Occupied == 0 && l.Waiters == 0 && l.Nominal == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s[occ=%d/%d wait=%d flows=%v]",
			l.Name, l.Occupied, l.Nominal, l.Waiters, l.Flows))
	}
	return strings.Join(parts, " ")
}

func naiveStateSummary(n *naiveController) string {
	var parts []string
	for _, name := range n.sortedLevelNames() {
		st := n.levels[name]
		if st.occupied == 0 && len(st.waiters) == 0 && st.nominal == 0 {
			continue
		}
		var flows []string
		for _, f := range st.flowOrder {
			if st.flowHasWaiters(f) {
				flows = append(flows, f)
			}
		}
		parts = append(parts, fmt.Sprintf("%s[occ=%d/%d wait=%d flows=%v]",
			name, st.occupied, st.nominal, len(st.waiters), flows))
	}
	return strings.Join(parts, " ")
}

func TestDifferentialRandom(t *testing.T) {
	seeds, ops := int64(40), 300
	if v, err := strconv.Atoi(os.Getenv("APF_DIFF_SEEDS")); err == nil {
		seeds = int64(v)
	}
	if v, err := strconv.Atoi(os.Getenv("APF_DIFF_OPS")); err == nil {
		ops = v
	}
	for seed := int64(0); seed < seeds; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			runDifferential(t, seed, ops)
		})
	}
}

func runDifferential(t *testing.T, seed int64, ops int) {
	rng := rand.New(rand.NewSource(seed))
	cfg := randomConfig(rng)
	real, err := NewController(t0, cfg)
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	naive, err := newNaive(t0, cfg)
	if err != nil {
		t.Fatalf("newNaive: %v", err)
	}
	t.Logf("initial config: %+v", cfg)

	var leases []leasePair
	var tickets []ticketPair
	now := t0

	checkEvents := func(op string) {
		t.Helper()
		realEvents, remaining, granted := drainRealEvents(tickets)
		tickets = remaining
		naiveEvents := naive.drainEvents()
		naiveStrs := naiveEventStrings(naiveEvents)
		if strings.Join(realEvents, ",") != strings.Join(naiveStrs, ",") {
			t.Fatalf("%s: ticket events differ\n real: %v\nnaive: %v", op, realEvents, naiveStrs)
		}
		for _, e := range naiveEvents {
			if e.lease != nil {
				realLease, ok := granted[e.ticket]
				if !ok {
					t.Fatalf("%s: naive granted lease for ticket #%d but real did not", op, e.ticket)
				}
				leases = append(leases, leasePair{real: realLease, naive: e.lease})
			}
		}
		if len(realEvents) > 0 {
			t.Logf("  events: %v", realEvents)
		}
	}

	checkState := func(op string) {
		t.Helper()
		_, levels := real.DebugState()
		rs, ns := stateSummary(levels), naiveStateSummary(naive)
		if rs != ns {
			t.Fatalf("%s: state differs\n real: %s\nnaive: %s", op, rs, ns)
		}
		t.Logf("  state: %s", rs)
	}

	for i := 0; i < ops; i++ {
		now = now.Add(time.Duration(rng.Intn(8)) * time.Second)
		choice := rng.Float64()
		switch {
		case choice < 0.6 || len(leases) == 0:
			req := randomRequest(rng)
			res, rerr := real.Admit(now, req)
			nlease, nticket, nerr := naive.admit(now, req)
			if kindString(rerr) != kindString(nerr) {
				t.Fatalf("op %d admit %+v: error kinds differ: real=%v naive=%v", i, req, rerr, nerr)
			}
			outcome := ""
			switch {
			case rerr != nil:
				outcome = "rejected: " + kindString(rerr)
			case res.Lease != nil:
				if nlease == nil {
					t.Fatalf("op %d: real granted lease, naive did not", i)
				}
				leases = append(leases, leasePair{real: res.Lease, naive: nlease})
				outcome = "execute immediately"
			default:
				if nticket < 0 {
					t.Fatalf("op %d: naive queued, real did not", i)
				}
				tickets = append(tickets, ticketPair{real: res.Ticket, naiveID: nticket})
				outcome = fmt.Sprintf("queued as ticket #%d", nticket)
			}
			t.Logf("op %d t=+%ds admit %+v -> %s", i, int(now.Sub(t0).Seconds()), req, outcome)
		case choice < 0.85:
			idx := rng.Intn(len(leases))
			lp := leases[idx]
			leases = append(leases[:idx], leases[idx+1:]...)
			rerr := lp.real.Finish(now)
			nerr := lp.naive.finish(now)
			if kindString(rerr) != kindString(nerr) {
				t.Fatalf("op %d finish: error kinds differ: real=%v naive=%v", i, rerr, nerr)
			}
			t.Logf("op %d t=+%ds finish lease -> %s", i, int(now.Sub(t0).Seconds()), kindString(rerr))
		default:
			cfg = randomConfig(rng)
			rerr := real.UpdateConfig(now, cfg)
			nerr := naive.updateConfig(now, cfg)
			if kindString(rerr) != kindString(nerr) {
				t.Fatalf("op %d update: error kinds differ: real=%v naive=%v", i, rerr, nerr)
			}
			t.Logf("op %d t=+%ds update config -> %s", i, int(now.Sub(t0).Seconds()), kindString(rerr))
		}
		checkEvents(fmt.Sprintf("op %d", i))
		checkState(fmt.Sprintf("op %d", i))
	}
}

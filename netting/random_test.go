package netting

import (
	"log"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

func encodeObligations(obl []Obligation) string {
	s := ""
	for _, o := range obl {
		s += " " + o.OID + ":" + o.From + "->" + o.To + "=" + itoa(o.Amount)
	}
	if s == "" {
		return "-"
	}
	return s[1:]
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func encodeResult(r CycleResult) string {
	s := "cycle=" + itoa(r.Cycle) + " defaulters="
	if len(r.Defaulters) == 0 {
		s += "-"
	}
	for _, rd := range r.Defaulters {
		s += "["
		for i, id := range rd {
			if i > 0 {
				s += ","
			}
			s += id
		}
		s += "]"
	}
	s += " revoked=["
	for i, o := range r.Revoked {
		if i > 0 {
			s += ","
		}
		s += o
	}
	s += "] nets={"
	for i, p := range r.Positions {
		if i > 0 {
			s += ","
		}
		s += p.ID + ":" + itoa(p.Net)
	}
	s += "} inst="
	if len(r.Instructions) == 0 {
		s += "-"
	}
	for _, in := range r.Instructions {
		s += " " + in.Payer + "->" + in.Payee + "=" + itoa(in.Amount)
	}
	return s
}

// assertInvariants verifies the algebraic guarantees independent of how the
// result was produced.
func assertInvariants(t *testing.T, parties map[string]int64, r CycleResult, basis string) {
	t.Helper()

	var sum int64
	defaulted := map[string]bool{}
	for _, rd := range r.Defaulters {
		for _, id := range rd {
			if defaulted[id] {
				t.Fatalf("%s: party %s defaulted in two rounds", basis, id)
			}
			defaulted[id] = true
		}
	}
	for _, p := range r.Positions {
		sum += p.Net
		if defaulted[p.ID] && p.Net != 0 {
			t.Fatalf("%s: defaulter %s has net %d, want 0", basis, p.ID, p.Net)
		}
		if !defaulted[p.ID] && p.Net < 0 && -p.Net > parties[p.ID] {
			t.Fatalf("%s: survivor %s net %d breaches cap %d", basis, p.ID, p.Net, parties[p.ID])
		}
	}
	if sum != 0 {
		t.Fatalf("%s: net positions sum to %d, want 0", basis, sum)
	}

	var payerTotal, payeeTotal int64
	residual := map[string]int64{}
	for _, p := range r.Positions {
		residual[p.ID] = p.Net
	}
	for _, in := range r.Instructions {
		if in.Amount <= 0 {
			t.Fatalf("%s: non-positive instruction %+v", basis, in)
		}
		payerTotal += in.Amount
		payeeTotal += in.Amount
		residual[in.Payer] += in.Amount
		residual[in.Payee] -= in.Amount
	}
	if payerTotal != payeeTotal {
		t.Fatalf("%s: payer total %d != payee total %d", basis, payerTotal, payeeTotal)
	}
	for id, v := range residual {
		if v != 0 {
			t.Fatalf("%s: instructions leave %s at %d", basis, id, v)
		}
	}
	if n := nonzeroParties(r.Positions); n > 0 && len(r.Instructions) > n-1 {
		t.Fatalf("%s: %d instructions exceed nonzero parties - 1 = %d", basis, len(r.Instructions), n-1)
	}
}

func generateCase(rng *rand.Rand) (map[string]int64, []Obligation) {
	n := 2 + rng.Intn(7) // 2..8 parties
	parties := make(map[string]int64, n)
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		id := partyID(i)
		ids[i] = id
		parties[id] = int64(rng.Intn(40)) // caps 0..39 create frequent defaults
	}

	m := rng.Intn(30) // 0..29 obligations
	obl := make([]Obligation, 0, m)
	for i := 0; i < m; i++ {
		from := ids[rng.Intn(n)]
		to := ids[rng.Intn(n)]
		if from == to {
			i--
			continue
		}
		obl = append(obl, Obligation{
			OID:    "o" + itoa(int64(i)),
			From:   from,
			To:     to,
			Amount: int64(1 + rng.Intn(60)),
		})
	}
	return parties, obl
}

func partyID(i int) string {
	// Single-byte, lexicographically ordered ids.
	return string(rune('a' + i))
}

func buildEngine(t *testing.T, parties map[string]int64, obl []Obligation, rng *rand.Rand) CycleResult {
	t.Helper()
	e := NewEngine()
	ids := make([]string, 0, len(parties))
	for id := range parties {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := e.Register(id, parties[id]); err != nil {
			t.Fatal(err)
		}
	}
	order := rng.Perm(len(obl))
	for _, idx := range order {
		o := obl[idx]
		if err := e.Submit(o.OID, o.From, o.To, o.Amount); err != nil {
			t.Fatalf("unexpected submit error: %v", err)
		}
	}
	return e.Close()
}

func TestRandomDifferential(t *testing.T) {
	const cases = 2000
	master := rand.New(rand.NewSource(20261001))

	for c := 0; c < cases; c++ {
		seed := master.Int63()
		rng := rand.New(rand.NewSource(seed))
		parties, obl := generateCase(rng)

		// Same set, two arbitrary submission orders -> identical Close output.
		r1 := buildEngine(t, parties, obl, rand.New(rand.NewSource(seed^0x5a5a)))
		r2 := buildEngine(t, parties, obl, rand.New(rand.NewSource(seed^0xa5a5)))
		if !reflect.DeepEqual(r1, r2) {
			t.Fatalf("case %d seed %d: order dependence\norder1 %s\norder2 %s",
				c, seed, encodeResult(r1), encodeResult(r2))
		}

		nRounds, nRev, nPos, nInst := naiveSettle(parties, obl, true)
		if !reflect.DeepEqual(nRounds, r1.Defaulters) ||
			!reflect.DeepEqual(nRev, r1.Revoked) ||
			!reflect.DeepEqual(nPos, r1.Positions) ||
			!reflect.DeepEqual(nInst, r1.Instructions) {
			t.Fatalf("case %d seed %d: engine vs naive differ\ninput    %s\nengine   %s\nnaive    rounds=%v rev=%v nets=%v inst=%v",
				c, seed, encodeObligations(obl), encodeResult(r1), nRounds, nRev, nPos, nInst)
		}

		basis := "case " + itoa(int64(c)) + " seed " + itoa(seed)
		assertInvariants(t, parties, r1, basis)

		log.Printf("random case %d seed=%d | input: parties=%v obl=%s | output: %s | basis: engine==naive(simultaneous), order-independent replay, invariants(sum=0, -net<=cap for survivors, totals equal, count<=nonzero-1)",
			c, seed, parties, encodeObligations(obl), encodeResult(r1))
	}
}

func TestConcurrent(t *testing.T) {
	e := NewEngine()
	parties := map[string]int64{"A": 1000, "B": 1000, "C": 1000, "D": 1000}
	for id, cap := range parties {
		mustReg(t, e, id, cap)
	}

	const submitters = 8
	const perSubmitter = 200
	var submitted int64
	var attempts int64

	var wg sync.WaitGroup
	for g := 0; g < submitters; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g) + 77))
			ids := []string{"A", "B", "C", "D"}
			for j := 0; j < perSubmitter; j++ {
				atomic.AddInt64(&attempts, 1)
				from := ids[rng.Intn(4)]
				to := ids[rng.Intn(4)]
				oid := "g" + itoa(int64(g)) + "-" + itoa(int64(j))
				err := e.Submit(oid, from, to, 1+int64(rng.Intn(20)))
				switch {
				case err == nil:
					atomic.AddInt64(&submitted, 1)
				case err == ErrSelfCounterparty:
					// expected for random endpoints; state unchanged
				default:
					t.Errorf("unexpected submit error: %v", err)
				}
			}
		}(g)
	}

	var results []CycleResult
	var closerWG sync.WaitGroup
	closerWG.Add(1)
	go func() {
		defer closerWG.Done()
		for atomic.LoadInt64(&attempts) < int64(submitters*perSubmitter) {
			results = append(results, e.Close())
		}
	}()

	wg.Wait()
	closerWG.Wait()
	results = append(results, e.Close())

	for _, r := range results {
		assertInvariants(t, parties, r, "concurrent cycle "+itoa(r.Cycle))
	}
}

package ontology

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

type call struct {
	op  string
	t   int
	k   int
	val int64
}

func runNaive(k int, calls []call) []string {
	s := newNaive(k)
	var out []string
	for _, c := range calls {
		if c.op == "Begin" {
			id := s.begin()
			out = append(out, fmt.Sprintf("Begin = T%d", id))
			continue
		}
		s.log = nil
		var r nResult
		switch c.op {
		case "Read":
			r = s.read(c.t, c.k-1)
		case "Write":
			r = s.write(c.t, c.k-1, c.val)
		case "Commit":
			r = s.commit(c.t)
		case "Abort":
			r = s.abort(c.t)
		}
		out = append(out, formatNaive(c.op, s.log, r))
	}
	return out
}

func formatNaive(op string, logs []string, r nResult) string {
	var b strings.Builder
	switch {
	case r.rejected != "":
		b.WriteString("=> REJECTED(" + r.rejected + ")")
	case r.deadlock:
		b.WriteString("=> DEADLOCK")
	default:
		b.WriteString("=> " + naiveStatusName(op, r.status))
		if op == "Read" {
			b.WriteString(fmt.Sprintf(" value=%d", r.value))
		}
	}
	for _, e := range r.events {
		switch {
		case e.grant:
			b.WriteString(fmt.Sprintf(" | GRANT T%d k%d %s v=%d",
				e.txn, e.key, string("SXC"[e.mode]), e.val))
		case e.committedKey == -1:
			b.WriteString(fmt.Sprintf(" | COMMIT T%d read-only", e.txn))
		default:
			b.WriteString(fmt.Sprintf(" | COMMIT T%d k%d v=%d",
				e.txn, e.committedKey, e.committedValue))
		}
	}
	return b.String()
}

func naiveStatusName(op string, st nStatus) string {
	switch st {
	case nActive:
		return "active"
	case nWaiting:
		return "waiting"
	case nCommitting:
		return "committing"
	default:
		if op == "Commit" {
			return "committed"
		}
		return "aborted"
	}
}

func runReal(k int, calls []call) []string {
	m, err := NewManager(k)
	if err != nil {
		panic(err)
	}
	idMap := map[int]int{}
	beginCount := 0
	var out []string
	for _, c := range calls {
		if c.op == "Begin" {
			beginCount++
			idMap[beginCount] = m.Begin().Txn
			out = append(out, fmt.Sprintf("Begin = T%d", idMap[beginCount]))
			continue
		}
		t := idMap[c.t]
		var r Result
		switch c.op {
		case "Read":
			r = m.Read(t, c.k)
		case "Write":
			r = m.Write(t, c.k, c.val)
		case "Commit":
			r = m.Commit(t)
		case "Abort":
			r = m.Abort(t)
		}
		out = append(out, formatReal(c.op, r))
	}
	return out
}

func formatReal(op string, r Result) string {
	var b strings.Builder
	switch {
	case r.Rejected:
		b.WriteString("=> REJECTED(" + r.RejectErr.Error() + ")")
	case r.Deadlock:
		b.WriteString("=> DEADLOCK")
	default:
		b.WriteString("=> " + statusName(r.Status))
		if op == "Read" {
			b.WriteString(fmt.Sprintf(" value=%d", r.Value))
		}
	}
	for _, e := range r.Events {
		switch {
		case e.Kind == Grant:
			b.WriteString(fmt.Sprintf(" | GRANT T%d k%d %s v=%d",
				e.Txn, e.Key, modeName(e.Mode), e.Value))
		case e.Key == -1:
			b.WriteString(fmt.Sprintf(" | COMMIT T%d read-only", e.Txn))
		default:
			b.WriteString(fmt.Sprintf(" | COMMIT T%d k%d v=%d", e.Txn, e.Key, e.Value))
		}
	}
	return b.String()
}

func normalizeRejects(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		for _, pair := range [][2]string{
			{ErrUnknownTxn.Error(), "unknown txn"},
			{ErrTxnNotAbortable.Error(), "not abortable"},
			{ErrTxnNotActive.Error(), "not active"},
			{ErrKeyOutOfRange.Error(), "bad key"},
		} {
			l = strings.ReplaceAll(l, pair[0], pair[1])
		}
		out[i] = l
	}
	return out
}

func genCalls(rng *rand.Rand, maxTxn, k, maxOps int) []call {
	n := 1 + rng.Intn(maxTxn)
	var calls []call
	for i := 1; i <= n; i++ {
		calls = append(calls, call{op: "Begin"})
	}
	ops := 1 + rng.Intn(maxOps)
	for i := 0; i < ops; i++ {
		t := 1 + rng.Intn(n)
		var c call
		switch rng.Intn(10) {
		case 0, 1, 2, 3:
			c = call{op: "Read", t: t, k: 1 + rng.Intn(k)}
			if rng.Intn(8) == 0 {
				c.k = 1 + k + rng.Intn(3)
			}
		case 4, 5, 6, 7:
			c = call{op: "Write", t: t, k: 1 + rng.Intn(k), val: int64(rng.Intn(7)) - 3}
		case 8:
			c = call{op: "Commit", t: t}
		case 9:
			c = call{op: "Abort", t: t}
		}
		calls = append(calls, c)
	}
	return calls
}

func TestDifferentialRandom(t *testing.T) {
	const groups = 2000
	mismatch := 0
	for g := 0; g < groups; g++ {
		rng := rand.New(rand.NewSource(int64(g + 1)))
		k := 1 + rng.Intn(4)
		calls := genCalls(rng, 6, k, 40)

		var input []string
		beginSeen := 0
		for _, c := range calls {
			switch c.op {
			case "Begin":
				beginSeen++
				input = append(input, fmt.Sprintf("Begin->T%d", beginSeen))
			case "Read":
				input = append(input, fmt.Sprintf("Read(T%d,k%d)", c.t, c.k))
			case "Write":
				input = append(input, fmt.Sprintf("Write(T%d,k%d,%d)", c.t, c.k, c.val))
			case "Commit":
				input = append(input, fmt.Sprintf("Commit(T%d)", c.t))
			case "Abort":
				input = append(input, fmt.Sprintf("Abort(T%d)", c.t))
			}
		}

		naive := normalizeRejects(runNaive(k, calls))
		real := normalizeRejects(runReal(k, calls))
		if len(naive) != len(real) {
			t.Fatalf("seed %d length mismatch", g+1)
		}
		bad := -1
		for i := range naive {
			if naive[i] != real[i] {
				bad = i
				break
			}
		}
		if bad >= 0 {
			mismatch++
			if mismatch <= 5 {
				t.Logf("SEED %d INPUT: %s", g+1, strings.Join(input, " "))
				lo, hi := bad-3, bad+4
				if lo < 0 {
					lo = 0
				}
				if hi > len(naive) {
					hi = len(naive)
				}
				for i := lo; i < hi; i++ {
					mark := "  "
					if naive[i] != real[i] {
						mark = "!="
					}
					t.Logf("  %s %-26s naive=%q real=%q", mark, input[i], naive[i], real[i])
				}
			}
		}
	}
	if mismatch != 0 {
		t.Fatalf("%d/%d sequences diverged from naive simulation", mismatch, groups)
	}
	t.Logf("all %d random sequences matched the naive simulation", groups)
}

func TestConcurrentSafety(t *testing.T) {
	m, _ := NewManager(8)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(seed)))
			tid := m.Begin().Txn
			for i := 0; i < 200; i++ {
				k := 1 + rng.Intn(8)
				switch rng.Intn(6) {
				case 0, 1:
					m.Read(tid, k)
				case 2, 3:
					m.Write(tid, k, int64(rng.Intn(5)))
				case 4:
					m.Commit(tid)
					tid = m.Begin().Txn
				case 5:
					m.Abort(tid)
					tid = m.Begin().Txn
				}
			}
		}(w)
	}
	wg.Wait()
	assertInvariants(t, m)
}

func assertInvariants(t *testing.T, m *Manager) {
	for ki := range m.keys {
		ks := &m.keys[ki]
		var holders []int
		for h := range ks.granted {
			holders = append(holders, h)
		}
		for i := 0; i < len(holders); i++ {
			for j := i + 1; j < len(holders); j++ {
				if !compatibleModes(ks.granted[holders[i]], ks.granted[holders[j]]) {
					t.Fatalf("k%d incompatible grants %v", ki+1, ks.granted)
				}
			}
		}
		sawNormal := false
		for _, e := range ks.queue {
			if e.mode != C {
				sawNormal = true
			} else if sawNormal {
				t.Fatalf("k%d conversion behind normal request", ki+1)
			}
		}
		if len(ks.queue) > 0 {
			head := ks.queue[0]
			if m.compatibleWithGranted(ki, head.mode, head.txn) {
				t.Fatalf("k%d head request is grantable", ki+1)
			}
		}
	}
}

package redlock

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

const maxClock = 1_000_000_000_000_000

func mustNew(t *testing.T, nodeCount, timeout, driftPermille, maxTTL int64) *Redlock {
	t.Helper()
	locker, err := New(nodeCount, timeout, driftPermille, maxTTL)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d): %v", nodeCount, timeout, driftPermille, maxTTL, err)
	}
	return locker
}

func TestInvalidConfigAndValidationOrder(t *testing.T) {
	cases := [][4]int64{
		{0, 1, 0, 1},
		{10, 1, 0, 1},
		{1, 0, 0, 1},
		{1, 1_000_001, 0, 1},
		{1, 1, -1, 1},
		{1, 1, 1001, 1},
		{1, 1, 0, 0},
		{1, 1, 0, 1_000_000_001},
	}
	for _, config := range cases {
		if _, err := New(config[0], config[1], config[2], config[3]); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("New(%v) error = %v", config, err)
		}
	}

	l := mustNew(t, 1, 5, 0, 10)
	l.clock = 10
	if _, err := l.Acquire("", 11, -1, []int64{}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Acquire order error = %v", err)
	}
	if _, err := l.Acquire("r", 11, -1, []int64{0}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Acquire TTL precedes time, got %v", err)
	}
	if _, err := l.Acquire("r", 1, -1, []int64{0}); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("Acquire time precedes rollback, got %v", err)
	}
	if _, err := l.Acquire("r", 1, 9, []int64{0}); !errors.Is(err, ErrClockMovedBack) {
		t.Fatalf("Acquire rollback error = %v", err)
	}
	if _, err := l.Unlock("", 0, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Unlock order error = %v", err)
	}
	if err := l.Restart(5, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Restart order error = %v", err)
	}
	if _, err := l.Count("", 0, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Count order error = %v", err)
	}
}

func requireResult(t *testing.T, result *AcquireResult, err error, want *AcquireResult) {
	t.Helper()
	if err != nil {
		t.Fatalf("Acquire returned error %v", err)
	}
	if *result != *want {
		t.Fatalf("Acquire result = %+v, want %+v", result, want)
	}
}

func TestSpecExamples(t *testing.T) {
	l := mustNew(t, 5, 50, 10, 1000)

	r, err := l.Acquire("r", 1000, 0, []int64{10, 10, 10, -1, 10})
	requireResult(t, r, err, &AcquireResult{1, true, 4, 898, 988, 90})

	if count, err := l.Count("r", 1, 1004); err != nil || count != 4 {
		t.Fatalf("Count at 1004 = %d, %v; want 4", count, err)
	}
	if count, err := l.Count("r", 1, 1005); err != nil || count != 3 {
		t.Fatalf("Count at 1005 = %d, %v; want 3", count, err)
	}

	r, err = l.Acquire("s", 100, 100, []int64{40, 40, 40, 40, 40})
	requireResult(t, r, err, &AcquireResult{2, false, 5, -103, 197, 300})
	if count, err := l.Count("s", 2, 300); err != nil || count != 0 {
		t.Fatalf("failed acquire left %d records, %v", count, err)
	}

	r, err = l.Acquire("t", 1000, 300, []int64{70, 10, 10, 10, 10})
	requireResult(t, r, err, &AcquireResult{3, true, 4, 898, 1288, 390})
	if count, err := l.Count("t", 3, 400); err != nil || count != 5 {
		t.Fatalf("Count after timeout-node grant = %d, %v; want 5", count, err)
	}
}

func TestArrivalUsesFloorHalfRTT(t *testing.T) {
	cases := []struct {
		rtt     int64
		arrival int64
	}{
		{11, 15},
		{12, 16},
	}
	for _, tc := range cases {
		l := mustNew(t, 1, 20, 0, 100)
		r, err := l.Acquire("r", 100, 10, []int64{tc.rtt})
		if err != nil {
			t.Fatalf("rtt %d: %v", tc.rtt, err)
		}
		entry := l.nodes[0].records["r"]
		if entry.expires != tc.arrival+100 {
			t.Fatalf("rtt %d arrival = %d, want %d", tc.rtt, entry.expires-100, tc.arrival)
		}
		if r.ClockEnd != 10+tc.rtt {
			t.Fatalf("rtt %d clock end = %d", tc.rtt, r.ClockEnd)
		}
	}
}

func TestQuorumBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rtt     []int64
		granted int
		success bool
	}{
		{"two", []int64{1, 1, -1, -1}, 2, false},
		{"three", []int64{1, 1, 1, -1}, 3, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := mustNew(t, 4, 100, 0, 1000)
			r, err := l.Acquire("r", 1000, 0, tc.rtt)
			if err != nil {
				t.Fatal(err)
			}
			if r.Granted != tc.granted || r.Success != tc.success {
				t.Fatalf("got granted=%d success=%v", r.Granted, r.Success)
			}
		})
	}
}

func TestValidForZeroAndOne(t *testing.T) {
	cases := []struct {
		elapsed int64
		valid   int64
		success bool
	}{
		{98, 0, false},
		{97, 1, true},
	}
	for _, tc := range cases {
		l := mustNew(t, 1, 200, 0, 1000)
		r, err := l.Acquire("r", 100, 0, []int64{tc.elapsed})
		if err != nil {
			t.Fatal(err)
		}
		if r.ValidFor != tc.valid || r.Success != tc.success {
			t.Fatalf("elapsed=%d got valid=%d success=%v, want %d/%v", tc.elapsed, r.ValidFor, r.Success, tc.valid, tc.success)
		}
	}
}

func TestDriftFloorsThenAddsTwo(t *testing.T) {
	l := mustNew(t, 1, 1000, 15, 1000)
	r, err := l.Acquire("r", 10, 0, []int64{0})
	if err != nil {
		t.Fatal(err)
	}
	if r.ValidFor != 8 || r.Until != 8 {
		t.Fatalf("valid=%d until=%d, want 8/8", r.ValidFor, r.Until)
	}

	zeroDrift := mustNew(t, 1, 1000, 0, 1000)
	r, err = zeroDrift.Acquire("r", 10, 0, []int64{0})
	if err != nil {
		t.Fatal(err)
	}
	if r.ValidFor != 8 {
		t.Fatalf("D=0 fixed drift leaves valid=%d, want 8", r.ValidFor)
	}
}

func TestTimedOutGrantNotCountedButReleased(t *testing.T) {
	l := mustNew(t, 3, 50, 0, 1000)
	r, err := l.Acquire("r", 100, 0, []int64{60, -1, -1})
	if err != nil {
		t.Fatal(err)
	}
	if r.Success || r.Granted != 0 || r.ClockEnd != 150 {
		t.Fatalf("result = %+v", r)
	}
	if _, ok := l.nodes[0].records["r"]; ok {
		t.Fatal("timed-out grant was not released")
	}
}

func TestUnreachableNodeReceivesRelease(t *testing.T) {
	l := mustNew(t, 3, 50, 0, 1000)
	l.nodes[2].records["r"] = record{token: 1, expires: 1000}
	_, err := l.Acquire("r", 100, 0, []int64{-1, -1, -1})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := l.nodes[2].records["r"]; ok {
		t.Fatal("failure release did not reach unreachable node")
	}
}

func TestExpiryBoundaryNX(t *testing.T) {
	cases := []struct {
		arrival int64
		granted bool
	}{
		{9, false},
		{10, true},
	}
	for _, tc := range cases {
		l := mustNew(t, 1, 100, 0, 1000)
		l.nodes[0].records["r"] = record{token: 1, expires: 10}
		r, err := l.Acquire("r", 100, 0, []int64{tc.arrival * 2})
		if err != nil {
			t.Fatal(err)
		}
		if (r.Granted == 1) != tc.granted {
			t.Fatalf("arrival=%d granted=%v, want %v", tc.arrival, r.Granted == 1, tc.granted)
		}
	}
}

func TestSameClientOldTokenIsRejected(t *testing.T) {
	l := mustNew(t, 1, 100, 0, 1000)
	first, err := l.Acquire("r", 100, 0, []int64{0})
	if err != nil {
		t.Fatal(err)
	}
	second, err := l.Acquire("r", 100, first.ClockEnd, []int64{0})
	if err != nil {
		t.Fatal(err)
	}
	if second.Granted != 0 || second.Success {
		t.Fatalf("active old token accepted: %+v", second)
	}
	if l.nodes[0].records["r"].token != first.Token {
		t.Fatal("failed acquire overwrote active old token")
	}
}

func TestFailedAcquireConsumesToken(t *testing.T) {
	l := mustNew(t, 1, 100, 0, 1000)
	failed, err := l.Acquire("r", 100, 0, []int64{100})
	if err != nil {
		t.Fatal(err)
	}
	if failed.Success || failed.Token != 1 {
		t.Fatalf("failed = %+v", failed)
	}
	next, err := l.Acquire("r", 100, failed.ClockEnd, []int64{0})
	if err != nil {
		t.Fatal(err)
	}
	if next.Token != 2 {
		t.Fatalf("next token = %d, want 2", next.Token)
	}
}

func TestRestartQuietBoundary(t *testing.T) {
	cases := []struct {
		name    string
		arrival int64
		granted bool
	}{
		{"before", 1399, false},
		{"equal", 1400, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := mustNew(t, 1, 1000, 0, 1000)
			if err := l.Restart(0, 400); err != nil {
				t.Fatalf("restart single node: %v", err)
			}
			r, err := l.Acquire("r", 1000, tc.arrival, []int64{0})
			if err != nil {
				t.Fatal(err)
			}
			if (r.Granted == 1) != tc.granted || r.Success != tc.granted {
				t.Fatalf("grant at quiet boundary = granted:%d success:%v, want %v", r.Granted, r.Success, tc.granted)
			}
		})
	}
}

func TestUnlockAndCountExpiryBoundaries(t *testing.T) {
	l := mustNew(t, 1, 100, 0, 1000)
	r, err := l.Acquire("r", 10, 0, []int64{0})
	if err != nil {
		t.Fatal(err)
	}
	if count, err := l.Count("r", r.Token, 10); err != nil || count != 0 {
		t.Fatalf("Count at exact expiry = %d,%v; want 0", count, err)
	}
	if count, err := l.Count("r", r.Token, 9); err != nil || count != 1 {
		t.Fatalf("Count before expiry = %d,%v; want 1", count, err)
	}
	if count, err := l.Unlock("r", r.Token, 20); err != nil || count != 1 {
		t.Fatalf("Unlock expired record = %d,%v; want 1", count, err)
	}
}

func TestRejectedOperationsDoNotChangeState(t *testing.T) {
	l := mustNew(t, 2, 50, 0, 100)
	ok, err := l.Acquire("r", 10, 5, []int64{0, 0})
	if err != nil {
		t.Fatal(err)
	}
	clock, tokens := l.clock, l.tokens
	quiet := append([]int64(nil), []int64{l.nodes[0].quietUntil, l.nodes[1].quietUntil}...)
	records := append([]record(nil), []record{l.nodes[0].records["r"], l.nodes[1].records["r"]}...)

	check := func(name string, call func() error) {
		t.Helper()
		if err := call(); !errors.Is(err, ErrInvalidArgument) && !errors.Is(err, ErrInvalidTime) && !errors.Is(err, ErrClockMovedBack) {
			t.Fatalf("%s returned %v", name, err)
		}
		if l.clock != clock || l.tokens != tokens {
			t.Fatalf("%s changed clock/token", name)
		}
		for i := range l.nodes {
			if l.nodes[i].quietUntil != quiet[i] || l.nodes[i].records["r"] != records[i] || len(l.nodes[i].records) != 1 {
				t.Fatalf("%s changed node %d state", name, i)
			}
		}
	}

	check("acquire argument", func() error { _, e := l.Acquire("", 10, 10, []int64{0, 0}); return e })
	check("acquire time", func() error { _, e := l.Acquire("x", 10, maxClock+1, []int64{0, 0}); return e })
	check("acquire rollback", func() error { _, e := l.Acquire("x", 10, 4, []int64{0, 0}); return e })
	check("unlock argument", func() error { _, e := l.Unlock("", 1, 10); return e })
	check("unlock time", func() error { _, e := l.Unlock("r", ok.Token, -1); return e })
	check("unlock rollback", func() error { _, e := l.Unlock("r", ok.Token, 4); return e })
	check("restart argument", func() error { return l.Restart(2, 10) })
	check("restart time", func() error { return l.Restart(0, maxClock+1) })
	check("restart rollback", func() error { return l.Restart(0, 4) })
	check("count argument", func() error { _, e := l.Count("", 1, 10); return e })
	check("count time", func() error { _, e := l.Count("r", ok.Token, -1); return e })
	check("count rollback", func() error { _, e := l.Count("r", ok.Token, 4); return e })
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	l := mustNew(t, 5, 50, 10, 1000)
	var wg sync.WaitGroup
	for worker := 0; worker < 20; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if count, err := l.Unlock(fmt.Sprintf("r-%d-%d", worker, i), 1, 100); err != nil || count != 0 {
					t.Errorf("concurrent unlock = %d,%v", count, err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	if l.clock != 100 || l.tokens != 0 {
		t.Fatalf("concurrent state = clock:%d tokens:%d", l.clock, l.tokens)
	}
}

type naiveNode struct {
	quietUntil int64
	records    map[string]record
}

type naiveLocker struct {
	nodes   []naiveNode
	clock   int64
	tokens  int64
	ttl     int64
	timeout int64
	drift   int64
}

func newNaive(nodeCount, timeout, driftPermille, maxTTL int64) *naiveLocker {
	nodes := make([]naiveNode, nodeCount)
	for i := range nodes {
		nodes[i].records = make(map[string]record)
	}
	return &naiveLocker{nodes: nodes, ttl: maxTTL, timeout: timeout, drift: driftPermille}
}

func (l *naiveLocker) acquire(resource string, ttl, start int64, rtt []int64) (*AcquireResult, error) {
	if resource == "" || ttl < 1 || ttl > l.ttl || len(rtt) != len(l.nodes) {
		return nil, ErrInvalidArgument
	}
	for _, latency := range rtt {
		if latency < -1 || latency > 2*l.timeout {
			return nil, ErrInvalidArgument
		}
	}
	if start < 0 || start > maxClock {
		return nil, ErrInvalidTime
	}
	if start < l.clock {
		return nil, ErrClockMovedBack
	}

	token := l.tokens + 1
	l.tokens = token
	clientClock := start
	granted := 0

	for i, latency := range rtt {
		if latency == -1 {
			clientClock += l.timeout
			continue
		}
		arrival := clientClock + latency/2
		grantedHere := false
		target := &l.nodes[i]
		if arrival >= target.quietUntil {
			if entry, ok := target.records[resource]; !ok || entry.expires <= arrival {
				target.records[resource] = record{token: token, expires: arrival + ttl}
				grantedHere = true
			}
		}
		if latency <= l.timeout {
			clientClock += latency
			if grantedHere {
				granted++
			}
		} else {
			clientClock += l.timeout
		}
	}

	drift := ttl*l.drift/1000 + 2
	validFor := ttl - (clientClock - start) - drift
	success := granted >= len(l.nodes)/2+1 && validFor > 0
	if !success {
		for i := range l.nodes {
			if entry, ok := l.nodes[i].records[resource]; ok && entry.token == token {
				delete(l.nodes[i].records, resource)
			}
		}
	}
	l.clock = clientClock
	return &AcquireResult{token, success, granted, validFor, start + ttl - drift, clientClock}, nil
}

func (l *naiveLocker) unlock(resource string, token, now int64) (int, error) {
	if resource == "" || token < 1 {
		return 0, ErrInvalidArgument
	}
	if now < 0 || now > maxClock {
		return 0, ErrInvalidTime
	}
	if now < l.clock {
		return 0, ErrClockMovedBack
	}
	count := 0
	for i := range l.nodes {
		if entry, ok := l.nodes[i].records[resource]; ok && entry.token == token {
			delete(l.nodes[i].records, resource)
			count++
		}
	}
	l.clock = now
	return count, nil
}

func (l *naiveLocker) restart(nodeIndex int, now int64) error {
	if nodeIndex < 0 || nodeIndex >= len(l.nodes) {
		return ErrInvalidArgument
	}
	if now < 0 || now > maxClock {
		return ErrInvalidTime
	}
	if now < l.clock {
		return ErrClockMovedBack
	}
	l.nodes[nodeIndex].records = make(map[string]record)
	l.nodes[nodeIndex].quietUntil = now + l.ttl
	l.clock = now
	return nil
}

func (l *naiveLocker) count(resource string, token, now int64) (int, error) {
	if resource == "" || token < 1 {
		return 0, ErrInvalidArgument
	}
	if now < 0 || now > maxClock {
		return 0, ErrInvalidTime
	}
	if now < l.clock {
		return 0, ErrClockMovedBack
	}
	count := 0
	for i := range l.nodes {
		if entry, ok := l.nodes[i].records[resource]; ok && entry.token == token && entry.expires > now {
			count++
		}
	}
	return count, nil
}

type randomOperation struct {
	kind     int
	resource string
	token    int64
	now      int64
	ttl      int64
	rtt      []int64
	node     int
}

type interval struct {
	start int64
	end   int64
}

func TestRandomNaiveComparisonAndDisjointIntervals(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))

	for trial := 0; trial < 2000; trial++ {
		nodeCount := int64(1 + rng.Intn(9))
		timeout := int64(1 + rng.Intn(100))
		driftPermille := int64(rng.Intn(21))
		maxTTL := int64(1 + rng.Intn(200))
		actual := mustNew(t, nodeCount, timeout, driftPermille, maxTTL)
		reference := newNaive(nodeCount, timeout, driftPermille, maxTTL)
		cursor := int64(0)
		lastInterval := make(map[string]interval)

		t.Logf("trial %d: input N=%d Tn=%d D=%d MaxTTL=%d; judgment=naive-state-and-interval-comparison", trial, nodeCount, timeout, driftPermille, maxTTL)

		for step := 0; step < 8; step++ {
			op := randomOperation{
				kind:     rng.Intn(4),
				resource: fmt.Sprintf("res-%d", 1+rng.Intn(3)),
				token:    int64(1 + rng.Intn(8)),
				node:     rng.Intn(int(nodeCount) + 2),
				ttl:      int64(1 + rng.Intn(int(maxTTL))),
			}
			cursor += int64(rng.Intn(120))
			op.now = cursor
			op.rtt = make([]int64, nodeCount)
			for i := range op.rtt {
				switch rng.Intn(5) {
				case 0:
					op.rtt[i] = -1
				case 1:
					op.rtt[i] = timeout + 1 + int64(rng.Intn(int(timeout)))
				default:
					op.rtt[i] = int64(rng.Intn(int(timeout) + 1))
				}
			}
			if rng.Intn(4) == 0 {
				switch rng.Intn(3) {
				case 0:
					op.resource = ""
				case 1:
					op.now = -1
				case 2:
					if op.kind == 0 {
						op.ttl = maxTTL + 1
						if rng.Intn(2) == 0 {
							op.rtt = []int64{0}
						}
					} else if op.kind == 2 {
						op.node = int(nodeCount)
					} else {
						op.token = 0
					}
				}
			}

			t.Logf("trial %d step %d input kind=%d res=%q token=%d now=%d ttl=%d rtt=%v node=%d", trial, step, op.kind, op.resource, op.token, op.now, op.ttl, op.rtt, op.node)

			var actualCount, referenceCount int
			var actualResult, referenceResult *AcquireResult
			var actualErr, referenceErr error

			switch op.kind {
			case 0:
				actualResult, actualErr = actual.Acquire(op.resource, op.ttl, op.now, op.rtt)
				referenceResult, referenceErr = reference.acquire(op.resource, op.ttl, op.now, op.rtt)
			case 1:
				actualCount, actualErr = actual.Unlock(op.resource, op.token, op.now)
				referenceCount, referenceErr = reference.unlock(op.resource, op.token, op.now)
			case 2:
				actualErr = actual.Restart(op.node, op.now)
				referenceErr = reference.restart(op.node, op.now)
			default:
				actualCount, actualErr = actual.Count(op.resource, op.token, op.now)
				referenceCount, referenceErr = reference.count(op.resource, op.token, op.now)
			}

			if !errors.Is(actualErr, referenceErr) {
				t.Fatalf("trial %d step %d errors actual=%v reference=%v", trial, step, actualErr, referenceErr)
			}
			if actualCount != referenceCount {
				t.Fatalf("trial %d step %d count actual=%d reference=%d", trial, step, actualCount, referenceCount)
			}
			if (actualResult == nil) != (referenceResult == nil) || actualResult != nil && *actualResult != *referenceResult {
				t.Fatalf("trial %d step %d result actual=%+v reference=%+v", trial, step, actualResult, referenceResult)
			}
			assertSameState(t, actual, reference, trial, step)

			if actualErr == nil && op.kind == 0 && actualResult.Success {
				if previous, ok := lastInterval[op.resource]; ok && actualResult.ClockEnd < previous.end {
					t.Fatalf("trial %d step %d overlapping intervals: new start=%d previous end=%d", trial, step, actualResult.ClockEnd, previous.end)
				}
				lastInterval[op.resource] = interval{start: actualResult.ClockEnd, end: actualResult.Until}
				t.Logf("trial %d step %d output success token=%d granted=%d valid=%d until=%d cend=%d; interval accepted", trial, step, actualResult.Token, actualResult.Granted, actualResult.ValidFor, actualResult.Until, actualResult.ClockEnd)
			} else if actualErr == nil && op.kind == 1 && actualCount > 0 {
				if previous, ok := lastInterval[op.resource]; ok && op.now < previous.end {
					lastInterval[op.resource] = interval{start: previous.start, end: op.now}
				}
				t.Logf("trial %d step %d output unlocked=%d at now=%d; interval endpoint updated", trial, step, actualCount, op.now)
			} else {
				t.Logf("trial %d step %d output err=%v count=%d result=%+v; no interval extension", trial, step, actualErr, actualCount, actualResult)
			}
		}
	}
}

func assertSameState(t *testing.T, actual *Redlock, reference *naiveLocker, trial, step int) {
	t.Helper()
	if actual.clock != reference.clock || actual.tokens != reference.tokens {
		t.Fatalf("trial %d step %d engine state actual=(%d,%d) reference=(%d,%d)", trial, step, actual.clock, actual.tokens, reference.clock, reference.tokens)
	}
	for i := range actual.nodes {
		if actual.nodes[i].quietUntil != reference.nodes[i].quietUntil {
			t.Fatalf("trial %d step %d node %d quiet actual=%d reference=%d", trial, step, i, actual.nodes[i].quietUntil, reference.nodes[i].quietUntil)
		}
		if len(actual.nodes[i].records) != len(reference.nodes[i].records) {
			t.Fatalf("trial %d step %d node %d record length %d != %d", trial, step, i, len(actual.nodes[i].records), len(reference.nodes[i].records))
		}
		for resource, actualEntry := range actual.nodes[i].records {
			referenceEntry, ok := reference.nodes[i].records[resource]
			if !ok || actualEntry != referenceEntry {
				t.Fatalf("trial %d step %d node %d resource %q actual=%+v reference=%+v", trial, step, i, resource, actualEntry, referenceEntry)
			}
		}
	}
}

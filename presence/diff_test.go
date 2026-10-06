package presence

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"testing"
)

// logf writes both to the test log and, when PRESENCE_LOG is set, to a file
// containing every input, output and the judgement basis.
var diffLog *os.File

func openDiffLog(t *testing.T) {
	if path := os.Getenv("PRESENCE_LOG"); path != "" && diffLog == nil {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		diffLog = f
	}
}

func dl(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	if diffLog != nil {
		fmt.Fprintln(diffLog, line)
	}
}

func errClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidArgument):
		return "invalid"
	case errors.Is(err, ErrClockBackwards):
		return "backwards"
	case errors.Is(err, ErrNotFound):
		return "notfound"
	case errors.Is(err, ErrTooManyDevices):
		return "toomany"
	case errors.Is(err, ErrAlreadySubscribed):
		return "dupsub"
	case errors.Is(err, ErrCannotSubscribeSelf):
		return "selfsub"
	case errors.Is(err, ErrCannotBlockSelf):
		return "selfblock"
	default:
		return "other:" + err.Error()
	}
}

func notesSummary(ns []Notification) string {
	if len(ns) == 0 {
		return "-"
	}
	s := ""
	for i, n := range ns {
		if i > 0 {
			s += ","
		}
		kind := "S"
		if !n.switchEvent {
			kind = "E"
		}
		s += fmt.Sprintf("%s:%s@%d%s", n.Target, n.Status, n.Effective, kind)
	}
	return s
}

func naiveSummary(ns []nNote) string {
	if len(ns) == 0 {
		return "-"
	}
	s := ""
	for i, n := range ns {
		if i > 0 {
			s += ","
		}
		kind := "S"
		if !n.switchE {
			kind = "E"
		}
		s += fmt.Sprintf("%s:%s@%d%s", n.target, n.status, n.eff, kind)
	}
	return s
}

func TestNaiveDifferential(t *testing.T) {
	openDiffLog(t)
	if diffLog != nil {
		defer diffLog.Close()
	}

	runs := 1500
	if v := os.Getenv("PRESENCE_RUNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			runs = n
		}
	}
	for seed := int64(1); seed <= int64(runs); seed++ {
		rng := rand.New(rand.NewSource(seed))
		T := int64(1 + rng.Intn(30))
		svc, err := New(Config{LeaseSeconds: T, Shards: 8})
		if err != nil {
			t.Fatal(err)
		}
		mdl := newNaive(T)
		dl("==== seed=%d T=%d ====", seed, T)

		const N = 6
		name := func(i int) string { return "u" + strconv.Itoa(i) }
		statuses := []Status{Away, Busy, Online}

		// Ensure a random subset of users exists.
		for i := 0; i < N; i++ {
			if rng.Intn(2) == 0 {
				now := int64(0)
				st := statuses[rng.Intn(3)]
				dev := "d" + strconv.Itoa(rng.Intn(9))
				e1 := svc.Report(name(i), dev, st, now)
				e2 := mdl.apply("report", name(i), dev, st, false, now)
				if errClass(e1) != errClass(e2) {
					t.Fatalf("seed %d init report mismatch %v vs %v", seed, e1, e2)
				}
				dl("seed=%d REPORT %s %s %s now=%d => %s", seed, name(i), dev, st, now, errClass(e1))
			}
		}

		maxSteps := 120
		for step := 0; step < maxSteps; step++ {
			now := rng.Int63n(80)
			a := name(rng.Intn(N))
			b := name(rng.Intn(N))
			dev := "d" + strconv.Itoa(rng.Intn(10))
			st := statuses[rng.Intn(3)]

			var op string
			var on bool
			var e1, e2 error
			var gotSvc string
			var gotNaive string

			switch rng.Intn(9) {
			case 0:
				op = "report"
				e1 = svc.Report(a, dev, st, now)
				e2 = mdl.apply(op, a, dev, st, false, now)
			case 1:
				op = "offline"
				e1 = svc.Offline(a, dev, now)
				e2 = mdl.apply(op, a, dev, 0, false, now)
			case 2:
				op = "invisible"
				on = rng.Intn(2) == 0
				e1 = svc.SetInvisible(a, on, now)
				e2 = mdl.apply(op, a, "", 0, on, now)
			case 3:
				op = "block"
				e1 = svc.Block(a, b, now)
				e2 = mdl.apply(op, a, b, 0, false, now)
			case 4:
				op = "unblock"
				e1 = svc.Unblock(a, b, now)
				e2 = mdl.apply(op, a, b, 0, false, now)
			case 5:
				op = "subscribe"
				e1 = svc.Subscribe(a, b, now)
				e2 = mdl.apply(op, a, b, 0, false, now)
			case 6:
				op = "unsubscribe"
				e1 = svc.Unsubscribe(a, b, now)
				e2 = mdl.apply(op, a, b, 0, false, now)
			case 7:
				op = "query"
				mdl.opCount++
				back := false
				if mdl.exists(a) && mdl.exists(b) &&
					(now < mdl.users[a] || now < mdl.users[b]) {
					back = true
				}
				r1, q1 := svc.Query(a, b, now)
				if q1 != nil {
					e1 = q1
					gotSvc = "err"
					wantClass := errClass(nil)
					if !mdl.exists(a) || !mdl.exists(b) {
						wantClass = "notfound"
					} else if back {
						wantClass = "backwards"
					}
					if got := errClass(q1); got != wantClass {
						t.Fatalf("seed %d query err %s->%s now=%d: impl=%s want=%s",
							seed, a, b, now, got, wantClass)
					}
					gotNaive = wantClass
				} else {
					if back || !mdl.exists(a) || !mdl.exists(b) {
						t.Fatalf("seed %d query accepted but naive rejects: %s->%s now=%d",
							seed, a, b, now)
					}
					gotSvc = fmt.Sprintf("%s/cnt=%d", r1.Status, r1.ActiveDevice)
					real, _, cnt := mdl.snapshot(b, now)
					want := real
					if a != b && (mdl.invisibleAt(b, now) || mdl.blockedAt(b, a, now)) {
						want = Offline
					}
					if errClass(nil) != "ok" || r1.Status != want || r1.ActiveDevice != cnt {
						t.Fatalf("seed %d query %s->%s now=%d: got %s want %s/cnt=%d",
							seed, a, b, now, gotSvc, want, cnt)
					}
					gotNaive = fmt.Sprintf("%s/cnt=%d", want, cnt)
					mdl.users[a] = now
					mdl.users[b] = now
				}
			case 8:
				op = "drain"
				mdl.opCount++
				targetsClockOK := true
				if mdl.exists(a) {
					if now < mdl.users[a] {
						targetsClockOK = false
					}
					for tb := range mdl.subs[a] {
						if now < mdl.users[tb] {
							targetsClockOK = false
						}
					}
				}
				r1, d1 := svc.Drain(a, now)
				if d1 != nil {
					e1 = d1
					gotSvc = "err"
					wantClass := "notfound"
					if mdl.exists(a) && !targetsClockOK {
						wantClass = "backwards"
					}
					if got := errClass(d1); got != wantClass {
						t.Fatalf("seed %d drain err %s now=%d: impl=%s want=%s",
							seed, a, now, got, wantClass)
					}
					gotNaive = wantClass
				} else {
					if !mdl.exists(a) || !targetsClockOK {
						t.Fatalf("seed %d drain accepted but naive rejects: %s now=%d",
							seed, a, now)
					}
					n2, drop2 := mdl.drain(a, now)
					gotSvc = fmt.Sprintf("[drop=%d]%s", r1.Dropped, notesSummary(r1.Notifications))
					gotNaive = fmt.Sprintf("[drop=%d]%s", drop2, naiveSummary(n2))
					if gotSvc != gotNaive {
						dl("MISMATCH seed=%d DRAIN %s now=%d\n  impl : %s\n  naive: %s",
							seed, a, now, gotSvc, gotNaive)
						t.Fatalf("seed %d drain mismatch for %s@%d\nimpl : %s\nnaive: %s",
							seed, a, now, gotSvc, gotNaive)
					}
					// Chain property: consecutive notes for the same target
					// have distinct statuses by construction; here we assert
					// ordering is non-decreasing in effective time.
					for i := 1; i < len(r1.Notifications); i++ {
						if r1.Notifications[i].Effective < r1.Notifications[i-1].Effective {
							t.Fatalf("seed %d drain not sorted", seed)
						}
					}
					mdl.users[a] = now
					for tb := range mdl.subs[a] {
						mdl.users[tb] = now
					}
					// Naive drain is cumulative; emulate queue clearing by
					// snapshotting delivered arrival points via re-subscribe
					// is unnecessary: tests track via matched outputs only.
				}
			}

			if op != "query" && op != "drain" {
				gotSvc = errClass(e1)
				gotNaive = errClass(e2)
				if gotSvc != gotNaive {
					dl("MISMATCH seed=%d %s a=%s b=%s dev=%s now=%d => impl=%s naive=%s",
						seed, op, a, b, dev, now, gotSvc, gotNaive)
					t.Fatalf("seed %d %s mismatch: impl=%s naive=%s",
						seed, op, gotSvc, gotNaive)
				}
			}
			dl("seed=%d step=%d %s a=%s b=%s dev=%s st=%s on=%v now=%d => impl=%s | naive=%s  [basis: error class and visible state must match]",
				seed, step, op, a, b, dev, st, on, now, gotSvc, gotNaive)
		}
		dl("seed=%d completed", seed)
	}
}

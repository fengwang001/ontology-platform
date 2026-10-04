package exec_test

import (
	"errors"
	"strconv"
	"testing"

	"ontology/exec"
)

func digestOf(i int) string { return "digest-" + strconv.Itoa(i) }

func TestRules(t *testing.T) {
	t.Run("prio rises on attach and falls on cancel", func(t *testing.T) {
		s := exec.New(3)
		must(t, s.Register("W", plat("os", "linux"), 2))
		s.Execute("a", plat("os", "linux"), 1, false)
		s.Execute("b", plat("os", "linux"), 4, false)
		hi, _ := s.Execute("a", plat("os", "linux"), 9, false)
		if got := s.QueueOrder(); !equalStr(got, []string{"a", "b"}) {
			t.Fatalf("queue = %v, want a,b", got)
		}
		if err := s.Cancel(hi); err != nil {
			t.Fatalf("cancel: %v", err)
		}
		if got := s.QueueOrder(); !equalStr(got, []string{"b", "a"}) {
			t.Fatalf("queue = %v, want b,a after prio falls back", got)
		}
	})

	t.Run("queue head mismatch does not block later match", func(t *testing.T) {
		s := exec.New(3)
		must(t, s.Register("win", plat("os", "win"), 2))
		s.Execute("x", plat("os", "linux"), 9, false)
		s.Execute("y", plat("os", "win"), 1, false)
		d, _, err := s.Poll("win")
		noErr(t, err, "poll")
		if d != "y" {
			t.Fatalf("poll = %s, want y (x skipped)", d)
		}
		d, _, err = s.Poll("win")
		if err != nil || d != "" {
			t.Fatalf("no match => empty non-error, got %q err=%v", d, err)
		}
	})

	t.Run("infra complete equals one loss reaching M", func(t *testing.T) {
		s := exec.New(1)
		must(t, s.Register("W", plat("k", "v"), 1))
		w, _ := s.Execute("g", plat("k", "v"), 1, false)
		d, att, _ := s.Poll("W")
		if d != "g" || att != 1 {
			t.Fatalf("poll = %s/%d", d, att)
		}
		if err := s.Complete("W", "g", 1, 0, true); err != nil {
			t.Fatalf("infra complete: %v", err)
		}
		if o, ok := receive(s, w); !ok || o.Kind != "Lost" {
			t.Fatalf("M=1 single infra loss => Lost, got %+v ok=%v", o, ok)
		}
	})

	t.Run("loss below M requeues and third loss loses", func(t *testing.T) {
		s := exec.New(3)
		must(t, s.Register("W", plat(), 1))
		w, _ := s.Execute("z", plat(), 0, false)
		for loss := 1; loss <= 2; loss++ {
			d, att, _ := s.Poll("W")
			if d != "z" || att != loss {
				t.Fatalf("round %d poll = %s/%d", loss, d, att)
			}
			noErr(t, s.Complete("W", "z", loss, 0, true), "infra loss")
			if _, ok := receive(s, w); ok {
				t.Fatalf("loss %d of 3 must not terminate", loss)
			}
		}
		d, att, _ := s.Poll("W")
		if d != "z" || att != 3 {
			t.Fatalf("third poll = %s/%d, want z/3", d, att)
		}
		noErr(t, s.Complete("W", "z", 3, 0, true), "third loss")
		if o, ok := receive(s, w); !ok || o.Kind != "Lost" {
			t.Fatalf("third loss => Lost, got %+v %v", o, ok)
		}
	})

	t.Run("loss returns to original seq position", func(t *testing.T) {
		s := exec.New(3)
		must(t, s.Register("W", plat(), 1))
		s.Execute("first", plat(), 5, false)
		s.Execute("second", plat(), 5, false)
		d, _, _ := s.Poll("W")
		if d != "first" {
			t.Fatalf("poll = %s want first", d)
		}
		noErr(t, s.Complete("W", "first", 1, 0, true), "loss first")
		if got := s.QueueOrder(); !equalStr(got, []string{"first", "second"}) {
			t.Fatalf("same prio original seq must win, got %v", got)
		}
	})

	t.Run("attach with different platform is invalid", func(t *testing.T) {
		s := exec.New(2)
		s.Execute("h", plat("os", "linux"), 1, false)
		if _, err := s.Execute("h", plat("os", "win"), 1, false); !errors.Is(err, exec.ErrInvalid) {
			t.Fatalf("different value err = %v", err)
		}
		if _, err := s.Execute("h", plat("os", "linux", "arch", "x86"), 1, false); !errors.Is(err, exec.ErrInvalid) {
			t.Fatalf("extra key err = %v", err)
		}
	})

	t.Run("skipCache attaches inflight and cache overrides on exit0", func(t *testing.T) {
		s := exec.New(2)
		must(t, s.Register("W", plat(), 1))
		w, _ := s.Execute("c", plat(), 0, false)
		w2, err := s.Execute("c", plat(), 0, true)
		noErr(t, err, "skipCache inflight attach")
		if s.InflightCount() != 1 {
			t.Fatalf("inflight = %d, want 1", s.InflightCount())
		}
		s.Poll("W")
		noErr(t, s.Complete("W", "c", 1, 0, false), "complete exit0")
		for _, id := range []int{w, w2} {
			if o, ok := receive(s, id); !ok || o.Kind != "Result" || o.Exit != 0 {
				t.Fatalf("waiter %d = %+v ok=%v want Result(0)", id, o, ok)
			}
		}
		w3, _ := s.Execute("c", plat(), 0, false)
		if o, ok := receive(s, w3); !ok || o.Kind != "Cached" || o.Exit != 0 {
			t.Fatalf("cache = %+v ok=%v want Cached(0)", o, ok)
		}
		// skipCache 无视缓存新建在途操作；再次 exit=0 覆盖缓存。
		fresh, _ := s.Execute("c", plat(), 0, true)
		d, att, _ := s.Poll("W")
		if d != "c" || att != 1 {
			t.Fatalf("fresh op poll = %s/%d want c/1", d, att)
		}
		noErr(t, s.Complete("W", "c", 1, 0, false), "override exit0")
		if o, ok := receive(s, fresh); !ok || o.Kind != "Result" {
			t.Fatalf("fresh waiter = %+v ok=%v want Result(0)", o, ok)
		}
		w4, _ := s.Execute("c", plat(), 0, false)
		if o, ok := receive(s, w4); !ok || o.Kind != "Cached" || o.Exit != 0 {
			t.Fatalf("override = %+v ok=%v want Cached(0)", o, ok)
		}
	})

	t.Run("nonzero exit is not cached", func(t *testing.T) {
		s := exec.New(2)
		must(t, s.Register("W", plat(), 1))
		w, _ := s.Execute("n", plat(), 0, false)
		s.Poll("W")
		noErr(t, s.Complete("W", "n", 1, 3, false), "exit3")
		if o, _ := receive(s, w); o.Kind != "Result" {
			t.Fatalf("want Result, got %+v", o)
		}
		w2, _ := s.Execute("n", plat(), 0, false)
		if o, ok := receive(s, w2); ok {
			t.Fatalf("nonzero exit must not cache, got %+v", o)
		}
	})

	t.Run("duplicate register, full slot, unknown worker and op errors", func(t *testing.T) {
		s := exec.New(2)
		must(t, s.Register("W", plat(), 1))
		if err := s.Register("W", plat(), 1); !errors.Is(err, exec.ErrExists) {
			t.Fatalf("duplicate register err = %v", err)
		}
		s.Execute("q", plat(), 0, false)
		s.Poll("W")
		if _, _, err := s.Poll("W"); !errors.Is(err, exec.ErrNoSlot) {
			t.Fatalf("full slot poll err = %v, want ErrNoSlot", err)
		}
		if err := s.Complete("ghost", "q", 1, 0, false); !errors.Is(err, exec.ErrNotFound) {
			t.Fatalf("complete by unknown worker err = %v, want ErrNotFound", err)
		}
		if err := s.Complete("W", "unknown", 1, 0, false); !errors.Is(err, exec.ErrState) {
			t.Fatalf("complete unknown op err = %v, want ErrState", err)
		}
	})

	t.Run("cancel twice conflicts and queued cancel deletes op", func(t *testing.T) {
		s := exec.New(2)
		w, _ := s.Execute("del", plat(), 0, false)
		if err := s.Cancel(w); err != nil {
			t.Fatalf("first cancel: %v", err)
		}
		if err := s.Cancel(w); !errors.Is(err, exec.ErrState) {
			t.Fatalf("second cancel err = %v, want ErrState", err)
		}
		if s.InflightCount() != 0 {
			t.Fatalf("queued op with no waiters must be deleted, inflight=%d", s.InflightCount())
		}
		if err := s.Cancel(99999); !errors.Is(err, exec.ErrNotFound) {
			t.Fatalf("cancel unknown waiter err = %v, want ErrNotFound", err)
		}
	})

	t.Run("invalid inputs and stale attempt precedence", func(t *testing.T) {
		s := exec.New(2)
		must(t, s.Register("W", plat(), 1))
		s.Execute("p", plat(), 0, false)
		s.Poll("W")
		if err := s.Complete("", "p", 9, 0, false); !errors.Is(err, exec.ErrInvalid) {
			t.Fatalf("invalid must take precedence, got %v", err)
		}
		if _, err := s.Execute("", plat(), 0, false); !errors.Is(err, exec.ErrInvalid) {
			t.Fatalf("empty digest err = %v", err)
		}
		if _, err := s.Execute("x", plat(), 10, false); !errors.Is(err, exec.ErrInvalid) {
			t.Fatalf("prio 10 err = %v", err)
		}
		if err := s.Register("bad", plat(), 0); !errors.Is(err, exec.ErrInvalid) {
			t.Fatalf("slots 0 err = %v", err)
		}
		if err := s.Register("bad", plat(), 65); !errors.Is(err, exec.ErrInvalid) {
			t.Fatalf("slots 65 err = %v", err)
		}
		if err := s.Complete("W", "p", 2, 0, false); !errors.Is(err, exec.ErrStaleAttempt) {
			t.Fatalf("stale attempt err = %v", err)
		}
	})

	t.Run("reregistration invalidates old attempt", func(t *testing.T) {
		s := exec.New(2)
		must(t, s.Register("W", plat(), 1))
		w, _ := s.Execute("r", plat(), 0, false)
		s.Poll("W")
		noErr(t, s.WorkerLost("W"), "lost")
		must(t, s.Register("W", plat(), 1))
		if err := s.Complete("W", "r", 1, 0, false); !errors.Is(err, exec.ErrState) {
			t.Fatalf("old attempt after reregister err = %v, want ErrState", err)
		}
		d, att, _ := s.Poll("W")
		if d != "r" || att != 2 {
			t.Fatalf("retry poll = %s/%d want r/2", d, att)
		}
		noErr(t, s.Complete("W", "r", 2, 0, false), "complete retry")
		if o, ok := receive(s, w); !ok || o.Kind != "Result" {
			t.Fatalf("waiter = %+v ok=%v", o, ok)
		}
	})

	t.Run("lookup budget independent of inflight size", func(t *testing.T) {
		measure := func(n int) int {
			s := exec.New(5)
			for i := 0; i < n; i++ {
				if _, err := s.Execute(digestOf(i), plat(), i%10, true); err != nil {
					t.Fatalf("seed execute: %v", err)
				}
			}
			before := s.Lookups()
			if _, err := s.Execute("target-absent-xyz", plat(), 0, false); err != nil {
				t.Fatalf("probe execute: %v", err)
			}
			return s.Lookups() - before
		}
		if got := measure(100); got > 2 {
			t.Fatalf("100 inflight: Execute lookups = %d, want <= 2", got)
		}
		if got := measure(10000); got > 2 {
			t.Fatalf("10000 inflight: Execute lookups = %d, want <= 2", got)
		}
	})
}

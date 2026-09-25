package api

import "errors"
import "reflect"
import "strconv"
import "strings"
import "sync"
import "testing"

func runScript(script string) (*Cluster, map[string]string) {
	c := New()
	m := map[string]string{}
	for _, op := range strings.Split(script, "|") {
		p := strings.Fields(op)
		i, _ := strconv.Atoi(p[1])
		switch p[0] {
		case "w":
			c.Write(p[1], p[2])
			m[p[1]] = p[2]
		case "f":
			c.Offline(i)
		case "n":
			c.Online(i)
		}
	}
	return c, m
}
func setup() (*Cluster, *Session) {
	c, _ := runScript("w k A|w k B|f 2|w k C|f 1|w k D|w k E|n 1|n 2")
	return c, c.Open(0)
}
func TestMonotonicRead(t *testing.T) {
	for _, route := range [][]int{{0, 0, 0}, {0, 1, 2}, {0, 2, 1, 0, 2}} {
		h, s := setup()
		prev := 0
		for _, idx := range route {
			if s.Replica != idx && h.SwitchReplica(s, idx) != nil {
				t.Fatal("switch failed")
			}
			v, l, err := h.Read(s, "k")
			if err != nil || v != "E" || l < prev {
				t.Fatalf("via R%d (%q,%d), want (E,>=%d)", idx, v, l, prev)
			}
			prev = l
		}
	}
}
func TestReferenceEquivalence(t *testing.T) {
	for _, tc := range []struct{ name, script string }{
		{"section3", "w k A|w k B|f 2|w k C|f 1|w k D|w k E|n 1|n 2"},
		{"two-keys-gap", "w x 1|f 1|w x 2|w y 3|n 1"},
	} {
		h, want := runScript(tc.script)
		if ref := h.View().Ref; !reflect.DeepEqual(map[string]string(ref), want) {
			t.Fatalf("%s Ref=%v want %v", tc.name, ref, want)
		}
		s := h.Open(0)
		for _, idx := range []int{0, 1, 2} {
			if idx != 0 && h.SwitchReplica(s, idx) != nil {
				t.Fatal("switch failed")
			}
			for k := range want {
				if v, _, _ := h.Read(s, k); v != want[k] {
					t.Fatalf("%s R%d/%s=%q want %q", tc.name, idx, k, v, want[k])
				}
			}
			if d := h.View().Replicas[idx].Data; !reflect.DeepEqual(d, want) {
				t.Fatalf("%s R%d data=%v want %v", tc.name, idx, d, want)
			}
		}
	}
}
func TestSwitchCaughtUp(t *testing.T) {
	for _, target := range []int{1, 2} {
		h, s := setup()
		if _, l, _ := h.Read(s, "k"); l != 5 {
			t.Fatalf("first read lsn=%d want 5", l)
		}
		if err := h.SwitchReplica(s, target); err != nil {
			t.Fatal(err)
		}
		r := h.View().Replicas[target]
		if r.Applied != 5 || r.Applied < s.Seen || r.Data["k"] != "E" {
			t.Fatalf("R%d applied=%d k=%q seen=%d", target, r.Applied, r.Data["k"], s.Seen)
		}
	}
}
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	h := New()
	h.Write("k", "v")
	h.Offline(1)
	s := h.Open(0)
	wants := []error{ErrEmptyKey, ErrEmptyKey, ErrReplicaIndex, ErrReplicaIndex, ErrReplicaIndex, ErrReplicaOffline}
	calls := []func() error{
		func() error { _, e := h.Write("", "z"); return e },
		func() error { _, _, e := h.Read(s, ""); return e },
		func() error { return h.Offline(3) },
		func() error { return h.Online(-1) },
		func() error { return h.SwitchReplica(s, 9) },
		func() error { return h.SwitchReplica(s, 1) },
	}
	errs := map[error]bool{}
	for i, call := range calls {
		st0, ss0 := h.View(), *s
		err := call()
		if !errors.Is(err, wants[i]) {
			t.Fatalf("%v want %v", err, wants[i])
		}
		if !reflect.DeepEqual(h.View(), st0) || *s != ss0 {
			t.Fatalf("rejected %v left a trace", err)
		}
		errs[err] = true
	}
	if len(errs) != 3 {
		t.Fatalf("distinct rejection errors=%d want 3", len(errs))
	}
	h.Write("k2", "w2") // cluster must still be usable
	if v, _, _ := h.Read(s, "k2"); v != "w2" {
		t.Fatalf("post-rejection read=%q want w2", v)
	}
}
func TestConcurrentReadsConsistent(t *testing.T) {
	for _, n := range []int{8, 64, 256} {
		h, s := setup()
		ls := make(chan int, n)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				v, l, err := h.Read(s, "k")
				h.View()
				if err != nil || v != "E" {
					t.Errorf("bad read (%q,%d): %v", v, l, err)
				}
				ls <- l
			}()
		}
		go New().SelfCheck()
		wg.Wait()
		close(ls)
		for l := range ls {
			if l != 5 {
				t.Fatalf("n=%d lsn=%d want 5", n, l)
			}
		}
	}
}

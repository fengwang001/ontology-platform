package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/api"
	"ontology/rep"
)

func TestEightStepTrace(t *testing.T) {
	e, _ := api.New(3)
	steps := []struct {
		put      bool
		val      string
		ver      int64
		reps     []int
		wantV    string
		wantR    int
		wantSnap string
	}{
		{put: true, val: "a", ver: 5, reps: []int{0, 1}, wantSnap: "[{a 5 true} {a 5 true} { 0 false}]"},
		{wantV: "a", wantR: 1, wantSnap: "[{a 5 true} {a 5 true} {a 5 true}]"},
		{put: true, val: "b", ver: 7, reps: []int{1, 2}, wantSnap: "[{a 5 true} {b 7 true} {b 7 true}]"},
		{put: true, val: "c", ver: 7, reps: []int{2}, wantSnap: "[{a 5 true} {b 7 true} {c 7 true}]"},
		{wantV: "c", wantR: 2, wantSnap: "[{c 7 true} {c 7 true} {c 7 true}]"},
		{put: true, val: "d", ver: 4, reps: []int{0}, wantSnap: "[{d 4 true} {c 7 true} {c 7 true}]"},
		{wantV: "c", wantR: 1, wantSnap: "[{c 7 true} {c 7 true} {c 7 true}]"},
		{wantV: "c", wantR: 0, wantSnap: "[{c 7 true} {c 7 true} {c 7 true}]"},
	}
	for i, s := range steps {
		if s.put {
			if err := e.Put("k", s.val, s.ver, s.reps); err != nil {
				t.Fatalf("step %d: %v", i+1, err)
			}
		} else if v, f, r, err := e.Read("k"); err != nil || !f || v != s.wantV || r != s.wantR {
			t.Fatalf("step %d read: (%q,%v,%d,%v)", i+1, v, f, r, err)
		}
		if got := fmt.Sprint(e.Snapshot("k")); got != s.wantSnap {
			t.Fatalf("step %d snap: got %s", i+1, got)
		}
	}
}

func TestRejections(t *testing.T) {
	for _, n := range []int{0, -2} {
		if _, err := api.New(n); !errors.Is(err, api.ErrBadN) {
			t.Fatalf("New(%d): %v", n, err)
		}
	}
	e, _ := api.New(3)
	_ = e.Put("k", "a", 5, []int{0, 1})
	before := fmt.Sprint(e.Snapshot("k"))
	cases := []struct {
		key  string
		ver  int64
		reps []int
		want error
	}{
		{"k", 0, []int{0}, api.ErrBadVersion},
		{"k", -3, []int{0}, api.ErrBadVersion},
		{"", 1, []int{0}, api.ErrEmptyKey},
		{"k", 1, nil, api.ErrBadReplicas},
		{"k", 1, []int{3}, api.ErrBadReplicas},
		{"k", 1, []int{-1}, api.ErrBadReplicas},
	}
	seen := map[error]bool{}
	for _, c := range cases {
		if err := e.Put(c.key, "x", c.ver, c.reps); !errors.Is(err, c.want) {
			t.Fatalf("put(%+v): got %v", c, err)
		}
		seen[c.want] = true
	}
	if len(seen) != 3 || seen[api.ErrBadN] {
		t.Fatal("sentinels not distinct")
	}
	if _, _, _, err := e.Read(""); !errors.Is(err, api.ErrEmptyKey) {
		t.Fatal("read empty key accepted")
	}
	if got := fmt.Sprint(e.Snapshot("k")); got != before {
		t.Fatal("state changed after rejects")
	}
	if err := e.Put("k", "b", 6, []int{2}); err != nil {
		t.Fatal("unusable after rejects")
	}
	if v, _, _, _ := e.Read("k"); v != "b" {
		t.Fatalf("got %q after recovery", v)
	}
}

func TestNoDowngrade(t *testing.T) {
	e, _ := api.New(4)
	_ = e.Put("k", "a", 9, []int{0, 1})
	_ = e.Put("k", "b", 3, []int{2}) // 晚到的低版本写
	pre := e.Snapshot("k")
	maxPre := int64(0)
	for _, s := range pre {
		if s.Ok && s.Ver > maxPre {
			maxPre = s.Ver
		}
	}
	if v, _, _, _ := e.Read("k"); v != "a" {
		t.Fatalf("winner %q", v)
	}
	for i, s := range e.Snapshot("k") {
		if pre[i].Ok && s.Ver < pre[i].Ver {
			t.Fatalf("R%d dropped %d->%d", i, pre[i].Ver, s.Ver)
		}
		if s.Ver != maxPre {
			t.Fatalf("R%d ver %d != max %d", i, s.Ver, maxPre)
		}
	}
}

func TestNaiveConsistency(t *testing.T) {
	e, _ := api.New(5)
	rng := rand.New(rand.NewSource(1))
	keys := []string{"a", "b", "c"}
	for it := 0; it < 200; it++ {
		reps := []int{rng.Intn(5)}
		for r := 0; r < 5; r++ {
			if rng.Intn(2) == 0 {
				reps = append(reps, r)
			}
		}
		_ = e.Put(keys[rng.Intn(3)], fmt.Sprint(rng.Intn(4)), int64(1+rng.Intn(10)), reps)
		for _, k := range keys {
			want, found := rep.Winner(e.Snapshot(k))
			v, f, _, err := e.Read(k)
			if err != nil || f != found || (found && v != want.Value) {
				t.Fatalf("it %d %s: (%q,%v) want (%q,%v)", it, k, v, f, want.Value, found)
			}
			for _, s := range e.Snapshot(k) {
				if found && (!s.Ok || s.Value != want.Value || s.Ver != want.Ver) {
					t.Fatalf("it %d %s: not converged", it, k)
				}
			}
			if _, _, r2, _ := e.Read(k); found && r2 != 0 {
				t.Fatalf("it %d %s: second read repaired %d", it, k, r2)
			}
		}
	}
}

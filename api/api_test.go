package api_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
	"ontology/kv"
)

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestReadYourWrites(t *testing.T) {
	st, s := api.New(), api.New().Open()
	type step struct {
		act, val string
		sync     int
		want     string
	}
	steps := []step{
		{"w", "A", 0, ""}, {"r", "", 0, "A"}, {"s", "", 1, ""},
		{"w", "B", 0, ""}, {"s", "", 2, ""}, {"w", "C", 0, ""}, {"r", "", 0, "C"},
	}
	for i, p := range steps {
		switch p.act {
		case "w":
			_, err := st.Write(s, "k", p.val)
			ok(t, err)
		case "s":
			ok(t, st.Sync(p.sync))
		case "r":
			val, ver, err := st.Read(s, "k")
			if err != nil || val != p.want || ver < s.WriteVersion("k") {
				t.Fatalf("step %d: (%q,%d) err=%v", i+1, val, ver, err)
			}
		}
	}
}

func TestReferenceConsistency(t *testing.T) {
	st, s := api.New(), api.New().Open()
	ref := map[string]kv.Entry{}
	for i := 0; i < 200; i++ {
		key, val := fmt.Sprintf("k%03d", i%40), fmt.Sprintf("v%d", i)
		v, err := st.Write(s, key, val)
		ok(t, err)
		ref[key] = kv.Entry{Val: val, Ver: v}
		got, gv, err := st.Read(s, key)
		if err != nil || got != val || gv != v {
			t.Fatalf("(%q,%d) != (%q,%d)", got, gv, val, v)
		}
	}
	view := st.View()
	if len(view) != len(ref) {
		t.Fatalf("View len %d want %d", len(view), len(ref))
	}
	for k, e := range ref {
		if view[k] != e {
			t.Fatalf("View[%s]=%v want %v", k, view[k], e)
		}
	}
}

func TestMonotonicAndSync(t *testing.T) {
	st, s := api.New(), api.New().Open()
	prev := int64(0)
	for i := 1; i <= 20; i++ {
		v, _ := st.Write(s, "k", fmt.Sprintf("v%d", i))
		if v <= prev {
			t.Fatalf("version %d after %d", v, prev)
		}
		prev = v
		for _, idx := range []int{1, 2} {
			ok(t, st.Sync(idx))
			_, r1, r2 := st.Snap()
			if got := []map[string]kv.Entry{nil, r1, r2}[idx]["k"].Ver; got != v {
				t.Fatalf("Sync(%d) ver %d want %d", idx, got, v)
			}
		}
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	st, s := api.New(), api.New().Open()
	_, err := st.Write(s, "keep", "1")
	ok(t, err)
	before := st.View()
	if _, err := st.Write(s, "", "z"); !errors.Is(err, api.ErrEmptyKey) {
		t.Fatalf("empty write %v", err)
	}
	if _, _, err := st.Read(s, ""); !errors.Is(err, api.ErrEmptyKey) {
		t.Fatalf("empty read %v", err)
	}
	for _, bad := range []int{0, 3, -1} {
		if err := st.Sync(bad); !errors.Is(err, api.ErrSyncIndex) {
			t.Fatalf("Sync(%d) %v", bad, err)
		}
	}
	st.Close(s)
	wv := s.WriteVersion("x")
	if _, err := st.Write(s, "x", "1"); !errors.Is(err, api.ErrClosed) {
		t.Fatalf("closed write %v", err)
	}
	if _, _, err := st.Read(s, "x"); !errors.Is(err, api.ErrClosed) || s.WriteVersion("x") != wv {
		t.Fatalf("closed read or writeVer changed: %v", err)
	}
	if after := st.View(); len(after) != len(before) || after["keep"] != before["keep"] {
		t.Fatalf("state changed: %v -> %v", before, after)
	}
	if v, _, err := st.Read(st.Open(), "keep"); err != nil || v != "1" {
		t.Fatalf("store unusable after rejections: %q %v", v, err)
	}
}

func TestConcurrentReadersIdenticalView(t *testing.T) {
	st, s := api.New(), api.New().Open()
	for i := 0; i < 500; i++ {
		_, err := st.Write(s, fmt.Sprintf("k%04d", i), fmt.Sprintf("v%d", i))
		ok(t, err)
	}
	const n = 64
	var wg sync.WaitGroup
	views := make([]map[string]kv.Entry, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; views[i] = st.View() }(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < n; i++ {
		if len(views[i]) != len(views[0]) {
			t.Fatalf("view %d len %d want %d", i, len(views[i]), len(views[0]))
		}
		for k, e := range views[0] {
			if views[i][k] != e {
				t.Fatalf("view %d differs at %s", i, k)
			}
		}
	}
}

func TestSelfCheck(t *testing.T) { ok(t, api.New().SelfCheck()) }

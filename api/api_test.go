package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestMatchesBatchReference pins invariant 1 against a batch reference.
func TestMatchesBatchReference(t *testing.T) {
	for _, seed := range []int64{7, 42, 2026} {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			d := api.New()
			ref := map[string]string{}
			var live []int
			pend := map[int]map[string]string{}
			for i := 0; i < 300; i++ {
				switch r.Intn(3) {
				case 0:
					tx := d.Begin()
					live = append(live, tx)
					pend[tx] = map[string]string{}
				case 1:
					if len(live) == 0 {
						continue
					}
					tx := live[r.Intn(len(live))]
					k, v := fmt.Sprintf("k%d", r.Intn(12)), fmt.Sprintf("v%d", i)
					must(t, d.Write(tx, k, v))
					pend[tx][k] = v
				case 2:
					if len(live) == 0 {
						continue
					}
					j := r.Intn(len(live))
					tx := live[j]
					live = append(live[:j], live[j+1:]...)
					must(t, d.Commit(tx))
					for k, v := range pend[tx] { // commit order == apply order
						ref[k] = v
					}
					delete(pend, tx)
				}
			}
			for k, want := range ref {
				if got, err := d.Read(k); err != nil || got != want {
					t.Fatalf("Read(%q)=%q,%v want %q", k, got, err, want)
				}
			}
			for _, k := range []string{"k100", "k101"} { // never touched
				if _, err := d.Read(k); !errors.Is(err, api.ErrKeyNotFound) {
					t.Fatalf("Read(%q) should be not-found: %v", k, err)
				}
			}
		})
	}
}

// TestNoDirtyRead pins invariant 2: no uncommitted write leaks out.
func TestNoDirtyRead(t *testing.T) {
	d := api.New()
	t0 := d.Begin()
	must(t, d.Write(t0, "k", "old"))
	must(t, d.Commit(t0))
	w := d.Begin()
	must(t, d.Write(w, "k", "new"))
	must(t, d.Write(w, "fresh", "x"))
	other := d.Begin()
	if v, _ := d.Read("k"); v != "old" {
		t.Fatalf("Read saw uncommitted %q", v)
	}
	if v, _ := d.ReadTx(other, "k"); v != "old" {
		t.Fatalf("ReadTx saw uncommitted %q", v)
	}
	if _, err := d.ReadTx(other, "fresh"); !errors.Is(err, api.ErrKeyNotFound) {
		t.Fatalf("uncommitted new key visible: %v", err)
	}
}

// TestOwnWriteVisible pins invariant 3: own uncommitted writes are visible.
func TestOwnWriteVisible(t *testing.T) {
	d := api.New()
	tx := d.Begin()
	must(t, d.Write(tx, "k", "mine"))
	if v, err := d.ReadTx(tx, "k"); err != nil || v != "mine" {
		t.Fatalf("own write not visible: %q, %v", v, err)
	}
	if _, err := d.Read("k"); !errors.Is(err, api.ErrKeyNotFound) {
		t.Fatalf("own write leaked to committed view: %v", err)
	}
}

// TestConcurrentReads: concurrent readers see identical per-key values.
func TestConcurrentReads(t *testing.T) {
	d := api.New()
	const keys = 32
	want := map[string]string{}
	for i := 0; i < keys; i++ {
		tx := d.Begin()
		k, v := fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i)
		must(t, d.Write(tx, k, v))
		must(t, d.Commit(tx))
		want[k] = v
	}
	reader := d.Begin() // shared live tx for concurrent ReadTx
	const n = 64
	results := make([]map[string]string, n)
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			read := d.Read
			if g%2 == 1 {
				read = func(k string) (string, error) { return d.ReadTx(reader, k) }
			}
			got := map[string]string{}
			for k := range want {
				if v, err := read(k); err == nil {
					got[k] = v
				}
			}
			results[g] = got
		}(g)
	}
	wg.Wait()
	for g, got := range results {
		for k, w := range want {
			if got[k] != w {
				t.Fatalf("goroutine %d: %q=%q want %q", g, k, got[k], w)
			}
		}
	}
}

func TestSelfCheck(t *testing.T) {
	must(t, api.New().SelfCheck())
}

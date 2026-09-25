package api_test

import (
	"math/rand"
	"strconv"
	"sync"
	"testing"

	"ontology/api"
)

// TestSerialReplay pins invariant 1: on randomized interleaved histories,
// Committed() equals replaying only successful write sets in commit order.
func TestSerialReplay(t *testing.T) {
	for _, sd := range []int64{1, 2, 7, 42, 99, 2026} {
		d := api.New()
		keys := []string{"k0", "k1", "k2", "k3", "k4"}
		ref := map[string]string{}
		seed := d.Begin()
		for _, k := range keys {
			seed.Write(k, "0")
			ref[k] = "0"
		}
		if err := seed.Commit(); err != nil {
			t.Fatal(err)
		}
		rng := rand.New(rand.NewSource(sd))
		type st struct {
			tx     *api.Txn
			active bool
			writes map[string]string
		}
		var txns []*st
		active := func() []int {
			var a []int
			for i, s := range txns {
				if s.active {
					a = append(a, i)
				}
			}
			return a
		}
		begin := func() int {
			txns = append(txns, &st{d.Begin(), true, map[string]string{}})
			return len(txns) - 1
		}
		commit := func(i int) {
			txns[i].active = false
			if err := txns[i].tx.Commit(); err == nil {
				for k, v := range txns[i].writes {
					ref[k] = v
				}
			}
		}
		for step := 0; step < 500; step++ {
			a := active()
			if len(a) == 0 || rng.Intn(5) == 0 {
				i := begin()
				for q := 0; q < rng.Intn(3); q++ {
					k := keys[rng.Intn(len(keys))]
					if rng.Intn(2) == 0 {
						txns[i].tx.Read(k)
					} else {
						v := strconv.Itoa(rng.Intn(1000))
						txns[i].tx.Write(k, v)
						txns[i].writes[k] = v
					}
				}
				continue
			}
			i := a[rng.Intn(len(a))]
			k := keys[rng.Intn(len(keys))]
			switch rng.Intn(3) {
			case 0:
				txns[i].tx.Read(k)
			case 1:
				v := strconv.Itoa(rng.Intn(1000))
				txns[i].tx.Write(k, v)
				txns[i].writes[k] = v
			case 2:
				commit(i)
			}
		}
		for _, i := range active() {
			commit(i)
		}
		got := d.Committed()
		if len(got) != len(ref) {
			t.Fatalf("seed %d: %d keys, replay %d", sd, len(got), len(ref))
		}
		for k, v := range ref {
			if got[k] != v {
				t.Fatalf("seed %d key %s: %q vs replay %q", sd, k, got[k], v)
			}
		}
	}
}

// TestConcurrentSnapshots pins concurrency with no sleeps: read-only txns
// always see a==b and only old ("0") or new ("1") values (atomic switch).
func TestConcurrentSnapshots(t *testing.T) {
	d := api.New()
	s := d.Begin()
	s.Write("a", "0")
	s.Write("b", "0")
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}
	const G, Iters = 8, 500
	obs := make(chan [2]string, G*Iters)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < Iters; j++ {
				tx := d.Begin()
				va, _ := tx.Read("a")
				vb, _ := tx.Read("b")
				obs <- [2]string{va, vb}
				tx.Commit()
			}
		}()
	}
	close(start)
	w := d.Begin()
	w.Write("a", "1")
	w.Write("b", "1")
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(obs)
	for o := range obs {
		if o[0] != o[1] || (o[0] != "0" && o[0] != "1") {
			t.Fatalf("torn/non-atomic snapshot: a=%q b=%q", o[0], o[1])
		}
	}
	if c := d.Committed(); c["a"] != "1" || c["b"] != "1" {
		t.Fatalf("final %v want a=b=1", c)
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

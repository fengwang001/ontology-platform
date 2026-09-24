package main

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"

	cdc "ontology/api"
)

func ev(k string, s int64) cdc.Event { return cdc.Event{Key: k, Seq: s} }

// sig renders decisions as Key+Seq+verdict runes, e.g. A2E, B1X.
func sig(ds []cdc.Decision) string {
	b := strings.Builder{}
	for _, d := range ds {
		c := byte('E')
		if d.Kind == cdc.DeadLettered {
			c = 'X'
		}
		fmt.Fprintf(&b, "%s%d%c", d.Key, d.Seq, c)
	}
	return b.String()
}

// seqOf renders seq values for one key from an event slice.
func seqOf(evs []cdc.Event, k string) string {
	s := ""
	for _, e := range evs {
		if e.Key == k {
			s += fmt.Sprintf("%d,", e.Seq)
		}
	}
	return s
}

// randomMatch builds a sharded random stream with injected failures and
// checks the drained result against the naive per-key serial reference.
func randomMatch(seed int64) bool {
	rnd := rand.New(rand.NewSource(seed))
	ks := []string{"p", "q", "r", "s"}
	rf, order, cnt := map[cdc.Event]int{}, []cdc.Event{}, map[string]int64{}
	for i := 0; i < 400; i++ {
		k := ks[rnd.Intn(len(ks))]
		cnt[k]++
		e := ev(k, cnt[k])
		order = append(order, e)
		if rnd.Intn(3) == 0 {
			rf[e] = rnd.Intn(5)
		}
	}
	a, _ := cdc.New(3, 200, rf)
	for i, e := range order {
		a.Submit(e)
		if i%5 == 0 {
			a.Tick()
		}
	}
	for len(a.Applied())+len(a.DeadLetters()) < len(order) {
		a.Tick()
	}
	for _, k := range ks {
		exA, exD := "", ""
		for s := int64(1); s <= cnt[k]; s++ {
			if rf[ev(k, s)] >= 3 {
				exD += fmt.Sprintf("%d,", s)
			} else {
				exA += fmt.Sprintf("%d,", s)
			}
		}
		if seqOf(a.Applied(), k) != exA || seqOf(a.DeadLetters(), k) != exD {
			return false
		}
	}
	return true
}

// concurrentOK runs N one-key producers against a concurrent ticker and
// verifies each key's applied order is 1..per (no sleeps: synchronization
// is via WaitGroups and a stop channel).
func concurrentOK() bool {
	const N, per = 8, 25
	a, _ := cdc.New(3, N*per, nil)
	stop := make(chan struct{})
	var twg, pwg sync.WaitGroup
	twg.Add(1)
	go func() {
		defer twg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				a.Tick()
			}
		}
	}()
	for i := 0; i < N; i++ {
		pwg.Add(1)
		go func(i int) {
			defer pwg.Done()
			k := string(rune('a' + i))
			for s := int64(1); s <= per; s++ {
				a.Submit(ev(k, s))
			}
		}(i)
	}
	pwg.Wait()
	close(stop)
	twg.Wait()
	for len(a.Applied()) < N*per {
		a.Tick()
	}
	for i := 0; i < N; i++ {
		k, want := string(rune('a'+i)), ""
		for s := 1; s <= per; s++ {
			want += fmt.Sprintf("%d,", s)
		}
		if seqOf(a.Applied(), k) != want {
			return false
		}
	}
	return true
}

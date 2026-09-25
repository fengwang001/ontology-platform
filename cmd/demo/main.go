// Command demo verifies sticky-session monotonic reads via the exported API
// only; the internal catch-up scan counter stays private (pinned by rep test).
package main

import (
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
)

var failed bool

func ok(name, detail string, pass bool) {
	if !pass {
		failed = true
	}
	fmt.Printf("%s %-20s %s\n", map[bool]string{true: "OK ", false: "FAIL"}[pass], name, detail)
}

func scenario() *api.Cluster {
	c := api.New()
	c.Write("k", "A")
	c.Write("k", "B")
	c.Offline(2)
	c.Write("k", "C")
	c.Offline(1)
	c.Write("k", "D")
	c.Write("k", "E")
	c.Online(1)
	c.Online(2)
	return c
}

func main() {
	// Five-step section-3 sequence; caught lsns inferred from public watermarks.
	c, s := scenario(), (*api.Session)(nil)
	s = c.Open(0)
	steps := []struct {
		switchTo int // <0 means Read; otherwise failover target
		catch    []int
	}{
		{-1, nil}, {1, []int{4, 5}}, {-1, nil}, {2, []int{3, 4, 5}}, {-1, nil},
	}
	for i, st := range steps {
		var caught []int
		v, lsn := "", 0
		if st.switchTo < 0 {
			b := c.View().Replicas[s.Replica].Applied
			v, lsn, _ = c.Read(s, "k")
			for l := b + 1; l <= c.View().Replicas[s.Replica].Applied; l++ {
				caught = append(caught, l)
			}
		} else {
			b := c.View().Replicas[st.switchTo].Applied
			c.SwitchReplica(s, st.switchTo)
			for l := b + 1; l <= c.View().Replicas[st.switchTo].Applied; l++ {
				caught = append(caught, l)
			}
		}
		pass := reflect.DeepEqual(caught, st.catch) && s.Seen == 5
		d := fmt.Sprintf("seen=%d caught=%v", s.Seen, caught)
		if st.switchTo < 0 {
			d += fmt.Sprintf(" read=(%q,%d)", v, lsn)
			pass = pass && v == "E" && lsn == 5
		}
		ok(fmt.Sprintf("five-step %d", i+1), d, pass)
	}
	v := c.View()
	eq := v.Ref["k"] == "E"
	for i := range v.Replicas {
		eq = eq && reflect.DeepEqual(v.Replicas[i].Data, map[string]string{"k": "E"})
	}
	ok("View==reference", fmt.Sprintf("lsn=%d", v.LSN), eq && api.New().SelfCheck() == nil)

	// Three distinct, decidable errors; rejection leaves no trace.
	g := api.New()
	g.Write("k", "v")
	g.Offline(1)
	sg := g.Open(0)
	before, sb := g.View(), *sg
	errs := map[error]bool{
		func() error { _, e := g.Write("", "z"); return e }():  true,
		func() error { _, _, e := g.Read(sg, ""); return e }(): true,
		func() error { return g.Offline(9) }():                 true,
		func() error { return g.Online(-1) }():                 true,
		func() error { return g.SwitchReplica(sg, 3) }():       true,
		func() error { return g.SwitchReplica(sg, 1) }():       true,
	}
	want := map[error]bool{api.ErrEmptyKey: true, api.ErrReplicaIndex: true, api.ErrReplicaOffline: true}
	noTrace := reflect.DeepEqual(g.View(), before) && *sg == sb
	ok("errors,no-trace", fmt.Sprintf("distinct=%d notrace=%v", len(errs), noTrace),
		reflect.DeepEqual(errs, want) && noTrace)

	// Large m: one entry behind must apply exactly one, regardless of m.
	deltas, good := "", true
	for _, m := range []int{100, 1000, 10000} {
		h := api.New()
		for i := 0; i < m-1; i++ {
			h.Write("k", "v")
		}
		h.Offline(1)
		h.Write("k", "v") // lsn m missed by R1
		sh := h.Open(0)
		h.Read(sh, "k") // seen = m
		h.Online(1)
		b := h.View().Replicas[1].Applied
		h.SwitchReplica(sh, 1)
		a := h.View().Replicas[1].Applied
		deltas += fmt.Sprintf("m%d:+%d ", m, a-b)
		good = good && b == m-1 && a == m && a-b == 1
	}
	ok("large-m catch-up", deltas, good)

	// Concurrent read-only sharing of one session: field-identical results.
	const n = 64
	p, sp := scenario(), (*api.Session)(nil)
	sp = p.Open(0)
	out := make(chan [2]interface{}, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			val, l, _ := p.Read(sp, "k")
			p.View()
			out <- [2]interface{}{val, l}
		}()
	}
	wg.Wait()
	close(out)
	same := true
	var first [2]interface{}
	i := 0
	for r := range out {
		if i == 0 {
			first = r
		} else if r != first {
			same = false
		}
		i++
	}
	ok("concurrent reads", fmt.Sprintf("%d goroutines=%v", n, first), same && first[0] == "E" && first[1] == 5)

	if failed {
		fmt.Println("FAIL overall")
		os.Exit(1)
	}
}

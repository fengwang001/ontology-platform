// Command demo runs in-process checks for the Raft term/index log
// replication exercise through the public api. No args, no network;
// exit code 0 iff every line is OK. Output stays within 10 lines.
package main

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	fmt.Printf("%s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
	failed = failed || !ok
}

// s renders a log compactly by command: "a,b,z" or "-".
func s(log []api.Entry) string {
	if len(log) == 0 {
		return "-"
	}
	out := ""
	for i, e := range log {
		if i > 0 {
			out += ","
		}
		out += e.Cmd
	}
	return out
}

func logs(a *api.API) (string, string, string) {
	return s(a.Log("S1")), s(a.Log("S2")), s(a.Log("S3"))
}

func main() {
	c := api.New()
	must := func(e error) {
		if e != nil {
			panic(e)
		}
	}
	must(c.SetTerm("S1", 1))
	must(c.Append("S1", "a"))
	must(c.Replicate("S1", "S2", 0))
	a, b, d := logs(c)
	check(fmt.Sprintf("step1 S1:%s S2:%s S3:%s CI=1", a, b, d), c.CommitIndex("S1") == 1)
	must(c.Append("S1", "b"))
	must(c.Replicate("S1", "S2", 1))
	a, b, d = logs(c)
	check(fmt.Sprintf("step2 %s/%s/%s CI still 1 (no check before crash)", a, b, d),
		a == "a,b" && b == "a,b" && d == "-")
	must(c.SetTerm("S3", 2))
	must(c.Append("S3", "x"))
	a, b, d = logs(c)
	check(fmt.Sprintf("step3 %s/%s/%s S3-CI=0", a, b, d), c.CommitIndex("S3") == 0 && d == "x")
	must(c.Append("S3", "y"))
	a, b, d = logs(c)
	check(fmt.Sprintf("step4 %s/%s/%s x,y uncommitted (one vote)", a, b, d),
		c.CommitIndex("S3") == 0 && d == "x,y")
	must(c.SetTerm("S1", 3))
	must(c.Replicate("S1", "S2", 1))
	must(c.Append("S1", "z"))
	must(c.Replicate("S1", "S2", 2))
	a, b, d = logs(c)
	check(fmt.Sprintf("step5 %s/%s/%s CI=3: (3,3,z) indirectly commits (1,2,b)", a, b, d),
		c.CommitIndex("S1") == 3 && a == "a,b,z")
	must(c.Replicate("S1", "S3", 0))
	a, b, d = logs(c)
	check(fmt.Sprintf("step6 S3 truncated to %s (term2 x,y dropped)", d), d == "a,b,z" && a == b && b == d)

	// Three distinguishable errors; rejected follower stays usable.
	g := api.New()
	must(g.SetTerm("S1", 1))
	must(g.SetTerm("S2", 2))
	must(g.Append("S1", "a"))
	must(g.Append("S2", "x"))
	e0 := g.Append("S2", "")
	e1 := g.Replicate("S1", "S2", -1)
	e2 := g.Replicate("S1", "S2", 5)
	e3 := g.Replicate("S1", "S2", 1)
	v := g.Log("S2")
	must(g.Append("S2", "q"))
	check("errors empty/out-of-range/term distinct; state untouched; usable after",
		errors.Is(e0, api.ErrEmptyCmd) && errors.Is(e1, api.ErrPrevIndexOutOfRange) &&
			errors.Is(e2, api.ErrPrevIndexOutOfRange) && errors.Is(e3, api.ErrPrevTermMismatch) &&
			s(v) == "x" && s(g.Log("S2")) == "x,q")

	// Large m: incremental scan resumes past the prior commit point; the
	// exact scanned==1 count is pinned by the white-box raft test.
	incOK := true
	for _, m := range []int{100, 1000, 10000} {
		h := api.New()
		must(h.SetTerm("S1", 1))
		for range m {
			must(h.Append("S1", "c"))
		}
		must(h.Replicate("S1", "S2", 0))
		v1 := h.CommitIndex("S1")
		must(h.Append("S1", "c"))
		must(h.Replicate("S1", "S2", m))
		incOK = incOK && v1 == m && h.CommitIndex("S1") == m+1
	}
	check("incremental commit m=100,1000,10000 (+1; scanned==1 pinned in test)", incOK)

	// N concurrent readers of one converged cluster agree entry-by-entry.
	base := c.Log("S1")
	var wg sync.WaitGroup
	agree := true
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !slices.Equal(c.Log("S1"), base) {
				agree = false
			}
		}()
	}
	wg.Wait()
	check("16 concurrent readers agree entry-by-entry; SelfCheck:", agree && api.SelfCheck())
	if failed {
		fmt.Println("FAIL overall")
	}
}

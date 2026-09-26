// Command demo exercises the api package and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"ontology/api"
	"ontology/ops"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	c2, _ := api.New(2)
	d, err := c2.Distance("abc", "yabd")
	check("distance(abc,yabd)=2", err == nil && d == 2)

	script, err := c2.EditScript("abc", "yabd")
	got, aerr := ops.Apply("abc", script)
	check("script applies, len==distance", err == nil && aerr == nil && got == "yabd" && len(script) == 2)

	_, eNeg := api.New(-1)
	c1, _ := api.New(1)
	_, eCap := c1.Distance("abc", "yabd")
	_, eLong := c2.Distance(strings.Repeat("x", 1<<20), "y")
	check("three distinct sentinel errors",
		errors.Is(eNeg, api.ErrNegativeCap) && errors.Is(eCap, api.ErrExceedsCap) &&
			errors.Is(eLong, api.ErrTooLong) &&
			!errors.Is(eNeg, eCap) && !errors.Is(eNeg, eLong) && !errors.Is(eCap, eLong))

	before, _ := c2.Distance("flaw", "lawn")
	after, _ := c2.Distance("flaw", "lawn")
	check("rejections leave no trace, selfcheck", before == 2 && after == 2 && c2.SelfCheck() == nil)

	c3, _ := api.New(3) // n=100000: a full n*n table (1e10 cells) could not finish
	big := strings.Repeat("a", 100000)
	bigB := big[:50000] + "bb" + big[50000:99998]
	start := time.Now()
	dBig, errBig := c3.Distance(big, bigB)
	check("banded cells not O(n^2) at n=1e5", errBig == nil && dBig == 2 && time.Since(start) < 10*time.Second)

	c8, _ := api.New(8)
	pairs := [][2]string{{"abc", "yabd"}, {"kitten", "sitting"}, {"flaw", "lawn"}, {"", "abcde"},
		{"aaaa", "aaaa"}, {"gumbo", "gambol"}, {"hello", "world"}, {"x", "xyz"}}
	want := make([]int, len(pairs))
	for i, p := range pairs {
		want[i], _ = c8.Distance(p[0], p[1])
	}
	gotC := make([]int, len(pairs))
	var wg sync.WaitGroup
	for i, p := range pairs {
		wg.Add(1)
		go func() { defer wg.Done(); gotC[i], _ = c8.Distance(p[0], p[1]) }()
	}
	wg.Wait()
	same := true
	for i := range pairs {
		same = same && gotC[i] == want[i]
	}
	check("concurrent results match serial", same)

	if failed {
		os.Exit(1)
	}
}

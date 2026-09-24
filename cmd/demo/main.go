// Command demo exercises the sequence gap detector and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"ontology/api"
)

func setString(xs map[int64]bool) string {
	out := make([]string, 0, len(xs))
	for x := range xs {
		out = append(out, fmt.Sprintf("%d", x))
	}
	sort.Strings(out)
	return "{" + strings.Join(out, ",") + "}"
}

func gapsString(xs []int64) string {
	s := make([]string, len(xs))
	for i, x := range xs {
		s[i] = fmt.Sprintf("%d", x)
	}
	return "{" + strings.Join(s, ",") + "}"
}

func main() {
	failed := false
	check := func(name string, ok bool, detail string) {
		if ok {
			fmt.Printf("OK %s %s\n", name, detail)
		} else {
			failed = true
			fmt.Printf("FAIL %s %s\n", name, detail)
		}
	}

	// 1) Eleven-step W=2 table; seen derived as arrivals still above H.
	d, _ := api.New(2)
	events := []int64{1, 2, 3, 4, 6, 9, 2, 3, 6, 10, 11}
	arrived := map[int64]bool{}
	var trace strings.Builder
	for i, e := range events {
		_ = d.Feed(e)
		arrived[e] = true
		h := d.Watermark()
		above := map[int64]bool{}
		for x := range arrived {
			if x > h {
				above[x] = true
			}
		}
		fmt.Fprintf(&trace, "%d:H%d/s%s/g%s ", i+1, h, setString(above), gapsString(d.Gaps()))
	}
	fmt.Println("OK steps " + strings.TrimSpace(trace.String()))

	// 2) Out-of-order tolerance: after Feed(6) with H=4, 5 is not a gap.
	d2, _ := api.New(2)
	for _, e := range []int64{1, 2, 3, 4, 6} {
		_ = d2.Feed(e)
	}
	check("out-of-order", d2.Watermark() == 4 && len(d2.Gaps()) == 0,
		fmt.Sprintf("H=%d gaps=%s (6 in flight, 5 not judged)", d2.Watermark(), gapsString(d2.Gaps())))

	// 3) Gap size: after Feed(9) gaps are exactly {5,7}, seen 6 never a gap.
	_ = d2.Feed(9)
	g := d2.Gaps()
	check("gap-size", d2.Watermark() == 7 && len(g) == 2 && g[0] == 5 && g[1] == 7,
		fmt.Sprintf("gaps=%s size=%d (6 present, 8 still in window)", gapsString(g), len(g)))

	// 4) Duplicate deliveries 2,3,6 change nothing.
	h0, gp0 := d2.Watermark(), d2.Gaps()
	for _, e := range []int64{2, 3, 6} {
		_ = d2.Feed(e)
	}
	dupOK := d2.Watermark() == h0 && gapsString(d2.Gaps()) == gapsString(gp0)
	check("duplicates", dupOK, fmt.Sprintf("H=%d gaps=%s throughout", d2.Watermark(), gapsString(d2.Gaps())))

	// 5) Three distinct, decidable sentinel errors.
	_, ew := api.New(0)
	es := d2.Feed(0)
	eo := d2.Feed(math.MaxInt64)
	distinct := errors.Is(ew, api.ErrInvalidWindow) && errors.Is(es, api.ErrInvalidSeq) &&
		errors.Is(eo, api.ErrSeqOverflow) && ew != es && es != eo && ew != eo
	check("errors", distinct, fmt.Sprintf("%v | %v | %v", ew, es, eo))

	// 6) Rejected feeds leave no trace; detector stays usable.
	h1, g1 := d2.Watermark(), d2.Gaps()
	_ = d2.Feed(-1)
	_ = d2.Feed(math.MaxInt64 - 1)
	notrace := d2.Watermark() == h1 && gapsString(d2.Gaps()) == gapsString(g1)
	_ = d2.Feed(10)
	_ = d2.Feed(11)
	notrace = notrace && d2.Watermark() == 11 && gapsString(d2.Gaps()) == "{5,7,8}"
	check("rejection-no-trace", notrace, fmt.Sprintf("then H=%d gaps=%s", d2.Watermark(), gapsString(d2.Gaps())))

	// 7) Large m: the single Feed(m+1) stays fast regardless of history size.
	var worst time.Duration
	o1 := true
	for _, m := range []int64{1000, 10000, 100000} {
		b, _ := api.New(2)
		for s := int64(1); s <= m; s++ {
			_ = b.Feed(s)
		}
		t0 := time.Now()
		_ = b.Feed(m + 1)
		if e := time.Since(t0); e > worst {
			worst = e
		}
		if b.Watermark() != m+1 || time.Since(t0) > 25*time.Millisecond {
			o1 = false
		}
	}
	check("o1-convergence", o1, fmt.Sprintf("Feed(m+1) worst %s for m<=100000", worst.Round(time.Microsecond)))

	// 8) Concurrent random-order delivery of 1..N converges with no gaps.
	const n = 1000
	c, _ := api.New(int64(n))
	var wg sync.WaitGroup
	for _, v := range rand.New(rand.NewSource(1)).Perm(n) {
		wg.Add(1)
		go func(x int) { defer wg.Done(); _ = c.Feed(int64(x) + 1) }(v)
	}
	wg.Wait()
	check("concurrent", c.Watermark() == n && len(c.Gaps()) == 0,
		fmt.Sprintf("H=%d gaps=%s", c.Watermark(), gapsString(c.Gaps())))

	// 9) Built-in self-check over the reference table and invariants.
	check("selfcheck", d.SelfCheck() == nil, fmt.Sprintf("err=%v", d.SelfCheck()))

	if failed {
		os.Exit(1)
	}
}

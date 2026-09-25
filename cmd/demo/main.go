package main

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"

	"ontology/edit"
	"ontology/lines"
	"ontology/patch"
	"ontology/udiff"
)

var fails int

func report(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
		return
	}
	fails++
	fmt.Printf("FAIL %s\n", name)
}

func diff(a, b string, c int) []byte {
	d, err := udiff.Diff([]byte(a), []byte(b), c, 0)
	if err != nil {
		panic(err)
	}
	return d
}

func main() {
	// 1 random round trip + reverse
	rng := rand.New(rand.NewSource(7))
	okRT := true
	for i := 0; i < 50; i++ {
		a, b := randText(rng), randText(rng)
		d := diff(a, b, 3)
		g, err := patch.Apply([]byte(a), d, patch.Options{})
		if err != nil || string(g) != b {
			okRT = false
			continue
		}
		p, _ := udiff.Parse(d, udiff.Limits{})
		r, err := patch.ApplyParsed([]byte(b), patch.Reverse(p), 0)
		if err != nil || string(r) != a {
			okRT = false
		}
	}
	report("random round trip + reverse", okRT)

	// 2 CRLF and missing final newline preserved
	d := diff("a\r\nb\n", "a\r\nB\n", 3)
	g, _ := patch.Apply([]byte("a\r\nb\n"), d, patch.Options{})
	d2 := diff("x", "y", 3)
	g2, _ := patch.Apply([]byte("x"), d2, patch.Options{})
	report("CRLF + no-final-newline preserved", string(g) == "a\r\nB\n" && string(g2) == "y")

	// 3 shortest vs O(NM) LCS DP
	okShort := true
	for i := 0; i < 100; i++ {
		a, b := randText(rng), randText(rng)
		s, err := edit.Diff(lines.Split(a), lines.Split(b), edit.Options{})
		if err != nil || s.Distance != lcs(a, b) {
			okShort = false
		}
	}
	report("shortest vs LCS DP", okShort)

	// 4 three zero-count headers
	hdr := func(a, b string, c int) string {
		return strings.Split(string(diff(a, b, c)), "\n")[2]
	}
	report("zero-count headers",
		hdr("x\n", "y\nx\n", 0) == "@@ -0,0 +1 @@" &&
			hdr("a\nb\nc\n", "a\nb\nX\nc\n", 0) == "@@ -2,0 +3 @@" &&
			hdr("a\n", "", 3) == "@@ -1 +0,0 @@")

	// 5 merge threshold both sides
	nh := func(gap, c int) int {
		a := "X\n" + strings.Repeat("m\n", gap) + "Y\n"
		b := "P\n" + strings.Repeat("m\n", gap) + "Q\n"
		p, _ := udiff.Build(lines.Split(a), lines.Split(b), c, 0)
		return len(p.Hunks)
	}
	report("merge threshold g=2C merge / 2C+1 split", nh(2, 1) == 1 && nh(3, 1) == 2)

	// 6 final-newline-only change
	de := diff("a\n", "a", 3)
	ge, _ := patch.Apply([]byte("a\n"), de, patch.Options{})
	report("final newline only", strings.Contains(string(de), `\ No newline`) && string(ge) == "a")

	// 7 offset chooses nearest
	old := strings.Repeat("z\n", 5) + "k\nOLD\nk\n"
	dd := diff("k\nOLD\nk\n", "k\nNEW\nk\n", 3)
	gg, err := patch.Apply([]byte(old), dd, patch.Options{Fuzz: 10})
	report("offset applies at nearest match", err == nil && strings.Contains(string(gg), "k\nNEW\nk\n"))

	// 8 atomic rejection leaves target unchanged
	da := diff("a\nb\nc\nd\ne\n", "A\nb\nc\nd\nE\n", 0)
	target := []byte("a\nb\nc\nd\nX\n")
	if out, err := patch.Apply(target, da, patch.Options{}); err == nil || out != nil {
		report("atomic rejection", false)
	} else {
		var ae *patch.ApplyError
		report("atomic rejection", errors.As(err, &ae) && string(target) == "a\nb\nc\nd\nX\n")
	}

	// 9 truncation and flipping never panic
	okPanic := true
	func() {
		defer func() {
			if recover() != nil {
				okPanic = false
			}
		}()
		for cut := 0; cut < len(da); cut++ {
			_, _ = patch.Apply([]byte("a\nb\n"), da[:cut], patch.Options{})
		}
		fb := append([]byte(nil), da...)
		fb[len(fb)/2] ^= 1
		_, _ = patch.Apply([]byte("a\nb\n"), fb, patch.Options{})
	}()
	report("truncation/flip never panic", okPanic)

	// 10 four error classes distinguishable
	_, eFmt := patch.Apply([]byte("a\n"), []byte("garbage"), patch.Options{})
	_, eCtx := patch.Apply([]byte("z\n"), diff("a\n", "b\n", 0), patch.Options{Fuzz: 0})
	_, eRange := patch.Apply([]byte(strings.Repeat("x\n", 40)+"a\n"), diff("a\n", "b\n", 0), patch.Options{Fuzz: 2})
	_, eDiff := udiff.Diff([]byte("a\nb\n"), []byte("c\nd\n"), 3, 1)
	report("four error classes distinct",
		errors.Is(eFmt, patch.ErrFormat) && errors.Is(eCtx, patch.ErrContext) &&
			errors.Is(eRange, patch.ErrOutOfRange) && errors.Is(eDiff, edit.ErrTooDifferent))

	// 11 concurrent store equals serial replay
	store := patch.NewStore()
	store.Put("doc", []byte("base\n"), 1)
	ps := [16]udiff.Patch{}
	for i := range ps {
		ps[i], _ = udiff.Build(lines.Split("base\n"), lines.Split(fmt.Sprintf("w%d\n", i)), 0, 0)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	okN := 0
	var mu sync.Mutex
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if _, err := store.Apply("doc", ps[i], 0); err == nil {
				mu.Lock()
				okN++
				mu.Unlock()
			}
		}(i)
	}
	close(start)
	wg.Wait()
	fin, _ := store.Get("doc")
	rep, _ := patch.Replay([]byte("base\n"), store.Log())
	report("concurrent == serial replay", fin.Version == 1+okN && string(rep) == string(fin.Content))

	// 12 step counter at two scales
	okSteps := true
	var smallSteps int
	for gi, n := range []int{1000, 100000} {
		a, b := scaleInput(n)
		s, _ := edit.Diff(a, b, edit.Options{})
		if s.Steps > 4*(n+n)*(s.Distance+1) {
			okSteps = false
		}
		if gi == 0 {
			smallSteps = s.Steps
		} else if s.Steps > 150*smallSteps {
			okSteps = false
		}
	}
	report("step counter near-linear at 1k/100k", okSteps)

	if fails == 0 {
		fmt.Println("TOTAL: ALL OK")
	} else {
		fmt.Printf("TOTAL: %d FAIL\n", fails)
	}
}

var words = []string{"a\n", "b\n", "c\n", "a\r\n", "\n"}

func randText(rng *rand.Rand) string {
	var sb strings.Builder
	for i, n := 0, rng.Intn(10); i < n; i++ {
		sb.WriteString(words[rng.Intn(len(words))])
	}
	return sb.String()
}

func scaleInput(n int) ([]lines.Line, []lines.Line) {
	a := make([]string, n)
	for i := range a {
		a[i] = fmt.Sprintf("line%d\n", i)
	}
	b := append([]string(nil), a...)
	b[n/4], b[n/2], b[3*n/4] = "C1\n", "C2\n", "C3\n"
	return lines.Split(strings.Join(a, "")), lines.Split(strings.Join(b, ""))
}

func lcs(a, b string) int {
	la, lb := lines.Split(a), lines.Split(b)
	dp := make([][]int, len(la)+1)
	for i := range dp {
		dp[i] = make([]int, len(lb)+1)
	}
	for i := len(la) - 1; i >= 0; i-- {
		for j := len(lb) - 1; j >= 0; j-- {
			if la[i] == lb[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] > dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	return len(la) + len(lb) - 2*dp[0][0]
}

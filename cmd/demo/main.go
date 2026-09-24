// Command demo prints OK/FAIL lines for the parenthesis packages.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"ontology/balance"
	"ontology/longest"
	"ontology/scan"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s: %v\n", name, map[bool]string{true: "OK", false: "FAIL"}[ok])
}

// visitsOf reads the unexported counter for demonstration only.
func visitsOf(s *longest.Solver) int64 {
	v := reflect.ValueOf(s).Elem().FieldByName("visits")
	return reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Interface().(*atomic.Int64).Load()
}

func shapes(n int) []string {
	nested := strings.Repeat("(", n/2) + strings.Repeat(")", n/2)
	return []string{strings.Repeat("(", n), strings.Repeat(")", n), nested, strings.Repeat("()", n/2)}
}

func main() {
	check("scan", scan.Classify('(') == scan.Left && scan.Classify(')') == scan.Right &&
		scan.Classify('x') == scan.Invalid && scan.FirstInvalid("()x(") == 2 && scan.FirstInvalid("()") == -1)
	chk, err := balance.New(1 << 20)
	ok, err2 := chk.IsBalanced("()(()())")
	check("balance", err == nil && ok && err2 == nil)
	var f *balance.Failure
	ok, err = chk.IsBalanced("())(")
	check("first-error-pos", !ok && errors.As(err, &f) && f.Pos == 2 && f.Kind == balance.ExcessRight)
	_, errLim := balance.New(0)
	_, errLong := chk.IsBalanced(string(make([]byte, 1<<20+1)))
	_, errChar := chk.IsBalanced("(a)")
	check("three-errors", errors.Is(errLim, balance.ErrInvalidLimit) && errors.Is(errLong, balance.ErrTooLong) &&
		errors.Is(errChar, balance.ErrInvalidChar) && !errors.Is(errLim, balance.ErrTooLong))
	sv := longest.New(chk)
	st, ln, _ := sv.Longest(")()())")
	check("longest-)()())", st == 1 && ln == 4)
	e1, l1, _ := sv.Longest("")
	e2, l2, _ := sv.Longest("(((")
	e3, l3, _ := sv.Longest(")))")
	check("empty/all-left/all-right", e1 == 0 && l1 == 0 && e2 == 0 && l2 == 0 && e3 == 0 && l3 == 0)
	st, ln, _ = sv.Longest("())()")
	check("tie-leftmost", st == 0 && ln == 2)
	rng, consistent := rand.New(rand.NewSource(1)), true
	for i := 0; i < 200; i++ {
		b := make([]byte, rng.Intn(40))
		for j := range b {
			b[j] = "()"[rng.Intn(2)]
		}
		st, ln, err := sv.Longest(string(b))
		consistent = consistent && err == nil && sv.SelfCheck(string(b), st, ln)
	}
	check("random-vs-naive", consistent)
	visitsOK := true
	for _, n := range []int{1000, 100000} {
		for _, sh := range shapes(n) {
			fresh := longest.New(chk)
			_, _, err := fresh.Longest(sh)
			visitsOK = visitsOK && err == nil && visitsOf(fresh) == int64(n)
		}
	}
	check("visits==n", visitsOK)
	const g = 32
	var wg sync.WaitGroup
	res := make([][2]int, g)
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); st, ln, _ := sv.Longest("())(())(()"); res[i] = [2]int{st, ln} }(i)
	}
	wg.Wait()
	same := true
	for _, r := range res {
		same = same && r == res[0]
	}
	check("concurrent-identical", same)
	if failed {
		os.Exit(1)
	}
}

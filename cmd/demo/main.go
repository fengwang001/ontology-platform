// Command demo prints OK/FAIL checks for the dist and align packages.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"

	"ontology/align"
	"ontology/dist"
)

var failures int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Println(status, name)
}

func main() {
	ok := true
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"CA", "AC", 1}, {"CA", "ABC", 2}, {"ab", "ba", 1},
		{"abc", "ca", 2}, {"", "abc", 3}, {"abc", "", 3}, {"", "", 0},
	} {
		d, err := dist.Distance(c.a, c.b)
		ok = ok && err == nil && d == c.want
	}
	check("samples (CA->ABC=2 etc.)", ok)

	ok = true
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"é", "e", 1}, {"日本", "本日", 1}, {"\xff", "\xfe", 1}, {"\xff", "\xff", 0},
	} {
		d, err := dist.Distance(c.a, c.b)
		ok = ok && err == nil && d == c.want
	}
	check("runes & invalid bytes", ok)

	check("metric props", metricOK())
	check("script len==dist & applies", scriptOK())
	check("deterministic x100", deterministicOK())
	check("limit error", limitOK())
	check("cell counter 100/1000", counterOK())
	fmt.Printf("total: %d failure(s)\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
}

func metricOK() bool {
	set := []string{"", "a", "b", "aa", "ab", "ba", "bb"}
	for _, a := range set {
		for _, b := range set {
			dab, _ := dist.Distance(a, b)
			dba, _ := dist.Distance(b, a)
			if dab != dba || (dab == 0) != (a == b) {
				return false
			}
			for _, c := range set {
				dac, _ := dist.Distance(a, c)
				dbc, _ := dist.Distance(b, c)
				if dac > dab+dbc {
					return false
				}
			}
		}
	}
	return true
}

func scriptOK() bool {
	pairs := [][2]string{
		{"CA", "AC"}, {"CA", "ABC"}, {"ab", "ba"}, {"abc", "ca"},
		{"", "abc"}, {"abc", ""}, {"", ""}, {"日本", "本日"},
		{"\xff", "\xfe"}, {"kitten", "sitting"},
	}
	for _, p := range pairs {
		s, err := align.Script(p[0], p[1])
		if err != nil {
			return false
		}
		d, _ := dist.Distance(p[0], p[1])
		got, err := align.Apply(p[0], s)
		if len(s) != d || err != nil || got != p[1] {
			return false
		}
	}
	return true
}

func deterministicOK() bool {
	base, err := align.Script("CA", "ABC")
	if err != nil {
		return false
	}
	for k := 0; k < 100; k++ {
		s, err := align.Script("CA", "ABC")
		if err != nil || !reflect.DeepEqual(base, s) {
			return false
		}
	}
	return true
}

func limitOK() bool {
	old := dist.SetMaxProduct(3)
	defer dist.SetMaxProduct(old)
	_, err := dist.Distance("abcd", "wxyz")
	return errors.Is(err, dist.ErrTooLarge)
}

func counterOK() bool {
	for _, n := range []int{100, 1000} {
		dist.ResetFilledCells()
		a, b := strings.Repeat("a", n), strings.Repeat("b", n)
		if _, err := dist.Distance(a, b); err != nil {
			return false
		}
		if dist.FilledCells() > int64(2*(n+1)*(n+1)) {
			return false
		}
	}
	return true
}

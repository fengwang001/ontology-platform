package edit_test

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/edit"
	"ontology/udiff"
)

func lcsLen(a, b [][]byte) int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if bytes.Equal(a[i], b[j]) {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	return dp[0][0]
}

func applyOps(a, b [][]byte, ops []edit.Op) [][]byte {
	var out [][]byte
	ai, bi := 0, 0
	for _, o := range ops {
		switch o {
		case edit.OpKeep:
			out = append(out, a[ai])
			ai++
			bi++
		case edit.OpDel:
			ai++
		case edit.OpIns:
			out = append(out, b[bi])
			bi++
		}
	}
	return out
}

func dist(ops []edit.Op) (d int) {
	for _, o := range ops {
		if o != edit.OpKeep {
			d++
		}
	}
	return d
}

func join(ls [][]byte) []byte { return bytes.Join(ls, nil) }

func TestShortest(t *testing.T) {
	pool := [][]byte{[]byte("a\n"), []byte("b\n"), []byte("c\n"), []byte("d\n")}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 300; i++ {
		var a, b [][]byte
		for j := 0; j < r.Intn(9); j++ {
			a = append(a, pool[r.Intn(len(pool))])
		}
		for j := 0; j < r.Intn(9); j++ {
			b = append(b, pool[r.Intn(len(pool))])
		}
		ops, err := edit.Diff(a, b, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := dist(ops), len(a)+len(b)-2*lcsLen(a, b); got != want {
			t.Fatalf("dist=%d want %d", got, want)
		}
		if got := join(applyOps(a, b, ops)); !bytes.Equal(got, join(b)) {
			t.Fatalf("script does not produce b")
		}
	}
}

func TestCounter(t *testing.T) {
	steps := map[int]int{}
	for _, n := range []int{1000, 100000} {
		a := make([][]byte, n)
		for i := range a {
			a[i] = []byte(fmt.Sprintf("line %d\n", i))
		}
		b := make([][]byte, 0, n)
		b = append(b, a[:n/4]...)
		b = append(b, []byte("changed\n"))
		b = append(b, a[n/4+1:n/2]...)
		b = append(b, a[n/2+1:3*n/4]...)
		b = append(b, []byte("inserted\n"))
		b = append(b, a[3*n/4:]...)
		ops, err := edit.Diff(a, b, 0)
		if err != nil {
			t.Fatal(err)
		}
		d := dist(ops)
		if got, lim := edit.Steps(), 4*(len(a)+len(b))*(d+1); got > lim {
			t.Fatalf("n=%d: steps=%d > bound %d", n, got, lim)
		}
		steps[n] = edit.Steps()
	}
	if got, lim := steps[100000], 150*steps[1000]; got > lim {
		t.Fatalf("steps ratio too high: %d > %d", got, lim)
	}
}

func TestMaxDist(t *testing.T) {
	var a, b [][]byte
	for i := 0; i < 10; i++ {
		a = append(a, []byte(fmt.Sprintf("a%d\n", i)))
		b = append(b, []byte(fmt.Sprintf("b%d\n", i)))
	}
	_, err := edit.Diff(a, b, 3)
	if !errors.Is(err, edit.ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
	if got, lim := edit.Steps(), 4*(len(a)+len(b))*(3+1); got > lim {
		t.Fatalf("steps=%d > bound %d", got, lim)
	}
}

func TestDeterministic(t *testing.T) {
	a, b := []byte("a\nb\nc\nd\n"), []byte("b\nX\nc\nY\n")
	f, err := udiff.Diff(a, b, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := udiff.Render(f)
	for i := 0; i < 100; i++ {
		f, err := udiff.Diff(a, b, 3, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := udiff.Render(f); !bytes.Equal(got, want) {
			t.Fatal("non-deterministic patch text")
		}
	}
}

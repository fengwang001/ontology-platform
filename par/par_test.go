package par_test

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"

	"ontology/norm"
	"ontology/par"
	"ontology/span"
)

const mixed = "a  \r\nb\t\rc \n  \r\n\nd \r e  \nf\n\n\ng"

func single(t *testing.T, s string, o norm.Options) (string, *span.Map) {
	t.Helper()
	n := norm.New(o)
	if _, err := n.Write([]byte(s)); err != nil {
		t.Fatalf("single Write: %v", err)
	}
	if err := n.Close(); err != nil {
		t.Fatalf("single Close: %v", err)
	}
	return string(n.Output()), n.Map()
}

func sameMap(t *testing.T, got *span.Map, wantOut string, want *span.Map) bool {
	t.Helper()
	if got.OutLen() != want.OutLen() || got.OrigLen() != want.OrigLen() {
		return false
	}
	for o := 0; o <= want.OutLen(); o++ {
		if got.ToOrig(o) != want.ToOrig(o) {
			return false
		}
	}
	for i := 0; i <= want.OrigLen(); i++ {
		if got.ToOut(i) != want.ToOut(i) {
			return false
		}
	}
	return true
}

func TestConsistency(t *testing.T) {
	for _, p := range []norm.Policy{norm.Keep, norm.EnsureOne, norm.Strip} {
		o := norm.Options{Policy: p}
		wantOut, wantMap := single(t, mixed, o)
		for k := 1; k <= 8; k++ {
			out, m, err := par.NormalizeK([]byte(mixed), k, o)
			if err != nil || string(out) != wantOut || !sameMap(t, m, wantOut, wantMap) {
				t.Fatalf("policy %d K=%d: %q err=%v want %q", p, k, out, err, wantOut)
			}
		}
		for c := 0; c <= len(mixed); c++ { // every 2-segment cut point
			out, m, err := par.Normalize([]byte(mixed), []int{c}, o)
			if err != nil || string(out) != wantOut || !sameMap(t, m, wantOut, wantMap) {
				t.Fatalf("policy %d cut=%d: %q err=%v want %q", p, c, out, err, wantOut)
			}
		}
	}
}

func TestDeterministic(t *testing.T) {
	in := []byte(strings.Repeat("row  \r\nx\ty \n", 2000))
	o := norm.Options{Policy: norm.EnsureOne}
	want, wm, err := par.NormalizeK(in, 8, o)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		got, gm, err := par.NormalizeK(in, 8, o)
		if err != nil || !bytes.Equal(got, want) || !sameMap(t, gm, string(want), wm) {
			t.Fatalf("run %d differs", i)
		}
	}
	var wg sync.WaitGroup // independent norm instances do not interfere
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			in := fmt.Sprintf("g%d  \r\n%ds \n", g, g)
			n := norm.New(norm.Options{})
			n.Write([]byte(in))
			n.Close()
			if got := string(n.Output()); got != fmt.Sprintf("g%d\n%ds\n", g, g) {
				t.Errorf("goroutine %d: %q", g, got)
			}
		}(g)
	}
	wg.Wait()
}

func TestErrors(t *testing.T) {
	var e *norm.Error
	in := []byte("ok  \r\nbad\x00line\n")
	_, _, err := par.NormalizeK(in, 4, norm.Options{Strict: true})
	if !errors.As(err, &e) || e.Kind != norm.KindNUL || e.Off != 9 {
		t.Fatalf("NUL: %v", err)
	}
	long := []byte(strings.Repeat("a\n", 100))
	_, _, err = par.NormalizeK(long, 4, norm.Options{MaxOut: 50})
	if !errors.As(err, &e) || e.Kind != norm.KindOut {
		t.Fatalf("MaxOut: %v", err)
	}
	ws := []byte("ab" + strings.Repeat(" ", 10) + "cd")
	_, _, err = par.NormalizeK(ws, 4, norm.Options{MaxWS: 4})
	if !errors.As(err, &e) || e.Kind != norm.KindWS {
		t.Fatalf("MaxWS: %v", err)
	}
	if _, _, err = par.Normalize([]byte("abc"), []int{2, 1}, norm.Options{}); err != par.ErrCuts {
		t.Fatalf("cuts: %v", err)
	}
}

func TestNormScale(t *testing.T) {
	var sb strings.Builder // 100k lines, mixed EOL + trailing blanks
	for i := 0; i < 100000; i++ {
		sb.WriteString([]string{"x  \r\n", "y\t\r", "z \n", "  \r\n"}[i%4])
	}
	_, m := single(t, sb.String(), norm.Options{})
	m.ToOrig(m.OutLen() / 2)
	if m.LastChecked() > 2*int(math.Log2(float64(m.Len())))+4 {
		t.Fatalf("checked %d runs (runs=%d)", m.LastChecked(), m.Len())
	}
	_, m2 := single(t, strings.Repeat("q\n", 5_000_000), norm.Options{}) // 10MB pure LF
	if m2.Len() > 4 {
		t.Fatalf("runs=%d for pure-LF 10MB input", m2.Len())
	}
}

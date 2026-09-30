package norm_test

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"ontology/norm"
	"ontology/par"
	"ontology/span"
)

func run(t *testing.T, in string, cfg norm.Config, chunks int) ([]byte, *span.Map) {
	t.Helper()
	n := norm.New(cfg)
	if chunks <= 0 {
		if _, err := n.Write([]byte(in)); err != nil {
			t.Fatal(err)
		}
	} else {
		for p := 0; p < len(in); p += chunks {
			q := p + chunks
			if q > len(in) {
				q = len(in)
			}
			if _, err := n.Write([]byte(in[p:q])); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}
	return n.Output(), n.Map()
}

func TestSemantics(t *testing.T) {
	pols := []norm.Policy{norm.Keep, norm.EnsureOne, norm.TrimEmpty}
	cases := []struct {
		in string
		ok [3]string
	}{
		{"a\r\nb", [3]string{"a\nb", "a\nb\n", "a\nb\n"}},
		{"a\rb", [3]string{"a\nb", "a\nb\n", "a\nb\n"}},
		{"\r\r\n", [3]string{"\n\n", "\n", ""}},
		{"a  \t\nb\t", [3]string{"a\nb", "a\nb\n", "a\nb\n"}},
		{"  \n x \n", [3]string{"\n x\n", "\n x\n", "\n x\n"}},
		{"", [3]string{"", "", ""}},
		{"\n", [3]string{"\n", "\n", ""}},
		{"\n\n", [3]string{"\n\n", "\n", ""}},
		{"  \r\n", [3]string{"\n", "\n", ""}},
		{"a\x00b", [3]string{"a\x00b", "a\x00b\n", "a\x00b\n"}},
		{"a\xffb", [3]string{"a\xffb", "a\xffb\n", "a\xffb\n"}},
	}
	for ci, c := range cases {
		for pi, pol := range pols {
			got, _ := run(t, c.in, norm.Config{EndPolicy: pol}, 0)
			if string(got) != c.ok[pi] {
				t.Fatalf("case %d pol %d: %q -> %q want %q", ci, pi, c.in, got, c.ok[pi])
			}
		}
	}
}

func TestChunkInvariance(t *testing.T) {
	in := "a\r\nb  \rc \t\r\n\r\nx \r \n z\t"
	pols := []norm.Policy{norm.Keep, norm.EnsureOne, norm.TrimEmpty}
	for _, pol := range pols {
		base, bm := run(t, in, norm.Config{EndPolicy: pol}, 0)
		for size := 1; size <= len(in); size++ {
			got, gm := run(t, in, norm.Config{EndPolicy: pol}, size)
			if !bytes.Equal(got, base) || !sameMap(gm, bm) {
				t.Fatalf("pol %d chunk %d mismatch", pol, size)
			}
		}
	}
}

func TestIdempotent(t *testing.T) {
	in := "a\r\n b  \r\n\n\nc\r"
	for _, pol := range []norm.Policy{norm.Keep, norm.EnsureOne, norm.TrimEmpty} {
		first, _ := run(t, in, norm.Config{EndPolicy: pol}, 0)
		second, _ := run(t, string(first), norm.Config{EndPolicy: pol}, 0)
		if !bytes.Equal(first, second) {
			t.Fatalf("pol %d not idempotent: %q vs %q", pol, first, second)
		}
	}
}

func TestMapLaws(t *testing.T) {
	in := "ab \r\n c\t\nd"
	out, m := run(t, in, norm.Config{}, 1)
	for o := 0; o <= len(out); o++ {
		i := m.ToOrig(o)
		if m.ToOut(i) != o {
			t.Fatalf("ToOut(ToOrig(%d))=%d", o, m.ToOut(i))
		}
	}
	prev := -1
	for i := 0; i <= len(in); i++ {
		if o := m.ToOut(i); o < prev {
			t.Fatalf("ToOut not monotone %d->%d", i, o)
		} else {
			prev = o
		}
	}
	// \r of \r\n and trailing spaces fold onto the following \n output offset.
	if i := strings.IndexByte(in, '\r'); m.ToOut(i) != strings.IndexByte(string(out), '\n') {
		t.Fatal("CR did not fold onto newline")
	}
}

func TestTruncation(t *testing.T) {
	in := "a \r\nb\r c  \r\nd"
	for cut := 0; cut <= len(in); cut++ {
		want, _ := run(t, in[:cut], norm.Config{}, 0)
		n := norm.New(norm.Config{})
		if _, err := n.Write([]byte(in[:cut])); err != nil {
			t.Fatal(err)
		}
		if err := n.Close(); err != nil || !bytes.Equal(n.Output(), want) {
			t.Fatalf("cut %d: %v %q want %q", cut, err, n.Output(), want)
		}
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name string
		cfg  norm.Config
		in   string
		want error
		at   int
	}{
		{"nul", norm.Config{StrictNUL: true}, "ab\x00", norm.ErrNUL, 2},
		{"space", norm.Config{SpaceBufferMax: 2}, "a   ", norm.ErrSpaceBuffer, 4},
		{"out", norm.Config{OutputMax: 2}, "abc", norm.ErrOutputLimit, 3},
	}
	for _, c := range cases {
		n := norm.New(c.cfg)
		_, err := n.Write([]byte(c.in))
		var oe *norm.OffsetError
		if !errors.As(err, &oe) || !errors.Is(err, c.want) || oe.At != c.at {
			t.Fatalf("%s: %v", c.name, err)
		}
		if _, err := n.Write(nil); !errors.Is(err, norm.ErrClosed) {
			t.Fatalf("%s: post-error write %v", c.name, err)
		}
	}
}

func sameMap(a, b *span.Map) bool {
	as, bs := a.Segs(), b.Segs()
	if len(as) != len(bs) {
		return false
	}
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func TestPar(t *testing.T) {
	in := []byte("a \r\nb\r c  \r\n\r x\t \n  \rz\n\n\n")
	for _, pol := range []norm.Policy{norm.Keep, norm.EnsureOne, norm.TrimEmpty} {
		cfg := norm.Config{EndPolicy: pol}
		base, bm := run(t, string(in), cfg, 0)
		for k := 1; k <= 8 && k <= len(in); k++ {
			cuts := make([]int, k-1)
			for q := range cuts {
				cuts[q] = (q + 1) * len(in) / k
			}
			out, m, err := par.Normalize(in, cuts, cfg)
			if err != nil || !bytes.Equal(out, base) || !sameMap(m, bm) {
				t.Fatalf("pol %d k %d: %v %q want %q", pol, k, err, out, base)
			}
		}
		// every single cut point
		for c := 0; c <= len(in); c++ {
			var cuts []int
			if c < len(in) {
				cuts = []int{c}
			}
			out, _, err := par.Normalize(in, cuts, cfg)
			if err != nil || !bytes.Equal(out, base) {
				t.Fatalf("pol %d cut %d: %v %q", pol, c, err, out)
			}
		}
	}
}

func TestScale(t *testing.T) {
	var sb strings.Builder
	for k := 0; k < 200_000; k++ {
		sb.WriteString("line content with a long payload of ordinary text and padding \t \r\n")
	}
	in := sb.String()
	// Requirement tier: 100k lines.
	if strings.Count(in, "\n") < 100_000 || len(in) < 10_000_000 {
		t.Fatalf("scale tier too small: %d lines %d bytes", strings.Count(in, "\n"), len(in))
	}
	out, m := run(t, in, norm.Config{}, 0)
	n := len(m.Segs())
	bound := int(2*math.Log2(float64(n)) + 4)
	m.ToOrig(len(out) / 2)
	if m.Checks() > bound {
		t.Fatalf("mixed: checks %d > %d, segs %d", m.Checks(), bound, n)
	}
	pure := strings.Repeat("\n", 10_000_000)
	_, pm := run(t, pure, norm.Config{}, 777)
	if len(pm.Segs()) > 2 {
		t.Fatalf("pure newlines segments %d", len(pm.Segs()))
	}
	if len(out) < 10_000_000 {
		t.Fatal("second scale tier too small")
	}
	fmt.Println(len(out))
}

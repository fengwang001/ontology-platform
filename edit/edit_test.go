package edit_test

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"

	"ontology/edit"
	"ontology/lines"
)

func TestSplitJoinRoundTrip(t *testing.T) {
	cases := []string{"", "a", "a\n", "a\nb", "a\nb\n", "a\r\nb\r\n", "\n", "a\n\n", "a\r\n"}
	for _, in := range cases {
		ls := lines.Split([]byte(in))
		if got := string(lines.Join(ls)); got != in {
			t.Fatalf("round trip %q -> %q", in, got)
		}
	}
}

func TestMaxDistanceAndTie(t *testing.T) {
	a := lines.Split([]byte("a\nb\nc\n"))
	b := lines.Split([]byte("x\ny\nz\n"))
	if _, err := edit.Diff(a, b, 3); !errors.Is(err, edit.ErrTooDifferent) {
		t.Fatalf("got %v want ErrTooDifferent", err)
	}
	if _, err := edit.Diff(a, b, 0); err != nil {
		t.Fatal(err)
	}
	ops, err := edit.Diff(lines.Split([]byte("a\nb\n")), lines.Split([]byte("b\na\n")), 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []byte
	for _, o := range ops {
		got = append(got, o.Kind)
	}
	if string(got) != "--++" {
		t.Fatalf("tie order %q want --++ (delete first)", got)
	}
}

func distance(ops []edit.Op) int {
	d := 0
	for _, o := range ops {
		if o.Kind != ' ' {
			d++
		}
	}
	return d
}

func lcsDP(a, b [][]byte) int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if bytes.Equal(a[i-1], b[j-1]) {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] > dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	return dp[len(a)][len(b)]
}

func TestShortestAndDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 200; iter++ {
		la, lb := lines.Split(randText(rng, 12)), lines.Split(randText(rng, 12))
		ops, err := edit.Diff(la, lb, 0)
		if err != nil {
			t.Fatal(err)
		}
		ca, cb := texts(la), texts(lb)
		want := len(ca) + len(cb) - 2*lcsDP(ca, cb)
		if distance(ops) != want {
			t.Fatalf("iter %d distance %d want %d", iter, distance(ops), want)
		}
		first := mustApply(t, la, ops)
		for k := 0; k < 100; k++ {
			again, _ := edit.Diff(la, lb, 0)
			if scriptSig(again) != scriptSig(ops) {
				t.Fatal("nondeterministic script")
			}
			if !bytes.Equal(mustApply(t, la, again), first) {
				t.Fatal("nondeterministic result")
			}
		}
	}
}

func randText(rng *rand.Rand, n int) []byte {
	var buf bytes.Buffer
	for i := 0; i < n; i++ {
		buf.WriteByte(byte('a' + rng.Intn(4)))
		if rng.Intn(3) == 0 {
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes()
}

func texts(ls []lines.Line) [][]byte {
	out := make([][]byte, len(ls))
	for i, l := range ls {
		out[i] = lines.Join([]lines.Line{l})
	}
	return out
}

func scriptSig(ops []edit.Op) string {
	var b bytes.Buffer
	for _, o := range ops {
		b.WriteByte(o.Kind)
	}
	return b.String()
}

func mustApply(t *testing.T, a []lines.Line, ops []edit.Op) []byte {
	t.Helper()
	var out []lines.Line
	ai := 0
	for _, o := range ops {
		switch o.Kind {
		case ' ':
			out = append(out, o.A)
			ai++
		case '-':
			ai++
		case '+':
			out = append(out, o.B)
		}
	}
	if ai != len(a) {
		t.Fatalf("consumed %d of %d old lines", ai, len(a))
	}
	return lines.Join(out)
}

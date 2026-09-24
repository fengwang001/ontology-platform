package norm_test

import (
	"bytes"
	"testing"

	"ontology/norm"
)

func runNorm(t *testing.T, in []byte, cfg norm.Config, chunk int) norm.Result {
	t.Helper()
	nw := norm.New(cfg)
	for off := 0; off < len(in); off += chunk {
		end := off + chunk
		if end > len(in) {
			end = len(in)
		}
		if _, err := nw.Write(in[off:end]); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	res, err := nw.Close()
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	return res
}

func TestCoreSemantics(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"crlf", "a\r\nb", "a\nb"},
		{"lone_cr", "a\rb", "a\nb"},
		{"cr_crlf", "\r\r\n", "\n\n"},
		{"trailing_ws", "a b  \t\nc", "a b\nc"},
		{"ws_only_line", "  \n", "\n"},
		{"mid_ws", "a \t b", "a \t b"},
		{"nul_passthrough", "a\x00b", "a\x00b"},
		{"bad_utf8", "a\xffb", "a\xffb"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, chunk := range []int{1, 2, 7, 4096} {
				res := runNorm(t, []byte(tc.in), norm.Config{}, chunk)
				if string(res.Output) != tc.want {
					t.Fatalf("chunk=%d got %q want %q", chunk, res.Output, tc.want)
				}
			}
		})
	}
}

func TestEndingPolicies(t *testing.T) {
	cases := []struct {
		in string
		k  norm.Ending
		w  string
	}{
		{"", norm.Keep, ""}, {"", norm.EnsureOne, ""}, {"", norm.Trim, ""},
		{"\n", norm.Keep, "\n"}, {"\n", norm.EnsureOne, "\n"}, {"\n", norm.Trim, "\n"},
		{"\n\n", norm.Keep, "\n\n"}, {"\n\n", norm.EnsureOne, "\n"}, {"\n\n", norm.Trim, "\n"},
		{"  \r\n", norm.Keep, "\n"}, {"  \r\n", norm.EnsureOne, "\n"}, {"  \r\n", norm.Trim, "\n"},
		{"a", norm.Keep, "a"}, {"a", norm.EnsureOne, "a\n"}, {"a", norm.Trim, "a\n"},
		{"a\n\n", norm.Trim, "a\n"},
	}
	for _, tc := range cases {
		res := runNorm(t, []byte(tc.in), norm.Config{Ending: tc.k}, 1)
		if string(res.Output) != tc.w {
			t.Fatalf("in=%q policy=%d got %q want %q", tc.in, tc.k, res.Output, tc.w)
		}
	}
}

func TestIdempotent(t *testing.T) {
	ins := []string{"", "\n", "\n\n", "  \r\n", "a\r\nb  \rc", "\t\t", "x\r\r\n y \t\n\n"}
	for k := norm.Keep; k <= norm.Trim; k++ {
		cfg := norm.Config{Ending: k}
		for _, in := range ins {
			r1 := runNorm(t, []byte(in), cfg, 1)
			r2 := runNorm(t, r1.Output, cfg, 3)
			if !bytes.Equal(r1.Output, r2.Output) {
				t.Fatalf("policy=%d in=%q N=%q N2=%q", k, in, r1.Output, r2.Output)
			}
		}
	}
}

func TestMappingInverse(t *testing.T) {
	ins := []string{"a\r\nb  \nc", "\r\r\n", "x \t\n", "  \n", "plain", "a\rb\t\t"}
	for _, in := range ins {
		res := runNorm(t, []byte(in), norm.Config{}, 1)
		for o := 0; o <= len(res.Output); o++ {
			i := res.Map.ToOrig(o)
			back := res.Map.ToOut(i)
			if back != o {
				t.Fatalf("in=%q o=%d orig=%d back=%d", in, o, i, back)
			}
		}
		var prev int
		for i := 0; i <= len(in); i++ {
			o := res.Map.ToOut(i)
			if o < prev {
				t.Fatalf("ToOut not monotone in=%q i=%d", in, i)
			}
			prev = o
		}
	}
}

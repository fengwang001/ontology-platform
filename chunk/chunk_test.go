package chunk

import "testing"

type wantRun struct {
	digit bool
	text  string
}

func TestSplit(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []wantRun
	}{
		{"empty", "", nil},
		{"letters", "abc", []wantRun{{false, "abc"}}},
		{"digits", "123", []wantRun{{true, "123"}}},
		{"mixed", "a01b", []wantRun{{false, "a"}, {true, "01"}, {false, "b"}}},
		{"adjacent", "a12b9", []wantRun{{false, "a"}, {true, "12"}, {false, "b"}, {true, "9"}}},
		{"fullwidth-digit-is-other", "１２", []wantRun{{false, "１２"}}},
		{"utf8-prefix", "é9", []wantRun{{false, "é"}, {true, "9"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Split(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d runs %v, want %d", len(got), got, len(tc.want))
			}
			for i, w := range tc.want {
				if got[i].Digit != w.digit || got[i].Text != w.text {
					t.Fatalf("run %d = {%v %q}, want {%v %q}", i, got[i].Digit, got[i].Text, w.digit, w.text)
				}
			}
			joined := ""
			for _, r := range got {
				joined += r.Text
			}
			if joined != tc.in {
				t.Fatalf("runs reconstruct %q, want %q", joined, tc.in)
			}
		})
	}
}

func TestScannerDone(t *testing.T) {
	sc := NewScanner("a1")
	if !sc.Next() || sc.Kind() != Other || sc.Text() != "a" {
		t.Fatal("first run mismatch")
	}
	if !sc.Next() || sc.Kind() != Digit || sc.Text() != "1" {
		t.Fatal("second run mismatch")
	}
	if sc.Next() || sc.Kind() != Done {
		t.Fatal("expected Done after runs")
	}
	if sc.Next() || sc.Kind() != Done {
		t.Fatal("Done must repeat")
	}
	if sc.Pos() != 2 {
		t.Fatalf("pos = %d, want 2", sc.Pos())
	}
}

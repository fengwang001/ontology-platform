package chunk

import "testing"

type part struct {
	text string
	dig  bool
}

func TestScanner(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []part
	}{
		{"empty", "", nil},
		{"letters", "abc", []part{{"abc", false}}},
		{"digits", "0123", []part{{"0123", true}}},
		{"alternate", "a9b", []part{{"a", false}, {"9", true}, {"b", false}}},
		{"runs", "ab12cd34", []part{
			{"ab", false}, {"12", true}, {"cd", false}, {"34", true},
		}},
		{"ascii-only", "a１２b0", []part{
			{"a１２b", false}, {"0", true},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(tc.in)
			var got []part
			for s.Next() {
				got = append(got, part{s.Text(), s.IsDigit()})
			}
			if len(got) != len(tc.want) {
				t.Fatalf("chunks = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("chunk %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
			if s.Pos() != len(tc.in) {
				t.Fatalf("Pos = %d, want %d", s.Pos(), len(tc.in))
			}
		})
	}
}

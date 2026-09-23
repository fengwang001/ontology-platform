package lex

import "testing"

func TestByteCounter(t *testing.T) {
	in := make([]byte, 0, 1<<20)
	pat := []byte(`a'b"c"\$\ x\` + "\n" + `"\$\\n"`)
	for len(in) < 1<<20 {
		in = append(in, pat...)
	}
	in = in[:1<<20]

	var seen int64
	m := New(func(Event) { seen++ })
	for _, c := range in {
		if err := m.Feed([]byte{c}); err != nil {
			t.Fatalf("Feed: %v", err)
		}
	}
	if m.Bytes() != int64(len(in)) {
		t.Fatalf("counter = %d, want %d", m.Bytes(), len(in))
	}

	var collected int64
	m2 := New(func(Event) { collected++ })
	if err := m2.Feed(in); err != nil {
		t.Fatalf("Feed whole: %v", err)
	}
	if m2.Bytes() != int64(len(in)) {
		t.Fatalf("whole counter = %d, want %d", m2.Bytes(), len(in))
	}
}

func TestMachineEOFErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want error
		off  int64
	}{
		{"single", `'abc`, ErrUnclosedSingle, 0},
		{"double", `"abc`, ErrUnclosedDouble, 0},
		{"double after bs", `"a\`, ErrUnclosedDouble, 0},
		{"trailing bs", `abc\`, ErrTrailingBackslash, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := New(func(Event) {})
			if err := m.Feed([]byte(tc.in)); err != nil {
				t.Fatalf("Feed: %v", err)
			}
			err := m.Close()
			oe, ok := err.(*OffsetError)
			if !ok || oe.Err != tc.want || oe.Offset != tc.off {
				t.Fatalf("Close = %v, want %v at %d", err, tc.want, tc.off)
			}
		})
	}
}

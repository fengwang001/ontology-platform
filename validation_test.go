package ontology

import "testing"

func TestValidationAndRejectedOperationsDoNotMutate(t *testing.T) {
	f := NewFinder()

	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"empty add", func() error { return f.Add("") }, ErrInvalidWord},
		{"invalid utf8 add", func() error { return f.Add(string([]byte{0xff})) }, ErrInvalidWord},
		{"start marker add", func() error { return f.Add("a\x02b") }, ErrInvalidWord},
		{"end marker add", func() error { return f.Add("a\x03b") }, ErrInvalidWord},
		{"remove missing", func() error { return f.Remove("missing") }, ErrWordMissing},
	}
	for _, tc := range cases {
		if err := tc.call(); err != tc.want {
			t.Fatalf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}

	if err := f.Add("same"); err != nil {
		t.Fatal(err)
	}
	if err := f.Add("same"); err != ErrWordExists {
		t.Fatalf("duplicate Add: got %v, want %v", err, ErrWordExists)
	}

	if _, err := f.Similar("", 1, 1); err != ErrInvalidWord {
		t.Fatalf("invalid q error = %v", err)
	}
	if _, err := f.Similar(string([]byte{0xff}), 1, 1); err != ErrInvalidWord {
		t.Fatalf("invalid UTF-8 q error = %v", err)
	}
	if _, err := f.Similar("\x02", 1, 1); err != ErrInvalidWord {
		t.Fatalf("marker q error = %v", err)
	}
	if _, err := f.Similar("q", 0, 1); err != ErrInvalidThreshold {
		t.Fatalf("theta=0 error = %v", err)
	}
	if _, err := f.Similar("q", 101, 1); err != ErrInvalidThreshold {
		t.Fatalf("theta=101 error = %v", err)
	}
	if _, err := f.Similar("q", 1, 0); err != ErrInvalidLimit {
		t.Fatalf("k=0 error = %v", err)
	}

	if err := f.Remove("same"); err != nil {
		t.Fatal(err)
	}
	if err := f.Remove("same"); err != ErrWordMissing {
		t.Fatalf("second Remove error = %v", err)
	}

	got, err := f.Similar("same", 100, 1)
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, got, []Match{}, "Similar(same,100,1) after Remove", "全部被拒绝操作均未改变最终空词典状态")
}

func TestSimilarChecksRejectionOrder(t *testing.T) {
	f := NewFinder()
	if _, err := f.Similar("", 0, 0); err != ErrInvalidWord {
		t.Fatalf("first rejection = %v, want %v", err, ErrInvalidWord)
	}
	if _, err := f.Similar("q", 0, 0); err != ErrInvalidThreshold {
		t.Fatalf("second rejection = %v, want %v", err, ErrInvalidThreshold)
	}
}

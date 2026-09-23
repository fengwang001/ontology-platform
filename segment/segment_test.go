package segment

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ontology/posting"
)

func testSegment() *Segment {
	lists := map[string]posting.List{}
	for i := 0; i < 200; i++ {
		var b posting.Builder
		for d := uint32(0); d < 5; d++ {
			_ = b.Add(d, uint32(i%7))
			_ = b.Add(d, uint32(i%7)+10)
		}
		term := string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+i/52))
		lists[term] = b.List()
	}
	return New(lists)
}

func writeTrunc(t *testing.T, dir string, data []byte, n int) string {
	t.Helper()
	p := filepath.Join(dir, "cut.seg")
	if err := os.WriteFile(p, data[:n], 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := testSegment()
	p := filepath.Join(dir, "a.seg")
	if err := Write(p, want); err != nil {
		t.Fatal(err)
	}
	got, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Terms) != len(want.Terms) {
		t.Fatalf("terms: want %d, got %d", len(want.Terms), len(got.Terms))
	}
	for _, term := range want.Terms {
		l, ok := got.Lookup(term)
		if !ok || len(l) != len(want.Lists[term]) {
			t.Fatalf("term %q not round-tripped", term)
		}
	}
}

func TestTruncationClassification(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "full.seg")
	if err := Write(full, testSegment()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	classes := []error{ErrHeaderIncomplete, ErrDictIncomplete, ErrPostingsIncomplete, ErrCRCMismatch}
	seen := map[error]int{}
	for cut := 1; cut < len(data); cut++ {
		p := writeTrunc(t, dir, data, cut)
		_, rerr := Read(p)
		matched := false
		for _, c := range classes {
			if errors.Is(rerr, c) {
				seen[c]++
				matched = true
			}
		}
		if !matched {
			t.Fatalf("cut %d: unclassified error %v", cut, rerr)
		}
	}
	for _, c := range classes {
		if seen[c] == 0 {
			t.Fatalf("class %v never observed", c)
		}
	}
	t.Logf("file=%d bytes; counts: header=%d dict=%d postings=%d crc=%d",
		len(data), seen[ErrHeaderIncomplete], seen[ErrDictIncomplete],
		seen[ErrPostingsIncomplete], seen[ErrCRCMismatch])
}

func TestRecoverNoDanglingTerms(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "full.seg")
	if err := Write(full, testSegment()); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(full)
	cases := []struct {
		name     string
		cut      int
		wantErr  error
		minTerms int
		maxTerms int
	}{
		{"crc cut keeps all", len(data) - 2, ErrCRCMismatch, 200, 200},
		{"mid postings cut", len(data) / 2, ErrPostingsIncomplete, 1, 199},
		{"near end postings", len(data) - 8, ErrPostingsIncomplete, 150, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeTrunc(t, dir, data, tc.cut)
			rec, err := ReadRecover(p)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
			if rec == nil {
				t.Fatal("want recoverable prefix, got nil")
			}
			n := len(rec.Terms)
			if n < tc.minTerms || n > tc.maxTerms {
				t.Fatalf("recovered %d terms, want [%d,%d]", n, tc.minTerms, tc.maxTerms)
			}
			for _, term := range rec.Terms { // 词典中每个词的链必须完整可读
				l, ok := rec.Lookup(term)
				if !ok || l == nil {
					t.Fatalf("dangling term %q", term)
				}
				for i := 1; i < len(l); i++ {
					if l[i-1].Doc >= l[i].Doc {
						t.Fatalf("term %q: docs not ascending", term)
					}
				}
			}
		})
	}
}

func TestCRCCorruption(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "full.seg")
	if err := Write(full, testSegment()); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(full)
	data[len(data)/2] ^= 0xff
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(full); !errors.Is(err, ErrCRCMismatch) {
		t.Fatalf("want ErrCRCMismatch, got %v", err)
	}
}

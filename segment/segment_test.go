package segment

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ontology/posting"
)

func writeTestSegment(t *testing.T, terms int) (string, map[string]posting.List) {
	t.Helper()
	lists := map[string]posting.List{}
	for i := 0; i < terms; i++ {
		term := string(rune('a'+i%26)) + string(rune('A'+i/26))
		var b posting.Builder
		for d := 0; d <= i%5; d++ {
			for p := 0; p <= i%3; p++ {
				b.Add(uint32(d), uint32(p))
			}
		}
		lists[term] = b.List()
	}
	path := filepath.Join(t.TempDir(), "test.seg")
	if err := Write(path, lists); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path, lists
}

func TestWriteOpenRoundTrip(t *testing.T) {
	path, want := writeTestSegment(t, 50)
	seg, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if len(seg.Terms()) != len(want) {
		t.Fatalf("want %d terms, got %d", len(want), len(seg.Terms()))
	}
	for term, wl := range want {
		gl, ok := seg.Postings(term)
		if !ok {
			t.Fatalf("term %q missing", term)
		}
		if len(gl) != len(wl) {
			t.Fatalf("term %q: want %d entries, got %d", term, len(wl), len(gl))
		}
	}
}

func classify(t *testing.T, data []byte, n int) error {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "trunc.seg")
	if err := os.WriteFile(path, data[:n], 0o644); err != nil {
		t.Fatalf("write trunc: %v", err)
	}
	_, err := Open(path)
	if err == nil {
		t.Fatalf("truncation at %d unexpectedly opened", n)
	}
	return err
}

func TestTruncationClassification(t *testing.T) {
	path, _ := writeTestSegment(t, 200)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	classes := map[error]int{
		ErrHeaderIncomplete:   0,
		ErrDictIncomplete:     0,
		ErrPostingsIncomplete: 0,
		ErrCRCMismatch:        0,
	}
	lo := map[error]int{}
	hi := map[error]int{}
	for n := 1; n < len(data); n++ {
		err := classify(t, data, n)
		matched := false
		for class := range classes {
			if errors.Is(err, class) {
				classes[class]++
				if _, ok := lo[class]; !ok {
					lo[class] = n
				}
				hi[class] = n
				matched = true
			}
		}
		if !matched {
			t.Fatalf("truncation at %d: unclassified error %v", n, err)
		}
	}
	for class, cnt := range classes {
		if cnt == 0 {
			t.Fatalf("class %v never occurred", class)
		}
		t.Logf("class %v: count=%d bytes=[%d,%d]", class, cnt, lo[class], hi[class])
	}
}

func TestRecoverNoDangling(t *testing.T) {
	path, lists := writeTestSegment(t, 200)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	fullTerms := len(lists)
	for n := 1; n < len(data); n++ {
		dir := t.TempDir()
		p := filepath.Join(dir, "trunc.seg")
		if err := os.WriteFile(p, data[:n], 0o644); err != nil {
			t.Fatalf("write trunc: %v", err)
		}
		seg, err := Recover(p)
		if err == nil {
			t.Fatalf("recover at %d: want classified error, got nil", n)
		}
		terms := seg.Terms()
		if len(terms) > fullTerms {
			t.Fatalf("recover at %d: more terms than original", n)
		}
		for _, term := range terms {
			l, ok := seg.Postings(term)
			if !ok {
				t.Fatalf("recover at %d: dangling term %q", n, term)
			}
			if _, derr := posting.Decode(posting.Encode(l)); derr != nil {
				t.Fatalf("recover at %d: term %q postings unreadable: %v", n, term, derr)
			}
		}
	}
}

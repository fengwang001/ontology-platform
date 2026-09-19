package ontology

import (
	"errors"
	"fmt"
	"testing"
)

func seed(t *testing.T, n int) (*Store, []string) {
	t.Helper()
	s := NewStore()
	keys := make([]string, 0, n)
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("k%03d", i)
		s.Put(k, i)
		keys = append(keys, k)
	}
	return s, keys
}

func pageKeys(p Page) []string {
	keys := make([]string, 0, len(p.Items))
	for _, it := range p.Items {
		keys = append(keys, it.Key)
	}
	return keys
}

func equalKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFullPaginationNoDupNoMiss(t *testing.T) {
	s, want := seed(t, 10)
	var got []string
	var moreSeq []bool
	cursor := ""
	for {
		p, err := s.Scan(cursor, 4)
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		got = append(got, pageKeys(p)...)
		moreSeq = append(moreSeq, p.HasMore)
		cursor = p.Next
		if !p.HasMore {
			break
		}
	}
	if !equalKeys(got, want) {
		t.Fatalf("pages = %v, want %v", got, want)
	}
	if !equalKeys(fmtSeq(moreSeq), []string{"true", "true", "false"}) {
		t.Fatalf("hasMore sequence = %v", moreSeq)
	}
}

func fmtSeq(b []bool) []string {
	out := make([]string, len(b))
	for i, v := range b {
		out[i] = fmt.Sprint(v)
	}
	return out
}

func TestScanAfterLastPage(t *testing.T) {
	s, _ := seed(t, 3)
	p, err := s.Scan("", 10)
	if err != nil || p.HasMore {
		t.Fatalf("first scan: hasMore=%v err=%v", p.HasMore, err)
	}
	tail, err := s.Scan(p.Next, 10)
	if err != nil {
		t.Fatalf("tail scan must not error: %v", err)
	}
	if len(tail.Items) != 0 || tail.HasMore {
		t.Fatalf("tail = %d items, hasMore=%v; want empty/false",
			len(tail.Items), tail.HasMore)
	}
}

func TestLimitZeroEmptyPageNoAdvance(t *testing.T) {
	s, _ := seed(t, 5)
	p, err := s.Scan("", 0)
	if err != nil {
		t.Fatalf("limit=0 must not error: %v", err)
	}
	if len(p.Items) != 0 {
		t.Fatalf("limit=0 returned %d items", len(p.Items))
	}
	again, err := s.Scan(p.Next, 0)
	if err != nil || again.Next != p.Next {
		t.Fatalf("cursor advanced on limit=0: %q -> %q", p.Next, again.Next)
	}
	full, err := s.Scan(p.Next, 100)
	if err != nil || len(full.Items) != 5 {
		t.Fatalf("cursor after limit=0 lost position: %d items, err=%v",
			len(full.Items), err)
	}
}

func TestLimitNegativeIsArgumentError(t *testing.T) {
	s, _ := seed(t, 3)
	_, err := s.Scan("", -1)
	if !errors.Is(err, ErrNegativeLimit) {
		t.Fatalf("err = %v, want ErrNegativeLimit", err)
	}
	if errors.Is(err, ErrInvalidCursor) || errors.Is(err, ErrInvalidSession) {
		t.Fatalf("negative limit must not be a cursor error: %v", err)
	}
	p, _ := s.Scan("", 1)
	if _, err := s.Scan(p.Next, -5); !errors.Is(err, ErrNegativeLimit) {
		t.Fatalf("mid-traversal err = %v, want ErrNegativeLimit", err)
	}
}

func TestLimitGreaterThanRemaining(t *testing.T) {
	s, want := seed(t, 4)
	p, err := s.Scan("", 100)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !equalKeys(pageKeys(p), want) || p.HasMore {
		t.Fatalf("got %v hasMore=%v", pageKeys(p), p.HasMore)
	}
}

func TestLimitExactlyRemaining(t *testing.T) {
	s, _ := seed(t, 5)
	p1, _ := s.Scan("", 2)
	p2, err := s.Scan(p1.Next, 3) // exactly the remaining 3
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(p2.Items) != 3 || p2.HasMore {
		t.Fatalf("exact remaining: %d items, hasMore=%v; want 3/false",
			len(p2.Items), p2.HasMore)
	}
	tail, err := s.Scan(p2.Next, 3)
	if err != nil || len(tail.Items) != 0 || tail.HasMore {
		t.Fatalf("tail after exact page: %d items, hasMore=%v, err=%v",
			len(tail.Items), tail.HasMore, err)
	}
}

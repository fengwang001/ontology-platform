package ontology

import (
	"errors"
	"reflect"
	"testing"
)

func keys(p Page) []string {
	out := make([]string, len(p.Objects))
	for i, o := range p.Objects {
		out[i] = o.Key
	}
	return out
}

func seedStore(n int) *Store {
	s := NewStore()
	for i := 0; i < n; i++ {
		s.Put(keyOf(i), i)
	}
	return s
}

// TestScanThreePages 正常翻完三页：不重、不漏、不报错。
func TestScanThreePages(t *testing.T) {
	s := seedStore(9)
	cursor := ""
	var got []string
	pages := 0
	for {
		p, err := s.Scan(cursor, 3)
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, keys(p)...)
		if !p.HasMore {
			break
		}
		if p.Cursor == "" {
			t.Fatal("HasMore=true but empty cursor")
		}
		cursor = p.Cursor
		pages++
	}
	if pages != 2 {
		t.Fatalf("expected 2 full pages before final, got %d", pages)
	}
	want := make([]string, 9)
	for i := range want {
		want[i] = keyOf(i)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paging mismatch: %v", got)
	}
}

// TestScanAfterLastPage 最后一页之后再扫：空页、HasMore=false、不报错。
func TestScanAfterLastPage(t *testing.T) {
	s := seedStore(3)
	p1, err := s.Scan("", 3)
	if err != nil || p1.HasMore || p1.Cursor != "" {
		t.Fatalf("first page: %+v err=%v", p1, err)
	}
}

// TestLimitZero 不前进、不报错，且游标保持原样。
func TestLimitZero(t *testing.T) {
	s := seedStore(5)
	p, err := s.Scan("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Objects) != 0 || p.HasMore || p.Cursor != "" {
		t.Fatalf("limit=0 should be empty no-advance: %+v", p)
	}
	// 用已有游标 limit=0：返回的游标字段为空（不前进），再扫内容不变。
	p1, _ := s.Scan("", 2)
	p0, err := s.Scan(p1.Cursor, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(p0.Objects) != 0 || p0.Cursor != "" {
		t.Fatalf("limit=0 must not advance cursor: %+v", p0)
	}
	p2, _ := s.Scan(p1.Cursor, 2)
	if !reflect.DeepEqual(keys(p2), []string{keyOf(2), keyOf(3)}) {
		t.Fatalf("cursor must not have moved: %v", keys(p2))
	}
}

// TestLimitNegative 是参数错误，与空页可区分。
func TestLimitNegative(t *testing.T) {
	s := seedStore(2)
	_, err := s.Scan("", -1)
	if !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("want ErrInvalidLimit, got %v", err)
	}
	p1, _ := s.Scan("", 2)
	_, err = s.Scan(p1.Cursor, -7)
	if !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("want ErrInvalidLimit, got %v", err)
	}
}

// TestLimitLargerAndExact 大于/恰好等于剩余数量时 HasMore 都必须为 false。
func TestLimitLargerAndExact(t *testing.T) {
	s := seedStore(5)
	big, err := s.Scan("", 100)
	if err != nil {
		t.Fatal(err)
	}
	if big.HasMore || big.Cursor != "" || len(big.Objects) != 5 {
		t.Fatalf("limit>remaining: %+v", big)
	}

	s2 := seedStore(4)
	first, _ := s2.Scan("", 2)
	exact, err := s2.Scan(first.Cursor, 2) // 恰好等于剩余 2
	if err != nil {
		t.Fatal(err)
	}
	if exact.HasMore || exact.Cursor != "" || len(exact.Objects) != 2 {
		t.Fatalf("limit==remaining must end cleanly, no trailing empty page: %+v", exact)
	}
	again, err := s2.Scan(first.Cursor, 2) // 末页游标重复扫仍为干净空结果
	if err != nil || again.HasMore || len(again.Objects) != 2 {
		t.Fatalf("idempotent end scan: %+v err=%v", again, err)
	}
}

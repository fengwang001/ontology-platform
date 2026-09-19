package ontology

import (
	"errors"
	"testing"
)

func keysOf(page Page) []string {
	keys := make([]string, len(page.Items))
	for i, item := range page.Items {
		keys[i] = item.Key
	}
	return keys
}

func seededStore(n int) *Store {
	st := NewStore()
	for i := 0; i < n; i++ {
		st.Put(keyOf(i), i)
	}
	return st
}

func keyOf(i int) string {
	// 补零保证字典序与插入序一致。
	return "key-" + twoDigits(i)
}

func twoDigits(i int) string {
	if i < 10 {
		return "0" + itoa(i)
	}
	return itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [24]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// 正常翻页：三页不重不漏，最后一页及之后 hasMore 均为 false。
func TestScanThreePagesNoDuplicatesNoGaps(t *testing.T) {
	st := seededStore(7)

	p1, err := st.Scan("", 3)
	if err != nil || len(p1.Items) != 3 || !p1.HasMore {
		t.Fatalf("page1: %v items=%d hasMore=%v", err, len(p1.Items), p1.HasMore)
	}
	p2, err := st.Scan(p1.NextCursor, 3)
	if err != nil || len(p2.Items) != 3 || !p2.HasMore {
		t.Fatalf("page2: %v items=%d hasMore=%v", err, len(p2.Items), p2.HasMore)
	}
	p3, err := st.Scan(p2.NextCursor, 3)
	if err != nil || len(p3.Items) != 1 || p3.HasMore {
		t.Fatalf("page3: %v items=%d hasMore=%v", err, len(p3.Items), p3.HasMore)
	}
	p4, err := st.Scan(p3.NextCursor, 3)
	if err != nil || len(p4.Items) != 0 || p4.HasMore {
		t.Fatalf("after-end page must be empty and hasMore=false: %v %+v", err, p4)
	}

	seen := map[string]bool{}
	for _, page := range []Page{p1, p2, p3} {
		for _, item := range page.Items {
			if seen[item.Key] {
				t.Fatalf("duplicate key across pages: %s", item.Key)
			}
			seen[item.Key] = true
		}
	}
	if len(seen) != 7 {
		t.Fatalf("expected 7 distinct keys, got %d", len(seen))
	}
}

// limit 恰好等于剩余元素数时 hasMore 必须为 false。
func TestLimitExactlyRemaining(t *testing.T) {
	st := seededStore(5)
	p1, _ := st.Scan("", 2)
	if !p1.HasMore {
		t.Fatal("first page should be truncated")
	}
	p2, err := st.Scan(p1.NextCursor, 3)
	if err != nil || len(p2.Items) != 3 || p2.HasMore {
		t.Fatalf("exact remainder: items=%d hasMore=%v err=%v", len(p2.Items), p2.HasMore, err)
	}
}

// limit 大于剩余元素数：返回剩余全部，hasMore 为 false。
func TestLimitLargerThanRemaining(t *testing.T) {
	st := seededStore(3)
	p, err := st.Scan("", 100)
	if err != nil || len(p.Items) != 3 || p.HasMore {
		t.Fatalf("items=%d hasMore=%v err=%v", len(p.Items), p.HasMore, err)
	}
}

// limit=0：空页、游标不前进、不报错；下一页内容与 limit=1 应一致。
func TestLimitZeroNoProgress(t *testing.T) {
	st := seededStore(3)
	zero, err := st.Scan("", 0)
	if err != nil || len(zero.Items) != 0 || zero.HasMore {
		t.Fatalf("limit=0: items=%d hasMore=%v err=%v", len(zero.Items), zero.HasMore, err)
	}
	one, err := st.Scan(zero.NextCursor, 1)
	if err != nil || len(one.Items) != 1 || one.Items[0].Key != keyOf(0) {
		t.Fatalf("cursor must not advance on limit=0: %+v err=%v", one, err)
	}
}

// limit<0：参数错误，与空页是可区分的两种结果。
func TestLimitNegativeIsError(t *testing.T) {
	st := seededStore(2)
	p, err := st.Scan("", -1)
	if !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("expected ErrInvalidLimit, got %v", err)
	}
	if len(p.Items) != 0 || p.NextCursor != "" {
		t.Fatalf("error result must be zero-value page, got %+v", p)
	}
	// 与 limit=0 对照：0 返回 nil 错误。
	if _, err := st.Scan("", 0); err != nil {
		t.Fatalf("limit=0 must not error, got %v", err)
	}
}

package hold

import (
	"fmt"
	"testing"

	"ontology/revenue"
)

// Earn 判定是否落入冻结区间时，比较的冻结条数只与该内容的冻结数有关。
func TestEarnHoldCompareCount(t *testing.T) {
	book, err := revenue.NewBook(0)
	if err != nil {
		t.Fatalf("NewBook: %v", err)
	}
	r := NewRegistry(book)
	parts := []revenue.Part{{Creator: "u", BPS: 10000}}
	for _, content := range []string{"a", "b", "c"} {
		if err := book.SetSplit(0, content, parts); err != nil {
			t.Fatalf("SetSplit: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		if err := r.Hold(0, fmt.Sprintf("ha%d", i), "a", 0, 100); err != nil {
			t.Fatalf("Hold: %v", err)
		}
	}
	for i := 0; i < 50; i++ {
		if err := r.Hold(0, fmt.Sprintf("hb%d", i), "b", 0, 100); err != nil {
			t.Fatalf("Hold: %v", err)
		}
	}
	cases := []struct {
		content string
		wantCmp int
	}{
		{"a", 3},
		{"b", 50},
		{"c", 0},
	}
	for i, tc := range cases {
		r.cmp = 0
		if _, err := book.Earn(int64(i+1), fmt.Sprintf("e%d", i), tc.content, 10); err != nil {
			t.Fatalf("Earn: %v", err)
		}
		if r.cmp != tc.wantCmp {
			t.Fatalf("content=%s 比较次数=%d, want %d（只与该内容的冻结数有关）",
				tc.content, r.cmp, tc.wantCmp)
		}
	}
}

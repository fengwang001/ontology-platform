package hist

import (
	"testing"

	"ontology/bucket"
)

func TestBucketNumber(t *testing.T) {
	cases := []struct {
		name         string
		v, anchor, w int
		want         int
	}{
		{"positive in bucket 0", 5, 0, 10, 0},
		{"positive in bucket 1", 12, 0, 10, 1},
		{"right edge goes to next bucket", 20, 0, 10, 2},
		{"left edge stays in bucket", 10, 0, 10, 1},
		{"negative must floor toward -inf", -3, 0, 10, -1}, // 截断除法会错成 0
		{"negative exact edge", -10, 0, 10, -1},
		{"negative past two buckets", -15, 0, 10, -2},
		{"shifted anchor", 9, 3, 7, 0},
		{"shifted anchor negative floor", -4, 3, 7, -1},
		{"width one around zero", -1, 0, 1, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := bucket.Number(c.v, c.anchor, c.w); got != c.want {
				t.Fatalf("Number(%d,%d,%d)=%d want %d", c.v, c.anchor, c.w, got, c.want)
			}
		})
	}
}

func TestBucketContains(t *testing.T) {
	cases := []struct {
		v, anchor, w, k int
		want            bool
	}{
		{0, 0, 10, 0, true}, {9, 0, 10, 0, true}, {10, 0, 10, 0, false},
		{10, 0, 10, 1, true}, {20, 0, 10, 1, false}, {20, 0, 10, 2, true},
		{-1, 0, 10, 0, false}, {-1, 0, 10, -1, true}, {-10, 0, 10, -1, true},
	}
	for _, c := range cases {
		if got := bucket.Contains(c.v, c.anchor, c.w, c.k); got != c.want {
			t.Errorf("Contains(v=%d,k=%d)=%v want %v", c.v, c.k, got, c.want)
		}
	}
}

func TestNewRejectsWidth(t *testing.T) {
	for _, w := range []int{0, -1, -10} {
		h, err := New(w, 0)
		if err != ErrWidth || h != nil {
			t.Fatalf("New(%d) = %+v,%v want nil,ErrWidth", w, h, err)
		}
	}
}

// TestLocateChecksIndependentOfM 钉住复杂度约束：铺 m 个相距很远的桶后
// Insert 全新桶，定位检查过的桶个数必须被一个与 m 无关的小常数界住。
// 直接读非导出字段 lastChecked（不经由任何导出方法）。
func TestLocateChecksIndependentOfM(t *testing.T) {
	const bound = 2
	ms := []int{100, 500, 1000, 5000, 10000}
	for _, m := range ms {
		h, err := New(1, 0)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < m; i++ {
			h.Insert(i * 1_000_000) // 每个值独占一桶，铺出 m 个桶
		}
		h.Insert(-1) // 落入全新桶
		if h.lastChecked > bound {
			t.Fatalf("m=%d: checked %d buckets > constant bound %d", m, h.lastChecked, bound)
		}
	}
}

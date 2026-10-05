package purge_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"ontology/purge"
	"ontology/quota"
)

func TestValidPath(t *testing.T) {
	long16 := "/" + strings.Repeat("a/", 15) + "a" // 16 段
	long17 := "/" + strings.Repeat("a/", 16) + "a" // 17 段
	max256 := "/" + strings.Repeat("a", 255)       // 恰 256 字节
	over256 := "/" + strings.Repeat("a", 256)      // 257 字节
	cases := []struct {
		path string
		want bool
	}{
		{"/", true},
		{"/img", true},
		{"/img/", true},
		{"/imgs/a", true},
		{"/a/b/c/d", true},
		{long16, true},
		{max256, true},
		{"", false},
		{"img", false},
		{"//", false},
		{"/a//b", false},
		{"/a//", false},
		{"/.", false},
		{"/..", false},
		{"/a/./b", false},
		{"/a/../b", false},
		{"/a/./", false},
		{long17, false},
		{over256, false},
	}
	for _, c := range cases {
		if got := purge.ValidPath(c.path); got != c.want {
			t.Errorf("ValidPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		name  string
		items []string
		urls  []string
		dirs  []string
	}{
		{"重复项合并", []string{"/a", "/a", "/a"}, []string{"/a"}, nil},
		{"目录覆盖同批 URL", []string{"/img/", "/img/a.jpg", "/img/a.jpg", "/css/x.css"},
			[]string{"/css/x.css"}, []string{"/img/"}},
		{"目录套目录只计外层", []string{"/a/", "/a/b/", "/a/b/c.jpg"}, nil, []string{"/a/"}},
		{"根目录覆盖一切", []string{"/", "/img", "/a/b/"}, nil, []string{"/"}},
		{"img 与 img/ 与 imgs/ 互不覆盖", []string{"/img", "/img/", "/imgs/"},
			[]string{"/img"}, []string{"/img/", "/imgs/"}},
		{"目录不覆盖同段名前缀", []string{"/img/", "/imgs/a"}, []string{"/imgs/a"}, []string{"/img/"}},
		{"URL 不覆盖任何项", []string{"/a", "/a/b"}, []string{"/a", "/a/b"}, nil},
		{"保持首次出现次序", []string{"/b", "/a", "/b"}, []string{"/b", "/a"}, nil},
	}
	for _, c := range cases {
		urls, dirs := purge.Normalize(c.items)
		if !equal(urls, c.urls) || !equal(dirs, c.dirs) {
			t.Errorf("%s: Normalize(%v) = (%v, %v), want (%v, %v)",
				c.name, c.items, urls, dirs, c.urls, c.dirs)
		}
	}
}

func equal(a, b []string) bool {
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

func newStore(t *testing.T, id string, q quota.Quotas) (*quota.Meter, *purge.Store) {
	t.Helper()
	m := quota.NewMeter()
	if err := m.Register(id, q); err != nil {
		t.Fatal(err)
	}
	return m, purge.NewStore(m)
}

func TestPurgeEpochContinuousAndShared(t *testing.T) {
	m := quota.NewMeter()
	for _, id := range []string{"t1", "t2"} {
		if err := m.Register(id, quota.Quotas{URL: 10, Dir: 10}); err != nil {
			t.Fatal(err)
		}
	}
	s := purge.NewStore(m)
	if got := s.Epoch(); got != 0 {
		t.Fatalf("初始纪元 = %d, want 0", got)
	}
	// 被拒绝的 Purge 不推进纪元。
	if _, err := s.Purge(0, "t1", []string{"/a", "/b", "/c", "/d", "/e", "/f", "/g", "/h", "/i", "/j", "/k"}); !errors.Is(err, quota.ErrURLQuota) {
		t.Fatalf("应报 URL 配额不足, got %v", err)
	}
	if got := s.Epoch(); got != 0 {
		t.Fatalf("拒绝后纪元 = %d, want 0", got)
	}
	// 各租户共用同一计数，自 1 连续无洞。
	for i, id := range []string{"t1", "t2", "t1"} {
		epoch, err := s.Purge(0, id, []string{fmt.Sprintf("/p%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if want := uint64(i + 1); epoch != want {
			t.Fatalf("第 %d 批纪元 = %d, want %d", i, epoch, want)
		}
	}
}

func TestPurgeArgumentAndClockOrder(t *testing.T) {
	_, s := newStore(t, "t", quota.Quotas{URL: 1, Dir: 1})
	if _, err := s.Purge(0, "t", nil); !errors.Is(err, purge.ErrInvalidArgument) {
		t.Fatalf("空批应报参数非法, got %v", err)
	}
	if _, err := s.Purge(0, "t", []string{"//"}); !errors.Is(err, purge.ErrInvalidArgument) {
		t.Fatalf("非法路径应报参数非法, got %v", err)
	}
	if _, err := s.Purge(quota.MaxNow+1, "t", []string{"/a"}); !errors.Is(err, purge.ErrInvalidArgument) {
		t.Fatalf("now 越界应报参数非法, got %v", err)
	}
	if _, err := s.Purge(100, "t", []string{"/a"}); err != nil {
		t.Fatal(err)
	}
	// 时钟回退优先于租户不存在。
	if _, err := s.Purge(99, "ghost", []string{"/b"}); !errors.Is(err, quota.ErrClockBackward) {
		t.Fatalf("应先报时钟回退, got %v", err)
	}
	if _, err := s.Purge(100, "ghost", []string{"/b"}); !errors.Is(err, quota.ErrTenantNotFound) {
		t.Fatalf("应报租户不存在, got %v", err)
	}
}

func TestMatchEpochRules(t *testing.T) {
	_, s := newStore(t, "t", quota.Quotas{URL: 10, Dir: 10})
	if _, err := s.Purge(0, "t", []string{"/img", "/img/", "/css/x.css"}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		url  string
		want uint64
	}{
		{"/img", 1},       // 精确 URL 规则
		{"/img/a.jpg", 1}, // 被目录 /img/ 覆盖
		{"/css/x.css", 1},
		{"/imgs/a", 0}, // /img/ 不覆盖 /imgs/a
		{"/im", 0},     // URL 规则 /img 不做前缀匹配
		{"/other", 0},
	}
	for _, c := range cases {
		if got := s.MatchEpoch("t", c.url); got != c.want {
			t.Errorf("MatchEpoch(t, %q) = %d, want %d", c.url, got, c.want)
		}
	}
	// 同一路径只保留较大纪元。
	if _, err := s.Purge(0, "t", []string{"/img"}); err != nil {
		t.Fatal(err)
	}
	if got := s.MatchEpoch("t", "/img"); got != 2 {
		t.Fatalf("同路径应保留较大纪元, got %d, want 2", got)
	}
	// 根目录规则匹配所有 URL。
	if _, err := s.Purge(0, "t", []string{"/"}); err != nil {
		t.Fatal(err)
	}
	if got := s.MatchEpoch("t", "/any/thing"); got != 3 {
		t.Fatalf("根目录规则应匹配所有 URL, got %d, want 3", got)
	}
	// 租户命名空间相互独立。
	if got := s.MatchEpoch("ghost", "/img"); got != 0 {
		t.Fatalf("其他租户应无规则, got %d", got)
	}
}

func TestMatchEpochVisitedBound(t *testing.T) {
	for _, nRules := range []int{100, 10000} {
		_, s := newStore(t, "t", quota.Quotas{URL: quota.MaxQuota, Dir: quota.MaxQuota})
		for done := 0; done < nRules; {
			var items []string
			for i := 0; i < purge.MaxItems && done < nRules; i++ {
				if done%2 == 0 {
					items = append(items, fmt.Sprintf("/r%06d", done))
				} else {
					items = append(items, fmt.Sprintf("/d%06d/", done))
				}
				done++
			}
			if _, err := s.Purge(0, "t", items); err != nil {
				t.Fatal(err)
			}
		}
		const url = "/a/b/c/d" // 4 段
		s.MatchEpoch("t", url)
		if got := s.LastVisited(); got > 4+2 {
			t.Errorf("规则 %d 条: visited = %d, want <= 段数+2 = 6", nRules, got)
		}
	}
}

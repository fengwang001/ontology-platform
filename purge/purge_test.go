package purge

import (
	"errors"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		raw  string
		kind Kind
		segs int
		err  error
	}{
		{"/", KindDir, 0, nil},
		{"/img", KindURL, 1, nil},
		{"/img/", KindDir, 1, nil},
		{"/img/a.jpg", KindURL, 2, nil},
		{"/imgs/a", KindURL, 2, nil},
		{"img", KindURL, 0, ErrInvalidPath},
		{"/a//b", KindURL, 0, ErrInvalidPath},
		{"/a/./b", KindURL, 0, ErrInvalidPath},
		{"/a/../b", KindURL, 0, ErrInvalidPath},
		{"/a/b/", KindDir, 2, nil},
		{"/", KindDir, 0, nil},
	}
	long := "/" + strings.Repeat("a/", 17) // 17 段
	overBytes := "/" + strings.Repeat("a", 256)
	cases = append(cases,
		struct {
			raw  string
			kind Kind
			segs int
			err  error
		}{long, KindDir, 0, ErrInvalidPath},
		struct {
			raw  string
			kind Kind
			segs int
			err  error
		}{overBytes, KindDir, 0, ErrInvalidPath},
	)
	for _, tc := range cases {
		p, err := Parse(tc.raw)
		if !errors.Is(err, tc.err) {
			t.Errorf("Parse(%q) err=%v want %v", tc.raw, err, tc.err)
			continue
		}
		if err != nil {
			continue
		}
		if p.Kind != tc.kind || len(p.Segs) != tc.segs {
			t.Errorf("Parse(%q) = kind %d segs %d, want kind %d segs %d",
				tc.raw, p.Kind, len(p.Segs), tc.kind, tc.segs)
		}
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		name  string
		items []string
		u, d  int
		kept  []string
		err   error
	}{
		{
			name:  "example",
			items: []string{"/img/", "/img/a.jpg", "/img/a.jpg", "/css/x.css"},
			u:     1, d: 1,
			kept: []string{"/img/", "/css/x.css"},
		},
		{
			name:  "img-vs-imgs",
			items: []string{"/img/", "/imgs/a", "/img", "/imgs/"},
			u:     1, d: 2, // /img（URL）不被 /img/ 覆盖；/imgs/a 被 /imgs/ 覆盖
			kept: []string{"/img/", "/imgs/", "/img"},
		},
		{
			name:  "nested dirs only outer",
			items: []string{"/a/b/c/x", "/a/b/", "/x/y"},
			u:     1, d: 1, // /a/b/c/x 被 /a/b/ 覆盖
			kept: []string{"/a/b/", "/x/y"},
		},
		{
			name:  "root covers all",
			items: []string{"/x", "/y/", "/", "/z/1"},
			u:     0, d: 1,
			kept: []string{"/"},
		},
		{
			name:  "dir not cover sibling name",
			items: []string{"/img", "/img/"},
			u:     1, d: 1,
			kept: []string{"/img/", "/img"},
		},
		{
			name:  "empty batch",
			items: nil,
			err:   ErrEmptyBatch,
		},
		{
			name:  "invalid item",
			items: []string{"/a", "/b/../c"},
			err:   ErrInvalidPath,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kept, u, d, err := Normalize(tc.items)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err=%v want %v", err, tc.err)
			}
			if err != nil {
				return
			}
			if u != tc.u || d != tc.d {
				t.Fatalf("u=%d d=%d want %d %d", u, d, tc.u, tc.d)
			}
			got := map[string]bool{}
			for _, p := range kept {
				got[p.Raw] = true
			}
			if len(got) != len(tc.kept) {
				t.Fatalf("kept=%v want %v", kept, tc.kept)
			}
			for _, raw := range tc.kept {
				if !got[raw] {
					t.Fatalf("kept missing %q: %v", raw, kept)
				}
			}
		})
	}
}

func TestTrieMaxEpochAndVisited(t *testing.T) {
	s := NewStore()
	put := func(tenant string, epoch int64, raws ...string) {
		kept := make([]Path, 0, len(raws))
		for _, raw := range raws {
			p, err := Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			kept = append(kept, p)
		}
		s.Put(tenant, kept, epoch)
	}
	put("t", 1, "/img/", "/css/x.css")
	put("t", 2, "/img/a.jpg", "/img/b.jpg")

	cases := []struct {
		url   string
		epoch int64
	}{
		{"/img/a.jpg", 2}, // 同名 URL 纪元 2 > 目录纪元 1
		{"/img/b.jpg", 2},
		{"/img/c.png", 1}, // 仅目录规则
		{"/img", 0},       // /img/ 不覆盖 /img
		{"/imgs/a", 0},    // 前缀但非目录边界
		{"/css/x.css", 1},
		{"/css/y.css", 0},
	}
	for _, tc := range cases {
		epoch, visited, err := s.Lookup("t", tc.url)
		if err != nil {
			t.Fatal(err)
		}
		if epoch != tc.epoch {
			t.Errorf("MaxEpoch(%q)=%d want %d", tc.url, epoch, tc.epoch)
		}
		p, _ := Parse(tc.url)
		if visited > len(p.Segs)+1 {
			t.Errorf("visited=%d > segs+1=%d for %q", visited, len(p.Segs)+1, tc.url)
		}
	}

	// 根目录覆盖一切非根 URL，且访问节点数与规则数无关。
	put("r", 5, "/")
	if epoch, v, _ := s.Lookup("r", "/a/b/c/d"); epoch != 5 || v != 1 {
		t.Fatalf("root rule epoch=%d visited=%d", epoch, v)
	}

	// 100 条与 10000 条规则两档：visited 有同样上界，与规则总数无关。
	visByN := map[int]int{}
	for _, n := range []int{100, 10_000} {
		st := NewStore()
		kept := make([]Path, 0, n)
		for i := 0; i < n; i++ {
			p, _ := Parse("/tree/" + itoa(i) + "/")
			kept = append(kept, p)
		}
		st.Put("big", kept, 7)
		url := "/tree/50/deep/leaf"
		_, visited, err := st.Lookup("big", url)
		if err != nil {
			t.Fatal(err)
		}
		p, _ := Parse(url)
		if visited > len(p.Segs)+1 {
			t.Fatalf("n=%d visited=%d bound=%d", n, visited, len(p.Segs)+1)
		}
		visByN[n] = visited
	}
	if visByN[100] != visByN[10_000] {
		t.Fatalf("visited depends on rule count: %v", visByN)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

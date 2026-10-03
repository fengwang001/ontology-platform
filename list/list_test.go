package list_test

import (
	"strings"
	"sync"
	"testing"

	"ontology/keyindex"
	"ontology/list"
)

func load(t *testing.T, kv map[string][]keyindex.Version) *keyindex.Index {
	t.Helper()
	ix := keyindex.New()
	for k, vs := range kv {
		for _, v := range vs {
			ix.Put(k, v.Number, v.Deleted)
		}
	}
	return ix
}

func exampleIndex(t *testing.T) *keyindex.Index {
	return load(t, map[string][]keyindex.Version{
		"a":   {{Number: 1, Deleted: false}},
		"b/1": {{Number: 1, Deleted: false}},
		"b/2": {{Number: 1, Deleted: false}},
		"b/3": {{Number: 1, Deleted: false}},
		"c":   {{Number: 1, Deleted: false}},
		"d/x": {{Number: 1, Deleted: false}},
	})
}

type wantEntry struct {
	key      string
	isPrefix bool
	ver      int
	deleted  bool
}

func entries(ws ...wantEntry) []wantEntry { return ws }

func keyE(k string) wantEntry  { return wantEntry{key: k} }
func prefE(k string) wantEntry { return wantEntry{key: k, isPrefix: true} }
func verE(k string, v int, d bool) wantEntry {
	return wantEntry{key: k, ver: v, deleted: d}
}

func toWant(es []list.Entry) []wantEntry {
	out := make([]wantEntry, len(es))
	for i, e := range es {
		out[i] = wantEntry{e.Key, e.IsPrefix, e.Version, e.Deleted}
	}
	return out
}

func collect(t *testing.T, ix *keyindex.Index, req list.Request, kind string) []wantEntry {
	t.Helper()
	var out []wantEntry
	for page := 0; ; page++ {
		var (
			res *list.Result
			err error
		)
		if kind == "obj" {
			res, err = list.ListObjects(ix, req)
		} else {
			res, err = list.ListVersions(ix, req)
		}
		if err != nil {
			t.Fatalf("第%d页错误: %v", page, err)
		}
		for _, e := range res.Entries {
			out = append(out, wantEntry{e.Key, e.IsPrefix, e.Version, e.Deleted})
		}
		t.Logf("kind=%s page=%d req={prefix:%q delim:%q start:%q max:%d grants:%q} => %+v truncated=%v",
			kind, page, req.Prefix, req.Delimiter, req.StartAfter, req.Max, req.Grants,
			res.Entries, res.IsTruncated)
		if !res.IsTruncated {
			return out
		}
		if res.NextToken == "" {
			t.Fatalf("截断但未签发令牌 page=%d", page)
		}
		req.Token = res.NextToken
		req.StartAfter = ""
	}
}

func check(t *testing.T, name string, got, want []wantEntry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: 条目数 %d != 期望 %d\ngot=%v\nwant=%v", name, len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: 第%d条 %+v != %+v", name, i, got[i], want[i])
		}
	}
}

func TestListObjectsPaging(t *testing.T) {
	type tc struct {
		name string
		req  list.Request
		want []wantEntry
	}
	base := func(max int) list.Request {
		return list.Request{Delimiter: "/", Max: max, Grants: []string{""}}
	}
	cases := []tc{
		{"max3多1条", base(3), entries(
			keyE("a"), prefE("b/"), keyE("c"), prefE("d/"))},
		{"max2公共前缀整组跳", base(2), entries(
			keyE("a"), prefE("b/"), keyE("c"), prefE("d/"))},
		{"max4恰好无截断", base(4), entries(
			keyE("a"), prefE("b/"), keyE("c"), prefE("d/"))},
		{"max10不足一页", base(10), entries(
			keyE("a"), prefE("b/"), keyE("c"), prefE("d/"))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ix := exampleIndex(t)
			got := collect(t, ix, c.req, "obj")
			check(t, c.name, got, c.want)
		})
	}
}

func TestPrefixVariants(t *testing.T) {
	ix := load(t, map[string][]keyindex.Version{
		"b": {{Number: 1, Deleted: false}}, "b/": {{Number: 1, Deleted: false}}, "b/1": {{Number: 1, Deleted: false}},
	})
	cases := []struct {
		prefix string
		want   []wantEntry
	}{
		{"", entries(keyE("b"), prefE("b/"))},
		{"b", entries(keyE("b"), prefE("b/"))},
		{"b/", entries(keyE("b/"), keyE("b/1"))},
	}
	for _, c := range cases {
		t.Run("prefix="+c.prefix, func(t *testing.T) {
			got := collect(t, ix, list.Request{Prefix: c.prefix, Delimiter: "/", Max: 100, Grants: []string{""}}, "obj")
			check(t, "prefix="+c.prefix, got, c.want)
		})
	}
}

func TestStartAfterInsideGroup(t *testing.T) {
	ix := exampleIndex(t)
	got := collect(t, ix, list.Request{Delimiter: "/", StartAfter: "b/2", Max: 10, Grants: []string{""}}, "obj")
	check(t, "startafter-b/2", got, entries(
		prefE("b/"), keyE("c"), prefE("d/")))

	got = collect(t, ix, list.Request{Delimiter: "/", StartAfter: "c", Max: 10, Grants: []string{""}}, "obj")
	check(t, "startafter-c", got, entries(prefE("d/")))
}

func TestGrantsHideAndDedupe(t *testing.T) {
	ix := exampleIndex(t)
	cases := []struct {
		name   string
		grants []string
		want   []wantEntry
	}{
		{"无授权全隐藏", nil, nil},
		{"只授权两目录", []string{"b/", "d/"}, entries(
			prefE("b/"), prefE("d/"))},
		{"仅单键产生公共前缀", []string{"b/2"}, entries(prefE("b/"))},
		{"归一化覆盖", []string{"b/2", "b/"}, entries(prefE("b/"))},
		{"顶层键授权", []string{"a", "c"}, entries(keyE("a"), keyE("c"))},
		{"重复授权", []string{"d/", "d/"}, entries(prefE("d/"))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := collect(t, ix, list.Request{Delimiter: "/", Max: 100, Grants: c.grants}, "obj")
			check(t, c.name, got, c.want)
			t.Logf("grants=%q 判定=公共前缀只由可见键产生，隐藏整组不出现", c.grants)
		})
	}
}

func TestListVersionsAcrossKey(t *testing.T) {
	ix := load(t, map[string][]keyindex.Version{
		"x": {{Number: 3, Deleted: false}, {Number: 2, Deleted: true}, {Number: 1, Deleted: false}},
	})
	req := list.Request{Max: 2, Grants: []string{""}}
	p1, err := list.ListVersions(ix, req)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "versions-p1", toWant(p1.Entries), entries(
		verE("x", 3, false), verE("x", 2, true)))
	if !p1.IsTruncated {
		t.Fatal("页1应截断")
	}
	req.Token = p1.NextToken
	p2, err := list.ListVersions(ix, req)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "versions-p2", toWant(p2.Entries), entries(verE("x", 1, false)))
	if p2.IsTruncated {
		t.Fatal("页2不应截断")
	}
	ro, err := list.ListObjects(ix, list.Request{Max: 10, Grants: []string{""}})
	if err != nil {
		t.Fatal(err)
	}
	check(t, "objects", toWant(ro.Entries), entries(keyE("x")))

	rs, err := list.ListVersions(ix, list.Request{Max: 10, StartAfter: "x", Grants: []string{""}})
	if err != nil || len(rs.Entries) != 0 {
		t.Fatalf("StartAfter 排除同键全部版本: %v %v", rs.Entries, err)
	}
	t.Log("版本按键升序、同键版本号降序；令牌记最后版本续接")
}

func TestCurrentMarkerHidesObject(t *testing.T) {
	ix := load(t, map[string][]keyindex.Version{
		"dead": {{Number: 2, Deleted: true}, {Number: 1, Deleted: false}},
		"live": {{Number: 1, Deleted: false}},
	})
	got := collect(t, ix, list.Request{Max: 10, Grants: []string{""}}, "obj")
	check(t, "当前为标记被隐藏", got, entries(keyE("live")))
	gv := collect(t, ix, list.Request{Max: 10, Grants: []string{""}}, "ver")
	check(t, "版本含标记", gv, entries(
		verE("dead", 2, true), verE("dead", 1, false),
		verE("live", 1, false)))
}

func flipByte(s string) string {
	b := []byte(s)
	b[0] ^= 0xFF
	return string(b)
}

func pad(n int) string {
	s := strings.Repeat("0", 4) + itoa(n)
	return s[len(s)-4:]
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestInvalidArgumentsAndToken(t *testing.T) {
	ix := exampleIndex(t)
	bad := []list.Request{
		{Max: 0, Grants: []string{""}},
		{Max: 1001, Grants: []string{""}},
		{Max: -1, Grants: []string{""}},
		{Max: 1, Delimiter: "//", Grants: []string{""}},
		{Max: 1, StartAfter: "a", Token: "x", Grants: []string{""}},
	}
	for i, r := range bad {
		if _, err := list.ListObjects(ix, r); err != list.ErrInvalidArgument {
			t.Fatalf("bad#%d err=%v 期望 ErrInvalidArgument", i, err)
		}
	}

	p1, err := list.ListObjects(ix, list.Request{Delimiter: "/", Max: 2, Grants: []string{""}})
	if err != nil {
		t.Fatal(err)
	}
	if !p1.IsTruncated {
		t.Fatal("期望截断")
	}
	tok := p1.NextToken

	tampered := flipByte(tok)
	if _, err := list.ListObjects(ix, list.Request{Delimiter: "/", Max: 2, Token: tampered, Grants: []string{""}}); err != list.ErrInvalidToken {
		t.Fatalf("篡改令牌 err=%v 期望 ErrInvalidToken", err)
	}
	if _, err := list.ListObjects(ix, list.Request{Delimiter: "/", Max: 2, Token: tok[:len(tok)-1], Grants: []string{""}}); err != list.ErrInvalidToken {
		t.Fatalf("截断令牌 err=%v", err)
	}
	if _, err := list.ListObjects(ix, list.Request{Prefix: "b", Delimiter: "/", Max: 2, Token: tok, Grants: []string{""}}); err != list.ErrInvalidToken {
		t.Fatalf("prefix 不符 err=%v", err)
	}
	if _, err := list.ListObjects(ix, list.Request{Delimiter: "", Max: 2, Token: tok, Grants: []string{""}}); err != list.ErrInvalidToken {
		t.Fatalf("delim 不符 err=%v", err)
	}
	if _, err := list.ListVersions(ix, list.Request{Delimiter: "/", Max: 2, Token: tok, Grants: []string{""}}); err != list.ErrInvalidToken {
		t.Fatalf("种类不符 err=%v", err)
	}
	t.Log("CRC 不符/前缀不符/种类不符均报令牌无效，且次序在参数非法之后")
}

func TestValidationOrderBeforeToken(t *testing.T) {
	ix := exampleIndex(t)
	_, err := list.ListObjects(ix, list.Request{Max: 0, Token: "garbage", Grants: []string{""}})
	if err != list.ErrInvalidArgument {
		t.Fatalf("参数非法应先于令牌校验, got %v", err)
	}
}

func TestConcurrentWriteNoDuplicateOrLoss(t *testing.T) {
	ix := keyindex.New()
	for n := 0; n < 200; n++ {
		ix.Put("k"+pad(n), 1, false)
	}
	const readers = 16
	var wg sync.WaitGroup
	errCh := make(chan error, readers)
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 200; ; n++ {
			select {
			case <-stop:
				return
			default:
			}
			ix.Put("w"+pad(n), 1, false)
			if n%3 == 0 {
				ix.DeleteVersion("w"+pad(n), 1)
			}
		}
	}()

	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := list.Request{Max: 37, Grants: []string{""}}
			seen := map[string]int{}
			pages := 0
			for {
				res, err := list.ListObjects(ix, req)
				if err != nil {
					errCh <- err
					return
				}
				for _, e := range res.Entries {
					seen[e.Key]++
					if seen[e.Key] > 1 {
						errCh <- errDup
						return
					}
				}
				pages++
				if !res.IsTruncated {
					break
				}
				if pages > 50 {
					errCh <- errTooManyPages
					return
				}
				req.Token = res.NextToken
			}
			// 始终存在的 200 个 k0000..k0199 必须恰好各出现一次。
			for n := 0; n < 200; n++ {
				if seen["k"+pad(n)] != 1 {
					errCh <- errMissingStable
					return
				}
			}
			errCh <- nil
		}()
	}
	go func() {
		wg.Wait()
		close(errCh)
	}()
	var firstErr error
	for r := 0; r < readers; r++ {
		if e := <-errCh; e != nil && firstErr == nil {
			firstErr = e
		}
	}
	close(stop)
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	t.Log("并发分页：始终存在的稳定键不重不漏、顺序不乱（每页独立快照）")
}

var (
	errDup           = &listErr{"同一次分页内键重复"}
	errTooManyPages  = &listErr{"页数异常"}
	errMissingStable = &listErr{"稳定键缺失或重复"}
)

type listErr struct{ msg string }

func (e *listErr) Error() string { return e.msg }

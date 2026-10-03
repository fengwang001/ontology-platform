package list

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"ontology/keyindex"
)

func dataSet() []keyindex.KeyVersions {
	v := func(ids ...int64) []keyindex.Version {
		var out []keyindex.Version
		for _, id := range ids {
			out = append(out, keyindex.Version{ID: id})
		}
		return out
	}
	return []keyindex.KeyVersions{
		{Key: "a", Versions: v(1)},
		{Key: "b/1", Versions: v(1)},
		{Key: "b/2", Versions: v(1)},
		{Key: "b/3", Versions: v(1)},
		{Key: "c", Versions: v(1)},
		{Key: "d/x", Versions: v(1)},
	}
}

func keys(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		if e.IsPrefix {
			out[i] = "P:" + e.Key
		} else {
			out[i] = e.Key
		}
	}
	return out
}

func TestSpecExamples(t *testing.T) {
	idx := keyindex.New()
	idx.Load(dataSet())
	srv := New(idx)

	base := Request{Prefix: "", Delimiter: "/", Max: 3, Grants: []string{""}}
	resp, err := srv.ListObjects(base)
	if err != nil {
		t.Fatal(err)
	}
	if got := keys(resp.Entries); strings.Join(got, ",") != "a,P:b/,c" {
		t.Fatalf("page1 = %v", got)
	}
	if !resp.IsTruncated {
		t.Fatal("page1 should truncate")
	}

	base.Token = resp.NextToken
	resp2, err := srv.ListObjects(base)
	if err != nil {
		t.Fatal(err)
	}
	if got := keys(resp2.Entries); len(got) != 1 || got[0] != "P:d/" {
		t.Fatalf("page2 = %v", got)
	}
	if resp2.IsTruncated || resp2.NextToken != "" {
		t.Fatal("page2 should not truncate")
	}

	r := Request{Delimiter: "/", Max: 2, Grants: []string{""}}
	p1, _ := srv.ListObjects(r)
	if got := keys(p1.Entries); strings.Join(got, ",") != "a,P:b/" || !p1.IsTruncated {
		t.Fatalf("max2 page1 = %v trunc=%v", got, p1.IsTruncated)
	}
	r.Token = p1.NextToken
	p2, _ := srv.ListObjects(r)
	if got := keys(p2.Entries); strings.Join(got, ",") != "c,P:d/" {
		t.Fatalf("max2 page2 = %v", got)
	}
	if p2.IsTruncated {
		t.Fatal("max2 page2 should end")
	}

	p4, _ := srv.ListObjects(Request{Delimiter: "/", Max: 4, Grants: []string{""}})
	if got := keys(p4.Entries); len(got) != 4 || p4.IsTruncated {
		t.Fatalf("max4 = %v trunc=%v", got, p4.IsTruncated)
	}

	pb, _ := srv.ListObjects(Request{Prefix: "b", Delimiter: "/", Max: 10, Grants: []string{""}})
	if got := keys(pb.Entries); len(got) != 1 || got[0] != "P:b/" {
		t.Fatalf("prefix b = %v", got)
	}
	pbs, _ := srv.ListObjects(Request{Prefix: "b/", Delimiter: "/", Max: 10, Grants: []string{""}})
	if got := keys(pbs.Entries); strings.Join(got, ",") != "b/1,b/2,b/3" {
		t.Fatalf("prefix b/ = %v", got)
	}
}

func TestPrefixBareKeyAndTrailingDelimKey(t *testing.T) {
	idx := keyindex.New()
	idx.Load([]keyindex.KeyVersions{
		{Key: "b", Versions: []keyindex.Version{{ID: 1}}},
		{Key: "b/", Versions: []keyindex.Version{{ID: 1}}},
		{Key: "b/x", Versions: []keyindex.Version{{ID: 1}}},
	})
	srv := New(idx)

	resp, _ := srv.ListObjects(Request{Prefix: "b", Delimiter: "/", Max: 10, Grants: []string{""}})
	if got := keys(resp.Entries); strings.Join(got, ",") != "b,P:b/" {
		t.Fatalf("got %v", got)
	}
	resp, _ = srv.ListObjects(Request{Delimiter: "/", Max: 10, Grants: []string{""}})
	if got := keys(resp.Entries); strings.Join(got, ",") != "b,P:b/" {
		t.Fatalf("got %v, want b,P:b/ (bare key b is an object)", got)
	}
}

func TestExactlyMaxAndPlusOne(t *testing.T) {
	idx := keyindex.New()
	idx.Load([]keyindex.KeyVersions{
		{Key: "k1", Versions: []keyindex.Version{{ID: 1}}},
		{Key: "k2", Versions: []keyindex.Version{{ID: 1}}},
		{Key: "k3", Versions: []keyindex.Version{{ID: 1}}},
	})
	srv := New(idx)

	exact, _ := srv.ListObjects(Request{Max: 3, Grants: []string{""}})
	if exact.IsTruncated || exact.NextToken != "" || len(exact.Entries) != 3 {
		t.Fatalf("exact max: trunc=%v token=%q n=%d", exact.IsTruncated, exact.NextToken, len(exact.Entries))
	}
	plus, _ := srv.ListObjects(Request{Max: 2, Grants: []string{""}})
	if !plus.IsTruncated || plus.NextToken == "" {
		t.Fatal("plus one must truncate with token")
	}
	if got := keys(plus.Entries); strings.Join(got, ",") != "k1,k2" {
		t.Fatalf("plus page = %v", got)
	}
}

func TestStartAfterWithinGroup(t *testing.T) {
	idx := keyindex.New()
	idx.Load(dataSet())
	srv := New(idx)
	resp, _ := srv.ListObjects(Request{Delimiter: "/", StartAfter: "b/2", Max: 10, Grants: []string{""}})
	if got := keys(resp.Entries); strings.Join(got, ",") != "P:b/,c,P:d/" {
		t.Fatalf("got %v", got)
	}
	resp, _ = srv.ListObjects(Request{Delimiter: "/", StartAfter: "b/3", Max: 10, Grants: []string{""}})
	if got := keys(resp.Entries); strings.Join(got, ",") != "c,P:d/" {
		t.Fatalf("got %v", got)
	}
}

func TestVersionsPaging(t *testing.T) {
	idx := keyindex.New()
	idx.Load([]keyindex.KeyVersions{
		{Key: "x", Versions: []keyindex.Version{
			{ID: 3}, {ID: 2, IsDelete: true}, {ID: 1},
		}},
	})
	srv := New(idx)

	p1, err := srv.ListVersions(Request{Max: 2, Grants: []string{""}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p1.Entries) != 2 || p1.Entries[0].Version != 3 || p1.Entries[1].Version != 2 ||
		!p1.Entries[1].IsDelete || !p1.IsTruncated {
		t.Fatalf("versions page1 = %+v", p1.Entries)
	}
	p2, err := srv.ListVersions(Request{Max: 2, Token: p1.NextToken, Grants: []string{""}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p2.Entries) != 1 || p2.Entries[0].Version != 1 || p2.IsTruncated {
		t.Fatalf("versions page2 = %+v trunc=%v", p2.Entries, p2.IsTruncated)
	}

	obj, _ := srv.ListObjects(Request{Max: 2, Grants: []string{""}})
	if len(obj.Entries) != 1 || obj.Entries[0].Key != "x" {
		t.Fatalf("objects = %+v", obj.Entries)
	}

	idx2 := keyindex.New()
	idx2.Load([]keyindex.KeyVersions{
		{Key: "x", Versions: []keyindex.Version{{ID: 9, IsDelete: true}, {ID: 1}}},
	})
	o2, _ := New(idx2).ListObjects(Request{Max: 2, Grants: []string{""}})
	if len(o2.Entries) != 0 || o2.IsTruncated {
		t.Fatalf("delete-marker current must hide: %+v", o2.Entries)
	}
	v2, _ := New(idx2).ListVersions(Request{Max: 10, Grants: []string{""}})
	if len(v2.Entries) != 2 {
		t.Fatalf("versions still listed: %+v", v2.Entries)
	}
}

func TestGrantFilteringAndHideWholeGroup(t *testing.T) {
	idx := keyindex.New()
	idx.Load(dataSet())
	srv := New(idx)

	resp, _ := srv.ListObjects(Request{Delimiter: "/", Max: 10, Grants: []string{"b/", "d/"}})
	if got := keys(resp.Entries); strings.Join(got, ",") != "P:b/,P:d/" {
		t.Fatalf("grants b/,d/ => %v", got)
	}
	resp, _ = srv.ListObjects(Request{Delimiter: "/", Max: 10, Grants: []string{"b/2"}})
	if got := keys(resp.Entries); len(got) != 1 || got[0] != "P:b/" {
		t.Fatalf("grant b/2 => %v", got)
	}
	resp, _ = srv.ListObjects(Request{Delimiter: "/", Max: 10, Grants: []string{"zzz"}})
	if len(resp.Entries) != 0 || resp.IsTruncated {
		t.Fatalf("no visibility leaked: %v", resp.Entries)
	}
	resp, _ = srv.ListObjects(Request{Max: 10})
	if len(resp.Entries) != 0 || resp.IsTruncated {
		t.Fatalf("empty grants => nothing: %v", resp.Entries)
	}
	p, _ := srv.ListObjects(Request{Delimiter: "/", Max: 1, Grants: []string{"b/", "c"}})
	if got := keys(p.Entries); len(got) != 1 || got[0] != "P:b/" || !p.IsTruncated {
		t.Fatalf("one slot group = %v trunc=%v", got, p.IsTruncated)
	}
}

func TestInvalidArguments(t *testing.T) {
	idx := keyindex.New()
	idx.Load(dataSet())
	srv := New(idx)

	bad := []Request{
		{Max: 0, Grants: []string{""}},
		{Max: 1001, Grants: []string{""}},
		{Max: -1, Grants: []string{""}},
		{Max: 3, Delimiter: "ab", Grants: []string{""}},
		{Max: 3, StartAfter: "a", Token: "x"},
	}
	for i, r := range bad {
		if _, err := srv.ListObjects(r); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("case %d: err=%v", i, err)
		}
	}
}

func TestTokenTamperAndMismatch(t *testing.T) {
	idx := keyindex.New()
	idx.Load(dataSet())
	srv := New(idx)

	p1, _ := srv.ListObjects(Request{Delimiter: "/", Max: 2, Grants: []string{""}})
	tok := p1.NextToken

	raw, _ := base64.RawURLEncoding.DecodeString(tok)
	raw[0] ^= 0xFF
	bad := base64.RawURLEncoding.EncodeToString(raw)
	if _, err := srv.ListObjects(Request{Delimiter: "/", Max: 2, Token: bad, Grants: []string{""}}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("tamper err=%v", err)
	}
	if _, err := srv.ListObjects(Request{Delimiter: "/", Max: 2, Token: "!!!", Grants: []string{""}}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("garbage err=%v", err)
	}
	if _, err := srv.ListObjects(Request{Prefix: "x", Delimiter: "/", Max: 2, Token: tok, Grants: []string{""}}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("prefix mismatch err=%v", err)
	}
	if _, err := srv.ListObjects(Request{Delimiter: "-", Max: 2, Token: tok, Grants: []string{""}}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("delim mismatch err=%v", err)
	}
	if _, err := srv.ListVersions(Request{Delimiter: "/", Max: 2, Token: tok, Grants: []string{""}}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("kind mismatch err=%v", err)
	}
}

func buildGroupIndex(t *testing.T, groups, perGroup int) (*keyindex.Index, []keyindex.KeyVersions) {
	t.Helper()
	idx := keyindex.New()
	var data []keyindex.KeyVersions
	for g := 0; g < groups; g++ {
		prefix := fmt.Sprintf("g%03d/", g)
		for j := 0; j < perGroup; j++ {
			k := fmt.Sprintf("%skey%05d", prefix, j)
			data = append(data, keyindex.KeyVersions{Key: k, Versions: []keyindex.Version{{ID: 1}}})
		}
	}
	idx.Load(data)
	return idx, data
}

func TestSeekStableAcrossGroupSizes(t *testing.T) {
	measureAllPages := func(perGroup int) int64 {
		idx, _ := buildGroupIndex(t, 100, perGroup)
		srv := New(idx)
		idx.ResetSeeks()
		req := Request{Delimiter: "/", Max: 10, Grants: []string{""}}
		for {
			resp, err := srv.ListObjects(req)
			if err != nil {
				t.Fatal(err)
			}
			if !resp.IsTruncated {
				break
			}
			req.Token = resp.NextToken
		}
		return idx.Seeks()
	}
	s10 := measureAllPages(10)
	s10000 := measureAllPages(10000)
	t.Logf("seeks 100groups x10=%d  x10000=%d", s10, s10000)
	if s10 != s10000 {
		t.Fatalf("seeks differ: %d vs %d", s10, s10000)
	}

	// 单页（100 个组恰好占满 Max=100）+ 多看一个：两档同样相等。
	measureOnePage := func(perGroup int) int64 {
		idx, _ := buildGroupIndex(t, 100, perGroup)
		srv := New(idx)
		idx.ResetSeeks()
		req := Request{Delimiter: "/", Max: 100, Grants: []string{""}}
		resp, err := srv.ListObjects(req)
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Entries) != 100 || resp.IsTruncated {
			t.Fatalf("entries=%d trunc=%v", len(resp.Entries), resp.IsTruncated)
		}
		return idx.Seeks()
	}
	if o1, o2 := measureOnePage(10), measureOnePage(10000); o1 != o2 {
		t.Fatalf("one-page seeks differ: %d vs %d", o1, o2)
	} else {
		t.Logf("one-page seeks=%d (both sizes)", o1)
	}
}

func TestSeekBound(t *testing.T) {
	idx, data := buildGroupIndex(t, 50, 20)
	srv := New(idx)
	grants := []string{"g010/", "g02", "g040/zzz"}
	req := Request{Delimiter: "/", Prefix: "", Max: 7, Grants: grants}

	idx.ResetSeeks()
	resp, err := srv.ListObjects(req)
	if err != nil {
		t.Fatal(err)
	}
	seeks := idx.Seeks()

	// 朴素计算判定依据：条目数、归一化授权前缀数、被跳过的删除标记键数。
	want := naiveScan(data, Request{Kind: Objects, Delimiter: "/", Max: 7, Grants: grants})
	_ = want
	normCount := len(authzNormalize(grants))
	// 删除标记键（本数据集中当前版本为标记的）数为 0。
	markers := 0
	bound := int64(2*(len(resp.Entries)+normCount+markers) + 2)
	t.Logf("entries=%d grants=%d markers=%d seeks=%d bound=%d => %v",
		len(resp.Entries), normCount, markers, seeks, bound, seeks <= bound)
	if seeks > bound {
		t.Fatalf("seeks %d exceed bound %d", seeks, bound)
	}
}

func TestConcurrentWritesNoDupNoLoss(t *testing.T) {
	idx2 := keyindex.New()
	var seed []keyindex.KeyVersions
	for k := 0; k < 20; k++ {
		seed = append(seed, keyindex.KeyVersions{
			Key: fmt.Sprintf("stable/%03d", k), Versions: []keyindex.Version{{ID: 1}},
		})
	}
	idx2.Load(seed)
	srv := New(idx2)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
				idx2.Put(fmt.Sprintf("volatile/%05d", i), keyindex.Version{ID: 1})
				i++
			}
		}
	}()

	seen := map[string]int{}
	var total int
	var pages int
	req := Request{Prefix: "stable/", Max: 3, Grants: []string{""}}
	for pages < 50 {
		resp, err := srv.ListObjects(req)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range resp.Entries {
			seen[e.Key]++
			total++
		}
		pages++
		if !resp.IsTruncated {
			break
		}
		req.Token = resp.NextToken
	}
	close(stop)
	wg.Wait()

	if total != 20 {
		t.Fatalf("stable entries seen %d, want 20 (dup/loss)", total)
	}
	for k, c := range seen {
		if c != 1 {
			t.Fatalf("%s seen %d times", k, c)
		}
	}

	// 同一静态快照重放：相同输入完全相同。
	first := mustReplay(t, srv, Request{Delimiter: "/", Max: 3, Grants: []string{""}})
	for i := 0; i < 3; i++ {
		if got := mustReplay(t, srv, Request{Delimiter: "/", Max: 3, Grants: []string{""}}); got != first {
			t.Fatalf("replay %d differs:\n%s\nvs\n%s", i, got, first)
		}
	}
}

func mustReplay(t *testing.T, srv *Server, req Request) string {
	t.Helper()
	var b strings.Builder
	for {
		resp, err := srv.ListObjects(req)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range resp.Entries {
			fmt.Fprintf(&b, "%s|", keys([]Entry{e})[0])
		}
		if !resp.IsTruncated {
			break
		}
		req.Token = resp.NextToken
	}
	return b.String()
}

// ---- 1500 组随机数据与请求，对照逐键朴素扫描 ----

const randomCases = 1500

func randomData(r *rand.Rand) []keyindex.KeyVersions {
	alphabet := []string{"a", "b", "/", "\xff", "c", "/", "\x00"}
	n := 1 + r.Intn(40)
	seen := map[string]bool{}
	var data []keyindex.KeyVersions
	for i := 0; i < n; i++ {
		l := r.Intn(6)
		var sb strings.Builder
		for j := 0; j < l; j++ {
			sb.WriteString(alphabet[r.Intn(len(alphabet))])
		}
		k := sb.String()
		if k == "" || seen[k] {
			k = fmt.Sprintf("uniq%04d", i)
		}
		seen[k] = true
		nv := 1 + r.Intn(4)
		ids := r.Perm(nv + 2)
		var vs []keyindex.Version
		for _, id := range ids[:nv] {
			vs = append(vs, keyindex.Version{ID: int64(id + 1), IsDelete: r.Intn(3) == 0})
		}
		data = append(data, keyindex.KeyVersions{Key: k, Versions: vs})
	}
	return data
}

func randomRequest(r *rand.Rand) Request {
	prefixes := []string{"", "a", "b", "b/", "\xff", "a\xff", "c/"}
	delims := []string{"", "/", "\xff", "b"}
	starts := []string{"", "a", "b/1", "\xff", "a\xff", "c"}
	grantSets := [][]string{
		{""},
		{"b/"},
		{"b/", "c/"},
		{"a", "c/"},
		{},
		{"zzz"},
		{"b/2"},
		{"a\xff"},
	}
	req := Request{
		Kind:       []Kind{Objects, Versions}[r.Intn(2)],
		Prefix:     prefixes[r.Intn(len(prefixes))],
		Delimiter:  delims[r.Intn(len(delims))],
		StartAfter: starts[r.Intn(len(starts))],
		Max:        1 + r.Intn(6),
		Grants:     grantSets[r.Intn(len(grantSets))],
	}
	return req
}

func TestRandomAgainstNaive(t *testing.T) {
	r := rand.New(rand.NewSource(20261003))
	for n := 0; n < randomCases; n++ {
		data := randomData(r)
		req := randomRequest(r)

		idx := keyindex.New()
		idx.Load(data)
		srv := New(idx)

		got, truncPages, err := paginate(srv, req)
		if err != nil {
			t.Fatalf("case %d error: %v\ninput=%+v", n, err, req)
		}
		want := naiveScan(data, req)

		if n == 31 {
			t.Logf("CASE31 data=%+v", data)
		}
		reason := fmt.Sprintf(
			"case=%d req={Kind:%d Prefix:%q Delim:%q Start:%q Max:%d Grants:%v}",
			n, req.Kind, req.Prefix, req.Delimiter, req.StartAfter, req.Max, req.Grants)
		if len(got) != len(want) {
			t.Fatalf("%s\ncount got=%d want=%d\ngot=%v\nwant=%v", reason, len(got), len(want), got, want)
		}
		for i := range want {
			if !got[i].same(Entry{IsPrefix: want[i].isPrefix, Key: want[i].key,
				Version: want[i].version, IsDelete: want[i].isDelete}) {
				t.Fatalf("%s\nmismatch at %d got=%+v want=%+v", reason, i, got[i], want[i])
			}
		}
		// 截断标记判定依据：只有最后一页为 false；条目数>0 时页数符合 Max。
		for i, tr := range truncPages[:len(truncPages)-1] {
			if !tr {
				t.Fatalf("%s\npage %d not truncated but more pages followed", reason, i)
			}
		}
		if truncPages[len(truncPages)-1] {
			t.Fatalf("%s\nlast page marked truncated", reason)
		}
		expectedPages := 0
		if len(want) > 0 {
			expectedPages = (len(want) + req.Max - 1) / req.Max
		}
		if expectedPages == 0 {
			expectedPages = 1
		}
		if len(truncPages) != expectedPages {
			t.Fatalf("%s\npages=%d want=%d", reason, len(truncPages), expectedPages)
		}
		if n < 8 || n%200 == 0 {
			t.Logf("INPUT %s\nOUTPUT entries=%d pages=%d lastTrunc=%v JUDGE=match-naive",
				reason, len(got), len(truncPages), truncPages[len(truncPages)-1])
		}

		// 单页全量（Max=1000）复核 seek 上界：
		// seeks <= 2*(返回条目数 + 归一化授权前缀数 + 被跳过的删除标记键数) + 2。
		fullReq := req
		fullReq.Max = 1000
		fullReq.Token = ""
		idx.ResetSeeks()
		full, ferr := (func() (Response, error) {
			if fullReq.Kind == Versions {
				return srv.ListVersions(fullReq)
			}
			return srv.ListObjects(fullReq)
		})()
		if ferr != nil {
			t.Fatalf("%s\nfull page err=%v", reason, ferr)
		}
		gcount := len(authzNormalize(fullReq.Grants))
		markers := 0
		if fullReq.Kind == Objects {
			ng := visibleGrants(authzNormalize(fullReq.Grants))
			for _, kv := range naiveSorted(data) {
				if kv.Key <= fullReq.StartAfter || !strings.HasPrefix(kv.Key, fullReq.Prefix) ||
					!ng.visible(kv.Key) {
					continue
				}
				rest := kv.Key[len(fullReq.Prefix):]
				if fullReq.Delimiter != "" && strings.Contains(rest, fullReq.Delimiter) {
					continue
				}
				if len(kv.Versions) > 0 && kv.Versions[0].IsDelete {
					markers++
				}
			}
		}
		seeks := idx.Seeks()
		bound := int64(2*(len(full.Entries)+gcount+markers) + 2)
		if seeks > bound {
			t.Fatalf("%s\nseeks=%d exceed bound=%d (entries=%d grants=%d markers=%d)",
				reason, seeks, bound, len(full.Entries), gcount, markers)
		}
	}
}

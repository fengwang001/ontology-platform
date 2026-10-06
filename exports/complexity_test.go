package exports

import (
	"fmt"
	"testing"
)

// 构造含 n 个通配键（均不命中 "./target/path"）加 1 个命中键的表。
func benchTable(n int) *Table {
	entries := make([]Entry, 0, n+2)
	entries = append(entries, Entry{Key: "./target/*", Target: StringTarget("./out/*")})
	for i := 0; i < n; i++ {
		entries = append(entries, Entry{
			Key:    fmt.Sprintf("./nomatch/%d/*", i),
			Target: StringTarget("./x/*"),
		})
	}
	tbl, err := NewTable(entries)
	if err != nil {
		panic(err)
	}
	return tbl
}

// 单次解析的哈希探测次数只取决于请求长度，与键总数无关。
func TestLookupProbesIndependentOfTableSize(t *testing.T) {
	const subpath = "./target/path"
	small := benchTable(10)
	large := benchTable(20000)

	_, _, probesSmall, okSmall := small.idx.lookup(subpath)
	_, _, probesLarge, okLarge := large.idx.lookup(subpath)
	if !okSmall || !okLarge {
		t.Fatal("both tables must resolve the subpath")
	}
	if probesSmall != probesLarge {
		t.Fatalf("probe count must not depend on table size: small=%d large=%d",
			probesSmall, probesLarge)
	}
	// 上界：1 次精确探测 + n(n+1)/2 次通配探测（命中提前结束只会更少）。
	n := len(subpath)
	if bound := 1 + n*(n+1)/2; probesSmall > bound {
		t.Fatalf("probes %d exceed bound %d", probesSmall, bound)
	}
	t.Logf("subpath=%q len=%d probes=%d bound=%d (table sizes 12 vs 20002 keys)",
		subpath, n, probesSmall, 1+n*(n+1)/2)
}

// 未命中时探测次数达到上界，但仍只与请求长度有关。
func TestLookupProbesMissBound(t *testing.T) {
	tbl := benchTable(20000)
	const subpath = "./zzz"
	_, _, probes, ok := tbl.idx.lookup(subpath)
	if ok {
		t.Fatal("subpath must not match")
	}
	n := len(subpath)
	if want := 1 + n*(n+1)/2; probes != want {
		t.Fatalf("miss probes=%d, want exactly %d", probes, want)
	}
	t.Logf("subpath=%q len=%d miss probes=%d", subpath, n, probes)
}

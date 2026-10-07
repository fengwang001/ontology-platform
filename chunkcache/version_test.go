package chunkcache

import (
	"bytes"
	"context"
	"errors"

	"testing"
)

func TestVersionChangeInvalidatesAndRejudges(t *testing.T) {
	v1 := mkData(8, 1)  // 2 片
	v2 := mkData(12, 5) // 3 片，长度也变化
	origin := newMockOrigin(4, map[string][]byte{"v1": v1, "v2": v2}, "v1")
	cache := newTestCache(t, 4, 8, origin, false)
	ctx := context.Background()

	// 冷读 v1 片0（discovery 即片0），缓存 v1 片0。
	res, err := cache.Get(ctx, "obj", mustRange(0, 3))
	if err != nil || !bytes.Equal(res.Data, v1[0:4]) {
		t.Fatalf("v1 read: err=%v", err)
	}

	// 源站切换到 v2（更长）。请求 [8,12)：按缓存中 v1 长度 8 解析 start==length
	// 本应不可满足，但规格的“重判”由回源驱动——这里改用先在已知长度下
	// 请求覆盖缺失片：读片2（新片），回源携带期望 v1 得到 v2。
	origin.setCurrent("v2")
	// 请求跨三片：片0 命中 v1，片1、2 缺失，回源 [1,2] 携带期望 v1 得到 v2
	// => 作废 v1 片0，meta 切 v2，重判后片0 也需按 v2 重新回源。
	res2, err := cache.Get(ctx, "obj", mustRange(0, 11))
	if err != nil {
		t.Fatalf("v2 read: %v", err)
	}
	if res2.Version != "v2" {
		t.Fatalf("version=%q", res2.Version)
	}
	if !bytes.Equal(res2.Data, v2[0:12]) {
		t.Fatalf("v2 data=%v", res2.Data)
	}
	// 旧版本切片必须全部作废：后续读取任何字节都应来自 v2。
	for _, rep := range res2.Chunks {
		if rep.Version != "v2" {
			t.Fatalf("stale report %+v", rep)
		}
	}

	// 再读一次应全部命中（v2 已缓存），无回源。
	before := origin.callCountNow()
	res3, err := cache.Get(ctx, "obj", mustRange(0, 11))
	if err != nil {
		t.Fatal(err)
	}
	if origin.callCountNow() != before {
		t.Fatalf("expected pure cache hit, calls delta=%d", origin.callCountNow()-before)
	}
	if !bytes.Equal(res3.Data, v2[0:12]) {
		t.Fatalf("hit data")
	}
	for _, rep := range res3.Chunks {
		if rep.Source != SourceCache {
			t.Fatalf("after invalidate chunk %d src=%v", rep.Index, rep.Source)
		}
	}
}

func TestVersionChangeRangeRejudgeUnsatisfiable(t *testing.T) {
	v1 := mkData(12, 1)
	v2 := mkData(4, 9) // 新版本更短
	origin := newMockOrigin(4, map[string][]byte{"v1": v1, "v2": v2}, "v1")
	cache := newTestCache(t, 4, 8, origin, false)
	ctx := context.Background()

	// 仅预热片0（含 discovery），片2 仍缺失。
	if _, err := cache.Get(ctx, "obj", mustRange(0, 3)); err != nil {
		t.Fatal(err)
	}
	origin.setCurrent("v2")
	// 起点 8 在 v1(长12) 可满足但片2缺失 => 回源得到 v2(长4) => 作废重判：
	// start=8>=4，报范围不可满足。
	_, err := cache.Get(ctx, "obj", mustRange(8, 11))
	if !errors.Is(err, ErrRangeNotSatisfiable) {
		t.Fatalf("want range after rejudge, got %v", err)
	}
}

// scriptedOrigin 按调用次序依次返回给定版本，用于精确制造版本震荡。
type scriptedOrigin struct {
	s        int64
	versions map[string][]byte
	script   []string
	n        int
}

func (o *scriptedOrigin) Fetch(ctx context.Context, req FetchRequest) (FetchResult, error) {
	ver := o.script[o.n%len(o.script)]
	o.n++
	data := o.versions[ver]
	var chunks [][]byte
	for i := req.First; i <= req.Last; i++ {
		lo := int64(i) * o.s
		hi := lo + o.s
		if hi > int64(len(data)) {
			hi = int64(len(data))
		}
		chunks = append(chunks, append([]byte(nil), data[lo:hi]...))
	}
	return FetchResult{Version: ver, Length: int64(len(data)), Chunks: chunks}, nil
}

func TestVersionOscillation(t *testing.T) {
	versions := map[string][]byte{"a": mkData(8, 1), "b": mkData(8, 4)}
	// prime：在 a 版本缓存片1（discovery 仅学长度，数据回源片1）。
	primeOrigin := newMockOrigin(4, versions, "a")
	cache := newTestCache(t, 4, 8, primeOrigin, false)
	ctx := context.Background()

	if _, err := cache.Get(ctx, "obj", mustRange(4, 7)); err != nil {
		t.Fatalf("prime: %v", err)
	}
	// 换成脚本源站，与缓存共享对象键：下一次请求片0缺失回源得 b（a->b，重判），
	// 重判时旧片1作废、片0/1 重新缺失，回源得 a（b->a，第二次变化）=> 震荡。
	o := &scriptedOrigin{s: 4, versions: versions, script: []string{"b", "a"}}
	cache.origin = o

	_, err := cache.Get(ctx, "obj", mustRange(0, 7))
	if !errors.Is(err, ErrVersionOscillation) {
		t.Fatalf("want oscillation, got %v", err)
	}
}

func TestSuffixAndFromRanges(t *testing.T) {
	data := mkData(10, 2) // S=4: 3 片，末片 2 字节
	origin := newMockOrigin(4, map[string][]byte{"v1": data}, "v1")
	cache := newTestCache(t, 4, 8, origin, false)
	ctx := context.Background()

	// suffix 3：字节 [7,10)，跨片 1、2。
	res, err := cache.Get(ctx, "obj", mustSuffix(3))
	if err != nil || !bytes.Equal(res.Data, data[7:10]) {
		t.Fatalf("suffix3: %v %v", err, res != nil)
	}
	// from 9：仅末片最后一个字节。
	res2, err := cache.Get(ctx, "obj", mustFrom(9))
	if err != nil || !bytes.Equal(res2.Data, data[9:10]) {
		t.Fatalf("from9: %v", err)
	}
	// suffix 超大被夹到起点 0。
	res3, err := cache.Get(ctx, "obj", mustSuffix(1000))
	if err != nil || !bytes.Equal(res3.Data, data) {
		t.Fatalf("suffix huge: %v", err)
	}
}

func TestLRUEvictionPinsRespected(t *testing.T) {
	data := mkData(16, 3) // 4 片
	origin := newMockOrigin(4, map[string][]byte{"v1": data}, "v1")
	cache := newTestCache(t, 4, 2, origin, false) // C=2
	ctx := context.Background()

	// 读取片0、片1 填满；再读片0（刷新为最近），再读片2 => 淘汰片1。
	if _, err := cache.Get(ctx, "obj", mustRange(0, 3)); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Get(ctx, "obj", mustRange(4, 7)); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Get(ctx, "obj", mustRange(0, 3)); err != nil {
		t.Fatal(err)
	}
	res, err := cache.Get(ctx, "obj", mustRange(8, 11))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(res.Data, data[8:12]) {
		t.Fatalf("evict read data")
	}
	// 现在缓存 {片0,片2}；读片1 需回源（已被淘汰）。
	before := origin.callCountNow()
	if _, err := cache.Get(ctx, "obj", mustRange(4, 7)); err != nil {
		t.Fatal(err)
	}
	if origin.callCountNow() == before {
		t.Fatalf("evicted chunk must be re-fetched")
	}
}

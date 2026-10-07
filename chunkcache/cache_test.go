package chunkcache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func mkData(n int, seed byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = seed + byte(i%7)
	}
	return b
}

func newTestCache(t *testing.T, s int64, c int, origin Origin, log bool) *Cache {
	t.Helper()
	var lg Logger = nopLogger{}
	if log {
		lg = printLogger{}
	}
	cache, err := New(Config{S: s, C: c, Origin: origin, Logger: lg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return cache
}

func TestBasicPartialHitAndCoalescing(t *testing.T) {
	// S=4，对象 12 字节（恰好 3 片），C=4。
	data := mkData(12, 1)
	origin := newMockOrigin(4, map[string][]byte{"v1": data}, "v1")
	cache := newTestCache(t, 4, 4, origin, false)
	ctx := context.Background()

	// 预热：读取第 1 片 [4,8)。
	r, _ := RangeStartEnd(4, 7)
	res, err := cache.Get(ctx, "obj", r)
	if err != nil {
		t.Fatalf("warm: %v", err)
	}
	if !bytes.Equal(res.Data, data[4:8]) {
		t.Fatalf("warm data mismatch")
	}
	if len(res.Chunks) != 1 || res.Chunks[0].Source != SourceOrigin {
		t.Fatalf("warm report=%+v", res.Chunks)
	}

	// 跨三片范围 [3,10)。chunk1 已缓存；discovery 不缓存数据，chunk0、chunk2
	// 均缺失但被命中片 chunk1 隔开 => 两次回源 [0,0] 与 [2,2]。
	r2, _ := RangeStartEnd(3, 9)
	res2, err := cache.Get(ctx, "obj", r2)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(res2.Data, data[3:10]) {
		t.Fatalf("data = %v want %v", res2.Data, data[3:10])
	}
	if len(res2.Chunks) != 3 {
		t.Fatalf("reports=%+v", res2.Chunks)
	}
	for _, rep := range res2.Chunks {
		want := SourceCache
		if rep.Index == 0 || rep.Index == 2 {
			want = SourceOrigin
		}
		if rep.Source != want {
			t.Fatalf("chunk %d source=%v want %v", rep.Index, rep.Source, want)
		}
	}
	if len(res2.OriginCalls) != 2 {
		t.Fatalf("origin calls = %+v want two runs [0],[2]", res2.OriginCalls)
	}

	// 相邻缺失合并：冷缓存请求整对象 [0,20)。discovery 仅学长度不缓存，
	// 因此数据阶段整个 [0,4] 连续缺失，合并为一次回源：共两次回源。
	data5 := make([]byte, 20)
	originCold := newMockOrigin(4, map[string][]byte{"v1": data5}, "v1")
	cacheCold := newTestCache(t, 4, 5, originCold, false)
	r3, _ := RangeFrom(0)
	res3, err := cacheCold.Get(ctx, "obj", r3)
	if err != nil {
		t.Fatalf("cold full: %v", err)
	}
	if !bytes.Equal(res3.Data, data5) {
		t.Fatalf("cold full data")
	}
	if len(res3.OriginCalls) != 2 || res3.OriginCalls[1].First != 0 || res3.OriginCalls[1].Last != 4 {
		t.Fatalf("cold full calls=%+v", res3.OriginCalls)
	}
}

func TestBoundaryOffByOne(t *testing.T) {
	data := mkData(8, 10)
	origin := newMockOrigin(4, map[string][]byte{"v1": data}, "v1")
	cache := newTestCache(t, 4, 2, origin, false)
	ctx := context.Background()

	// 恰落边界：[0,4) 只触片 0。
	r, _ := RangeStartEnd(0, 3)
	res, err := cache.Get(ctx, "obj", r)
	if err != nil || !bytes.Equal(res.Data, data[0:4]) {
		t.Fatalf("boundary [0,4): %v %v", err, res != nil)
	}
	// 差一字节越界：[0,5) 触片 0,1。
	r2, _ := RangeStartEnd(0, 4)
	res2, err := cache.Get(ctx, "obj", r2)
	if err != nil || !bytes.Equal(res2.Data, data[0:5]) || len(res2.Chunks) != 2 {
		t.Fatalf("off-by-one [0,5): %v reports=%v", err, res2)
	}
	// 总长恰为整数倍：最后字节 index 7 落片 1。
	r3, _ := RangeStartEnd(7, 7)
	res3, err := cache.Get(ctx, "obj", r3)
	if err != nil || !bytes.Equal(res3.Data, data[7:8]) || res3.Chunks[0].Index != 1 {
		t.Fatalf("last byte: %v", err)
	}
}

func TestZeroLengthObject(t *testing.T) {
	origin := newMockOrigin(4, map[string][]byte{"v1": {}}, "v1")
	cache := newTestCache(t, 4, 1, origin, false)
	ctx := context.Background()

	// range from 0 on zero-length => start>=length。
	r, _ := RangeFrom(0)
	_, err := cache.Get(ctx, "obj", r)
	if !errors.Is(err, ErrRangeNotSatisfiable) || err.(*Error).Reason != ReasonStartAtOrAfterSize {
		t.Fatalf("zero from: %v", err)
	}
	// suffix 0 区分。
	r2, _ := RangeSuffix(0)
	_, err = cache.Get(ctx, "obj", r2)
	if !errors.Is(err, ErrRangeNotSatisfiable) || err.(*Error).Reason != ReasonZeroSuffix {
		t.Fatalf("zero suffix: %v", err)
	}
	// 非空后缀在零长对象上也不可满足（起点=0>=0 长度）。
	r3, _ := RangeSuffix(5)
	_, err = cache.Get(ctx, "obj", r3)
	if !errors.Is(err, ErrRangeNotSatisfiable) {
		t.Fatalf("suffix 5 on empty: %v", err)
	}
	// 容量/范围拒绝不改变缓存与访问顺序：零长对象的“长度”是通过 discovery
	// 学到的，拒绝后回滚，因此下一次请求须再次 discovery（每次拒绝恰好 1 次，
	// 且不会有任何数据回源）。三次拒绝 => 3 次 discovery。
	if got := origin.callCountNow(); got != 3 {
		t.Fatalf("origin calls=%d want 3 discoveries (one per rejected request)", got)
	}
}

func TestCapacityExactAndOffByOne(t *testing.T) {
	data := mkData(16, 1) // S=4 -> 4 chunks
	origin := newMockOrigin(4, map[string][]byte{"v1": data}, "v1")
	cache := newTestCache(t, 4, 3, origin, false) // C=3 < 4
	ctx := context.Background()

	// 空缓存请求整片跨度 4：超容量，拒绝且无回源（除 discovery 外不得再有请求）。
	r, _ := RangeStartEnd(0, 15)
	before := origin.callCountNow()
	_, err := cache.Get(ctx, "obj", r)
	if !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("want capacity, got %v", err)
	}
	after := origin.callCountNow()
	// discovery 为取得长度不可避免；容量拒绝不得再追加数据回源。
	if after-before != 1 {
		t.Fatalf("calls delta=%d want 1 (discovery only)", after-before)
	}

	// C=4 时恰好容纳，应成功。
	origin2 := newMockOrigin(4, map[string][]byte{"v1": data}, "v1")
	cache2 := newTestCache(t, 4, 4, origin2, false)
	res, err := cache2.Get(ctx, "obj", r)
	if err != nil {
		t.Fatalf("exact capacity: %v", err)
	}
	if !bytes.Equal(res.Data, data) {
		t.Fatalf("exact data")
	}
}

func TestErrorPriority(t *testing.T) {
	origin := newMockOrigin(4, map[string][]byte{"v1": mkData(12, 1)}, "v1")
	cache := newTestCache(t, 4, 2, origin, false) // C=2：单片可容，双片跨度超容
	ctx := context.Background()

	// 参数非法优先于一切：end<start 即使对象未知也不回源。
	before := origin.callCountNow()
	if _, err := RangeStartEnd(5, 2); err == nil {
		t.Fatal("constructor must reject end<start")
	}
	if origin.callCountNow() != before {
		t.Fatal("constructor error must not call origin")
	}

	// 已知长度对象上：范围不可满足优先于容量不足。
	// 先 discovery 一次建立长度。
	d, _ := RangeSuffix(1)
	if _, err := cache.Get(ctx, "obj", d); err != nil {
		t.Fatalf("discovery suffix: %v", err)
	}
	r, _ := RangeFrom(100)
	_, err := cache.Get(ctx, "obj", r)
	if !errors.Is(err, ErrRangeNotSatisfiable) {
		t.Fatalf("range before capacity: %v", err)
	}

	// 可满足但跨度 3 > C=2 => 容量不足（优先于回源失败）。
	r2, _ := RangeStartEnd(0, 11)
	_, err = cache.Get(ctx, "obj", r2)
	if !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("capacity: %v", err)
	}

}

func mustRange(start, end int64) ByteRange {
	r, err := RangeStartEnd(start, end)
	if err != nil {
		panic(err)
	}
	return r
}

func mustSuffix(n int64) ByteRange {
	r, err := RangeSuffix(n)
	if err != nil {
		panic(err)
	}
	return r
}

func mustFrom(start int64) ByteRange {
	r, err := RangeFrom(start)
	if err != nil {
		panic(err)
	}
	return r
}

func TestFailureNotCached(t *testing.T) {
	data := mkData(8, 1)
	origin := newMockOrigin(4, map[string][]byte{"v1": data}, "v1")
	origin.failEvery = 2 // 第二次回源失败
	cache := newTestCache(t, 4, 4, origin, false)
	ctx := context.Background()

	// discovery 成功（第1次），补缺 [1,1] 是第2次 => 失败。
	r, _ := RangeStartEnd(0, 7)
	_, err := cache.Get(ctx, "obj", r)
	if !errors.Is(err, ErrBackend) {
		t.Fatalf("want backend, got %v", err)
	}
	// 之后重新请求整片：失败切片必须再次回源（未被缓存）。
	origin.failEvery = 0
	res, err := cache.Get(ctx, "obj", r)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !bytes.Equal(res.Data, data) {
		t.Fatalf("retry data")
	}
}

func TestLoggingEmitsSteps(t *testing.T) {
	var sb strings.Builder
	data := mkData(8, 1)
	origin := newMockOrigin(4, map[string][]byte{"v1": data}, "v1")
	cache, _ := New(Config{S: 4, C: 4, Origin: origin, Logger: writerLogger{&sb}})
	r, _ := RangeStartEnd(0, 7)
	if _, err := cache.Get(context.Background(), "obj", r); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[origin]", "[ok]"} {
		if !strings.Contains(sb.String(), want) {
			t.Fatalf("log missing %q:\n%s", want, sb.String())
		}
	}
}

type writerLogger struct{ b *strings.Builder }

func (w writerLogger) Logf(format string, args ...any) {
	fmt.Fprintf(w.b, format+"\n", args...)
}

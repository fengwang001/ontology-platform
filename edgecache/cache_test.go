package edgecache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func newTestCache(t *testing.T, s int64, cap int, src Source) *Cache {
	t.Helper()
	c, err := New(Config{ChunkSize: s, Capacity: cap, Source: src})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func mustGet(t *testing.T, c *Cache, key string, r RangeSpec) *Response {
	t.Helper()
	resp, err := c.Get(context.Background(), key, r)
	if err != nil {
		t.Fatalf("Get(%s, %+v): %v", key, r, err)
	}
	return resp
}

func lruOrder(c *Cache) []chunkKey {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []chunkKey
	for e := c.lru.Front(); e != nil; e = e.Next() {
		out = append(out, e.Value.(chunkKey))
	}
	return out
}

func chunkPattern(resp *Response) []bool {
	out := make([]bool, len(resp.Chunks))
	for i, ch := range resp.Chunks {
		out[i] = ch.FromCache
	}
	return out
}

func TestNewConfigValidation(t *testing.T) {
	src := newFakeSource(4)
	if _, err := New(Config{ChunkSize: 0, Capacity: 1, Source: src}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("chunk size 0: %v", err)
	}
	if _, err := New(Config{ChunkSize: 4, Capacity: -1, Source: src}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("capacity -1: %v", err)
	}
	if _, err := New(Config{ChunkSize: 4, Capacity: 1, Source: nil}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil source: %v", err)
	}
}

// 范围恰落在切片边界与差一字节。
func TestRangeBoundaries(t *testing.T) {
	src := newFakeSource(4)
	src.set("k", "v1", []byte("0123456789AB")) // 12 字节，3 个切片
	c := newTestCache(t, 4, 16, src)

	cases := []struct {
		name   string
		r      RangeSpec
		want   string
		start  int64
		chunks []int64
	}{
		{"整切片0", Bytes(0, 3), "0123", 0, []int64{0}},
		{"整切片1", Bytes(4, 7), "4567", 4, []int64{1}},
		{"边界差一字节-左", Bytes(3, 4), "34", 3, []int64{0, 1}},
		{"边界差一字节-跨", Bytes(0, 4), "01234", 0, []int64{0, 1}},
		{"末字节", Bytes(11, 11), "B", 11, []int64{2}},
		{"终点超出截断", Bytes(8, 100), "89AB", 8, []int64{2}},
		{"起点到末尾", From(11), "B", 11, []int64{2}},
		{"后缀整切片", LastN(4), "89AB", 8, []int64{2}},
		{"后缀差一字节", LastN(5), "789AB", 7, []int64{1, 2}},
		{"后缀等于全长", LastN(12), "0123456789AB", 0, []int64{0, 1, 2}},
		{"后缀超出全长", LastN(13), "0123456789AB", 0, []int64{0, 1, 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := mustGet(t, c, "k", tc.r)
			if string(resp.Data) != tc.want {
				t.Fatalf("data = %q, want %q", resp.Data, tc.want)
			}
			if resp.Start != tc.start || resp.Total != 12 || resp.Version != "v1" {
				t.Fatalf("meta = %+v, want start %d", resp, tc.start)
			}
			if len(resp.Chunks) != len(tc.chunks) {
				t.Fatalf("chunks = %+v, want indices %v", resp.Chunks, tc.chunks)
			}
			for i, idx := range tc.chunks {
				if resp.Chunks[i].Index != idx {
					t.Fatalf("chunks = %+v, want indices %v", resp.Chunks, tc.chunks)
				}
			}
			// 再取一次应全部命中缓存。
			resp2 := mustGet(t, c, "k", tc.r)
			for _, ch := range resp2.Chunks {
				if !ch.FromCache {
					t.Fatalf("second get not from cache: %+v", resp2.Chunks)
				}
			}
		})
	}
}

// 跨多个切片的部分命中：被命中切片隔开的缺失段分别回源，命中切片不重复回源。
func TestPartialHitAndGapMerge(t *testing.T) {
	src := newFakeSource(4)
	src.set("k", "v1", []byte("0123456789ABCDEFGHIJ")) // 20 字节，5 个切片
	c := newTestCache(t, 4, 16, src)

	mustGet(t, c, "k", Bytes(4, 7))   // 缓存切片 1
	mustGet(t, c, "k", Bytes(12, 15)) // 缓存切片 3
	before := len(src.fetchRecords())

	resp := mustGet(t, c, "k", Bytes(0, 19))
	if string(resp.Data) != "0123456789ABCDEFGHIJ" {
		t.Fatalf("data = %q", resp.Data)
	}
	wantPattern := []bool{false, true, false, true, false}
	got := chunkPattern(resp)
	if fmt.Sprint(got) != fmt.Sprint(wantPattern) {
		t.Fatalf("from-cache pattern = %v, want %v", got, wantPattern)
	}
	recs := src.fetchRecords()[before:]
	wantRecs := []fetchRecord{
		{key: "k", first: 0, last: 0, expect: "v1"},
		{key: "k", first: 2, last: 2, expect: "v1"},
		{key: "k", first: 4, last: 4, expect: "v1"},
	}
	if fmt.Sprint(recs) != fmt.Sprint(wantRecs) {
		t.Fatalf("fetch records = %+v, want %+v", recs, wantRecs)
	}
}

// 相邻缺失段合并为一次回源。
func TestAdjacentMissesMerged(t *testing.T) {
	src := newFakeSource(4)
	src.set("k", "v1", []byte("0123456789ABCDEF")) // 16 字节
	c := newTestCache(t, 4, 16, src)

	mustGet(t, c, "k", Bytes(4, 7)) // 缓存切片 1，元信息就绪
	before := len(src.fetchRecords())
	resp := mustGet(t, c, "k", Bytes(0, 15))
	if string(resp.Data) != "0123456789ABCDEF" {
		t.Fatalf("data = %q", resp.Data)
	}
	recs := src.fetchRecords()[before:]
	wantRecs := []fetchRecord{
		{key: "k", first: 0, last: 0, expect: "v1"},
		{key: "k", first: 2, last: 3, expect: "v1"}, // 相邻缺失合并
	}
	if fmt.Sprint(recs) != fmt.Sprint(wantRecs) {
		t.Fatalf("fetch records = %+v, want %+v", recs, wantRecs)
	}
}

// 总长度恰为切片大小整数倍：不得产生幻影切片。
func TestTotalExactMultipleOfChunkSize(t *testing.T) {
	src := newFakeSource(4)
	src.set("k", "v1", []byte("01234567")) // 8 字节，恰 2 个切片
	c := newTestCache(t, 4, 16, src)

	resp := mustGet(t, c, "k", LastN(1))
	if string(resp.Data) != "7" || len(resp.Chunks) != 1 || resp.Chunks[0].Index != 1 {
		t.Fatalf("resp = %+v", resp)
	}
	resp = mustGet(t, c, "k", Bytes(4, 7))
	if string(resp.Data) != "4567" {
		t.Fatalf("data = %q", resp.Data)
	}
	if _, err := c.Get(context.Background(), "k", From(8)); !errors.Is(err, ErrRangeNotSatisfiable) {
		t.Fatalf("From(8) err = %v", err)
	}
	for _, rec := range src.fetchRecords() {
		if rec.last > 1 {
			t.Fatalf("fetched phantom chunk: %+v", rec)
		}
	}
}

// 总长度为零的对象。
func TestZeroLengthObject(t *testing.T) {
	src := newFakeSource(4)
	src.set("z", "v1", []byte{})
	c := newTestCache(t, 4, 16, src)

	resp := mustGet(t, c, "z", LastN(5))
	if len(resp.Data) != 0 || len(resp.Chunks) != 0 || resp.Total != 0 {
		t.Fatalf("resp = %+v", resp)
	}
	for _, r := range []RangeSpec{From(0), Bytes(0, 0)} {
		_, err := c.Get(context.Background(), "z", r)
		var re *RangeError
		if !errors.As(err, &re) || re.Reason != RangeStartBeyondTotal {
			t.Fatalf("Get(%+v) err = %v", r, err)
		}
	}
}

// 三种范围不可满足的原因必须可区分。
func TestRangeReasonsDistinguishable(t *testing.T) {
	src := newFakeSource(4)
	src.set("k", "v1", []byte("01234567"))
	c := newTestCache(t, 4, 16, src)
	mustGet(t, c, "k", Bytes(0, 0)) // 让元信息就绪

	cases := []struct {
		r      RangeSpec
		reason RangeReason
	}{
		{Bytes(8, 9), RangeStartBeyondTotal},
		{From(8), RangeStartBeyondTotal},
		{LastN(0), RangeSuffixZero},
		{Bytes(5, 2), RangeEndBeforeStart},
	}
	for _, tc := range cases {
		_, err := c.Get(context.Background(), "k", tc.r)
		if !errors.Is(err, ErrRangeNotSatisfiable) {
			t.Fatalf("Get(%+v) err = %v", tc.r, err)
		}
		var re *RangeError
		if !errors.As(err, &re) || re.Reason != tc.reason {
			t.Fatalf("Get(%+v) reason = %v, want %v", tc.r, re.Reason, tc.reason)
		}
	}
	// 参数非法优先于范围不可满足。
	if _, err := c.Get(context.Background(), "", Bytes(5, 2)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty key err = %v", err)
	}
	if _, err := c.Get(context.Background(), "k", Bytes(-1, -5)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("negative bounds err = %v", err)
	}
	if _, err := c.Get(context.Background(), "k", RangeSpec{Kind: RangeKind(99)}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad kind err = %v", err)
	}
}

// 版本变化：旧版本全部切片作废，请求按新版本重新判定并完成。
func TestVersionChangeInvalidatesAndRejudges(t *testing.T) {
	src := newFakeSource(4)
	src.set("k", "v1", []byte("AAAABBBBCCCC")) // 12 字节
	c := newTestCache(t, 4, 8, src)

	mustGet(t, c, "k", Bytes(0, 3))     // 缓存 v1 切片 0，元信息就绪
	src.set("k", "v2", []byte("DDDEE")) // 换版本并缩短到 5 字节

	resp := mustGet(t, c, "k", Bytes(0, 11))
	if string(resp.Data) != "DDDEE" || resp.Version != "v2" || resp.Total != 5 {
		t.Fatalf("resp = %+v", resp)
	}
	// 旧版本切片已全部作废，缓存里只剩新版本切片。
	c.mu.Lock()
	for ck := range c.chunks {
		if ck.key == "k" && ck.version != "v2" {
			c.mu.Unlock()
			t.Fatalf("stale chunk survived: %+v", ck)
		}
	}
	c.mu.Unlock()
	if st := c.Stats(); st.Chunks != 2 {
		t.Fatalf("stats = %+v, want 2 chunks", st)
	}
	// 新版本切片已记录，再次请求命中缓存。
	resp2 := mustGet(t, c, "k", Bytes(0, 3))
	if !resp2.Chunks[0].FromCache || resp2.Version != "v2" {
		t.Fatalf("resp2 = %+v", resp2)
	}
}

// 版本变化后按新总长度重新判定：范围不可满足。
func TestVersionRejudgeRangeUnsatisfiable(t *testing.T) {
	src := newFakeSource(4)
	src.set("k", "v1", []byte("AAAABBBBCCCC"))
	c := newTestCache(t, 4, 8, src)

	mustGet(t, c, "k", Bytes(0, 3))
	src.set("k", "v2", []byte("DDDD")) // 缩短到 4 字节

	_, err := c.Get(context.Background(), "k", From(8))
	var re *RangeError
	if !errors.As(err, &re) || re.Reason != RangeStartBeyondTotal {
		t.Fatalf("err = %v, want RangeStartBeyondTotal", err)
	}
}

// 一次请求内版本连续变化两次：版本震荡。
func TestVersionThrash(t *testing.T) {
	src := newFakeSource(4)
	src.set("k", "v1", []byte("AAAABBBB"))
	c := newTestCache(t, 4, 8, src)

	mustGet(t, c, "k", Bytes(0, 3)) // 元信息就绪（v1）
	src.armFlip("k",
		fakeObj{version: "v2", data: []byte("CCCCDDDD")},
		fakeObj{version: "v3", data: []byte("EEEEFFFF")},
	)
	_, err := c.Get(context.Background(), "k", Bytes(0, 7))
	if !errors.Is(err, ErrVersionThrash) {
		t.Fatalf("err = %v, want ErrVersionThrash", err)
	}
}

// 并发请求同一切片：只回源一次，结果一致。
func TestConcurrentSameChunkSingleFetch(t *testing.T) {
	src := newFakeSource(4)
	content := []byte("0123456789ABCDEF")
	src.set("k", "v1", content)
	src.gate = make(chan struct{})
	src.started = make(chan struct{}, 64)
	c := newTestCache(t, 4, 16, src)

	const workers = 8
	var wg sync.WaitGroup
	resps := make([]*Response, workers)
	errs := make([]error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resps[w], errs[w] = c.Get(context.Background(), "k", Bytes(0, 15))
		}()
	}
	<-src.started // 探取回源开始
	src.gate <- struct{}{}
	<-src.started // 缺失段回源开始
	src.gate <- struct{}{}
	wg.Wait()

	for w := 0; w < workers; w++ {
		if errs[w] != nil {
			t.Fatalf("worker %d err = %v", w, errs[w])
		}
		if !bytes.Equal(resps[w].Data, content) {
			t.Fatalf("worker %d data = %q", w, resps[w].Data)
		}
	}
	if got := src.callCount(); got != 2 {
		t.Fatalf("source calls = %d, want 2 (probe + one merged run)", got)
	}
	// 每个切片下标至多回源一次。
	seen := map[int64]int{}
	for _, rec := range src.fetchRecords() {
		for i := rec.first; i <= rec.last; i++ {
			seen[i]++
		}
	}
	for i, n := range seen {
		if n != 1 {
			t.Fatalf("chunk %d fetched %d times", i, n)
		}
	}
}

// 回源失败：所有等待者都报回源失败，失败不写缓存，不影响不相关请求。
func TestConcurrentFetchFailure(t *testing.T) {
	src := newFakeSource(4)
	src.set("k", "v1", []byte("AAAABBBB"))
	src.set("other", "v1", []byte("ZZZZ"))
	c := newTestCache(t, 4, 8, src)

	mustGet(t, c, "other", Bytes(0, 3)) // 不相关对象预先入缓存
	callsBefore := src.callCount()

	src.gate = make(chan struct{})
	src.started = make(chan struct{}, 64)
	src.failNext(100, errors.New("boom"))

	const workers = 5
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[w] = c.Get(context.Background(), "k", Bytes(0, 7))
		}()
	}
	close(start)
	<-src.started
	// 等其余 worker 都挂上同一个 singleflight 调用后再放行回源。
	time.Sleep(200 * time.Millisecond)
	src.gate <- struct{}{}
	wg.Wait()

	for w := 0; w < workers; w++ {
		if !errors.Is(errs[w], ErrSourceFailure) {
			t.Fatalf("worker %d err = %v, want ErrSourceFailure", w, errs[w])
		}
	}
	if got := src.callCount() - callsBefore; got != 1 {
		t.Fatalf("source calls = %d, want 1 (singleflight)", got)
	}
	if st := c.Stats(); st.Chunks != 1 { // 只剩 other 的切片
		t.Fatalf("stats = %+v, want 1 cached chunk", st)
	}
	c.mu.Lock()
	inflight := len(c.inflight)
	c.mu.Unlock()
	if inflight != 0 {
		t.Fatalf("inflight not cleaned: %d", inflight)
	}
	// 不相关请求不受影响。
	resp := mustGet(t, c, "other", Bytes(0, 3))
	if !resp.Chunks[0].FromCache {
		t.Fatalf("unrelated chunk lost: %+v", resp.Chunks)
	}
	// 失败不写入缓存：故障恢复后重试成功。
	src.failNext(0, nil)
	src.gate = nil
	resp = mustGet(t, c, "k", Bytes(0, 7))
	if string(resp.Data) != "AAAABBBB" {
		t.Fatalf("data = %q", resp.Data)
	}
}

// 容量恰等于所需切片数则成功，差一（所需 = C+1）则报容量不足，
// 且被拒绝的请求不得回源、不得改变缓存内容与访问顺序。
func TestCapacityExactAndOneShort(t *testing.T) {
	src := newFakeSource(4)
	src.set("k", "v1", []byte("0123456789ABCDEF"))
	c := newTestCache(t, 4, 2, src)

	mustGet(t, c, "k", Bytes(0, 7)) // 元信息就绪，缓存切片 0、1
	callsBefore := src.callCount()
	lruBefore := lruOrder(c)

	// 需要 3 个切片 > C=2：拒绝。
	_, err := c.Get(context.Background(), "k", Bytes(0, 8))
	if !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("err = %v, want ErrCapacityExceeded", err)
	}
	if got := src.callCount(); got != callsBefore {
		t.Fatalf("rejected request made %d source calls", got-callsBefore)
	}
	if fmt.Sprint(lruOrder(c)) != fmt.Sprint(lruBefore) {
		t.Fatalf("rejected request changed lru: %v -> %v", lruBefore, lruOrder(c))
	}
	if st := c.Stats(); st.Chunks != 2 {
		t.Fatalf("rejected request changed cache: %+v", st)
	}

	// 需要 2 个切片 = C：成功。
	resp := mustGet(t, c, "k", Bytes(0, 7))
	if got := chunkPattern(resp); fmt.Sprint(got) != fmt.Sprint([]bool{true, true}) {
		t.Fatalf("pattern = %v", got)
	}
}

// 元信息未知且容量不足时：探取回源学到总长度后，仍按容量不足拒绝。
func TestCapacityUnknownTotal(t *testing.T) {
	src := newFakeSource(4)
	src.set("k", "v1", []byte("0123456789ABCDEF"))
	c := newTestCache(t, 4, 1, src)

	_, err := c.Get(context.Background(), "k", Bytes(0, 8))
	if !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("err = %v, want ErrCapacityExceeded", err)
	}
}

// 错误优先级：范围不可满足 > 容量不足 > 回源失败 > 版本震荡。
func TestErrorPriority(t *testing.T) {
	// 范围不可满足优先于容量不足。
	src := newFakeSource(1)
	src.set("k", "v1", []byte("0123456789"))
	c := newTestCache(t, 1, 1, src)
	mustGet(t, c, "k", Bytes(0, 0)) // 元信息就绪
	if _, err := c.Get(context.Background(), "k", From(10)); !errors.Is(err, ErrRangeNotSatisfiable) {
		t.Fatalf("range>capacity: %v", err)
	}

	// 容量不足优先于回源失败（且不得回源）。
	src.failNext(10, errors.New("boom"))
	callsBefore := src.callCount()
	if _, err := c.Get(context.Background(), "k", Bytes(0, 5)); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("capacity>source: %v", err)
	}
	if got := src.callCount(); got != callsBefore {
		t.Fatalf("capacity rejection fetched %d times", got-callsBefore)
	}

	// 回源失败优先于版本震荡：一段回源换版本、另一段回源失败。
	src2 := newFakeSource(1)
	src2.set("k", "v1", []byte("abcd"))
	c2 := newTestCache(t, 1, 10, src2)
	mustGet(t, c2, "k", Bytes(1, 1)) // 缓存切片 1，元信息就绪
	src2.armFlip("k", fakeObj{version: "v2", data: []byte("wxyz")})
	src2.failNext(1, errors.New("boom")) // 第二次回源失败
	_, err := c2.Get(context.Background(), "k", Bytes(0, 3))
	if !errors.Is(err, ErrSourceFailure) {
		t.Fatalf("source>thrash: %v", err)
	}
}

// LRU：命中刷新访问顺序，超容淘汰最久未访问；被拒绝的请求不刷新。
func TestLRUEvictionAndHitRefresh(t *testing.T) {
	src := newFakeSource(4)
	src.set("k1", "v1", []byte("000011112222"))
	src.set("k2", "v1", []byte("2222"))
	src.set("k3", "v1", []byte("3333"))
	c := newTestCache(t, 4, 2, src)

	mustGet(t, c, "k1", Bytes(0, 3)) // lru: [k1:0]
	mustGet(t, c, "k1", Bytes(4, 7)) // lru: [k1:1, k1:0]
	resp := mustGet(t, c, "k1", Bytes(0, 3))
	if !resp.Chunks[0].FromCache {
		t.Fatalf("expected cache hit")
	}
	// 命中刷新后 k1:1 成为最久未访问。
	want := []chunkKey{{"k1", "v1", 0}, {"k1", "v1", 1}}
	if fmt.Sprint(lruOrder(c)) != fmt.Sprint(want) {
		t.Fatalf("lru = %v, want %v", lruOrder(c), want)
	}

	mustGet(t, c, "k2", Bytes(0, 3)) // 插入 k2:0，淘汰 k1:1
	want = []chunkKey{{"k2", "v1", 0}, {"k1", "v1", 0}}
	if fmt.Sprint(lruOrder(c)) != fmt.Sprint(want) {
		t.Fatalf("lru = %v, want %v", lruOrder(c), want)
	}

	// 被拒绝的请求（需 3 切片 > C=2）不刷新访问顺序。
	if _, err := c.Get(context.Background(), "k1", Bytes(0, 8)); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("err = %v", err)
	}
	if fmt.Sprint(lruOrder(c)) != fmt.Sprint(want) {
		t.Fatalf("rejected request changed lru: %v", lruOrder(c))
	}

	mustGet(t, c, "k3", Bytes(0, 3)) // 插入 k3:0，淘汰 k1:0
	want = []chunkKey{{"k3", "v1", 0}, {"k2", "v1", 0}}
	if fmt.Sprint(lruOrder(c)) != fmt.Sprint(want) {
		t.Fatalf("lru = %v, want %v", lruOrder(c), want)
	}

	// k1 的切片已被淘汰，重新请求须回源。
	resp = mustGet(t, c, "k1", Bytes(0, 3))
	if resp.Chunks[0].FromCache {
		t.Fatalf("expected cache miss after eviction")
	}
}

// 被未完成请求 pin 住的切片不得被淘汰。
func TestPinnedChunksNotEvicted(t *testing.T) {
	src := newFakeSource(4)
	src.set("k1", "v1", []byte("0000111122223333"))
	src.set("k2", "v1", []byte("aaaabbbb"))
	c := newTestCache(t, 4, 3, src)

	mustGet(t, c, "k1", Bytes(0, 7)) // 缓存 k1:0、k1:1

	// 预热完成后再装上闸门与信号通道。
	src.gates = map[string]chan struct{}{"k1": make(chan struct{})}
	src.started = make(chan struct{}, 64)

	done := make(chan struct{})
	var resp1 *Response
	var err1 error
	go func() {
		defer close(done)
		resp1, err1 = c.Get(context.Background(), "k1", Bytes(0, 11)) // 需 3 切片 = C
	}()
	<-src.started // R1 已 pin 住 k1:0、k1:1，正阻塞在切片 2 的回源上

	if st := c.Stats(); st.Pinned != 2 {
		t.Fatalf("pinned = %d, want 2", st.Pinned)
	}
	// R2 挤占容量：只能淘汰自己新写入的未 pin 切片，k1 的切片必须存活。
	resp2 := mustGet(t, c, "k2", Bytes(0, 7))
	if string(resp2.Data) != "aaaabbbb" {
		t.Fatalf("resp2 = %q", resp2.Data)
	}
	st := c.Stats()
	if st.Chunks != 3 || st.Pinned != 2 {
		t.Fatalf("stats = %+v, want 3 chunks with 2 pinned", st)
	}
	c.mu.Lock()
	_, ok0 := c.chunks[chunkKey{"k1", "v1", 0}]
	_, ok1 := c.chunks[chunkKey{"k1", "v1", 1}]
	c.mu.Unlock()
	if !ok0 || !ok1 {
		t.Fatalf("pinned chunks evicted: %v %v", ok0, ok1)
	}

	close(src.gates["k1"]) // 放行 R1 的回源
	<-done
	if err1 != nil {
		t.Fatalf("R1 err = %v", err1)
	}
	if string(resp1.Data) != "000011112222" {
		t.Fatalf("resp1 = %q", resp1.Data)
	}
	if st := c.Stats(); st.Pinned != 0 || st.Chunks > 3 {
		t.Fatalf("final stats = %+v", st)
	}
}

// 回源次数等于连续缺失段个数（元信息未知时加一次探取）。
func TestFetchCountBound(t *testing.T) {
	src := newFakeSource(4)
	src.set("k", "v1", []byte("0123456789ABCDEF"))
	c := newTestCache(t, 4, 16, src)

	// 元信息未知：1 次探取 + 1 个连续缺失段 = 2 次。
	mustGet(t, c, "k", Bytes(0, 15))
	if got := src.callCount(); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
	// 全部命中：0 次。
	before := src.callCount()
	mustGet(t, c, "k", Bytes(0, 15))
	if got := src.callCount() - before; got != 0 {
		t.Fatalf("calls = %d, want 0", got)
	}

	// 间隔缺失：3 个缺失段 = 3 次。
	src2 := newFakeSource(4)
	src2.set("k", "v1", []byte("0123456789ABCDEF"))
	c2 := newTestCache(t, 4, 16, src2)
	mustGet(t, c2, "k", Bytes(1, 1)) // 切片 0
	mustGet(t, c2, "k", Bytes(8, 8)) // 切片 2
	before = src2.callCount()
	mustGet(t, c2, "k", Bytes(0, 15)) // 缺 1、3：两段
	if got := src2.callCount() - before; got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
}

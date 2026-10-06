// Package edgecache 实现内容分发边缘节点的大对象分片缓存：
// 对象按固定大小切片缓存，字节范围请求部分命中，只对缺失的
// 连续切片段向源站回源，并处理对象版本变化、并发请求与容量约束。
//
// 关键性质：
//   - 命中判定与访问顺序维护均为 O(1)，不随缓存切片总数增长。
//   - 同一切片的并发缺失只回源一次（singleflight），回源失败不写缓存。
//   - 回源发现版本变化时作废旧版本全部切片，并按新版本重新判定一次范围；
//     一次请求内再次出现版本变化报 ErrVersionThrash。
//   - 超出容量时淘汰最久未访问的切片；被未完成请求引用（pin）的切片不淘汰。
//   - 并发调用的结果等价于某个串行顺序：全部缓存状态由一把互斥锁保护，
//     源站调用在锁外进行，其结果在锁内原子生效。
package edgecache

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// errVersionChanged 是内部信号：本次请求所依据的版本已过期，需重新判定。
var errVersionChanged = errors.New("edgecache: version changed during request")

// Config 是缓存配置：切片大小 S 与最大缓存切片数 C。
type Config struct {
	ChunkSize int64  // S，必须为正
	Capacity  int    // C，最大缓存切片数，必须非负
	Source    Source // 源站，必须非 nil
}

// chunkKey 是缓存切片的键：对象键 + 版本标识 + 切片下标。
type chunkKey struct {
	key     string
	version string
	index   int64
}

// meta 是对象键当前已知的版本标识与总长度。
type meta struct {
	version string
	total   int64
}

// entry 是一个缓存切片。所有缓存中的切片都在 LRU 链表中；
// pins > 0 表示被未完成请求引用，淘汰时跳过。
type entry struct {
	key    chunkKey
	data   []byte
	pins   int
	elem   *list.Element
	cached bool
}

// call 是一次进行中的单切片回源，等待者共享其结果。
type call struct {
	done  chan struct{}
	data  []byte
	ver   string
	total int64
	err   error
}

// Cache 是分片缓存，并发安全。
type Cache struct {
	s   int64
	cap int
	src Source

	mu       sync.Mutex
	metas    map[string]meta
	chunks   map[chunkKey]*entry
	lru      *list.List // 元素为 chunkKey，队首为最近访问
	inflight map[chunkKey]*call
	fetches  int64 // 累计回源次数（统计）
}

// New 校验配置并创建缓存。
func New(cfg Config) (*Cache, error) {
	if cfg.ChunkSize <= 0 {
		return nil, fmt.Errorf("%w: chunk size must be positive, got %d", ErrInvalidArgument, cfg.ChunkSize)
	}
	if cfg.Capacity < 0 {
		return nil, fmt.Errorf("%w: capacity must be non-negative, got %d", ErrInvalidArgument, cfg.Capacity)
	}
	if cfg.Source == nil {
		return nil, fmt.Errorf("%w: source is nil", ErrInvalidArgument)
	}
	return &Cache{
		s:        cfg.ChunkSize,
		cap:      cfg.Capacity,
		src:      cfg.Source,
		metas:    make(map[string]meta),
		chunks:   make(map[chunkKey]*entry),
		lru:      list.New(),
		inflight: make(map[chunkKey]*call),
	}, nil
}

// Stats 是缓存的运行时统计，主要用于测试与观测。
type Stats struct {
	Objects int   // 已知版本/总长度的对象键数
	Chunks  int   // 当前缓存的切片数
	Pinned  int   // 被未完成请求引用的切片数
	Fetches int64 // 累计回源次数
}

// Stats 返回当前统计快照。
func (c *Cache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := Stats{
		Objects: len(c.metas),
		Chunks:  len(c.chunks),
		Fetches: atomic.LoadInt64(&c.fetches),
	}
	for _, e := range c.chunks {
		if e.pins > 0 {
			st.Pinned++
		}
	}
	return st
}

// validateRequest 做不依赖总长度的参数校验与范围判定。
// 参数非法优先于范围不可满足。
func validateRequest(key string, r RangeSpec) error {
	if key == "" {
		return fmt.Errorf("%w: empty object key", ErrInvalidArgument)
	}
	switch r.Kind {
	case RangeStartEnd:
		if r.Start < 0 || r.End < 0 {
			return fmt.Errorf("%w: negative range bound", ErrInvalidArgument)
		}
		if r.End < r.Start {
			return &RangeError{Reason: RangeEndBeforeStart, Key: key, Start: r.Start, End: r.End}
		}
	case RangeFrom:
		if r.Start < 0 {
			return fmt.Errorf("%w: negative range start", ErrInvalidArgument)
		}
	case RangeSuffix:
		if r.N < 0 {
			return fmt.Errorf("%w: negative suffix length", ErrInvalidArgument)
		}
		if r.N == 0 {
			return &RangeError{Reason: RangeSuffixZero, Key: key}
		}
	default:
		return fmt.Errorf("%w: unknown range kind %d", ErrInvalidArgument, r.Kind)
	}
	return nil
}

// computeRange 在已知总长度时把范围规格换算为闭区间 [start, end]。
// empty 为真表示合法的空应答（仅后缀写法作用于零长度对象时出现）。
func computeRange(key string, r RangeSpec, total int64) (start, end int64, empty bool, err error) {
	switch r.Kind {
	case RangeStartEnd, RangeFrom:
		start = r.Start
		if start >= total {
			return 0, 0, false, &RangeError{Reason: RangeStartBeyondTotal, Key: key, Start: r.Start, Total: total}
		}
		end = total - 1
		if r.Kind == RangeStartEnd && r.End < end {
			end = r.End
		}
		return start, end, false, nil
	case RangeSuffix:
		n := r.N
		if n > total {
			n = total
		}
		if n == 0 {
			return 0, 0, true, nil
		}
		return total - n, total - 1, false, nil
	}
	return 0, 0, false, fmt.Errorf("%w: unknown range kind %d", ErrInvalidArgument, r.Kind)
}

// Get 按范围取对象数据。错误类别见包级错误变量，优先级：
// 参数非法 > 范围不可满足 > 容量不足 > 回源失败 > 版本震荡。
func (c *Cache) Get(ctx context.Context, key string, r RangeSpec) (*Response, error) {
	if err := validateRequest(key, r); err != nil {
		return nil, err
	}
	rejudged := false
	for {
		resp, err := c.attempt(ctx, key, r)
		if err == errVersionChanged {
			if rejudged {
				return nil, ErrVersionThrash
			}
			rejudged = true
			continue
		}
		return resp, err
	}
}

// chunkOutcome 是单个切片在一次尝试中的结果。
type chunkOutcome struct {
	index     int64
	data      []byte
	fromCache bool
}

// waitItem 关联一个缺失切片与其 singleflight 调用。
type waitItem struct {
	pos  int64 // 在 outcomes 中的下标
	call *call
	led  bool // 是否由本次请求发起回源
}

// attempt 执行一次完整的取数尝试。若回源发现版本变化，返回 errVersionChanged，
// 此时元信息已更新、旧版本切片已作废，调用方应重新判定。
func (c *Cache) attempt(ctx context.Context, key string, r RangeSpec) (*Response, error) {
	var pinned []*entry
	defer c.releaseAll(&pinned)

	// 总长度未知时先回源一次取得版本与总长度。
	c.mu.Lock()
	_, known := c.metas[key]
	c.mu.Unlock()
	if !known {
		if err := c.probe(ctx, key, c.probeIndex(r)); err != nil {
			return nil, err
		}
	}

	c.mu.Lock()
	m, ok := c.metas[key]
	if !ok {
		c.mu.Unlock()
		return nil, fmt.Errorf("%w: metadata unavailable after probe", ErrSourceFailure)
	}
	start, end, empty, rerr := computeRange(key, r, m.total)
	if rerr != nil {
		c.mu.Unlock()
		return nil, rerr
	}
	if empty {
		c.mu.Unlock()
		return &Response{Key: key, Version: m.version, Total: m.total, Data: []byte{}}, nil
	}
	lo, hi := start/c.s, end/c.s
	n := hi - lo + 1
	// 容量检查先于命中判定：被拒绝的请求不得刷新访问顺序，也不得回源。
	if n > int64(c.cap) {
		c.mu.Unlock()
		return nil, fmt.Errorf("%w: need %d chunks, capacity %d", ErrCapacityExceeded, n, c.cap)
	}
	ver := m.version
	total := m.total
	outcomes := make([]chunkOutcome, n)
	var waits []waitItem
	var ledRuns [][2]int64
	// 命中判定、pin、缺失段合并与 singleflight 登记在同一个临界区内完成，
	// 保证并发请求等价于某个串行顺序。
	i := lo
	for i <= hi {
		ck := chunkKey{key: key, version: ver, index: i}
		if e := c.chunks[ck]; e != nil {
			c.pinLocked(e)
			pinned = append(pinned, e)
			outcomes[i-lo] = chunkOutcome{index: i, data: e.data, fromCache: true}
			i++
			continue
		}
		if cl := c.inflight[ck]; cl != nil {
			waits = append(waits, waitItem{pos: i - lo, call: cl})
			i++
			continue
		}
		// 相邻且无人回源的缺失切片合并为一个回源段，由本次请求领队。
		j := i
		for j+1 <= hi {
			nck := chunkKey{key: key, version: ver, index: j + 1}
			if c.chunks[nck] != nil || c.inflight[nck] != nil {
				break
			}
			j++
		}
		for x := i; x <= j; x++ {
			cl := &call{done: make(chan struct{})}
			c.inflight[chunkKey{key: key, version: ver, index: x}] = cl
			waits = append(waits, waitItem{pos: x - lo, call: cl, led: true})
		}
		ledRuns = append(ledRuns, [2]int64{i, j})
		i = j + 1
	}
	c.mu.Unlock()

	// 锁外回源，每个连续缺失段一次调用。
	for _, run := range ledRuns {
		c.fetchRun(ctx, key, ver, run[0], run[1])
	}

	// 汇总：回源失败优先于版本震荡。
	var firstErr error
	changed := false
	for _, w := range waits {
		<-w.call.done
		if w.call.err != nil {
			if firstErr == nil {
				firstErr = w.call.err
			}
			continue
		}
		if w.call.ver != ver {
			changed = true
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	if changed {
		return nil, errVersionChanged
	}
	for _, w := range waits {
		outcomes[w.pos] = chunkOutcome{index: lo + w.pos, data: w.call.data, fromCache: !w.led}
	}

	resp := &Response{Key: key, Version: ver, Total: total, Start: start}
	size := end - start + 1
	resp.Data = make([]byte, 0, size)
	for pos := range outcomes {
		oc := outcomes[pos]
		off := int64(0)
		if pos == 0 {
			off = start - oc.index*c.s
		}
		data := oc.data[off:]
		if rem := size - int64(len(resp.Data)); int64(len(data)) > rem {
			data = data[:rem]
		}
		resp.Data = append(resp.Data, data...)
		resp.Chunks = append(resp.Chunks, ChunkInfo{Index: oc.index, FromCache: oc.fromCache})
	}
	return resp, nil
}

// probeIndex 选出总长度未知时用于探取元信息的切片下标：
// 起点类写法取起点所在切片（该切片几乎必然被需要），后缀写法取切片 0。
func (c *Cache) probeIndex(r RangeSpec) int64 {
	if r.Kind == RangeSuffix {
		return 0
	}
	return r.Start / c.s
}

// probe 在总长度未知时回源单个切片以取得版本与总长度；
// 并发探取通过 inflight 合并为一次回源。
func (c *Cache) probe(ctx context.Context, key string, idx int64) error {
	ck := chunkKey{key: key, index: idx}
	c.mu.Lock()
	if _, known := c.metas[key]; known {
		c.mu.Unlock()
		return nil
	}
	if cl := c.inflight[ck]; cl != nil {
		c.mu.Unlock()
		<-cl.done
		return cl.err
	}
	cl := &call{done: make(chan struct{})}
	c.inflight[ck] = cl
	c.mu.Unlock()
	c.fetchRun(ctx, key, "", idx, idx)
	<-cl.done
	return cl.err
}

// fetchRun 以一次源站调用取切片 [first, last]，并在锁内原子地完成：
// 版本变化则作废旧版本切片并更新元信息、写入新切片、唤醒所有等待者。
// 失败只唤醒等待者，不写缓存。
func (c *Cache) fetchRun(ctx context.Context, key, expectVer string, first, last int64) {
	res, err := c.src.FetchChunks(ctx, key, first, last, expectVer)
	atomic.AddInt64(&c.fetches, 1)
	if err == nil {
		err = validateResult(c.s, res, first, last)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		ferr := fmt.Errorf("%w: %w", ErrSourceFailure, err)
		for x := first; x <= last; x++ {
			ck := chunkKey{key: key, version: expectVer, index: x}
			if cl := c.inflight[ck]; cl != nil {
				cl.err = ferr
				close(cl.done)
				delete(c.inflight, ck)
			}
		}
		return
	}
	if cur, ok := c.metas[key]; !ok || cur.version != res.Version {
		c.invalidateLocked(key, res.Version)
	}
	c.metas[key] = meta{version: res.Version, total: res.Total}
	for k, data := range res.Data {
		ck := chunkKey{key: key, version: res.Version, index: first + int64(k)}
		if _, exists := c.chunks[ck]; !exists {
			c.insertLocked(ck, data)
		}
	}
	for x := first; x <= last; x++ {
		ck := chunkKey{key: key, version: expectVer, index: x}
		cl := c.inflight[ck]
		if cl == nil {
			continue
		}
		if k := x - first; int(k) < len(res.Data) {
			cl.data = res.Data[k]
		}
		cl.ver = res.Version
		cl.total = res.Total
		close(cl.done)
		delete(c.inflight, ck)
	}
}

// validateResult 校验源站应答是否符合契约：切片数量与对象末尾一致、
// 每个切片长度正确（仅对象末切片可不足 S）。违反契约按回源失败处理。
func validateResult(s int64, res SourceResult, first, last int64) error {
	if res.Total < 0 {
		return fmt.Errorf("negative total length %d", res.Total)
	}
	var want int64
	if res.Total > 0 {
		lastIdx := (res.Total - 1) / s
		if first <= lastIdx {
			want = min(last, lastIdx) - first + 1
		}
	}
	if int64(len(res.Data)) != want {
		return fmt.Errorf("source returned %d chunks, want %d", len(res.Data), want)
	}
	for k, d := range res.Data {
		idx := first + int64(k)
		size := s
		if idx == (res.Total-1)/s {
			size = res.Total - idx*s
		}
		if int64(len(d)) != size {
			return fmt.Errorf("chunk %d has %d bytes, want %d", idx, len(d), size)
		}
	}
	return nil
}

// pinLocked 引用一个切片：命中即刷新访问顺序（移到队首），并阻止淘汰。
func (c *Cache) pinLocked(e *entry) {
	e.pins++
	if e.elem != nil {
		c.lru.MoveToFront(e.elem)
	}
}

// releaseAll 释放一次请求持有的全部引用，并尝试把缓存淘汰回容量以内。
func (c *Cache) releaseAll(pinned *[]*entry) {
	if len(*pinned) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range *pinned {
		e.pins--
	}
	c.trimLocked()
}

// insertLocked 写入一个新切片并置于队首，随后淘汰超量部分。
func (c *Cache) insertLocked(ck chunkKey, data []byte) {
	e := &entry{key: ck, data: data, cached: true}
	c.chunks[ck] = e
	e.elem = c.lru.PushFront(ck)
	c.trimLocked()
}

// trimLocked 从队首最远端（最久未访问）淘汰，直到不超过容量；
// 被 pin 的切片跳过。若全部切片都被 pin，允许暂时超容，
// 待引用释放后的下一次 trim 再淘汰回来。
func (c *Cache) trimLocked() {
	for len(c.chunks) > c.cap {
		back := c.lru.Back()
		for back != nil && c.chunks[back.Value.(chunkKey)].pins > 0 {
			back = back.Prev()
		}
		if back == nil {
			return
		}
		ck := back.Value.(chunkKey)
		e := c.chunks[ck]
		delete(c.chunks, ck)
		c.lru.Remove(back)
		e.cached = false
		e.elem = nil
	}
}

// invalidateLocked 作废对象键下除 keepVersion 外全部版本的切片。
// 被 pin 的切片仅从缓存摘除，其数据由引用方继续持有直至释放。
func (c *Cache) invalidateLocked(key, keepVersion string) {
	for ck, e := range c.chunks {
		if ck.key == key && ck.version != keepVersion {
			delete(c.chunks, ck)
			if e.elem != nil {
				c.lru.Remove(e.elem)
				e.elem = nil
			}
			e.cached = false
		}
	}
}

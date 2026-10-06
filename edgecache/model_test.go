package edgecache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// modelCache 是独立实现的朴素串行模型：同样的语义，最直白的写法
// （切片用 map、访问顺序用切片、无 singleflight、无 pin），
// 用于与真实缓存做逐步骤对照。
type modelCache struct {
	s   int64
	cap int
	src *fakeSource

	meta map[string]meta
	data map[chunkKey][]byte
	lru  []chunkKey // 队首 = 最近访问

	lastProbes int // 最近一次 get 的探取回源次数
	lastRuns   int // 最近一次 get 的缺失段回源次数
}

func newModelCache(s int64, cap int, src *fakeSource) *modelCache {
	return &modelCache{
		s:    s,
		cap:  cap,
		src:  src,
		meta: make(map[string]meta),
		data: make(map[chunkKey][]byte),
	}
}

func (m *modelCache) touch(ck chunkKey) {
	for i, v := range m.lru {
		if v == ck {
			m.lru = append(m.lru[:i], m.lru[i+1:]...)
			break
		}
	}
	m.lru = append([]chunkKey{ck}, m.lru...)
}

func (m *modelCache) evict() {
	for len(m.data) > m.cap && len(m.lru) > 0 {
		back := m.lru[len(m.lru)-1]
		m.lru = m.lru[:len(m.lru)-1]
		delete(m.data, back)
	}
}

func (m *modelCache) applyResult(key string, res SourceResult, first int64) {
	if cur, ok := m.meta[key]; !ok || cur.version != res.Version {
		for ck := range m.data {
			if ck.key == key && ck.version != res.Version {
				delete(m.data, ck)
				for i, v := range m.lru {
					if v == ck {
						m.lru = append(m.lru[:i], m.lru[i+1:]...)
						break
					}
				}
			}
		}
	}
	m.meta[key] = meta{version: res.Version, total: res.Total}
	for k, d := range res.Data {
		ck := chunkKey{key: key, version: res.Version, index: first + int64(k)}
		if _, ok := m.data[ck]; !ok {
			m.data[ck] = d
			m.lru = append([]chunkKey{ck}, m.lru...)
			m.evict()
		}
	}
}

func (m *modelCache) doFetch(key, expect string, a, b int64) (SourceResult, error) {
	res, err := m.src.FetchChunks(context.Background(), key, a, b, expect)
	if err == nil {
		err = validateResult(m.s, res, a, b)
	}
	if err != nil {
		return SourceResult{}, fmt.Errorf("%w: %w", ErrSourceFailure, err)
	}
	m.applyResult(key, res, a)
	return res, nil
}

func (m *modelCache) get(key string, r RangeSpec) (*Response, error) {
	m.lastProbes, m.lastRuns = 0, 0
	if err := validateRequest(key, r); err != nil {
		return nil, err
	}
	rejudged := false
	for {
		resp, changed, err := m.attempt(key, r)
		if err != nil {
			return nil, err
		}
		if !changed {
			return resp, nil
		}
		if rejudged {
			return nil, ErrVersionThrash
		}
		rejudged = true
	}
}

func (m *modelCache) attempt(key string, r RangeSpec) (*Response, bool, error) {
	if _, ok := m.meta[key]; !ok {
		idx := int64(0)
		if r.Kind != RangeSuffix {
			idx = r.Start / m.s
		}
		m.lastProbes++
		if _, err := m.doFetch(key, "", idx, idx); err != nil {
			return nil, false, err
		}
	}
	mt := m.meta[key]
	start, end, empty, rerr := computeRange(key, r, mt.total)
	if rerr != nil {
		return nil, false, rerr
	}
	if empty {
		return &Response{Key: key, Version: mt.version, Total: mt.total, Data: []byte{}}, false, nil
	}
	lo, hi := start/m.s, end/m.s
	if hi-lo+1 > int64(m.cap) {
		return nil, false, fmt.Errorf("%w: need %d chunks, capacity %d", ErrCapacityExceeded, hi-lo+1, m.cap)
	}
	ver := mt.version
	outcomes := make([]chunkOutcome, hi-lo+1)
	type run struct{ a, b int64 }
	var runs []run
	i := lo
	for i <= hi {
		ck := chunkKey{key: key, version: ver, index: i}
		if d, ok := m.data[ck]; ok {
			m.touch(ck)
			outcomes[i-lo] = chunkOutcome{index: i, data: d, fromCache: true}
			i++
			continue
		}
		j := i
		for j+1 <= hi {
			if _, ok := m.data[chunkKey{key: key, version: ver, index: j + 1}]; ok {
				break
			}
			j++
		}
		runs = append(runs, run{i, j})
		i = j + 1
	}
	m.lastRuns += len(runs)
	changed := false
	var firstErr error
	fetched := map[int64][]byte{}
	for _, rn := range runs {
		res, err := m.doFetch(key, ver, rn.a, rn.b)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if res.Version != ver {
			changed = true
		}
		for k, d := range res.Data {
			fetched[rn.a+int64(k)] = d
		}
	}
	if firstErr != nil {
		return nil, false, firstErr
	}
	if changed {
		return nil, true, nil
	}
	for idx, d := range fetched {
		outcomes[idx-lo] = chunkOutcome{index: idx, data: d, fromCache: false}
	}

	resp := &Response{Key: key, Version: ver, Total: mt.total, Start: start}
	size := end - start + 1
	resp.Data = make([]byte, 0, size)
	for pos := range outcomes {
		oc := outcomes[pos]
		off := int64(0)
		if pos == 0 {
			off = start - oc.index*m.s
		}
		data := oc.data[off:]
		if rem := size - int64(len(resp.Data)); int64(len(data)) > rem {
			data = data[:rem]
		}
		resp.Data = append(resp.Data, data...)
		resp.Chunks = append(resp.Chunks, ChunkInfo{Index: oc.index, FromCache: oc.fromCache})
	}
	return resp, false, nil
}

var errBoom = errors.New("boom")

// errSig 把错误归约为可比较的签名：类别 + 范围不可满足的具体原因。
func errSig(err error) string {
	if err == nil {
		return "nil"
	}
	var re *RangeError
	switch {
	case errors.Is(err, ErrInvalidArgument):
		return "invalid"
	case errors.As(err, &re):
		return fmt.Sprintf("range/%d", re.Reason)
	case errors.Is(err, ErrRangeNotSatisfiable):
		return "range"
	case errors.Is(err, ErrCapacityExceeded):
		return "capacity"
	case errors.Is(err, ErrSourceFailure):
		return "source"
	case errors.Is(err, ErrVersionThrash):
		return "thrash"
	}
	return fmt.Sprintf("other:%v", err)
}

func respSig(resp *Response) string {
	if resp == nil {
		return "<nil>"
	}
	return fmt.Sprintf("ver=%s total=%d start=%d len=%d chunks=%v",
		resp.Version, resp.Total, resp.Start, len(resp.Data), chunkPattern(resp))
}

func respEqual(a, b *Response) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Version == b.Version &&
		a.Total == b.Total &&
		a.Start == b.Start &&
		bytes.Equal(a.Data, b.Data) &&
		reflect.DeepEqual(a.Chunks, b.Chunks)
}

// assertStateEqual 校验真实缓存与模型的内部状态完全一致：
// 元信息、切片集合与内容、LRU 访问顺序。
func assertStateEqual(t *testing.T, c *Cache, m *modelCache) {
	t.Helper()
	c.mu.Lock()
	realMetas := make(map[string]meta, len(c.metas))
	for k, v := range c.metas {
		realMetas[k] = v
	}
	realChunks := make(map[chunkKey][]byte, len(c.chunks))
	for ck, e := range c.chunks {
		realChunks[ck] = e.data
	}
	var realLRU []chunkKey
	for e := c.lru.Front(); e != nil; e = e.Next() {
		realLRU = append(realLRU, e.Value.(chunkKey))
	}
	c.mu.Unlock()

	if !reflect.DeepEqual(realMetas, m.meta) {
		t.Fatalf("meta mismatch: real=%v model=%v", realMetas, m.meta)
	}
	if len(realChunks) != len(m.data) {
		t.Fatalf("chunk count mismatch: real=%d model=%d", len(realChunks), len(m.data))
	}
	for ck, d := range m.data {
		rd, ok := realChunks[ck]
		if !ok || !bytes.Equal(rd, d) {
			t.Fatalf("chunk %+v mismatch: real=%v model=%v", ck, ok, d)
		}
	}
	if fmt.Sprint(realLRU) != fmt.Sprint(m.lru) {
		t.Fatalf("lru mismatch:\nreal =%v\nmodel=%v", realLRU, m.lru)
	}
}

func randomContent(rnd *rand.Rand, s int64) []byte {
	var n int
	switch rnd.Intn(4) {
	case 0:
		n = 0 // 零长度对象
	case 1:
		n = int(s) * (1 + rnd.Intn(3)) // 恰为切片大小整数倍
	default:
		n = rnd.Intn(int(4*s) + 4)
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + rnd.Intn(26))
	}
	return b
}

func randomRange(rnd *rand.Rand, s int64) RangeSpec {
	bound := 4*s + 6
	switch rnd.Intn(3) {
	case 0:
		return Bytes(rnd.Int63n(bound+3)-2, rnd.Int63n(bound+3)-2) // 含负数与终点小于起点
	case 1:
		return From(rnd.Int63n(bound+3) - 2)
	default:
		return LastN(rnd.Int63n(bound + 2)) // 含零后缀
	}
}

// 与独立朴素模型对照：随机请求序列逐步比对输入、输出与内部状态，
// 并验证回源次数恰为 连续缺失段数（元信息未知时加一次探取）。
func TestRandomAgainstModel(t *testing.T) {
	const sequences = 1200
	for seed := int64(0); seed < sequences; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rnd := rand.New(rand.NewSource(seed))
			s := int64(1 + rnd.Intn(8))
			capN := rnd.Intn(8)
			realSrc := newFakeSource(s)
			modelSrc := newFakeSource(s)
			c, err := New(Config{ChunkSize: s, Capacity: capN, Source: realSrc})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			m := newModelCache(s, capN, modelSrc)

			keys := []string{"alpha", "beta", "gamma"}
			verCounter := map[string]int{}
			newObj := func(key string) fakeObj {
				verCounter[key]++
				return fakeObj{
					version: fmt.Sprintf("v%d", verCounter[key]),
					data:    randomContent(rnd, s),
				}
			}
			for _, k := range keys {
				obj := newObj(k)
				realSrc.set(k, obj.version, obj.data)
				modelSrc.set(k, obj.version, obj.data)
			}
			t.Logf("init: s=%d cap=%d", s, capN)

			ops := 15 + rnd.Intn(15)
			for step := 0; step < ops; step++ {
				switch rnd.Intn(10) {
				case 0, 1: // 源站换版本
					k := keys[rnd.Intn(len(keys))]
					obj := newObj(k)
					realSrc.set(k, obj.version, obj.data)
					modelSrc.set(k, obj.version, obj.data)
					t.Logf("step %d: set %s -> %s len=%d", step, k, obj.version, len(obj.data))
				case 2: // 回源中途翻版本（1~2 次，2 次触发版本震荡）
					k := keys[rnd.Intn(len(keys))]
					n := 1 + rnd.Intn(2)
					objs := make([]fakeObj, n)
					for i := range objs {
						objs[i] = newObj(k)
					}
					realSrc.armFlip(k, objs...)
					modelSrc.armFlip(k, objs...)
					t.Logf("step %d: arm flip %s x%d", step, k, n)
				case 3: // 注入回源失败
					n := 1 + rnd.Intn(3)
					realSrc.failNext(n, errBoom)
					modelSrc.failNext(n, errBoom)
					t.Logf("step %d: fail next %d fetches", step, n)
				default: // 范围请求
					k := keys[rnd.Intn(len(keys))]
					r := randomRange(rnd, s)
					realBefore := realSrc.callCount()
					modelBefore := modelSrc.callCount()
					gotResp, gotErr := c.Get(context.Background(), k, r)
					wantResp, wantErr := m.get(k, r)
					realFetches := realSrc.callCount() - realBefore
					modelFetches := modelSrc.callCount() - modelBefore
					t.Logf("step %d: get %s %+v -> err=%s resp=%s | fetches real=%d model=%d (probe=%d runs=%d)",
						step, k, r, errSig(gotErr), respSig(gotResp),
						realFetches, modelFetches, m.lastProbes, m.lastRuns)

					if gs, ws := errSig(gotErr), errSig(wantErr); gs != ws {
						t.Fatalf("step %d: err %s, want %s (resp %s vs %s)",
							step, gs, ws, respSig(gotResp), respSig(wantResp))
					}
					if !respEqual(gotResp, wantResp) {
						t.Fatalf("step %d: resp %s, want %s",
							step, respSig(gotResp), respSig(wantResp))
					}
					if realFetches != modelFetches {
						t.Fatalf("step %d: real fetches %d, model %d", step, realFetches, modelFetches)
					}
					if modelFetches != m.lastProbes+m.lastRuns {
						t.Fatalf("step %d: fetches %d exceed bound probe=%d runs=%d",
							step, modelFetches, m.lastProbes, m.lastRuns)
					}
					assertStateEqual(t, c, m)
				}
			}
		})
	}
}

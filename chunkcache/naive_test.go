package chunkcache

import (
	"container/list"
	"context"
)

// naiveModel 是与实现完全独立的顺序参考模型（随机差分测试用）。
// 逐切片判定；连续缺失段合并为一次回源；LRU 用标准库 list 维护。
type naiveModel struct {
	s       int64
	c       int
	origin  Origin
	version map[string]string
	length  map[string]int64
	known   map[string]bool
	data    map[chunkKey][]byte
	lru     *list.List // front=most recent, value=chunkKey
	elem    map[chunkKey]*list.Element
	calls   int64
}

type naiveResp struct {
	version string
	data    []byte
	source  map[int]ChunkSource
	calls   int
	err     *Error
}

func newNaiveModel(s int64, c int, origin Origin) *naiveModel {
	return &naiveModel{
		s: s, c: c, origin: origin,
		version: map[string]string{},
		length:  map[string]int64{},
		known:   map[string]bool{},
		data:    map[chunkKey][]byte{},
		lru:     list.New(),
		elem:    map[chunkKey]*list.Element{},
	}
}

func (m *naiveModel) touch(id chunkKey) {
	if el, ok := m.elem[id]; ok {
		m.lru.MoveToFront(el)
	}
}

// addBack 把新片挂尾部，稍后由 finalizeOrder 按请求下标顺序重排。
func (m *naiveModel) addBack(id chunkKey, b []byte) {
	if el, ok := m.elem[id]; ok {
		m.data[id] = b
		m.lru.MoveToFront(el)
		return
	}
	m.data[id] = b
	m.elem[id] = m.lru.PushBack(id)
}

// reserveEvict 为 n 个新片腾位，但不淘汰 protected 中的（本请求 span）切片。
func (m *naiveModel) reserveEvict(n int, protected map[chunkKey]bool) {
	target := m.lru.Len() + n
	for target > m.c {
		var victim *list.Element
		for el := m.lru.Back(); el != nil; el = el.Prev() {
			if !protected[el.Value.(chunkKey)] {
				victim = el
				break
			}
		}
		if victim == nil {
			panic("naive: no evictable chunk")
		}
		id := victim.Value.(chunkKey)
		m.lru.Remove(victim)
		delete(m.elem, id)
		delete(m.data, id)
		target--
	}
}

func (m *naiveModel) invalidate(key string) {
	for id := range m.data {
		if id.key == key {
			delete(m.data, id)
		}
	}
	for id, el := range m.elem {
		if id.key == key {
			m.lru.Remove(el)
			delete(m.elem, id)
		}
	}
}

func (m *naiveModel) finalizeOrder(key, ver string, first, last int) {
	for i := first; i <= last; i++ {
		m.touch(chunkKey{key: key, version: ver, index: i})
	}
}

func (m *naiveModel) cacheOrder() []chunkKey {
	var out []chunkKey
	for el := m.lru.Front(); el != nil; el = el.Next() {
		out = append(out, el.Value.(chunkKey))
	}
	return out
}

func (m *naiveModel) get(ctx context.Context, key string, br ByteRange) naiveResp {
	resp := naiveResp{source: map[int]ChunkSource{}}
	callsBefore := m.calls

	mined := map[int]bool{}
	discoveredNow := false
	if !m.known[key] {
		res, err := m.origin.Fetch(ctx, FetchRequest{Key: key, First: 0, Last: 0})
		m.calls++
		if err != nil {
			return naiveResp{err: newError(KindBackend, "naive.ensureMeta", "fetch", err)}
		}
		m.version[key] = res.Version
		m.length[key] = res.Length
		m.known[key] = true
		discoveredNow = true
		// 发现回源仅学长度，不缓存数据。
	}
	rollbackDiscovery := func() {
		if discoveredNow {
			delete(m.known, key)
			delete(m.version, key)
			delete(m.length, key)
		}
	}

	rejudged := false
	for {
		ver := m.version[key]
		length := m.length[key]
		rr, rerr := br.resolve("naive", length)
		if rerr != nil {
			rollbackDiscovery()
			return naiveResp{calls: int(m.calls - callsBefore), err: asError(rerr)}
		}
		if rr.end <= rr.start {
			rollbackDiscovery()
			return naiveResp{calls: int(m.calls - callsBefore),
				err: newError(KindRangeNotSatisfiable, "naive", ReasonZeroSuffix, nil)}
		}
		first, last := chunkSpan(m.s, rr.start, rr.end)
		if last-first+1 > m.c {
			rollbackDiscovery()
			return naiveResp{calls: int(m.calls - callsBefore),
				err: newError(KindCapacity, "naive", "span_exceeds_capacity", nil)}
		}

		chunks := map[int][]byte{}
		var runs [][2]int
		for i := first; i <= last; i++ {
			id := chunkKey{key: key, version: ver, index: i}
			if b, ok := m.data[id]; ok {
				chunks[i] = b
			} else {
				runs = append(runs, [2]int{i, i})
			}
		}
		merged := naiveMerge(runs)

		fetched := map[int]FetchResult{}
		var fetchErr error
		for _, run := range merged {
			res, err := m.origin.Fetch(ctx, FetchRequest{
				Key: key, First: run[0], Last: run[1], ExpectedVersion: ver,
			})
			m.calls++
			if err != nil {
				fetchErr = err
				break
			}
			fetched[run[0]] = res
			for off := range res.Chunks {
				if len(res.Chunks[off]) > 0 {
					mined[run[0]+off] = true
				}
			}
		}
		if fetchErr != nil {
			return naiveResp{calls: int(m.calls - callsBefore),
				err: newError(KindBackend, "naive", "fetch", fetchErr)}
		}

		versionChanged := false
		var newVer string
		var newLen int64
		for _, res := range fetched {
			if res.Version != ver {
				versionChanged = true
				newVer = res.Version
				newLen = res.Length
			}
		}

		if versionChanged {
			if rejudged {
				return naiveResp{calls: int(m.calls - callsBefore),
					err: newError(KindVersionOscillation, "naive", "second_version_change", nil)}
			}
			m.invalidate(key)
			m.version[key] = newVer
			m.length[key] = newLen
			totalNew := 0
			for run0, res := range fetched {
				if res.Version != newVer {
					continue
				}
				for _, b := range res.Chunks {
					if len(b) > 0 {
						totalNew++
					}
				}
				_ = run0
			}
			m.reserveEvict(totalNew, map[chunkKey]bool{})
			for run0, res := range fetched {
				if res.Version != newVer {
					continue
				}
				for off, b := range res.Chunks {
					if len(b) > 0 {
						m.addBack(chunkKey{key: key, version: newVer, index: run0 + off}, b)
					}
				}
			}
			m.finalizeOrder(key, newVer, first, last)
			rejudged = true
			continue
		}

		protected := map[chunkKey]bool{}
		for i := first; i <= last; i++ {
			protected[chunkKey{key: key, version: ver, index: i}] = true
		}
		totalAdd := 0
		for run0, res := range fetched {
			for off, b := range res.Chunks {
				if len(b) == 0 {
					continue
				}
				id := chunkKey{key: key, version: ver, index: run0 + off}
				if _, exists := m.data[id]; !exists {
					totalAdd++
				}
			}
		}
		m.reserveEvict(totalAdd, protected)
		for run0, res := range fetched {
			for off, b := range res.Chunks {
				idx := run0 + off
				if len(b) > 0 {
					m.addBack(chunkKey{key: key, version: ver, index: idx}, b)
				}
				chunks[idx] = b
			}
		}
		m.finalizeOrder(key, ver, first, last)

		var out []byte
		for i := first; i <= last; i++ {
			b := chunks[i]
			lo := int64(i) * m.s
			cbLo := rr.start - lo
			if cbLo < 0 {
				cbLo = 0
			}
			cbHi := rr.end - lo
			if cbHi > int64(len(b)) {
				cbHi = int64(len(b))
			}
			if cbHi > cbLo {
				out = append(out, b[cbLo:cbHi]...)
			}
		}
		for i := first; i <= last; i++ {
			if mined[i] {
				resp.source[i] = SourceOrigin
			} else {
				resp.source[i] = SourceCache
			}
		}
		resp.version = ver
		resp.data = out
		resp.calls = int(m.calls - callsBefore)
		return resp
	}
}

func asError(err error) *Error {
	if e, ok := err.(*Error); ok {
		return e
	}
	return newError(KindBackend, "naive", "convert", err)
}

func naiveMerge(runs [][2]int) [][2]int {
	if len(runs) == 0 {
		return nil
	}
	out := [][2]int{runs[0]}
	for _, r := range runs[1:] {
		last := &out[len(out)-1]
		if r[0] == (*last)[1]+1 {
			(*last)[1] = r[1]
		} else {
			out = append(out, r)
		}
	}
	return out
}

package chunkcache

import "context"

// errVersionChanged 是 fillSpan 的内部信号：本轮回源发现版本变化，
// 已在锁内作废旧版本切片并把新版本数据入库；Get 须按新版本重判一次。
var errVersionChanged = newError(KindVersionOscillation, "fillSpan", "version_changed_internal", nil)

// fillSpan 取得 [first,last] 全部切片：命中即用，缺失段按连续区间合并回源。
// 返回版本变化信号时不组装数据，由 Get 重判。
func (c *Cache) fillSpan(
	ctx context.Context,
	key, version string,
	length int64,
	first, last int,
	resp *GetResponse,
	fetchedReq map[int]bool,
) ([][]byte, []ChunkReport, error) {
	var missing []int
	c.mu.Lock()
	for i := first; i <= last; i++ {
		id := chunkKey{key: key, version: version, index: i}
		if !c.st.hasLocked(id) {
			missing = append(missing, i)
		}
	}
	c.mu.Unlock()

	segments := splitRuns(missing)
	type grp struct {
		f      *flight
		leader bool
	}
	var groups []grp
	c.mu.Lock()
	reg := c.registryLocked(key)
	for _, seg := range segments {
		plan := c.registerLocked(key, version, seg[0], seg[1], reg)
		for gi, f := range plan.groups {
			f.recorder = resp
			groups = append(groups, grp{f: f, leader: plan.leader[gi]})
		}
	}
	leaders := map[*flight]bool{}
	for _, g := range groups {
		if g.leader {
			leaders[g.f] = true
		}
	}
	c.mu.Unlock()

	for fl := range leaders {
		flightRef := fl
		c.executeLeader(ctx, flightRef, func(res FetchResult) {
			c.publishFetchLocked(flightRef, res)
		})
	}

	finals := map[*flight]FetchResult{}
	for _, g := range groups {
		res, err := waitFlight(ctx, g.f)
		if err != nil {
			return nil, nil, newError(KindBackend, "fillSpan", "fetch", err)
		}
		finals[g.f] = res
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for _, g := range groups {
		if g.leader && g.f.malformed {
			return nil, nil, newError(KindBackend, "fillSpan", "malformed_chunks", nil)
		}
	}

	anyVersionChange := false
	newVersion := version
	newLength := length
	for _, g := range groups {
		if finals[g.f].Version != version {
			anyVersionChange = true
			newVersion = finals[g.f].Version
			newLength = finals[g.f].Length
		}
	}

	if anyVersionChange {
		removed := c.st.invalidateKeyLocked(key)
		c.st.meta[key] = objectMeta{version: newVersion, length: newLength}
		c.log.Logf("[version] key=%s %q->%q invalidated=%d", key, version, newVersion, removed)
		for _, g := range groups {
			if g.f.err != nil || g.f.malformed || finals[g.f].Version != newVersion {
				continue
			}
			n := 0
			for _, data := range finals[g.f].Chunks {
				if len(data) > 0 {
					n++
				}
			}
			c.st.reserveLocked(c.c, n, map[chunkKey]bool{})
			for off, data := range finals[g.f].Chunks {
				if len(data) == 0 {
					continue
				}
				idx := g.f.first + off
				fetchedReq[idx] = true
				c.st.insertLocked(chunkKey{key: key, version: newVersion, index: idx}, data, g.f.gen)
			}
		}
		return nil, nil, errVersionChanged
	}

	for _, g := range groups {
		if g.f.err == nil {
			m := c.st.meta[key]
			m.length = finals[g.f].Length
			c.st.meta[key] = m
			break
		}
	}

	installed := map[int][]byte{}
	var toInsert []struct {
		id  chunkKey
		b   []byte
		gen int64
		idx int
	}
	for _, g := range groups {
		if g.f.err != nil || g.f.malformed {
			continue
		}
		for off, data := range finals[g.f].Chunks {
			idx := g.f.first + off
			if idx < first || idx > last {
				continue
			}
			toInsert = append(toInsert, struct {
				id  chunkKey
				b   []byte
				gen int64
				idx int
			}{chunkKey{key: key, version: version, index: idx}, data, g.f.gen, idx})
			installed[idx] = data
		}
	}
	protected := map[chunkKey]bool{}
	for i := first; i <= last; i++ {
		protected[chunkKey{key: key, version: version, index: i}] = true
	}
	newCount := 0
	for _, it := range toInsert {
		if len(it.b) > 0 {
			if _, exists := c.st.peekLocked(it.id); !exists {
				newCount++
			}
		}
	}
	// 仅安装非空切片（越界占位空片不入库）。
	filtered := toInsert[:0]
	for _, it := range toInsert {
		if len(it.b) > 0 {
			filtered = append(filtered, it)
		}
	}
	toInsert = filtered
	c.st.reserveLocked(c.c, newCount, protected)
	for _, it := range toInsert {
		c.st.insertBackLocked(it.id, it.b, it.gen)
	}
	c.st.reorderSpanLocked(key, version, first, last)

	data := make([][]byte, 0, last-first+1)
	reports := make([]ChunkReport, 0, last-first+1)
	for _, g := range groups {
		if g.f.malformed {
			continue
		}
		for off := range finals[g.f].Chunks {
			idx := g.f.first + off
			fetchedReq[idx] = true
		}
	}
	for i := first; i <= last; i++ {
		var chunk []byte
		e, cacheHit := c.st.peekLocked(chunkKey{key: key, version: version, index: i})
		if cacheHit {
			chunk = e.data
		} else if b, ok := installed[i]; ok {
			chunk = b
		}
		if chunk == nil {
			return nil, nil, newError(KindBackend, "fillSpan", "missing_chunk_after_fetch", nil)
		}
		data = append(data, chunk)
		src := SourceOrigin
		if cacheHit && !fetchedReq[i] {
			src = SourceCache
			c.stats.CacheHits++
		}
		reports = append(reports, ChunkReport{Index: i, Version: version, Source: src})
	}
	return data, reports, nil
}

// publishFetchLocked 为一个数据 flight 的锁内发布：校验形状并记账。
func (c *Cache) publishFetchLocked(f *flight, res FetchResult) {
	if want := f.last - f.first + 1; len(res.Chunks) != want {
		f.malformed = true
		return
	}
	c.st.gen++
	f.gen = c.st.gen
	if f.recorder != nil {
		c.recordCallLocked(f, res, f.recorder)
	}
}

// recordCallLocked 记录本请求对外可见的一次回源。
func (c *Cache) recordCallLocked(f *flight, res FetchResult, resp *GetResponse) {
	c.log.Logf("[origin] done key=%s idx=[%d,%d] got=%q len=%d chunks=%d",
		f.key, f.first, f.last, res.Version, res.Length, len(res.Chunks))
	resp.OriginCalls = append(resp.OriginCalls, OriginCallRecord{
		First:           f.first,
		Last:            f.last,
		ExpectedVersion: f.expect,
		GotVersion:      res.Version,
	})
	if f.gen != 0 {
		resp.gens[f.gen] = struct{}{}
	}
}

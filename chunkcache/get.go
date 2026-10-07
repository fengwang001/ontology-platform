package chunkcache

import (
	"context"
	"sort"
)

// pinSet 为一次请求在其生命周期内持有的 pin（defer 统一释放）。
type pinSet map[pinKey]struct{}

func (p pinSet) add(k pinKey) { p[k] = struct{}{} }

// releaseLocked 释放本请求的全部 pin（调用方持锁）。
func (p pinSet) releaseLocked(s *state) {
	for k := range p {
		s.unpinLocked(k)
	}
}

// Get 处理一次范围请求。
//
// 错误固定优先级：参数非法 > 范围不可满足 > 容量不足 > 回源失败 > 版本震荡。
// 所有缓存状态变更都在 mu 下完成；源站调用在锁外执行（见 executeLeader）。
func (c *Cache) Get(ctx context.Context, key string, br ByteRange) (*GetResponse, error) {
	if key == "" {
		return nil, newError(KindInvalidParam, "Get", ReasonEmptyKey, nil)
	}
	pins := pinSet{}
	defer func() {
		c.mu.Lock()
		pins.releaseLocked(c.st)
		c.mu.Unlock()
	}()

	resp := &GetResponse{gens: map[int64]struct{}{}}

	// 阶段 1：取得“当前视图”的版本与总长度（首次访问须先回源）。
	// fetchedByReq 记录“本请求”通过回源得到的切片下标，供来源标注使用。
	fetchedByReq := map[int]bool{}
	version, length, discoveredNow, err := c.ensureMeta(ctx, key, pins, resp)
	if err != nil {
		return nil, err
	}
	rollbackDiscovery := func() {
		if discoveredNow {
			c.mu.Lock()
			delete(c.st.meta, key)
			c.mu.Unlock()
		}
	}

	// 阶段 2..4 最多两轮：第二轮是版本变化后的唯一一次重判。
	rejudged := false
	for {
		rr, rerr := br.resolve("Get", length)
		if rerr != nil {
			rollbackDiscovery()
			return nil, c.rejectNoCall(rerr)
		}
		if rr.end <= rr.start {
			rollbackDiscovery()
			return nil, c.rejectNoCall(newError(KindRangeNotSatisfiable, "Get", ReasonZeroSuffix, nil))
		}
		// 空对象上范围若可满足（仅可能是空后缀之外的非法情形已被 resolve 拦截），
		// 不会走到这里：length==0 时一切非空范围都不可满足。
		first, last := chunkSpan(c.s, rr.start, rr.end)
		if first > last {
			rollbackDiscovery()
			return nil, c.rejectNoCall(newError(KindRangeNotSatisfiable, "Get", ReasonZeroSuffix, nil))
		}

		if ok, gateErr := c.admit(key, first, last, pins); !ok {
			rollbackDiscovery()
			return nil, c.rejectNoCall(gateErr)
		}

		data, reports, fillErr := c.fillSpan(ctx, key, version, length, first, last, resp, fetchedByReq)
		if fillErr != nil {
			if fillErr == errVersionChanged {
				if rejudged {
					return nil, newError(KindVersionOscillation, "Get", "second_version_change", nil)
				}
				c.mu.Lock()
				c.stats.Rejudges++
				nm := c.st.meta[key]
				c.mu.Unlock()
				rejudged = true
				version = nm.version
				length = nm.length
				c.log.Logf("[version] key=%s rejudge -> %q len=%d", key, version, length)
				continue
			}
			return nil, fillErr
		}

		off := rr.start - int64(first)*c.s
		n := rr.end - rr.start
		resp.Version = version
		resp.Data = concatChunks(data, off, n)
		sort.Slice(reports, func(i, j int) bool { return reports[i].Index < reports[j].Index })
		resp.Chunks = reports
		c.log.Logf("[ok] key=%s ver=%q bytes=[%d,%d) chunks=%d originCalls=%d",
			key, version, rr.start, rr.end, len(reports), len(resp.OriginCalls))
		return resp, nil
	}
}

// rejectNoCall 保证被拒绝请求不发出回源、不改变缓存与访问顺序。
func (c *Cache) rejectNoCall(err error) error {
	c.mu.Lock()
	c.stats.RejectedNoCall++
	c.mu.Unlock()
	c.log.Logf("[reject] %v", err)
	return err
}

// ensureMeta 返回某键当前已知版本/长度；首次访问时通过一次回源取得。
//
// 首访的“发现回源”是不可避免的额外一次回源（规格要求总长度未知须先回源）。
// 发现取回下标 0：空对象时源站返回 Length=0、Chunks 为空；
// 非空对象时该切片同时作为数据切片服务后续 fillSpan。
//
// 并发：同一时刻多个首访请求共享同一个 discovery flight；leader 在锁内发布
// meta 与切片后再唤醒 follower，保证 follower 醒来即可见已发布状态。
func (c *Cache) ensureMeta(ctx context.Context, key string, pins pinSet, resp *GetResponse) (string, int64, bool, error) {
	c.mu.Lock()
	if m, ok := c.st.meta[key]; ok {
		c.mu.Unlock()
		return m.version, m.length, false, nil
	}
	reg := c.registryLocked(key)
	plan := c.registerLocked(key, "", 0, 0, reg)
	f := plan.groups[0]
	isLeader := plan.leader[0]
	c.mu.Unlock()

	if isLeader {
		// 锁外回源；完成后在锁内发布 meta/切片，最后广播。
		c.log.Logf("[origin] call key=%s idx=[%d,%d] expect=%q (discovery)", f.key, f.first, f.last, f.expect)
		res, err := c.origin.Fetch(ctx, FetchRequest{
			Key: f.key, First: f.first, Last: f.last, ExpectedVersion: f.expect,
		})
		c.mu.Lock()
		f.result, f.err = res, err
		for k := f.first; k <= f.last; k++ {
			if r := c.flightRegs[f.key]; r != nil && r[k] == f {
				delete(r, k)
			}
		}
		c.stats.OriginCalls++
		if err == nil {
			c.stats.OriginChunks += int64(len(res.Chunks))
			c.publishDiscoveryLocked(key, f, res, resp)
		}
		close(f.done)
		c.mu.Unlock()
	}

	res, err := waitFlight(ctx, f)
	if err != nil {
		return "", 0, false, newError(KindBackend, "ensureMeta", "fetch", err)
	}
	c.mu.Lock()
	if !isLeader {
		c.recordCallLocked(f, res, resp)
	}
	m := c.st.meta[key]
	c.mu.Unlock()
	return m.version, m.length, true, nil
}

// publishDiscoveryLocked 记录首访发现的版本/长度；leader 写入新切片。调用方持锁。
func (c *Cache) publishDiscoveryLocked(key string, f *flight, res FetchResult, resp *GetResponse) {
	c.st.meta[key] = objectMeta{version: res.Version, length: res.Length}
	c.st.gen++
	f.gen = c.st.gen
	c.recordCallLocked(f, res, resp)
	// 发现回源只用于学习版本/长度，不缓存数据切片：在容量判定完成前，
	// 不允许它改变缓存内容与访问顺序；所需切片随后由 fillSpan 正常缺失回源。
	c.log.Logf("[origin] done key=%s idx=[%d,%d] got=%q len=%d chunks=%d (discovery)",
		f.key, f.first, f.last, res.Version, res.Length, len(res.Chunks))
}

// admit 执行并发安全的容量门控；通过即为本请求 span 完成 pin 占位。
func (c *Cache) admit(key string, first, last int, pins pinSet) (bool, error) {
	need := map[pinKey]struct{}{}
	for i := first; i <= last; i++ {
		need[pinKey{key: key, index: i}] = struct{}{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// 释放本请求上一轮（如版本重判）span 中、不再属于当前 span 的 pin。
	for pk := range pins {
		if _, inSpan := need[pk]; !inSpan && pk.key == key {
			c.st.unpinLocked(pk)
			delete(pins, pk)
		}
	}
	if !c.st.canAdmitLocked(c.c, need) {
		return false, newError(KindCapacity, "admit", "span_exceeds_capacity", nil)
	}
	for k := range need {
		if _, already := pins[k]; !already {
			c.st.pinLocked(k)
		}
		pins.add(k)
	}
	return true, nil
}

// registryLocked 返回某键的 flight 注册表（惰性创建）。调用方持锁。
func (c *Cache) registryLocked(key string) map[int]*flight {
	reg := c.flightRegs[key]
	if reg == nil {
		reg = make(map[int]*flight)
		c.flightRegs[key] = reg
	}
	return reg
}

// splitRuns 把递增下标切片切成连续段，每段为 [first,last]。
func splitRuns(idxs []int) [][2]int {
	if len(idxs) == 0 {
		return nil
	}
	var runs [][2]int
	start, prev := idxs[0], idxs[0]
	for _, i := range idxs[1:] {
		if i == prev+1 {
			prev = i
			continue
		}
		runs = append(runs, [2]int{start, prev})
		start, prev = i, i
	}
	return append(runs, [2]int{start, prev})
}

// concatChunks 把整切片数据按区间内偏移拼出 [off, off+n)。
func concatChunks(chunks [][]byte, off, n int64) []byte {
	out := make([]byte, 0, n)
	var pos int64
	for _, ch := range chunks {
		l := int64(len(ch))
		if n == 0 {
			break
		}
		if pos+l <= off {
			pos += l
			continue
		}
		lo := off - pos
		if lo < 0 {
			lo = 0
		}
		avail := l - lo
		take := avail
		if take > n {
			take = n
		}
		out = append(out, ch[lo:lo+take]...)
		n -= take
		off += take
		pos += l
	}
	return out
}

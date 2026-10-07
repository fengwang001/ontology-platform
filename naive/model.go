// Package naive 是差分测试用的朴素参照模型：
// 独立维护全部版本，所有查询均为线性扫描，不复用索引代码。
package naive

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"ontology/core"
)

// Model 是朴素线性扫描参照模型。
type Model struct {
	mu       sync.Mutex
	versions map[core.Key][]core.Version
	minBiz   map[core.Key]int64
}

// New 创建空模型。
func New() *Model {
	return &Model{versions: make(map[core.Key][]core.Version), minBiz: make(map[core.Key]int64)}
}

// Write 按与正式实现相同的规则与拒绝次序提交一次写入，
// 但用独立的直白实现（不复用 store 包代码），以便差分对照。
func (m *Model) Write(req core.WriteRequest) (core.Version, *core.Error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if req.Key.Type == "" || req.Key.ID == "" {
		return core.Version{}, &core.Error{Code: core.ErrInvalidArgument,
			Message: "object type and primary key must both be non-empty"}
	}
	if req.BizStart < 0 {
		return core.Version{}, &core.Error{Code: core.ErrInvalidArgument,
			Message: fmt.Sprintf("bizStart %d is negative", req.BizStart)}
	}
	expected, perr := core.ParseCredential(req.Credential)
	if perr != nil {
		return core.Version{}, &core.Error{Code: core.ErrInvalidArgument, Message: perr.Error()}
	}

	vs := m.versions[req.Key]
	latest := uint64(len(vs))
	if expected != latest {
		return core.Version{}, &core.Error{Code: core.ErrConcurrencyConflict,
			Message: fmt.Sprintf("key %s: bases on seq %d, latest is %d", req.Key, expected, latest)}
	}
	if latest > 0 && req.BizStart < m.minBiz[req.Key] {
		return core.Version{}, &core.Error{Code: core.ErrBeforeBoundary,
			Message: fmt.Sprintf("key %s: bizStart %d before boundary %d", req.Key, req.BizStart, m.minBiz[req.Key])}
	}

	v := core.Version{
		Seq:       latest + 1,
		BizStart:  req.BizStart,
		Payload:   req.Payload,
		Deleted:   req.Delete,
		WallClock: time.Now(),
	}
	m.versions[req.Key] = append(vs, v)
	if latest == 0 || req.BizStart < m.minBiz[req.Key] {
		m.minBiz[req.Key] = req.BizStart
	}
	return v, nil
}

// Query 线性扫描全部版本回答双时态点查询：
// 在 Seq <= sysQ 的版本中，按业务起点分组取系统时间最晚者，
// 再由起点排序得到区间划分，返回覆盖 bizQ 的区间代表版本。
func (m *Model) Query(k core.Key, sysQ uint64, bizQ int64) core.QueryResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	vs := m.versions[k]
	if len(vs) == 0 {
		return core.QueryResult{Visibility: core.NeverExisted}
	}
	best := make(map[int64]core.Version)
	for _, v := range vs {
		if v.Seq > sysQ {
			continue
		}
		if cur, ok := best[v.BizStart]; !ok || v.Seq > cur.Seq {
			best[v.BizStart] = v
		}
	}
	if len(best) == 0 {
		return core.QueryResult{Visibility: core.NotVisible}
	}
	starts := make([]int64, 0, len(best))
	for s := range best {
		starts = append(starts, s)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	i := sort.Search(len(starts), func(i int) bool { return starts[i] > bizQ }) - 1
	if i < 0 {
		return core.QueryResult{Visibility: core.NotVisible}
	}
	v := best[starts[i]]
	if v.Deleted {
		return core.QueryResult{Visibility: core.Deleted, Version: &v}
	}
	return core.QueryResult{Visibility: core.Found, Version: &v}
}

// LatestSeq 返回主键当前最新版本号（无版本时为 0）。
func (m *Model) LatestSeq(k core.Key) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return uint64(len(m.versions[k]))
}

// MinBizStart 返回主键已提交的最早可追溯边界（无版本时返回 0）。
func (m *Model) MinBizStart(k core.Key) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.minBiz[k]
}

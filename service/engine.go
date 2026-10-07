// Package service 负责对外读写接口与错误归一化：
// 组合版本仲裁器与双时态索引，提供线性化的读写入口。
package service

import (
	"sync"

	"ontology/bitemporal"
	"ontology/core"
	"ontology/store"
)

// Engine 是子系统对外的统一入口。
type Engine struct {
	mu    sync.Mutex
	store *store.Store
	index *bitemporal.Index
}

// NewEngine 创建 Engine。
func NewEngine() *Engine {
	return &Engine{store: store.New(), index: bitemporal.New()}
}

// Write 提交一次写入（含逻辑删除）。错误为归一化的 *core.Error，
// 三类拒绝（参数非法 / 凭证冲突 / 早于可追溯边界）可相互区分。
//
// 仲裁与索引更新在同一把锁内完成：只有仲裁成功的版本才会进入索引，
// 且进入索引前不会被其他写入插队，保证读写线性化。
func (e *Engine) Write(req core.WriteRequest) (core.Version, *core.Error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, werr := e.store.Commit(req)
	if werr != nil {
		return core.Version{}, werr
	}
	e.index.Apply(req.Key, v)
	return v, nil
}

// Put 是写入业务内容的便捷方法。
func (e *Engine) Put(k core.Key, bizStart int64, payload, credential string) (core.Version, *core.Error) {
	return e.Write(core.WriteRequest{Key: k, BizStart: bizStart, Payload: payload, Credential: credential})
}

// Remove 是逻辑删除的便捷方法：占用一个系统时间版本号，
// 声明「该事实自 bizStart 起不再成立」，不携带新业务内容。
func (e *Engine) Remove(k core.Key, bizStart int64, credential string) (core.Version, *core.Error) {
	return e.Write(core.WriteRequest{Key: k, BizStart: bizStart, Delete: true, Credential: credential})
}

// Query 按 (sysQ, bizQ) 双时态坐标点查询。
func (e *Engine) Query(k core.Key, sysQ uint64, bizQ int64) core.QueryResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, _ := e.index.Query(k, sysQ, bizQ)
	return r
}

// QueryWithSteps 同 Query，附带索引探测步数（复杂度验证用）。
func (e *Engine) QueryWithSteps(k core.Key, sysQ uint64, bizQ int64) (core.QueryResult, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.index.Query(k, sysQ, bizQ)
}

// HistoryAsOf 重建 sysQ 系统时刻下该主键的完整可见时间线。
func (e *Engine) HistoryAsOf(k core.Key, sysQ uint64) []core.Interval {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.index.RebuildAsOf(k, sysQ)
}

// Chain 返回主键的版本链快照。
func (e *Engine) Chain(k core.Key) []core.Version {
	e.mu.Lock()
	defer e.mu.Unlock()
	vs, _, _ := e.store.Snapshot(k)
	return vs
}

// LatestSeq 返回主键当前最新版本号（无版本时为 0）。
func (e *Engine) LatestSeq(k core.Key) uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.store.LatestSeq(k)
}

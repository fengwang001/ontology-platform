// Package shard 维护分片计数、键在基础分片上的累计计数、热点判定与路由决策。
// 不依赖其他包。
package shard

// Event 是一条 CDC 事件：Key 为键，Base 为基础路由给出的分片号。
type Event struct {
	Key  string
	Base int
}

// Engine 是分片计数引擎。所有方法都不是并发安全的，并发控制由上层（api）负责。
type Engine struct {
	S   int // 基础分片数
	T   int // 热点阈值：键在基础分片上累计事件数 >= T 即成为热点
	cnt []int64
	// keyBase 记录未迁移键在其基础分片上的累计事件数；键迁移后不再更新。
	keyBase map[string]int64
	hot     map[string]bool
	// checked 是非导出计数器：Feed 路径上为判定「该键是否已迁移」而检查的键个数。
	// 每次判定只做一次哈希定位，故每事件恰好 +1，与已迁移键总数无关。
	checked int64
}

// New 构造引擎，cnt 初始为 S 个 0。
func New(S, T int) *Engine {
	return &Engine{
		S:       S,
		T:       T,
		cnt:     make([]int64, S),
		keyBase: make(map[string]int64),
		hot:     make(map[string]bool),
	}
}

// Migrated 报告键是否已迁移（热点即已迁移，一旦迁移永久保持）。
// 仅供 Feed 路由路径调用：每次调用计为检查了一个键。
func (e *Engine) Migrated(key string) bool {
	e.checked++
	return e.hot[key]
}

// IsHot 是只读查询，不计入 checked。
func (e *Engine) IsHot(key string) bool { return e.hot[key] }

// Add 给分片 s 计一个事件。调用方保证 s 合法且每个事件恰好调用一次。
func (e *Engine) Add(s int) { e.cnt[s]++ }

// ObserveBase 把键在基础分片上的累计数加一，并报告是否达到阈值（>= T）。
// 仅当键尚未迁移时调用。
func (e *Engine) ObserveBase(key string) (reached bool) {
	e.keyBase[key]++
	return e.keyBase[key] >= int64(e.T)
}

// MarkHot 把键标记为热点（永久）。
func (e *Engine) MarkHot(key string) { e.hot[key] = true }

// NewShard 追加一个专属分片（计数从 0 开始），返回其分片号。
// 分片号天然等于 S + 已分配专属分片数。
func (e *Engine) NewShard() int {
	e.cnt = append(e.cnt, 0)
	return len(e.cnt) - 1
}

// Counts 返回各分片累计计数的副本（按分片号升序）。
func (e *Engine) Counts() []int64 {
	out := make([]int64, len(e.cnt))
	copy(out, e.cnt)
	return out
}

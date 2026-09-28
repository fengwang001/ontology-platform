package dedup

import (
	"sort"
	"sync"
)

// Counter 是并发安全的分组去重计数器。
//
// 零值不可用，请通过 NewCounter 构造。
type Counter struct {
	mu sync.RWMutex

	// maxBatchSize 是单批条目数上限（不含长度 0 的空批争议，见 Apply）。
	maxBatchSize int

	// mult 保存每个组内每个值当前的净次数（多重性，插入为正、撤回为负
	// 折叠后的结果）。多重性归零的 (组,值) 会被删除以保持视图紧凑。
	mult map[string]map[string]int

	// counts 保存每个组当前的去重计数：该组 mult 中多重性为正的值的个数。
	counts map[string]int

	// log 是已提交批产生的、按顺序不可变的输出日志；每次成功的 Apply
	// 对应其中一条记录（空批对应空切片）。
	log [][]GroupChange
}

// defaultMaxBatchSize 是构造参数非法时的兜底上限。
const defaultMaxBatchSize = 10_000

// NewCounter 构造一个空计数器。maxBatchSize 是单批条目数上限，
// 必须为正数；传入非正数会使用默认上限。
func NewCounter(maxBatchSize int) *Counter {
	if maxBatchSize <= 0 {
		maxBatchSize = defaultMaxBatchSize
	}
	return &Counter{
		maxBatchSize: maxBatchSize,
		mult:         make(map[string]map[string]int),
		counts:       make(map[string]int),
	}
}

// Apply 原子地应用一批变更。
//
// 批按条目顺序逐条在"此前已提交状态 + 批内此前各条已生效结果"上校验：
// 任何一条非法都会拒绝整批，多重性、视图与日志均不改变。
// 成功时提交状态并向日志追加一条记录，返回本批涉及各组的 Before/After
// （组名字典序，输出顺序确定）。
func (c *Counter) Apply(entries []Entry) ([]GroupChange, error) {
	// 批级校验先做：条目数超限不需要持锁。
	if len(entries) > c.maxBatchSize {
		return nil, &BatchError{
			Reason: ReasonTooManyEntries,
			Index:  -1,
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// batch[g][v] 是本批内对 (g,v) 的符号折叠（尚未提交）。
	// 校验与提交都只依据 committed + batch，拒绝时直接丢弃 batch 即可，
	// 保证被拒绝的批不触碰 mult / counts / log。
	batch := make(map[string]map[string]int)
	groups := make(map[string]struct{})

	for i, e := range entries {
		if e.Group == "" {
			return nil, &BatchError{Reason: ReasonEmptyGroup, Index: i, Value: e.Value}
		}
		if e.Value == "" {
			return nil, &BatchError{Reason: ReasonEmptyValue, Index: i, Group: e.Group}
		}
		if e.Delta != 1 && e.Delta != -1 {
			return nil, &BatchError{
				Reason: ReasonInvalidSign,
				Index:  i,
				Group:  e.Group,
				Value:  e.Value,
			}
		}

		// 当前多重性 = 已提交净次数 + 批内此前各条折叠结果。
		current := c.mult[e.Group][e.Value] + batch[e.Group][e.Value]
		if e.Delta == -1 && current <= 0 {
			return nil, &BatchError{
				Reason: ReasonWithdrawZero,
				Index:  i,
				Group:  e.Group,
				Value:  e.Value,
			}
		}

		g, ok := batch[e.Group]
		if !ok {
			g = make(map[string]int)
			batch[e.Group] = g
		}
		g[e.Value] += e.Delta
		groups[e.Group] = struct{}{}
	}

	// 全部条目合法：折叠出各组 Before/After 并提交。
	changes := c.commit(batch, groups)

	// 深拷贝一份给调用方，内部日志保留自己的不可变副本。
	out := append([]GroupChange(nil), changes...)
	c.log = append(c.log, changes)
	return out, nil
}

// commit 在持锁状态下把折叠后的批内增量合并进多重性视图，
// 并按"每个值跨越零点"的贡献之和计算各组去重计数的净变化。
// 调用方必须保证 batch 只包含合法变更（任何值的最终多重性非负）。
func (c *Counter) commit(batch map[string]map[string]int, groups map[string]struct{}) []GroupChange {
	groupNames := make([]string, 0, len(groups))
	for g := range groups {
		groupNames = append(groupNames, g)
	}
	sort.Strings(groupNames)

	changes := make([]GroupChange, 0, len(groupNames))
	for _, gName := range groupNames {
		before := c.counts[gName]
		delta := 0

		values := c.mult[gName]
		for v, d := range batch[gName] {
			old := values[v] // 缺省 0
			new := old + d
			// 单个值对去重计数的贡献：1{多重性>0}。
			delta += posOne(new) - posOne(old)
			switch {
			case new == 0:
				delete(values, v)
			case values == nil:
				// 该组此前不存在，需要先建内层 map。
				values = map[string]int{v: new}
				c.mult[gName] = values
			default:
				values[v] = new
			}
		}
		// 组内所有值都撤回干净时移除空组，保持快照整洁。
		if len(c.mult[gName]) == 0 {
			delete(c.mult, gName)
			c.counts[gName] = 0
			delete(c.counts, gName)
		} else {
			c.counts[gName] = before + delta
		}
		changes = append(changes, GroupChange{
			Group:  gName,
			Before: before,
			After:  before + delta,
		})
	}
	return changes
}

// posOne 在多重性为正时返回 1，否则返回 0。
func posOne(m int) int {
	if m > 0 {
		return 1
	}
	return 0
}

// Snapshot 返回某一刻逐字段一致的视图：各组当前的去重计数
// （组名为键，去重计数为值）。返回的是深拷贝，调用方可自由修改。
func (c *Counter) Snapshot() map[string]int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]int, len(c.counts))
	for g, n := range c.counts {
		out[g] = n
	}
	return out
}

// Log 返回已提交批输出日志的拷贝（按提交顺序，每次成功 Apply 一条）。
// GroupChange 为值类型，拷贝后与内部状态完全隔离。
func (c *Counter) Log() [][]GroupChange {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([][]GroupChange, len(c.log))
	for i, batch := range c.log {
		out[i] = append([]GroupChange(nil), batch...)
	}
	return out
}

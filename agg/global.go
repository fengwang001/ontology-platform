package agg

import (
	"log"
	"sort"
	"strings"
	"sync"
)

type groupState struct {
	sum    int64
	count  int64
	values map[string]int64 // 值 -> 全局现存行数
}

// Global 是只接收部分聚合并合并的全局聚合状态。
type Global struct {
	mu         sync.RWMutex
	maxGroups  int
	groups     map[string]*groupState
	accepted   int64 // 已接受（成功合并）的提交次数，即「已发送并被接收」计数
	sentGroups int64 // 已接收的部分组次数
}

// New 创建带组数上限的全局状态。
// maxGroups 必须大于 0，否则全局状态不允许任何组存在。
func New(maxGroups int) *Global {
	return &Global{
		maxGroups: maxGroups,
		groups:    make(map[string]*groupState),
	}
}

// Submit 校验并合并一个部分聚合，任何非法输入都整体拒绝。
//
// 拒绝类别（按以下顺序判定，原因互不相同）：
//  1. ErrEmptyGroup：部分聚合携带空组名；
//  2. ErrInvalidOperation：值净增减为 0 的条目不应被发送（本地阶段会剔除，
//     直接构造部分聚合时视为非法载荷）；
//  3. ErrWithdrawBeforeAdd：合并后某值行数为负（撤回不存在的行）；
//  4. ErrTooManyGroups：合并后存活组数超过上限。
//
// 校验采用「先投影、后提交」：任一组失败都不会写入任何状态，
// 已发送计数也保持不变，失败不留痕。
func (g *Global) Submit(p *Partial) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if p == nil {
		log.Printf("[agg-global] 收到空部分聚合，按空批接受，依据=无任何组需要变更")
		return nil
	}
	log.Printf("[agg-global] 收到部分聚合 %s", formatPartial(p))

	// 投影阶段：以当前状态为底构造合并后的视图，只在全部通过后落盘。
	projection := make(map[string]*groupState, len(g.groups)+len(p.Groups))
	for name, state := range g.groups {
		values := make(map[string]int64, len(state.values))
		for value, n := range state.values {
			values[value] = n
		}
		projection[name] = &groupState{sum: state.sum, count: state.count, values: values}
	}

	for _, pg := range p.Groups {
		if strings.TrimSpace(pg.Group) == "" {
			log.Printf("[agg-global] 判定拒绝: 部分聚合含空组名，依据=group_name_empty，状态不变")
			return ErrEmptyGroup
		}
		state, ok := projection[pg.Group]
		if !ok {
			state = &groupState{values: make(map[string]int64)}
			projection[pg.Group] = state
		}
		state.sum += pg.SumDelta
		state.count += pg.CountDelta

		values := make([]string, 0, len(pg.ValueDeltas))
		for value := range pg.ValueDeltas {
			values = append(values, value)
		}
		sort.Strings(values)
		for _, value := range values {
			delta := pg.ValueDeltas[value]
			if delta == 0 {
				log.Printf("[agg-global] 判定拒绝: 组 %q 携带值 %q 的零增量条目，依据=zero_delta_is_invalid_payload，状态不变",
					pg.Group, value)
				return ErrInvalidOperation
			}
			next := state.values[value] + delta
			if next < 0 {
				log.Printf("[agg-global] 判定拒绝: 组 %q 撤回不存在的值 %q（现存 %d，净增减 %d，结果 %d），依据=value_row_count_negative，状态不变",
					pg.Group, value, state.values[value], delta, next)
				return ErrWithdrawBeforeAdd
			}
			if next == 0 {
				delete(state.values, value)
			} else {
				state.values[value] = next
			}
		}

		if state.count < 0 {
			log.Printf("[agg-global] 判定拒绝: 组 %q 合并后计数为负（%d），依据=count_negative，状态不变",
				pg.Group, state.count)
			return ErrWithdrawBeforeAdd
		}
	}

	for name, state := range projection {
		if state.count == 0 {
			delete(projection, name)
		}
	}
	if g.maxGroups > 0 && len(projection) > g.maxGroups {
		log.Printf("[agg-global] 判定拒绝: 合并后组数 %d 超过上限 %d，依据=too_many_groups，状态不变",
			len(projection), g.maxGroups)
		return ErrTooManyGroups
	}

	// 提交阶段：投影全部合法，替换状态。
	g.groups = projection
	g.accepted++
	g.sentGroups += int64(len(p.Groups))
	log.Printf("[agg-global] 判定接受: 合并后存活组数 %d，已接受提交 %d 次，依据=投影全部合法",
		len(projection), g.accepted)
	return nil
}

// Results 返回当前全部存活组的结果。
// 返回按组名排序的快照，可被多个执行体与 Submit 并发调用。
func (g *Global) Results() []Result {
	g.mu.RLock()
	defer g.mu.RUnlock()

	names := make([]string, 0, len(g.groups))
	for name, state := range g.groups {
		if state.count > 0 {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	results := make([]Result, 0, len(names))
	for _, name := range names {
		state := g.groups[name]
		results = append(results, Result{
			Group:         name,
			Sum:           state.sum,
			Count:         state.count,
			Avg:           float64(state.sum) / float64(state.count),
			DistinctCount: len(state.values),
		})
	}
	return results
}

// AcceptedSubmits 返回已成功合并的提交次数（被拒提交不计数）。
func (g *Global) AcceptedSubmits() int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.accepted
}

// SentPartialGroups 返回已被接收的部分组累计个数（被拒提交不计数）。
func (g *Global) SentPartialGroups() int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.sentGroups
}

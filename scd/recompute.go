package scd

import "sort"

// Recompute 是纯函数式批量重算：把同一批事件按到达顺序折叠到变更点集合，
// 再由变更点集合一次性生成历史区间。它与增量维护必须始终给出相同结果。
//
// 同一键同一生效时间的多条事件以后到者（切片中更靠后者）为准；非法输入
// 与 History.Commit 使用同一套静态校验规则，非法时整批拒绝。
//
// 返回 map 中每个键的区间按起点升序。第二个返回值在出现非法输入时为
// *CommitError，且不会返回任何部分结果。
func Recompute(events []Event) (map[string][]Interval, error) {
	plan, reasons := normalize(events)
	if len(reasons) > 0 {
		return nil, &CommitError{Reasons: reasons}
	}

	keys := make([]string, 0, len(plan.byKey))
	for key := range plan.byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make(map[string][]Interval, len(keys))
	for _, key := range keys {
		planAt := plan.byKey[key] // 已按事件下标（到达顺序）升序。
		points := make(map[int64]ChangePoint, len(planAt))
		order := make([]int64, 0, len(planAt))
		for seq, pp := range planAt {
			if _, exists := points[pp.at]; !exists {
				order = append(order, pp.at)
			}
			points[pp.at] = ChangePoint{
				At: pp.at, Op: pp.event.Op, Value: pp.event.Value, Seq: int64(seq + 1),
			}
		}
		sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
		out[key] = buildIntervals(key, order, points)
	}
	return out, nil
}

// Replay 把多批事件按给定顺序逐批进行纯重算（批内按下标、批间按顺序），
// 用于验证“分批增量提交”与“一次性批量重算”的等价性。任一批非法即整体拒绝。
func Replay(batches ...[]Event) (map[string][]Interval, error) {
	var all []Event
	for _, batch := range batches {
		all = append(all, batch...)
	}
	return Recompute(all)
}

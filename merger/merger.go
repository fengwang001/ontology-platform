package merger

import (
	"slices"
	"sort"
)

// sameResponse 判断两次返回内容是否一致。
func sameResponse(a, b ShardResponse) bool {
	return a.Shard == b.Shard && a.Kind == b.Kind &&
		a.Result.Value == b.Result.Value &&
		a.Result.HasValue == b.Result.HasValue &&
		slices.Equal(a.Result.Values, b.Result.Values)
}

// Merge 将一组分片响应合并为带可信范围的答案。
// 合并结果与响应到达顺序无关：同一组响应以任意顺序传入，
// 得到的 Answer 逐字段相同。
func Merge(shards []ShardInfo, agg Aggregation, responses []ShardResponse) Answer {
	ans := Answer{Agg: agg}

	registered := make(map[string]ShardInfo, len(shards))
	for _, s := range shards {
		registered[s.Name] = s
	}

	// 按分片分组，保证与到达顺序无关：
	// 内容一致的重复返回只取首个、后到者丢弃并计为重复；
	// 内容冲突的多次返回无法核对，该分片按违反登记处理。
	byShard := make(map[string][]ShardResponse, len(shards))
	order := make([]string, 0, len(shards))
	for _, r := range responses {
		if _, seen := byShard[r.Shard]; !seen {
			order = append(order, r.Shard)
		}
		byShard[r.Shard] = append(byShard[r.Shard], r)
	}

	valid := make(map[string]Result, len(shards))
	for _, name := range order {
		group := byShard[name]
		ans.Duplicates += len(group) - 1
		first := group[0]
		conflict := false
		for _, r := range group[1:] {
			if !sameResponse(r, first) {
				conflict = true
				break
			}
		}
		info, ok := registered[name]
		switch {
		case !ok:
			// 未登记的分片无法核对上界，按违反登记处理。
			ans.Violations++
		case conflict:
			ans.Violations++
		case first.Kind == RespError:
			ans.Errors++
		case first.Kind == RespTimeout:
			ans.Timeouts++
		case violatesBounds(info, agg, first.Result):
			ans.Violations++
		default:
			valid[name] = first.Result
		}
	}
	ans.Succeeded = len(valid)

	// 缺失分片及其上界汇总（排序保证确定性）。
	var missRowSum, missRowValueSum, missMaxValueBound uint64
	for _, s := range shards {
		if _, ok := valid[s.Name]; ok {
			continue
		}
		ans.Missing = append(ans.Missing, s.Name)
		missRowSum = satAdd(missRowSum, s.RowBound)
		missRowValueSum = satAdd(missRowValueSum, satMul(s.RowBound, s.ValueBound))
		if s.ValueBound > missMaxValueBound {
			missMaxValueBound = s.ValueBound
		}
	}
	sort.Strings(ans.Missing)
	ans.Complete = len(ans.Missing) == 0

	if ans.Succeeded == 0 {
		ans.Inconclusive = true
		return ans
	}

	switch agg.Kind {
	case AggCount:
		var sum uint64
		for _, r := range valid {
			sum = satAdd(sum, r.Value)
		}
		ans.HasValue = true
		ans.Lower = sum
		ans.Upper = satAdd(sum, missRowSum)
	case AggSum:
		var sum uint64
		for _, r := range valid {
			sum = satAdd(sum, r.Value)
		}
		ans.HasValue = true
		ans.Lower = sum
		ans.Upper = satAdd(sum, missRowValueSum)
	case AggMin:
		minV, have := uint64(0), false
		for _, r := range valid {
			if r.HasValue && (!have || r.Value < minV) {
				minV, have = r.Value, true
			}
		}
		if !have {
			// 全部成功但都为空：精确地无数据；否则无法给出「不大于 x」的结论。
			ans.Inconclusive = !ans.Complete
			return ans
		}
		ans.HasValue = true
		if ans.Complete {
			ans.Lower, ans.Upper = minV, minV
		} else {
			// 缺失分片可能含更小值，只能给出「真值 ≤ 已收到最小值」。
			ans.Lower, ans.Upper = 0, minV
		}
	case AggMax:
		maxV, have := uint64(0), false
		for _, r := range valid {
			if r.HasValue && (!have || r.Value > maxV) {
				maxV, have = r.Value, true
			}
		}
		if !have {
			ans.Inconclusive = !ans.Complete
			return ans
		}
		ans.HasValue = true
		if ans.Complete {
			ans.Lower, ans.Upper = maxV, maxV
		} else {
			ans.Lower = maxV
			ans.Upper = maxV
			if missMaxValueBound > ans.Upper {
				ans.Upper = missMaxValueBound
			}
		}
	case AggTopK:
		var all []uint64
		for _, r := range valid {
			all = append(all, r.Values...)
		}
		sort.Slice(all, func(i, j int) bool { return all[i] > all[j] })
		if len(all) > agg.K {
			all = all[:agg.K]
		}
		ans.Items = all
		ans.HasValue = len(all) > 0
		if ans.Complete {
			ans.CertainPrefix = len(all)
		} else {
			// 严格大于全部缺失分片取值上界的前缀为确定：
			// 缺失分片不可能贡献 ≥ 该值的元素将其挤出前 K。
			prefix := 0
			for prefix < len(all) && all[prefix] > missMaxValueBound {
				prefix++
			}
			ans.CertainPrefix = prefix
		}
	}
	return ans
}

// violatesBounds 校验分片结果是否违反其登记上界。
func violatesBounds(info ShardInfo, agg Aggregation, r Result) bool {
	switch agg.Kind {
	case AggCount:
		return r.Value > info.RowBound
	case AggSum:
		return r.Value > satMul(info.RowBound, info.ValueBound)
	case AggMin, AggMax:
		if !r.HasValue {
			return false
		}
		if info.RowBound == 0 {
			return true // 登记为 0 行却返回了值
		}
		return r.Value > info.ValueBound
	case AggTopK:
		limit := uint64(agg.K)
		if info.RowBound < limit {
			limit = info.RowBound
		}
		if uint64(len(r.Values)) > limit {
			return true
		}
		for _, v := range r.Values {
			if v > info.ValueBound {
				return true
			}
		}
	}
	return false
}

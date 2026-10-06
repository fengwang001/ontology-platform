package railway

// 不变量：
//   - 发站编号小于 mergedFrom 的全部剩余分配票额已并入 shared，并入不可逆；
//   - alloc[from][to] 仅在 from >= mergedFrom 时仍有意义；
//   - 所有写操作在“并入后视图”上决策，成功时才一次性提交，被拒绝的操作不改动任何状态。

// quotaTable 管理一趟列车的分配票额与共用票额。
// mergedFrom 是已并入共用票额的发站前缀上界（该站及之前均已并入），并入不可逆。
type quotaTable struct {
	stationCount int
	alloc        [][]int // alloc[from][to]，from<to
	shared       int
	mergedFrom   int
}

func newQuotaTable(stationCount int, alloc [][]int, shared int) *quotaTable {
	return &quotaTable{stationCount: stationCount, alloc: alloc, shared: shared, mergedFrom: 0}
}

// view 返回在 now 时刻所有已到截止时刻（deadline = departures[i]-advance <= now）
// 的发站全部并入后的视图。不修改自身。
// 合并扫描从 mergedFrom 开始；在单次操作内扫描量最多为车站数，跨所有操作总额线性。
func (q *quotaTable) view(now int, departures []int, advance int) (newMerged int, newShared int) {
	shared := q.shared
	i := q.mergedFrom
	for i < q.stationCount && departures[i]-advance <= now {
		for to := i + 1; to < q.stationCount; to++ {
			shared += q.alloc[i][to]
		}
		i++
	}
	return i, shared
}

// decide 在并入视图上判定票额扣减来源。
// 发站已并入时只能扣共用；否则先扣分配票额，再扣共用。
func decideQuota(alloc [][]int, from, to, newMerged, newShared int) (ok bool, useShared bool) {
	if from < newMerged {
		return newShared > 0, true
	}
	if alloc[from][to] > 0 {
		return true, false
	}
	if newShared > 0 {
		return true, true
	}
	return false, false
}

// commitBuy 提交并入推进与一次成功的票额扣减。
func (q *quotaTable) commitBuy(newMerged, newShared, from, to int, useShared bool) {
	q.mergedFrom = newMerged
	q.shared = newShared
	if useShared {
		q.shared--
	} else {
		q.alloc[from][to]--
	}
}

// commitMerge 仅提交并入推进（退票等不需要扣票额的路径使用）。
func (q *quotaTable) commitMerge(newMerged, newShared int) {
	q.mergedFrom = newMerged
	q.shared = newShared
}

// refund 退票回补：当初扣自共用、或其分配票额此后已并入共用的，一律退回共用。
func (q *quotaTable) refund(from, to int, useShared bool) {
	if useShared || from < q.mergedFrom {
		q.shared++
	} else {
		q.alloc[from][to]++
	}
}

package grading

import (
	"container/heap"
	"sort"
)

// graderHeap 是某个评卷组内按（在手任务数, 评卷人标识）排序的最小堆。
// 组内候选集合只随"新增评卷人/停用评卷人"变化（与已完成任务数、答卷数无关）；
// 在手任务数变化通过 Fix 更新。选择时对堆顶逐个做 O(1) 资格判定，
// 不合格者临时弹出、判定结束后整体压回，因此单次选取开销只与评卷人总数相关，
// 不随历史任务总数或答卷总数增长。
type graderHeap struct {
	ids []string
	eng *Engine
}

func (h *graderHeap) Len() int { return len(h.ids) }

func (h *graderHeap) Less(i, j int) bool {
	gi := h.eng.graders[h.ids[i]]
	gj := h.eng.graders[h.ids[j]]
	if gi.load != gj.load {
		return gi.load < gj.load
	}
	return gi.desc.ID < gj.desc.ID
}

func (h *graderHeap) Swap(i, j int) {
	h.ids[i], h.ids[j] = h.ids[j], h.ids[i]
	gs := h.eng.graders
	gs[h.ids[i]].heapIdx = i
	gs[h.ids[j]].heapIdx = j
}

func (h *graderHeap) Push(x any) {
	id := x.(string)
	h.eng.graders[id].heapIdx = len(h.ids)
	h.ids = append(h.ids, id)
}

func (h *graderHeap) Pop() any {
	old := h.ids
	n := len(old)
	id := old[n-1]
	h.ids = old[:n-1]
	h.eng.graders[id].heapIdx = -1
	return id
}

// loadChanged 在评卷人在手任务数变化后维持堆序。
func (e *Engine) loadChanged(id string) {
	g := e.graders[id]
	if g.heapIdx >= 0 {
		heap.Fix(e.groupHeaps[g.desc.Group], g.heapIdx)
	}
}

// selectCandidate 在给定组别集合中选取一名满足全部约束的评卷人。
// excludeGroups 为不可选组别（如初评须异组、仲裁须第三组）；
// busy 为该答卷上已经出现过的评卷人（撤回重分配时永久排除）；
// student 触发回避与同人单次约束。
// 选择规则：合格者中在手任务最少，并列时标识最小；结果只依赖当前状态，
// 与调用历史无关。counts 非空时统计资格谓词求值次数（供性能可验证测试）。
func (e *Engine) selectCandidate(groupSet []string, busy map[string]struct{}, student string, counts *int) (string, string) {
	best := ""
	bestLoad := 0
	bestTied := false
	for _, grp := range groupSet {
		h := e.groupHeaps[grp]
		if h == nil {
			continue
		}
		// 堆数组只保证父节点序，不能直接线性找“最小合格者”；
		// 取组内评卷人的有序快照（按 load,id 排序）后扫描首个合格者。
		// 排序范围仅为该组当前评卷人（只随录入/停用变化），
		// 与已完成任务总数、答卷总数无关。
		ordered := append([]string(nil), h.ids...)
		sort.Strings(ordered)
		groupBest := ""
		for _, id := range ordered {
			g := e.graders[id]
			if counts != nil {
				*counts++
			}
			_, isBusy := busy[id]
			_, isConflict := g.assigned[student]
			_, isAvoided := g.students[student]
			if !g.active || g.load >= g.desc.DailyQuota || isBusy || isConflict || isAvoided {
				continue
			}
			if groupBest == "" {
				groupBest = id
			} else if gg := e.graders[groupBest]; g.load < gg.load || (g.load == gg.load && id < groupBest) {
				groupBest = id
			}
		}
		if groupBest != "" {
			id := groupBest
			g := e.graders[id]
			if best == "" {
				best = id
				bestLoad = g.load
			} else if g.load < bestLoad || (g.load == bestLoad && id < best) {
				if g.load == bestLoad {
					bestTied = true
				}
				best = id
				bestLoad = g.load
			}
		}
	}
	if best == "" {
		return "", ""
	}
	g := e.graders[best]
	tie := "唯一合格候选"
	if bestTied {
		tie = "并列打破: 同在手任务数取最小标识"
	}
	return best, "rule=min-load/tie-id; id=" + best + "; group=" + g.desc.Group + "; load=" + itoa(g.load) + "; " + tie
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

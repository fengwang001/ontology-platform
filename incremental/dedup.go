package incremental

// CycleDeduper 负责单个未确认周期内的去重判定，并维护一个有界的
// 跨周期“近期窗口”，用于位点推导回退后的重复检查。
//
// 开销有界性（可复核）：
//   - 周期内集合 seen 只保存本周期首次接受的写入 ID，周期结束
//     （确认或中止）即整体丢弃，大小不超过本周期去重后的写入数；
//   - 跨周期窗口 window 的大小不超过构造时给定的 windowCap，
//     与链路历史输出总量无关；
//   - 因此每次判定的代价为 O(1) 哈希查找，占用内存不超过
//     “本周期写入数 + windowCap”，不随历史总量增长。
//
// 测试通过 Stats 在持续追加历史后断言窗口大小不超过上限来复核。
type CycleDeduper struct {
	seen   map[string]struct{}
	order  []string
	window map[string]struct{}
	winCap int
}

// NewCycleDeduper 创建去重器。recentIDs 为链路最近已确认输出中的
// 写入 ID（由调用方按 windowCap 截断提供），windowCap 为窗口上限。
func NewCycleDeduper(recentIDs []string, windowCap int) *CycleDeduper {
	d := &CycleDeduper{
		seen:   make(map[string]struct{}),
		window: make(map[string]struct{}, windowCap),
		winCap: windowCap,
	}
	if len(recentIDs) > windowCap {
		recentIDs = recentIDs[len(recentIDs)-windowCap:]
	}
	for _, id := range recentIDs {
		d.window[id] = struct{}{}
	}
	return d
}

// Accept 判定一条写入是否应进入本周期输出。
//
// 返回值 accepted 为 true 表示首次被接受（调用方应将其追加到输出
// 缓冲）；为 false 表示被去重：duplicateInCycle 为 true 时是本周期
// 内的重试，否则是命中近期窗口的跨周期重复（位点回退后的重复检查）。
// 无论哪种情况，首次接受的位置与顺序都不会因重试而改变。
func (d *CycleDeduper) Accept(id string) (accepted, duplicateInCycle bool) {
	if _, ok := d.seen[id]; ok {
		return false, true
	}
	if _, ok := d.window[id]; ok {
		return false, false
	}
	d.seen[id] = struct{}{}
	d.order = append(d.order, id)
	return true, false
}

// Order 返回本周期首次被接受的写入 ID，按接受先后顺序排列。
func (d *CycleDeduper) Order() []string {
	out := make([]string, len(d.order))
	copy(out, d.order)
	return out
}

// Stats 暴露去重器内部规模，用于复核开销有界性。
type DedupStats struct {
	CycleSetSize int
	WindowSize   int
	WindowCap    int
}

// Stats 返回当前去重器规模快照。
func (d *CycleDeduper) Stats() DedupStats {
	return DedupStats{CycleSetSize: len(d.seen), WindowSize: len(d.window), WindowCap: d.winCap}
}

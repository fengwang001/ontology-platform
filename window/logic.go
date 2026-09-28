package window

import (
	"fmt"
	"sort"
)

// windowStart 返回时间戳 ts 所属窗口的起点（含）。
//
// 窗口大小为 size，按数学地板划分（与语言截断取余不同，负时间戳同样正确）：
//
//	...、[-2size,-size)、[-size,0)、[0,size)、[size,2size)、...
//
// 例如 size=10 时：ts=9 → 0，ts=10 → 10，ts=-1 → -10，ts=-10 → -10。
func windowStart(ts, size int64) int64 {
	r := ts % size
	if r < 0 { // Go 的 % 对负数向零取余，修正为数学取模
		r += size
	}
	return ts - r
}

// engine 是单批次处理的工作副本，与 Counter 本体隔离：
// 只有整批成功结束后，Counter 才会用 engine 的内容覆盖自身，
// 因此任何中途拒绝都不会改变计数器对外可见的状态。
type engine struct {
	cfg     Config
	hasMax  bool                              // 是否已见过事件
	maxTS   int64                             // 见过的最大事件时间
	wm      int64                             // 当前水位线 = maxTS - Delay
	wmSet   bool                              // 水位线是否已初始化
	open    map[string]map[int64]int64        // 未清除窗口：key -> 起点 -> 计数
	fired   map[string]map[int64]int64        // 已触发窗口（open 的子集）：key -> 起点 -> 计数
	results map[string]map[int64]WindowResult // 已触发窗口的最新结果（与 fired 同步增删）
	openN   int                               // 未结算窗口总数（跨所有键）
	history []Emission                        // 截至上一批的输出历史（副本）
	out     []Emission                        // 本批新产出
	dropped int64                             // 丢弃事件总数
	seq     int64                             // 全局输出序号
	logbuf  []string                          // 本批日志缓冲，提交后才刷出，拒绝时整体丢弃
}

// logf 把一条判定日志追加到批次缓冲：在批次提交前，任何处理细节都不外泄，
// 保证被整体拒绝的批次不会在日志中留下幻影的输入/水位线/丢弃记录。
func (e *engine) logf(format string, args ...any) {
	e.logbuf = append(e.logbuf, fmt.Sprintf(format, args...))
}

// rejectLogf 立即输出一行拒绝原因（批次回滚后唯一可见的记录）。
// 调用发生在 Counter 的写锁内，对同一 Logger 的调用天然串行。
func (e *engine) rejectLogf(format string, args ...any) {
	if e.cfg.Logger != nil {
		e.cfg.Logger.Logf("reject "+format, args...)
	}
}

// flushLog 在批次成功提交后把缓冲日志一次性输出（同样处于写锁内）。
func (e *engine) flushLog() {
	if e.cfg.Logger != nil {
		for _, line := range e.logbuf {
			e.cfg.Logger.Logf("%s", line)
		}
	}
}

func (e *engine) isFired(key string, start int64) bool {
	if m, ok := e.fired[key]; ok {
		_, ok := m[start]
		return ok
	}
	return false
}

// createWindow 开立一个新窗口；返回 false 表示未结算窗口数将超过上限。
func (e *engine) createWindow(key string, start int64) bool {
	if e.cfg.MaxOpenWindows > 0 && e.openN >= e.cfg.MaxOpenWindows {
		return false
	}
	m := e.open[key]
	if m == nil {
		m = make(map[int64]int64)
		e.open[key] = m
	}
	m[start] = 0
	e.openN++
	return true
}

func (e *engine) clearWindow(key string, start, end int64) {
	delete(e.open[key], start)
	if len(e.open[key]) == 0 {
		delete(e.open, key)
	}
	if m, ok := e.fired[key]; ok {
		delete(m, start)
		if len(m) == 0 {
			delete(e.fired, key)
		}
	}
	if m, ok := e.results[key]; ok {
		delete(m, start)
		if len(m) == 0 {
			delete(e.results, key)
		}
	}
	e.openN--
	gcTime := end + e.cfg.AllowedLateness
	e.logf("clear  key=%q window=[%d,%d) reason=\"watermark %d >= gc-time %d\"",
		key, start, end, e.wm, gcTime)
}

func (e *engine) emit(kind EmissionKind, key string, start, end, count int64) {
	reason := "watermark " + itoa(e.wm) + " >= end " + itoa(end)
	if kind == EmissionCorrected {
		reason = "late event within allowed lateness " + itoa(e.cfg.AllowedLateness)
	}
	e.seq++
	em := Emission{
		Seq:  e.seq,
		Kind: kind,
		Result: WindowResult{
			Key:   key,
			Start: start,
			End:   end,
			Count: count,
		},
	}
	e.history = append(e.history, em)
	e.out = append(e.out, em)
	// emit 只会在窗口触发（或触发后修正）时调用，故两种类型都登记为已触发。
	m := e.fired[key]
	if m == nil {
		m = make(map[int64]int64)
		e.fired[key] = m
	}
	m[start] = count

	rm := e.results[key]
	if rm == nil {
		rm = make(map[int64]WindowResult)
		e.results[key] = rm
	}
	rm[start] = WindowResult{Key: key, Start: start, End: end, Count: count}
	e.logf("output seq=%d kind=%s key=%q window=[%d,%d) count=%d reason=%q",
		e.seq, kind, key, start, end, count, reason)
}

// advance 将水位线推进的影响落地：触发所有到期窗口，并清除超过迟到容限的窗口。
// 遍历按 (键字典序, 起点升序) 进行，保证同一输入序列的输出顺序完全确定。
//
// 对单个窗口：wm >= end 即触发；wm >= end+AllowedLateness 即清除（边界为闭区间，
// 恰好相等的时刻触发/清除立即生效）。AllowedLateness 为 0 时，同一轮内触发后随即清除。
func (e *engine) advance() {
	keys := make([]string, 0, len(e.open))
	for k := range e.open {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		starts := make([]int64, 0, len(e.open[k]))
		for s := range e.open[k] {
			starts = append(starts, s)
		}
		sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
		for _, s := range starts {
			end := s + e.cfg.Size
			if !e.isFired(k, s) && e.wm >= end {
				e.emit(EmissionFired, k, s, end, e.open[k][s])
			}
			if e.isFired(k, s) && e.wm >= end+e.cfg.AllowedLateness {
				e.clearWindow(k, s, end)
			}
		}
	}
}

// runEngine 按输入顺序逐事件处理整批事件。
//
// 每个事件先把水位线推进到 max(旧最大值, 本事件时间)-Delay（天然单调），
// 落地触发与清除，再判定本事件归属：
//   - wm >= 窗口结束时间+容限：丢弃并计数（边界相等即丢弃）；
//   - 窗口已触发且仍在容限内：接受计数并产出修正输出；
//   - 窗口未触发但 wm 已过其结束时间（容限内首次出现的空窗口）：接受并立即触发；
//   - 其余：计入缓冲区，暂不输出。
//
// 返回本批新产出的输出。任一步骤返回 error 时整批作废，调用方不得提交 engine。
func runEngine(e *engine, events []Event) ([]Emission, error) {
	for _, ev := range events {
		if ev.Key == "" {
			e.rejectLogf("event={ts:%d} reason=%q", ev.Timestamp, "empty key")
			return nil, ErrEmptyKey
		}
	}
	for _, ev := range events {
		e.logf("input  key=%q ts=%d", ev.Key, ev.Timestamp)

		if !e.hasMax || ev.Timestamp > e.maxTS {
			e.maxTS = ev.Timestamp
		}
		e.hasMax = true
		e.wm = e.maxTS - e.cfg.Delay
		e.wmSet = true
		e.logf("watermark max-event-ts=%d delay=%d -> watermark=%d",
			e.maxTS, e.cfg.Delay, e.wm)

		e.advance()

		start := windowStart(ev.Timestamp, e.cfg.Size)
		end := start + e.cfg.Size
		gcTime := end + e.cfg.AllowedLateness

		if e.wm >= gcTime {
			e.dropped++
			e.logf("drop   key=%q ts=%d window=[%d,%d) dropped-total=%d reason=\"watermark %d >= gc-time %d\"",
				ev.Key, ev.Timestamp, start, end, e.dropped, e.wm, gcTime)
			continue
		}

		if _, ok := e.open[ev.Key][start]; !ok {
			if !e.createWindow(ev.Key, start) {
				e.rejectLogf("key=%q ts=%d reason=%q open-windows=%d limit=%d",
					ev.Key, ev.Timestamp, "too many open windows", e.openN, e.cfg.MaxOpenWindows)
				return nil, ErrTooManyOpenWindows
			}
		}
		e.open[ev.Key][start]++
		count := e.open[ev.Key][start]

		switch {
		case e.isFired(ev.Key, start):
			e.emit(EmissionCorrected, ev.Key, start, end, count)
		case e.wm >= end:
			// 窗口此前为空、首个事件却晚于触发线到达：仍在容限内，立即触发。
			e.emit(EmissionFired, ev.Key, start, end, count)
		default:
			e.logf("buffer key=%q ts=%d window=[%d,%d) count=%d",
				ev.Key, ev.Timestamp, start, end, count)
		}
	}
	return e.out, nil
}

// itoa 避免在热路径日志中引入 strconv 的格式分歧；仅用于日志拼接。
func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

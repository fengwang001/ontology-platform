package cedu

import "strconv"

// eventKind 为已接受（改变状态）的操作种类。
type eventKind int

const (
	evRegister eventKind = iota + 1
	evCredit
	evCorrect
	evRevoke
)

// event 是一次被接受操作的完整事实日志。按序重放该日志即可精确
// 重建任意历史时刻状态，是历史查询与正确性自检的唯一事实来源。
type event struct {
	kind     eventKind
	at       int // 操作时钟 now
	issue    int // evRegister 的发证日
	recordID string
	cat      Category
	credits  int
	earnedOn int
	org      string
	oldVal   int // evCorrect 的更正前学分数
}

// cycleAgg 为单个周期窗口内“当前有效记录”的学分汇总；撤销/更正即时生效。
// win 为取得日位于周期本体 [start,end) 的量；zon 为取得日位于宽限带
// [end,end+GraceDays) 的量（仅在窗口判定失败时才可能被采纳）。
type cycleAgg struct {
	win [3]int
	zon [3]int
}

func (a *cycleAgg) add(zone bool, cat Category, v int) {
	if zone {
		a.zon[cat] += v
	} else {
		a.win[cat] += v
	}
}

// outcome 为周期终结结果。
type outcome int

const (
	ocLive      outcome = iota // 尚未终结
	ocPass                     // 周期末日本体达标
	ocGracePass                // 宽限期内补足
	ocExpired                  // 宽限届满未达标
)

// cached 为周期窗口判定结果缓存。
type cached struct {
	outcome  outcome
	winMet   bool // 仅周期本体记录（含上周期结转）是否达标
	carryOut int
}

type holder struct {
	id        string
	issueDate int
	lastNow   int
	expired   bool

	// front 为尚未终结的最早周期序号。
	front int

	aggs    map[int]*cycleAgg
	cache   map[int]cached
	records map[string]*Record // dupKey -> record
	byID    map[string]*Record
	seq     int
	events  []event
}

func newHolder(id string, issueDate, now int) *holder {
	h := &holder{
		id:        id,
		issueDate: issueDate,
		lastNow:   now,
		aggs:      map[int]*cycleAgg{},
		cache:     map[int]cached{},
		records:   map[string]*Record{},
		byID:      map[string]*Record{},
	}
	h.aggs[0] = &cycleAgg{}
	return h
}

func (h *holder) agg(k int) *cycleAgg {
	a := h.aggs[k]
	if a == nil {
		a = &cycleAgg{}
		h.aggs[k] = a
	}
	return a
}

func dupKey(org string, earnedOn int, cat Category) string {
	return org + "#" + strconv.Itoa(earnedOn) + "#" + strconv.Itoa(int(cat))
}

func recordID(holderID string, seq int) string {
	return holderID + "-R" + strconv.Itoa(seq)
}

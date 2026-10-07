package ontology

import (
	"sync"
	"time"
)

// DedupVerdict 是去重判定的结论。
type DedupVerdict int

const (
	// VerdictNew 表示事件是首次出现的真实执行。
	VerdictNew DedupVerdict = iota
	// VerdictDuplicate 表示事件是此前已处理事件的重复投递。
	VerdictDuplicate
	// VerdictUndecidable 表示事件标识信息缺失或自相矛盾。
	VerdictUndecidable
)

func (v DedupVerdict) String() string {
	switch v {
	case VerdictNew:
		return "New"
	case VerdictDuplicate:
		return "Duplicate"
	case VerdictUndecidable:
		return "Undecidable"
	default:
		return "Unknown"
	}
}

// DecisionRecord 记录一次去重判定的输入、所依据的事件标识与结论，
// 追加到审计日志中以便事后核查。
type DecisionRecord struct {
	Seq               int64
	Time              time.Time
	EventID           string
	ActionExecutionID string
	// ComparedAgainst 是判定所对照的已处理事件标识（重复判定时为
	// 首次投递的 EventID；新事件判定为空）。
	ComparedAgainst string
	Verdict         DedupVerdict
	Reason          string
}

// Deduper 基于事件身份做 O(1) 去重判定。
//
// 内部使用两张哈希表（按 ActionExecutionID 与按 EventID），判定开销
// 不随累计已处理事件总量线性增长；probes 记录哈希表探测次数，
// 供独立验证该性质（每次判定至多 2 次探测）。
type Deduper struct {
	mu         sync.Mutex
	byActionID map[string]string // actionExecutionID -> 首次投递的 eventID
	byEventID  map[string]string // eventID -> 首次投递的 actionExecutionID
	log        []DecisionRecord
	probes     int64
	seq        int64
	now        func() time.Time
}

// NewDeduper 构造一个去重器。now 用于决策日志时间戳，测试可注入。
func NewDeduper(now func() time.Time) *Deduper {
	if now == nil {
		now = time.Now
	}
	return &Deduper{
		byActionID: make(map[string]string),
		byEventID:  make(map[string]string),
		now:        now,
	}
}

// Classify 判定事件与已处理事件的关系，并记录决策日志。
func (d *Deduper) Classify(evt ChangeEvent) (DedupVerdict, DecisionRecord) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.seq++
	rec := DecisionRecord{
		Seq:               d.seq,
		Time:              d.now(),
		EventID:           evt.EventID,
		ActionExecutionID: evt.ActionExecutionID,
	}

	switch {
	case evt.EventID == "" || evt.ActionExecutionID == "":
		rec.Verdict = VerdictUndecidable
		rec.Reason = "事件标识缺失：EventID 或 ActionExecutionID 为空"
	case d.lookup(evt, &rec):
		// lookup 已填充 rec
	default:
		rec.Verdict = VerdictNew
		rec.Reason = "事件标识首次出现，判定为独立的真实执行"
		d.byActionID[evt.ActionExecutionID] = evt.EventID
		d.byEventID[evt.EventID] = evt.ActionExecutionID
	}

	d.log = append(d.log, rec)
	return rec.Verdict, rec
}

// lookup 在已处理事件索引中查找。返回 true 表示命中了某种历史记录
// （重复或矛盾），并填充 rec。至多 2 次哈希探测，与历史总量无关。
func (d *Deduper) lookup(evt ChangeEvent, rec *DecisionRecord) bool {
	d.probes++
	firstEventID, seenAction := d.byActionID[evt.ActionExecutionID]
	if seenAction {
		rec.ComparedAgainst = firstEventID
		if firstEventID == evt.EventID {
			rec.Verdict = VerdictDuplicate
			rec.Reason = "ActionExecutionID 与 EventID 均与首次投递一致，判定为重复投递"
		} else {
			rec.Verdict = VerdictUndecidable
			rec.Reason = "ActionExecutionID 已出现但 EventID 不一致，标识信息自相矛盾"
		}
		return true
	}
	d.probes++
	firstActionID, seenEvent := d.byEventID[evt.EventID]
	if seenEvent {
		rec.ComparedAgainst = firstActionID
		rec.Verdict = VerdictUndecidable
		rec.Reason = "EventID 已出现但 ActionExecutionID 不一致，标识信息自相矛盾"
		return true
	}
	return false
}

// Probes 返回累计哈希探测次数。处理 n 条事件后该值恒不超过 2n，
// 证明单次判定开销为 O(1)，不随历史总量线性增长。
func (d *Deduper) Probes() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.probes
}

// Decisions 返回决策日志的副本，供事后核查。
func (d *Deduper) Decisions() []DecisionRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]DecisionRecord, len(d.log))
	copy(out, d.log)
	return out
}

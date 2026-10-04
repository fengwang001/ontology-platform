// Package flow 实现带隔离区的逐条数据质量闸门：接收、判定、隔离、
// 惰性重判、人工处置（修复/强制放行/丢弃）与全有或全无的批量接收。
package flow

import (
	"errors"
	"fmt"
	"sync"

	"ontology/quarantine"
	"ontology/rule"
)

var (
	ErrInvalidParam = errors.New("flow: invalid parameter")
	ErrBanned       = errors.New("flow: key is banned")

	// 透传其余包的哨兵，使调用方只 import flow 即可用 errors.Is 区分全部拒绝原因。
	ErrKeyFull      = quarantine.ErrKeyFull
	ErrFull         = quarantine.ErrFull
	ErrNoQueue      = quarantine.ErrNoQueue
	ErrRuleNotFound = rule.ErrRuleNotFound
)

// maxFields 限制单条记录的字段数（1..16）。
const maxFields = 16

// Status 表示一条被接受记录的去向。
type Status int

const (
	StatusPassed      Status = iota + 1 // 直接放行，已进 Out
	StatusQuarantined                   // 成为队首
	StatusHeld                          // 挂到已有队尾，未判定
)

func (s Status) String() string {
	switch s {
	case StatusPassed:
		return "Passed"
	case StatusQuarantined:
		return "Quarantined"
	case StatusHeld:
		return "Held"
	default:
		return "Unknown"
	}
}

// OutRecord 是一条放行记录；Forced=true 表示 Release 强制放行。
type OutRecord struct {
	Seq        int64
	Key        string
	Fields     map[string]int64
	RV         int64
	Violations []string
	Forced     bool
}

// DroppedRecord 是一条被 Discard 移入丢弃日志的记录。
type DroppedRecord struct {
	Seq    int64
	Key    string
	Fields map[string]int64
}

// KeyEvent 是 Out 与 Dropped 按全局发生时间交错后的单个终态事件。
type KeyEvent struct {
	Seq    int64
	Key    string
	Forced bool
	Kind   string // "out" 或 "dropped"
}

// Item 是 IngestBatch 的单条输入。
type Item struct {
	Key    string
	Fields map[string]int64
}

// BatchError 报告整批拒绝时最小下标项的原因。
type BatchError struct {
	Index int
	Err   error
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("flow: batch item %d rejected: %v", e.Index, e.Err)
}

func (e *BatchError) Unwrap() error { return e.Err }

// Gate 是带隔离区的数据质量闸门；单把互斥锁串行全部操作，结果等价某串行序。
type Gate struct {
	mu    sync.Mutex
	rules *rule.Set
	zone  *quarantine.Zone

	seq     int64
	out     []OutRecord
	dropped []DroppedRecord
	events  []KeyEvent
	evals   int64
}

// New 创建闸门，容量参数规则见 quarantine.New。
func New(C, K, Dmax int) (*Gate, error) {
	z, err := quarantine.New(C, K, Dmax)
	if err != nil {
		return nil, mapErr(err)
	}
	return &Gate{rules: rule.NewSet(), zone: z}, nil
}

// Rules 返回内部规则集（PutRule/DropRule 的操作对象）。
func (g *Gate) Rules() *rule.Set { return g.rules }

// PutRule 新增或覆盖规则并使 rv 加 1；规则变更不自动触发重判。
func (g *Gate) PutRule(r rule.Rule) error { return mapErr(g.rules.Put(r)) }

// DropRule 删除规则；不存在时错误满足 errors.Is(err, ErrRuleNotFound)。
func (g *Gate) DropRule(id string) error { return mapErr(g.rules.Drop(id)) }

// RV 返回当前规则版本号。
func (g *Gate) RV() int64 { return g.rules.RV() }

func validMap(m map[string]int64) bool {
	if len(m) < 1 || len(m) > maxFields {
		return false
	}
	for name := range m {
		if name == "" {
			return false
		}
	}
	return true
}

func cloneFields(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneIDs(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, rule.ErrInvalidParam) || errors.Is(err, quarantine.ErrInvalidParam):
		return ErrInvalidParam
	default:
		return err
	}
}

// evaluate 在调用方已持锁的前提下做一次判定并计数。
func (g *Gate) evaluate(fields map[string]int64) rule.Verdict {
	g.evals++
	return g.rules.Eval(fields)
}

func (g *Gate) emitOut(e quarantine.Entry, rv int64, violations []string, forced bool) {
	g.out = append(g.out, OutRecord{
		Seq:        e.Seq,
		Key:        e.Key,
		Fields:     e.Fields,
		RV:         rv,
		Violations: cloneIDs(violations),
		Forced:     forced,
	})
	g.events = append(g.events, KeyEvent{Seq: e.Seq, Key: e.Key, Forced: forced, Kind: "out"})
}

// Ingest 接收一条记录并返回去向。拒绝优先级：
// 参数非法 > ErrBanned > ErrKeyFull > ErrFull；被拒记录不占 seq。
func (g *Gate) Ingest(key string, fields map[string]int64) (Status, error) {
	if key == "" || !validMap(fields) {
		return 0, ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.zone.Banned(key) {
		return 0, ErrBanned
	}

	// 已有非空队列：不判定直接挂尾（保序，evals 不增加）。
	if g.zone.Has(key) {
		g.seq++
		err := g.zone.Append(key, quarantine.Entry{
			Seq:    g.seq,
			Key:    key,
			Fields: cloneFields(fields),
			State:  quarantine.Held,
		})
		if err != nil {
			g.seq--
			return 0, err
		}
		return StatusHeld, nil
	}

	// 无队列：判定一次。通过则直接放行，隔离区已满也不受影响；不通过才占用容量。
	capC, _ := g.zone.Cap()
	beforeEvals := g.evals
	v := g.evaluate(fields)
	if !v.HasBlock {
		g.seq++
		g.emitOut(quarantine.Entry{Seq: g.seq, Key: key, Fields: cloneFields(fields)},
			v.RV, v.Violations, false)
		return StatusPassed, nil
	}
	// 被拒绝的操作不改变任何状态（含 evals）。
	if g.zone.Total() >= capC {
		g.evals = beforeEvals
		return 0, ErrFull
	}

	g.seq++
	err := g.zone.Append(key, quarantine.Entry{
		Seq:        g.seq,
		Key:        key,
		Fields:     cloneFields(fields),
		RV:         v.RV,
		Violations: cloneIDs(v.Violations),
		State:      quarantine.Quarantined,
	})
	if err != nil {
		g.seq--
		return 0, err
	}
	return StatusQuarantined, nil
}

// reevalLocked 从队首起逐条用当前规则判定：通过者放行出队，
// 遇到首个不通过者停下并把其快照更新为 Quarantined。调用方须持锁。
func (g *Gate) reevalLocked(key string) (int, error) {
	if g.zone.Front(key) == nil {
		return 0, quarantine.ErrNoQueue
	}
	released := 0
	for g.zone.Front(key) != nil {
		front := g.zone.Front(key)
		v := g.evaluate(front.Fields)
		if v.HasBlock {
			front.State = quarantine.Quarantined
			front.RV = v.RV
			front.Violations = cloneIDs(v.Violations)
			break
		}
		e, _ := g.zone.PopFront(key)
		g.emitOut(e, v.RV, v.Violations, false)
		released++
	}
	return released, nil
}

// Reeval 对单个键重判；无队列时返回 ErrNoQueue。
func (g *Gate) Reeval(key string) (int, error) {
	if key == "" {
		return 0, ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	n, err := g.reevalLocked(key)
	return n, mapErr(err)
}

// ReevalAll 按键字节序逐个重判，返回放行总数。
func (g *Gate) ReevalAll() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	total := 0
	for _, key := range g.zone.Keys() {
		n, _ := g.reevalLocked(key)
		total += n
	}
	return total
}

// Fix 把 patch 合并覆盖进队首记录，随后从该队首起隐式重判。
// 合并后字段数不得超过 16；超限在修改前拒绝，状态不变。
func (g *Gate) Fix(key string, patch map[string]int64) (int, error) {
	if key == "" || !validMap(patch) {
		return 0, ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	front := g.zone.Front(key)
	if front == nil {
		return 0, ErrNoQueue
	}
	added := 0
	for name := range patch {
		if _, ok := front.Fields[name]; !ok {
			added++
		}
	}
	if len(front.Fields)+added > maxFields {
		return 0, ErrInvalidParam
	}
	for name, val := range patch {
		front.Fields[name] = val
	}
	n, err := g.reevalLocked(key)
	return n, mapErr(err)
}

// Release 不经判定强制放行队首（Forced=true，违规列表取最近一次判定快照），
// 随后对新队首隐式重判。
func (g *Gate) Release(key string) (int, error) {
	if key == "" {
		return 0, ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.zone.Front(key) == nil {
		return 0, ErrNoQueue
	}
	e, _ := g.zone.PopFront(key)
	g.emitOut(e, e.RV, e.Violations, true)
	n, _ := g.reevalLocked(key)
	return n, nil
}

// Discard 把队首移入丢弃日志并记账；累计达 Dmax 即封禁该键，
// 随后对新队首隐式重判。封禁只挡此后的新 Ingest，不影响存量处置。
func (g *Gate) Discard(key string) (int, error) {
	if key == "" {
		return 0, ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.zone.Front(key) == nil {
		return 0, ErrNoQueue
	}
	e, _ := g.zone.PopFront(key)
	g.zone.MarkDiscarded(key)
	g.dropped = append(g.dropped, DroppedRecord{Seq: e.Seq, Key: e.Key, Fields: e.Fields})
	g.events = append(g.events, KeyEvent{Seq: e.Seq, Key: e.Key, Kind: "dropped"})
	n, _ := g.reevalLocked(key)
	return n, nil
}

type plan struct {
	status Status
	rv     int64
	viol   []string
}

// IngestBatch 全有或全无地接收 1..100 条记录。按批内次序逐条模拟
// （同键前一条入队会使后一条成为 Held，容量逐条累计）；任一条会被拒绝
// 则整批拒绝，返回最小下标及其原因，不占 seq、不增 evals、不改任何状态。
func (g *Gate) IngestBatch(items []Item) ([]Status, error) {
	if len(items) < 1 || len(items) > 100 {
		return nil, ErrInvalidParam
	}
	for i, it := range items {
		if it.Key == "" || !validMap(it.Fields) {
			return nil, &BatchError{Index: i, Err: ErrInvalidParam}
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	capC, capK := g.zone.Cap()
	plans := make([]plan, len(items))
	simLen := map[string]int{} // 模拟期间每键新增条数
	simTotal := g.zone.Total()
	evalPlans := 0 // 批次被接受时需要计入 evals 的判定条数（Passed/Quarantined 各 1 次）

	for i, it := range items {
		baseLen := g.zone.Len(it.Key)
		switch {
		case g.zone.Banned(it.Key):
			return nil, &BatchError{Index: i, Err: ErrBanned}
		case baseLen+simLen[it.Key] > 0:
			// 挂尾：不判定；拒绝次序 ErrKeyFull 先于 ErrFull。
			if baseLen+simLen[it.Key] >= capK {
				return nil, &BatchError{Index: i, Err: ErrKeyFull}
			}
			if simTotal >= capC {
				return nil, &BatchError{Index: i, Err: ErrFull}
			}
			plans[i] = plan{status: StatusHeld}
			simLen[it.Key]++
			simTotal++
		default:
			// 真实区与影子区都无队列：干跑判定（不计 evals，提交时复用快照）。
			v := g.rules.Eval(it.Fields)
			evalPlans++
			if !v.HasBlock {
				plans[i] = plan{status: StatusPassed, rv: v.RV, viol: cloneIDs(v.Violations)}
				continue
			}
			if simTotal >= capC {
				return nil, &BatchError{Index: i, Err: ErrFull}
			}
			plans[i] = plan{status: StatusQuarantined, rv: v.RV, viol: cloneIDs(v.Violations)}
			simLen[it.Key]++
			simTotal++
		}
	}

	// 全部可接受：按序提交。全程持锁，模拟后规则集不可能变更，快照直接复用。
	g.evals += int64(evalPlans)
	statuses := make([]Status, len(items))
	for i, it := range items {
		g.seq++
		p := plans[i]
		statuses[i] = p.status
		e := quarantine.Entry{Seq: g.seq, Key: it.Key, Fields: cloneFields(it.Fields)}
		switch p.status {
		case StatusPassed:
			g.emitOut(e, p.rv, p.viol, false)
		case StatusHeld:
			e.State = quarantine.Held
			_ = g.zone.Append(it.Key, e)
		case StatusQuarantined:
			e.RV = p.rv
			e.Violations = cloneIDs(p.viol)
			e.State = quarantine.Quarantined
			_ = g.zone.Append(it.Key, e)
		}
	}
	return statuses, nil
}

// Evals 返回累计判定次数（每次 rules.Eval 计 1）。
func (g *Gate) Evals() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.evals
}

// NextSeq 返回下一个将分配的接收序号（即当前已接受记录数）。
func (g *Gate) NextSeq() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.seq + 1
}

// Quarantined 返回隔离区当前记录总数。
func (g *Gate) Quarantined() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.zone.Total()
}

// QueueLen 返回某键队列长度。
func (g *Gate) QueueLen(key string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.zone.Len(key)
}

// Banned 报告某键是否已被封禁。
func (g *Gate) Banned(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.zone.Banned(key)
}

// Front 返回队首记录快照与 ok；仅供观测（快照与内部状态解耦）。
func (g *Gate) Front(key string) (quarantine.Entry, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.zone.Front(key)
	if e == nil {
		return quarantine.Entry{}, false
	}
	snap := *e
	snap.Fields = cloneFields(e.Fields)
	snap.Violations = cloneIDs(e.Violations)
	return snap, true
}

// Out 返回放行日志的拷贝。Out 内全局次序为各操作实际放行次序，seq 不必单调；
// 但同一键在 Out∪Dropped 中合并后的 seq 严格递增。
func (g *Gate) Out() []OutRecord {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]OutRecord, len(g.out))
	for i, r := range g.out {
		r.Fields = cloneFields(r.Fields)
		r.Violations = cloneIDs(r.Violations)
		out[i] = r
	}
	return out
}

// Dropped 返回丢弃日志的拷贝。
func (g *Gate) Dropped() []DroppedRecord {
	g.mu.Lock()
	defer g.mu.Unlock()
	dr := make([]DroppedRecord, len(g.dropped))
	for i, r := range g.dropped {
		r.Fields = cloneFields(r.Fields)
		dr[i] = r
	}
	return dr
}

// Events 返回 Out 与 Dropped 按全局发生时间交错后的终态事件流拷贝。
func (g *Gate) Events() []KeyEvent {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]KeyEvent(nil), g.events...)
}

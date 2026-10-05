// Package flow 实现带隔离区的逐条数据质量闸门与按键保序的接收放行管线。
package flow

import (
	"fmt"
	"sync"

	"ontology/quarantine"
	"ontology/rule"
)

// 各类哨兵错误的统一出口，均可用 errors.Is 区分。
var (
	ErrInvalid = rule.ErrInvalid
	ErrNoRule  = rule.ErrNoRule
	ErrKeyFull = quarantine.ErrKeyFull
	ErrFull    = quarantine.ErrFull
	ErrBanned  = quarantine.ErrBanned
	ErrNoQueue = quarantine.ErrNoQueue
)

// OutEntry 为全局输出日志条目。
type OutEntry struct {
	Seq        uint64
	Key        string
	Violations []string // 判定时违规列表；Forced 时为最近一次判定的全部违规
	RV         uint64   // 判定时的规则版本
	Forced     bool     // 是否经 Release 强制放行
}

// Item 为 IngestBatch 的一条输入。
type Item struct {
	Key    string
	Fields map[string]int64
}

// BatchError 报告整批拒绝时最小失败下标及其原因，可用 errors.Is 匹配原因。
type BatchError struct {
	Index int
	Err   error
}

func (e *BatchError) Error() string { return fmt.Sprintf("flow: batch item %d: %v", e.Index, e.Err) }
func (e *BatchError) Unwrap() error { return e.Err }

// Gate 为数据质量闸门。所有方法可并发调用，效果等价于某个串行顺序。
type Gate struct {
	mu    sync.Mutex
	rules *rule.Store
	zone  *quarantine.Zone
	out   []OutEntry
	seq   uint64
	evals int64
}

// NewGate 构造闸门：C 为隔离区总容量（1..1e5），K 为每键队列上限（1..C），
// Dmax 为封禁阈值（1..1000）。
func NewGate(c, k, dmax int) (*Gate, error) {
	if c < 1 || c > 100000 {
		return nil, fmt.Errorf("%w: C=%d out of [1,100000]", ErrInvalid, c)
	}
	if k < 1 || k > c {
		return nil, fmt.Errorf("%w: K=%d out of [1,%d]", ErrInvalid, k, c)
	}
	if dmax < 1 || dmax > 1000 {
		return nil, fmt.Errorf("%w: Dmax=%d out of [1,1000]", ErrInvalid, dmax)
	}
	z, err := quarantine.New(c, k, dmax)
	if err != nil {
		return nil, err
	}
	return &Gate{rules: rule.NewStore(), zone: z}, nil
}

func validateRecord(key string, fields map[string]int64) error {
	if key == "" {
		return fmt.Errorf("%w: empty key", ErrInvalid)
	}
	if len(fields) < 1 || len(fields) > 16 {
		return fmt.Errorf("%w: %d fields out of [1,16]", ErrInvalid, len(fields))
	}
	return nil
}

func copyFields(fields map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(fields))
	for f, v := range fields {
		out[f] = v
	}
	return out
}

// PutRule 按 id 新增或覆盖规则；被接受的变更使 rv 加 1。
func (g *Gate) PutRule(id, field string, lo, hi int64, sev rule.Severity) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.rules.Put(rule.Rule{ID: id, Field: field, Lo: lo, Hi: hi, Severity: sev})
}

// DropRule 按 id 删除规则；规则不存在时报 ErrNoRule。
func (g *Gate) DropRule(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.rules.Drop(id)
}

// RV 返回当前规则版本。
func (g *Gate) RV() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.rules.RV()
}

// Evals 返回累计判定次数。
func (g *Gate) Evals() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.evals
}

// Out 返回全局输出日志的副本。
func (g *Gate) Out() []OutEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]OutEntry(nil), g.out...)
}

// Dropped 返回丢弃日志的副本。
func (g *Gate) Dropped() []quarantine.DropEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.zone.Dropped()
}

// Queued 按键字节序、队内保序返回隔离区全部记录的副本。
func (g *Gate) Queued() []quarantine.Record {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.zone.Snapshot()
}

// eval 做一次判定并计数。
func (g *Gate) eval(fields map[string]int64) []string {
	g.evals++
	return g.rules.Eval(fields)
}

// Ingest 接收一条记录：同键有队列则直接 Held 入队，否则判定后放行或隔离。
// 拒绝次序：参数非法 > ErrBanned > ErrKeyFull > ErrFull；被拒绝不占 seq。
func (g *Gate) Ingest(key string, fields map[string]int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.ingestLocked(key, fields)
}

func (g *Gate) ingestLocked(key string, fields map[string]int64) error {
	if err := validateRecord(key, fields); err != nil {
		return err
	}
	if g.zone.Banned(key) {
		return fmt.Errorf("%w: %q", ErrBanned, key)
	}
	if g.zone.HasQueue(key) {
		if err := g.zone.CheckAdd(key); err != nil {
			return err
		}
		g.seq++
		g.zone.Push(&quarantine.Record{
			Seq:    g.seq,
			Key:    key,
			Fields: copyFields(fields),
			Status: quarantine.Held,
		})
		return nil
	}
	violations := g.eval(fields)
	if g.rules.HasBlock(violations) {
		if err := g.zone.CheckAdd(key); err != nil {
			return err
		}
		g.seq++
		g.zone.Push(&quarantine.Record{
			Seq:        g.seq,
			Key:        key,
			Fields:     copyFields(fields),
			Status:     quarantine.Quarantined,
			RV:         g.rules.RV(),
			Violations: violations,
		})
		return nil
	}
	g.seq++
	g.out = append(g.out, OutEntry{
		Seq:        g.seq,
		Key:        key,
		Violations: violations,
		RV:         g.rules.RV(),
	})
	return nil
}

// Reeval 从队首起逐条重判，通过者放行，遇首个不通过者停下；返回放行条数。
func (g *Gate) Reeval(key string) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.reevalLocked(key)
}

func (g *Gate) reevalLocked(key string) (int, error) {
	if !g.zone.HasQueue(key) {
		return 0, fmt.Errorf("%w: %q", ErrNoQueue, key)
	}
	released := 0
	for g.zone.HasQueue(key) {
		front := g.zone.Front(key)
		violations := g.eval(front.Fields)
		if g.rules.HasBlock(violations) {
			front.Status = quarantine.Quarantined
			front.RV = g.rules.RV()
			front.Violations = violations
			break
		}
		g.zone.Pop(key)
		g.out = append(g.out, OutEntry{
			Seq:        front.Seq,
			Key:        key,
			Violations: violations,
			RV:         g.rules.RV(),
		})
		released++
	}
	return released, nil
}

// ReevalAll 按键字节序对每个有队列的键做 Reeval，返回放行总数。
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

// Fix 把 patch 的字段合并覆盖进队首记录，然后对该键隐式执行一次 Reeval。
func (g *Gate) Fix(key string, patch map[string]int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(patch) == 0 {
		return fmt.Errorf("%w: empty patch", ErrInvalid)
	}
	if !g.zone.HasQueue(key) {
		return fmt.Errorf("%w: %q", ErrNoQueue, key)
	}
	front := g.zone.Front(key)
	merged := copyFields(front.Fields)
	for f, v := range patch {
		merged[f] = v
	}
	if len(merged) > 16 {
		return fmt.Errorf("%w: merged %d fields out of [1,16]", ErrInvalid, len(merged))
	}
	front.Fields = merged
	_, _ = g.reevalLocked(key)
	return nil
}

// Release 不经判定强制放行队首（Forced=true，违规取最近一次判定的全部违规），
// 然后对该键隐式执行一次 Reeval。
func (g *Gate) Release(key string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.zone.HasQueue(key) {
		return fmt.Errorf("%w: %q", ErrNoQueue, key)
	}
	front := g.zone.Pop(key)
	g.out = append(g.out, OutEntry{
		Seq:        front.Seq,
		Key:        key,
		Violations: append([]string(nil), front.Violations...),
		RV:         front.RV,
		Forced:     true,
	})
	if g.zone.HasQueue(key) {
		_, _ = g.reevalLocked(key)
	}
	return nil
}

// Discard 把队首移入丢弃日志并累计丢弃账（达 Dmax 封禁该键），
// 然后对该键隐式执行一次 Reeval。
func (g *Gate) Discard(key string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.zone.HasQueue(key) {
		return fmt.Errorf("%w: %q", ErrNoQueue, key)
	}
	g.zone.Discard(key)
	if g.zone.HasQueue(key) {
		_, _ = g.reevalLocked(key)
	}
	return nil
}

// IngestBatch 全有或全无地接收 1..100 条记录：任一失败则整批拒绝，
// 报最小下标及其原因，不占 seq、不改任何状态。
func (g *Gate) IngestBatch(items []Item) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(items) < 1 || len(items) > 100 {
		return fmt.Errorf("%w: batch size %d out of [1,100]", ErrInvalid, len(items))
	}
	added := make(map[string]int, len(items))
	addedTotal := 0
	for i, it := range items {
		queued, err := g.dryIngest(it, added, addedTotal)
		if err != nil {
			return &BatchError{Index: i, Err: err}
		}
		if queued {
			added[it.Key]++
			addedTotal++
		}
	}
	for _, it := range items {
		if err := g.ingestLocked(it.Key, it.Fields); err != nil {
			panic(fmt.Sprintf("flow: batch apply diverged from dry run: %v", err))
		}
	}
	return nil
}

// dryIngest 在真实状态叠加批内已模拟入队数（added/addedTotal）上只读模拟一条
// Ingest，不判定计数、不改状态；返回该条是否会入队。
func (g *Gate) dryIngest(it Item, added map[string]int, addedTotal int) (bool, error) {
	if err := validateRecord(it.Key, it.Fields); err != nil {
		return false, err
	}
	if g.zone.Banned(it.Key) {
		return false, fmt.Errorf("%w: %q", ErrBanned, it.Key)
	}
	if qlen := g.zone.Len(it.Key) + added[it.Key]; qlen > 0 {
		if qlen >= g.zone.K() {
			return false, fmt.Errorf("%w: key %q", ErrKeyFull, it.Key)
		}
		if g.zone.Total()+addedTotal >= g.zone.C() {
			return false, fmt.Errorf("%w: key %q", ErrFull, it.Key)
		}
		return true, nil
	}
	violations := g.rules.Eval(it.Fields)
	if g.rules.HasBlock(violations) {
		if g.zone.Total()+addedTotal >= g.zone.C() {
			return false, fmt.Errorf("%w: key %q", ErrFull, it.Key)
		}
		return true, nil
	}
	return false, nil
}

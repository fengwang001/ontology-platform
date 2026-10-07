package ontology

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Phase 枚举批量执行管线与恢复管线中可注入中断的阶段。
// 故障注入只发生在相邻阶段之间；阶段内部的原子性由记录级 CRC 保证
// （撕裂的记录在加载时被截断，等价于该阶段未发生）。
type Phase int

const (
	PhaseBeginControl Phase = iota // 写 CONTROL（批次出生证明）并落盘
	PhaseBeginJournal              // 追加 BEGIN 记录（写集 + 全部变更）
	PhaseJournalMut                // 追加第 Seq 条 MUT 记录并落盘（WAL：先日志后数据）
	PhaseApply                     // 应用第 Seq 条变更到实例存储并落盘
	PhasePreCommit                 // 提交前检查点（正常回退的决策点）
	PhaseCommit                    // 追加 COMMIT 记录并落盘 —— 唯一“已生效”判定时刻
	PhaseCheckpoint                // 实例存储检查点（快照重写 + 增量清理）
	PhaseClose                     // DONE 记录、CONTROL 复位、状态落盘、段删除、解锁
	PhaseDone                      // 虚拟阶段：管线已全部完成
)

// 恢复管线的阶段。
const (
	PhaseRScan     Phase = 100 + iota // 读取 CONTROL 并扫描活跃批次段
	PhaseRFix                         // 对第 Seq 条变更执行幂等撤销/重做
	PhaseRFinalize                    // 检查点、状态落盘、CONTROL 复位、段删除
	PhaseRDone                        // 虚拟阶段：恢复已全部完成
)

// StagePoint 标识管线中一个可注入中断的位置。
type StagePoint struct {
	Phase Phase
	Seq   int // 对 JournalMut/Apply/RFix 有效，表示变更下标
}

func (p StagePoint) String() string {
	names := map[Phase]string{
		PhaseBeginControl: "BeginControl", PhaseBeginJournal: "BeginJournal",
		PhaseJournalMut: "JournalMut", PhaseApply: "Apply",
		PhasePreCommit: "PreCommit", PhaseCommit: "Commit",
		PhaseCheckpoint: "Checkpoint", PhaseClose: "Close", PhaseDone: "Done",
		PhaseRScan: "RScan", PhaseRFix: "RFix",
		PhaseRFinalize: "RFinalize", PhaseRDone: "RDone",
	}
	if p.Phase == PhaseJournalMut || p.Phase == PhaseApply || p.Phase == PhaseRFix {
		return fmt.Sprintf("%s[%d]", names[p.Phase], p.Seq)
	}
	return names[p.Phase]
}

// BatchPoints 列出含 nMut 条变更的批次在执行管线中的全部中断注入点（按序）。
// 在点 P 注入中断意味着 P 之前的所有阶段已完成、P 及其后均未发生。
func BatchPoints(nMut int) []StagePoint {
	pts := []StagePoint{{Phase: PhaseBeginControl}, {Phase: PhaseBeginJournal}}
	for i := 0; i < nMut; i++ {
		pts = append(pts, StagePoint{Phase: PhaseJournalMut, Seq: i})
	}
	for i := 0; i < nMut; i++ {
		pts = append(pts, StagePoint{Phase: PhaseApply, Seq: i})
	}
	return append(pts,
		StagePoint{Phase: PhasePreCommit},
		StagePoint{Phase: PhaseCommit},
		StagePoint{Phase: PhaseCheckpoint},
		StagePoint{Phase: PhaseClose},
		StagePoint{Phase: PhaseDone},
	)
}

// RecoveryPoints 列出含 nMut 条变更的批次在恢复管线中的全部中断注入点（按序）。
func RecoveryPoints(nMut int) []StagePoint {
	pts := []StagePoint{{Phase: PhaseRScan}}
	for i := 0; i < nMut; i++ {
		pts = append(pts, StagePoint{Phase: PhaseRFix, Seq: i})
	}
	return append(pts, StagePoint{Phase: PhaseRFinalize}, StagePoint{Phase: PhaseRDone})
}

// controlFile 是 CONTROL 记录的文件名。
const controlFile = "control.json"

// controlState 是 CONTROL 记录的内容：固定大小，与历史批次总数无关。
type controlState struct {
	NextBatch   BatchID `json:"next_batch"`
	ActiveBatch BatchID `json:"active_batch"` // 0 表示无未决批次
	// ActiveWriteSet 是未决批次的写集，随 CONTROL 一起持久，
	// 保证重启后、恢复完成前写集内实例始终处于不确定拒绝状态。
	ActiveWriteSet []InstanceID `json:"active_write_set,omitempty"`
}

// BatchOptions 控制批次执行行为（测试用）。
type BatchOptions struct {
	// AbortAtPreCommit 在 PreCommit 阶段主动回退（模拟校验失败等正常回退）。
	AbortAtPreCommit bool
}

// crashSignal 是故障注入触发崩溃时的 panic 值。
type crashSignal struct{}

// Engine 是本体批量更新引擎。
type Engine struct {
	disk  Disk
	audit *AuditLogger
	// hook 是故障注入钩子：返回 true 表示在该阶段点之后立即崩溃。
	// 仅用于测试；生产为 nil。
	hook func(StagePoint) bool

	mu         sync.Mutex
	crashed    bool
	crashPoint StagePoint
	ctrl       controlState
	store      *store
	locked     map[InstanceID]BatchID // 未决批次写集 → 批次
	inFlight   map[BatchID]bool       // 本进程内已开始未结束的批次
}

// NewEngine 在给定磁盘上打开（或初始化）引擎。
// 若存在未决批次，引擎进入待恢复状态：写集内实例的读写被拒绝，
// 直到 Recover 完成。hook 与 audit 可为 nil。
func NewEngine(disk Disk, audit *AuditLogger, hook func(StagePoint) bool) (*Engine, error) {
	e := &Engine{
		disk:     disk,
		audit:    audit,
		hook:     hook,
		locked:   make(map[InstanceID]BatchID),
		inFlight: make(map[BatchID]bool),
	}
	data, err := disk.ReadFile(controlFile)
	switch {
	case err == ErrNotFound:
		e.ctrl = controlState{NextBatch: 1}
		e.writeControl()
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(data, &e.ctrl); err != nil {
			return nil, fmt.Errorf("ontology: corrupt control file: %w", err)
		}
	}
	st, err := loadStore(disk)
	if err != nil {
		return nil, err
	}
	e.store = st
	// 存在未决批次：从 CONTROL 重建写集锁，拒绝不确定实例的读写。
	if e.ctrl.ActiveBatch != 0 {
		for _, id := range e.ctrl.ActiveWriteSet {
			e.locked[id] = e.ctrl.ActiveBatch
		}
	}
	return e, nil
}

// point 在执行管线阶段点处触发故障注入检查。
func (e *Engine) point(p StagePoint) {
	if e.hook != nil && e.hook(p) {
		e.crashPoint = p
		e.crash()
	}
}

// crash 执行一次模拟崩溃：丢弃未落盘数据并冻结引擎。
func (e *Engine) crash() {
	e.logCrash(e.crashPoint)
	e.crashed = true
	e.disk.Crash()
	panic(crashSignal{})
}

// guard 检查引擎是否仍可用。
func (e *Engine) guard() error {
	if e.crashed {
		return ErrEngineCrashed
	}
	return nil
}

func (e *Engine) writeControl() {
	data, _ := json.Marshal(e.ctrl)
	e.disk.WriteFile(controlFile, data)
	e.disk.Sync(controlFile)
}

func statusFile(id BatchID) string {
	return fmt.Sprintf("status/%08d.json", uint64(id))
}

func (e *Engine) writeStatus(id BatchID, s BatchStatus) {
	data, _ := json.Marshal(struct {
		Status BatchStatus `json:"status"`
	}{s})
	f := statusFile(id)
	e.disk.WriteFile(f, data)
	e.disk.Sync(f)
}

// BatchStatus 查询批次状态。
func (e *Engine) BatchStatus(id BatchID) (BatchStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.guard(); err != nil {
		return "", err
	}
	if e.inFlight[id] || e.ctrl.ActiveBatch == id {
		return StatusInFlight, nil
	}
	data, err := e.disk.ReadFile(statusFile(id))
	if err == ErrNotFound {
		return "", ErrBatchNotFound
	}
	if err != nil {
		return "", err
	}
	var rec struct {
		Status BatchStatus `json:"status"`
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		return "", err
	}
	return rec.Status, nil
}

// Read 读取实例；若实例处于未决批次写集中则拒绝。
func (e *Engine) Read(id InstanceID) (Instance, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.guard(); err != nil {
		return Instance{}, err
	}
	if _, ok := e.locked[id]; ok {
		return Instance{}, ErrInstanceUncertain
	}
	in, ok := e.store.get(id)
	if !ok {
		return Instance{}, ErrInstanceNotFound
	}
	return in, nil
}

// Write 对单个实例执行即时持久的写入（创建或更新），版本号递增。
// 未决批次写集内的实例会被拒绝。
func (e *Engine) Write(id InstanceID, props map[string]string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.guard(); err != nil {
		return err
	}
	if _, ok := e.locked[id]; ok {
		return ErrInstanceUncertain
	}
	in, ok := e.store.get(id)
	if !ok {
		in = Instance{ID: id, Props: map[string]string{}}
	}
	in.Version++
	for k, v := range props {
		in.Props[k] = v
	}
	e.store.apply(in)
	e.store.syncDelta()
	return nil
}

// Snapshot 返回当前全部实例的确定性视图。
// 这是面向测试与核验的内省接口，绕过未决锁。
func (e *Engine) Snapshot() []Instance {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.store.snapshot()
}

// RunBatch 执行一次批量更新，返回批次号与终态。
// 若故障注入触发崩溃，返回 ErrEngineCrashed，此后本引擎不可用，
// 需在同一磁盘上重新 NewEngine 并调用 Recover。
func (e *Engine) RunBatch(muts []Mutation, opts BatchOptions) (id BatchID, status BatchStatus, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(crashSignal); ok {
				err = ErrEngineCrashed
				return
			}
			panic(r)
		}
	}()
	if err := e.guard(); err != nil {
		return 0, "", err
	}
	for _, m := range muts {
		if _, ok := e.locked[m.Instance]; ok {
			return 0, "", ErrInstanceUncertain
		}
	}
	seen := make(map[InstanceID]bool, len(muts))
	for _, m := range muts {
		if seen[m.Instance] {
			return 0, "", ErrDuplicateInstance
		}
		seen[m.Instance] = true
	}

	id = e.ctrl.NextBatch
	e.inFlight[id] = true
	defer func() {
		if status.Terminal() {
			delete(e.inFlight, id)
		}
	}()

	// 前置校验：批量变更只允许作用于已存在的实例。
	// 校验失败的批次不发生任何状态变化，直接以正常回退终态结束。
	for _, m := range muts {
		if _, ok := e.store.get(m.Instance); !ok {
			e.ctrl.NextBatch = id + 1
			e.writeControl()
			e.writeStatus(id, StatusRolledBack)
			status = StatusRolledBack
			e.logBatchFinal(id, status)
			return id, status, nil
		}
	}

	// 阶段 BeginControl：登记 CONTROL（批次出生证明）。
	e.point(StagePoint{Phase: PhaseBeginControl})
	e.ctrl.NextBatch = id + 1
	e.ctrl.ActiveBatch = id
	e.ctrl.ActiveWriteSet = nil
	for _, m := range muts {
		e.ctrl.ActiveWriteSet = append(e.ctrl.ActiveWriteSet, m.Instance)
	}
	e.writeControl()

	// 阶段 BeginJournal：BEGIN 记录携带完整写集与变更。
	e.point(StagePoint{Phase: PhaseBeginJournal})
	jw := newJournalWriter(e.disk, id)
	jw.append(&journalRecord{Type: recBegin, Batch: id, Muts: muts})
	for _, m := range muts {
		e.locked[m.Instance] = id
	}

	// 阶段 JournalMut[i]：捕获前像/后像，追加 MUT 记录并落盘（WAL）。
	type plan struct {
		mut    Mutation
		before Instance
	}
	plans := make([]plan, 0, len(muts))
	for i, m := range muts {
		e.point(StagePoint{Phase: PhaseJournalMut, Seq: i})
		before, _ := e.store.get(m.Instance)
		plans = append(plans, plan{mut: m, before: before})
		after := before
		after.Version = before.Version + 1
		after.LastBatch = id
		after.Props = mergeProps(before.Props, m.Props)
		jw.append(&journalRecord{
			Type: recMut, Batch: id, Seq: i, Instance: m.Instance,
			Before: imageOf(before), After: imageOf(after),
		})
		jw.sync()
	}

	// 阶段 Apply[i]：应用变更到实例存储（steal：提交前即可落盘）。
	for i, p := range plans {
		e.point(StagePoint{Phase: PhaseApply, Seq: i})
		after := p.before
		after.ID = p.mut.Instance
		after.Version = p.before.Version + 1
		after.LastBatch = id
		after.Props = mergeProps(p.before.Props, p.mut.Props)
		e.store.apply(after)
		e.store.syncDelta()
	}

	// 阶段 PreCommit：正常回退决策点。
	e.point(StagePoint{Phase: PhasePreCommit})
	if opts.AbortAtPreCommit {
		for i := len(plans) - 1; i >= 0; i-- {
			cur, ok := e.store.get(plans[i].mut.Instance)
			if ok && cur.LastBatch == id {
				e.store.apply(plans[i].before)
				e.store.syncDelta()
			}
		}
		e.store.checkpoint()
		e.ctrl.ActiveBatch = 0
		e.ctrl.ActiveWriteSet = nil
		e.writeControl()
		e.writeStatus(id, StatusRolledBack)
		e.disk.Delete(segmentFile(id))
		for _, m := range muts {
			delete(e.locked, m.Instance)
		}
		status = StatusRolledBack
		e.logBatchFinal(id, status)
		return id, status, nil
	}

	// 阶段 Commit：追加 COMMIT 记录并落盘。
	// 这是“已被外部认定为已生效”的唯一判定时刻：
	// 此前任意中断 → 恢复为未生效；此后任意中断 → 恢复为已生效。
	e.point(StagePoint{Phase: PhaseCommit})
	jw.append(&journalRecord{Type: recCommit, Batch: id})
	jw.sync()

	// 阶段 Checkpoint：实例存储检查点。
	e.point(StagePoint{Phase: PhaseCheckpoint})
	e.store.checkpoint()

	// 阶段 Close：收尾（DONE、CONTROL 复位、状态落盘、段删除、解锁）。
	e.point(StagePoint{Phase: PhaseClose})
	jw.append(&journalRecord{Type: recDone, Batch: id})
	jw.sync()
	e.ctrl.ActiveBatch = 0
	e.ctrl.ActiveWriteSet = nil
	e.writeControl()
	e.writeStatus(id, StatusCommitted)
	e.disk.Delete(segmentFile(id))
	for _, m := range muts {
		delete(e.locked, m.Instance)
	}
	status = StatusCommitted
	e.logBatchFinal(id, status)
	return id, status, nil
}

func (e *Engine) logBatchFinal(id BatchID, s BatchStatus) {
	if e.audit != nil {
		e.audit.Log(AuditEvent{
			Kind:   AuditBatchFinal,
			Batch:  id,
			Status: s,
		})
	}
}

func mergeProps(base, delta map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(delta))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range delta {
		out[k] = v
	}
	return out
}

func imageOf(in Instance) image {
	return image{
		Version:   in.Version,
		LastBatch: in.LastBatch,
		Props:     mergeProps(in.Props, nil),
		Exists:    true,
	}
}

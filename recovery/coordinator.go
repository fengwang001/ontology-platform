package recovery

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Coordinator 是快照损坏修复与动作重放的原子性协调器。
//
// 一把排他锁覆盖“校验—版本检查—原子重放—发布结果”全流程，
// 修复进行中新到达的动作要么在修复前、要么在修复后完整生效，
// 不会基于部分恢复的中间状态被应用，因此所有并发调用的最终
// 可观察结果等价于某个全局串行顺序。被拒绝的请求在锁内只做
// 只读检查，不改变快照、日志或任何既有恢复判定。
type Coordinator struct {
	mu sync.RWMutex

	snapshot Snapshot
	log      Log

	snapView *snapshotView
	actions  []Action
	report   *Report

	decisionLog []DecisionLogEntry
}

// NewCoordinator 基于快照与日志构建协调器并完成一次修复重放。
//
// 快照记录损坏与动作记录损坏属于修复结果而非拒绝理由：
// 构造始终成功，并在对象级报告中逐条标明。只有版本不衔接
// 这类结构性错误才作为 error 返回（ErrVersionMismatch）。
func NewCoordinator(snap Snapshot, lg Log) (*Coordinator, error) {
	c := &Coordinator{snapshot: snap, log: lg}
	if err := c.rebuildLocked(); err != nil {
		return nil, err
	}
	return c, nil
}

// rebuildLocked 重放整个（快照 + 已接受的完整动作）序列。
// 调用方持有 mu（写锁）。
func (c *Coordinator) rebuildLocked() error {
	dlog := make([]DecisionLogEntry, 0)

	c.snapView = verifySnapshot(&c.snapshot)
	for _, obj := range c.snapView.corrupt {
		dlog = append(dlog, DecisionLogEntry{
			Stage:  "snapshot-verify",
			Input:  fmt.Sprintf("object=%s", obj),
			Output: "corrupt/unreadable-at-snapshot-time",
			Reason: "record checksum mismatch; object distinguished from absent",
		})
	}

	lv := verifyActions(&c.log)
	c.actions = lv.actions
	for _, id := range lv.corrupt {
		dlog = append(dlog, DecisionLogEntry{
			Stage:  "action-verify",
			Input:  fmt.Sprintf("action=%s", id),
			Output: "whole-record-discarded",
			Reason: "record unparseable or checksum mismatch; no partial fields are trusted",
		})
	}

	if c.snapshot.Version != lv.base {
		err := fmt.Errorf("%w: snapshot=%q log-base=%q",
			ErrVersionMismatch, c.snapshot.Version, lv.base)
		dlog = append(dlog, DecisionLogEntry{
			Stage:  "version-check",
			Input:  fmt.Sprintf("snapshot=%q log-base=%q", c.snapshot.Version, lv.base),
			Output: "rejected",
			Reason: "log start does not line up with snapshot instant",
		})
		c.decisionLog = dlog
		return err
	}
	dlog = append(dlog, DecisionLogEntry{
		Stage:  "version-check",
		Input:  fmt.Sprintf("snapshot=%q log-base=%q", c.snapshot.Version, lv.base),
		Output: "ok",
		Reason: "versions line up",
	})

	res := newEngine().replay(c.snapView, c.actions, &dlog)

	c.report = &Report{
		SnapshotVersion: c.snapshot.Version,
		LogBase:         lv.base,
		Objects:         res.objects,
		CorruptObjects:  append([]ObjectID(nil), c.snapView.corrupt...),
		CorruptActions:  append([]ActionID(nil), lv.corrupt...),
	}

	for obj, rep := range res.objects {
		dlog = append(dlog, DecisionLogEntry{
			Stage:  "object-classify",
			Input:  fmt.Sprintf("object=%s snapshot-status=%s anchor=%d", obj, rep.Snapshot, rep.AnchorIndex),
			Output: fmt.Sprintf("source=%s final=%q", rep.Source, rep.FinalState),
			Reason: classificationReason(rep),
		})
	}

	c.decisionLog = dlog
	return nil
}

func classificationReason(rep ObjectReport) string {
	switch rep.Source {
	case SourceSnapshot:
		return "snapshot record intact and no action changed the classification"
	case SourceReplay:
		return fmt.Sprintf("snapshot=%s; complete action at index %d supplies/rebuilds state", rep.Snapshot, rep.AnchorIndex)
	case SourceUnreadable:
		return "snapshot unreadable and no complete action ever supplied a starting state"
	default:
		return ""
	}
}

// Recover 对指定对象发起修复/查询；nil 或空切片表示请求全部覆盖对象。
//
// 拒绝优先级：对象超出覆盖范围 > 版本不衔接。
// 本构造器已经保证版本衔接（否则 NewCoordinator 返回错误），
// 因此运行时只可能命中覆盖范围拒绝。快照/动作损坏不是错误，
// 逐条体现在返回的对象级报告中。
func (c *Coordinator) Recover(objectIDs []ObjectID) (*Report, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(objectIDs) == 0 {
		return cloneReport(c.report), nil
	}

	var missing []ObjectID
	for _, obj := range objectIDs {
		if _, ok := c.report.Objects[obj]; !ok {
			missing = append(missing, obj)
		}
	}
	if len(missing) > 0 {
		return nil, &CoverageError{ObjectIDs: missing}
	}

	out := &Report{
		SnapshotVersion: c.report.SnapshotVersion,
		LogBase:         c.report.LogBase,
		Objects:         make(map[ObjectID]ObjectReport, len(objectIDs)),
	}
	for _, obj := range objectIDs {
		out.Objects[obj] = c.report.Objects[obj]
	}
	return out, nil
}

// Classify 返回单个对象最终状态来源分类。
// 直接查对象级报告（底层为 map 索引），不扫描动作日志，
// 因此检查成本为 O(1)，与日志总长度无关。
func (c *Coordinator) Classify(obj ObjectID) (ObjectSource, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	rep, ok := c.report.Objects[obj]
	if !ok {
		return 0, &CoverageError{ObjectIDs: []ObjectID{obj}}
	}
	return rep.Source, nil
}

// AppendAction 在全局串行顺序中追加一条新到达的动作记录。
// 记录损坏时返回 ErrActionCorrupt 且不改变任何既有状态与判定；
// 追加完整动作后在同一临界区内重放并原子发布，
// 外部永远观察不到“部分恢复”的中间状态。
func (c *Coordinator) AppendAction(rec ActionRecord) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if rec.ActionID == "" || rec.Effects == nil {
		c.decisionLog = append(c.decisionLog, DecisionLogEntry{
			Stage:  "action-append-verify",
			Input:  fmt.Sprintf("action=%q", rec.ActionID),
			Output: "rejected-corrupt",
			Reason: "record unparseable; state unchanged",
		})
		return fmt.Errorf("%w: action=%q unparseable", ErrActionCorrupt, rec.ActionID)
	}
	want := ActionChecksum(rec.ActionID, rec.Base, rec.Effects)
	if rec.Checksum == "" || rec.Checksum != want {
		c.decisionLog = append(c.decisionLog, DecisionLogEntry{
			Stage:  "action-append-verify",
			Input:  fmt.Sprintf("action=%q", rec.ActionID),
			Output: "rejected-corrupt",
			Reason: "record checksum mismatch; state unchanged",
		})
		return fmt.Errorf("%w: action=%q checksum mismatch", ErrActionCorrupt, rec.ActionID)
	}

	// 先在临时结构上重放，成功后才提交，保证失败不留副作用。
	snap2 := c.snapshot
	log2 := c.log
	log2.Records = append(append([]ActionRecord(nil), c.log.Records...), rec)

	snapView := verifySnapshot(&snap2)
	lv := verifyActions(&log2)
	if snap2.Version != lv.base {
		return fmt.Errorf("%w: snapshot=%q action-base=%q",
			ErrVersionMismatch, snap2.Version, rec.Base)
	}
	dlog := append([]DecisionLogEntry(nil), c.decisionLog...)
	dlog = append(dlog, DecisionLogEntry{
		Stage:  "action-append",
		Input:  fmt.Sprintf("action=%s objects=%s", rec.ActionID, fmt.Sprint(sortedKeys(rec.Effects))),
		Output: "accepted-and-replayed",
		Reason: "complete intact record appended after existing replay point",
	})
	res := newEngine().replay(snapView, lv.actions, &dlog)

	// 提交点：以下赋值在锁内整体生效。
	c.log = log2
	c.snapView = snapView
	c.actions = lv.actions
	c.report = &Report{
		SnapshotVersion: snap2.Version,
		LogBase:         lv.base,
		Objects:         res.objects,
		CorruptObjects:  append([]ObjectID(nil), snapView.corrupt...),
		CorruptActions:  append([]ActionID(nil), lv.corrupt...),
	}
	c.decisionLog = dlog
	return nil
}

// DecisionLog 返回本次修复全部判定的输入、输出与依据副本。
func (c *Coordinator) DecisionLog() []DecisionLogEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]DecisionLogEntry, len(c.decisionLog))
	copy(out, c.decisionLog)
	return out
}

// SnapshotCorruptObjects 与 ActionCorruptIDs 提供结构化的损坏清单，
// 使快照对象级损坏与动作记录损坏两类错误可分别获取、互不混报。
func (c *Coordinator) SnapshotCorruptObjects() []ObjectID {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]ObjectID(nil), c.snapView.corrupt...)
}

// ActionCorruptIDs 返回校验失败被整条丢弃的动作 ID 清单。
func (c *Coordinator) ActionCorruptIDs() []ActionID {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []ActionID
	for _, rec := range c.log.Records {
		if rec.ActionID == "" || rec.Effects == nil {
			out = append(out, rec.ActionID)
			continue
		}
		if ActionChecksum(rec.ActionID, rec.Base, rec.Effects) != rec.Checksum {
			out = append(out, rec.ActionID)
		}
	}
	return out
}

func cloneReport(r *Report) *Report {
	out := &Report{SnapshotVersion: r.SnapshotVersion, LogBase: r.LogBase,
		Objects:        make(map[ObjectID]ObjectReport, len(r.Objects)),
		CorruptObjects: append([]ObjectID(nil), r.CorruptObjects...),
		CorruptActions: append([]ActionID(nil), r.CorruptActions...)}
	for k, v := range r.Objects {
		out.Objects[k] = v
	}
	return out
}

func sortedKeys[V any](m map[ObjectID]V) []ObjectID {
	ids := make([]ObjectID, 0, len(m))
	for k := range m {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	return ids
}

// IsCoverage 是 errors.Is 的便捷封装。
func IsCoverage(err error) bool { return errors.Is(err, ErrObjectOutOfCoverage) }
func IsVersion(err error) bool  { return errors.Is(err, ErrVersionMismatch) }
func IsActionCorrupt(err error) bool {
	return errors.Is(err, ErrActionCorrupt)
}

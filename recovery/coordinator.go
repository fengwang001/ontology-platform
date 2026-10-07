package recovery

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Apply 定义动作对已知状态施加变更的组合规则。
// 协调器本身不解释状态语义；本实现采用覆盖语义：动作产生的 Change
// 即为对象的新状态。独立的朴素重放模型使用同一函数，从而可对拍。
func Apply(previous State, change State) State {
	return change
}

type entry struct {
	exists bool
	state  State
	source Source
	// anchorSeq 记录状态链起点动作的序号；source==snapshot 时为 -1。
	anchorSeq int
}

// Coordinator 是快照损坏修复与动作重放的原子性协调器。
//
// 所有可变状态都在 mu 保护下访问；新动作的追加与修复/查询共用同一把锁，
// 因此并发调用的最终可观察结果等价于某个全局串行顺序，新动作不可能
// 基于部分恢复的中间状态被应用。
type Coordinator struct {
	mu sync.RWMutex

	baseVersion Version
	logStart    Version

	// known 是增量维护的对象状态投影：每个对象只保留一条最终条目，
	// 定位某对象来源分类时只需一次 map 查询，与日志总长度无关。
	known map[ObjectID]entry

	// covered 是覆盖范围：快照中出现过（完好或不可读）或被动作涉及的对象。
	covered map[ObjectID]bool

	// unreadable 记录快照时刻状态不可读的对象（对象级）。
	unreadable map[ObjectID]string

	// versionMismatch 记录快照与日志版本不衔接；衔接失败时不进行重放，
	// 但仍可对请求做覆盖范围判定（拒绝优先级：超范围 > 不衔接）。
	versionMismatch bool

	judgments []ActionJudgment
	logger    DecisionLogger
}

// NewCoordinator 从快照与动作日志字节流构建协调器，加载阶段即完成
// 逐条校验、版本衔接检查与全量重放，并物化增量投影。
func NewCoordinator(snapshotData, logData []byte, logger DecisionLogger) (*Coordinator, error) {
	if logger == nil {
		logger = NopLogger{}
	}
	snap, corrupt, err := DecodeSnapshot(snapshotData)
	if err != nil {
		return nil, err
	}
	logStart, actions, corruptErrs, err := DecodeLog(logData)
	if err != nil {
		return nil, err
	}
	c := &Coordinator{
		baseVersion: snap.BaseVersion,
		logStart:    logStart,
		known:       map[ObjectID]entry{},
		covered:     map[ObjectID]bool{},
		unreadable:  corrupt,
		logger:      logger,
	}
	for id, why := range corrupt {
		c.covered[id] = true
		c.logger.Log(DecisionLogEntry{
			Stage:  "snapshot_load",
			Input:  fmt.Sprintf("object=%q", id),
			Output: "snapshot_unreadable",
			Reason: why,
		})
	}
	for _, obj := range snap.Objects {
		c.covered[obj.ID] = true
		c.known[obj.ID] = entry{
			exists: obj.Exists, state: obj.State,
			source: SourceSnapshot, anchorSeq: -1,
		}
		c.logger.Log(DecisionLogEntry{
			Stage:  "snapshot_load",
			Input:  fmt.Sprintf("object=%q state=%q", obj.ID, obj.State),
			Output: "snapshot_ok",
			Reason: "record checksum verified",
		})
	}

	if !logStart.Equals(snap.BaseVersion) {
		c.logger.Log(DecisionLogEntry{
			Stage:  "version_check",
			Input:  fmt.Sprintf("snapshot_base=%s log_start=%s", snap.BaseVersion, logStart),
			Output: "mismatch",
			Reason: "action log must start exactly at snapshot base version",
		})
		c.versionMismatch = true
		// 不衔接时动作不得用于恢复：不做重放，避免任何状态被误采信。
		return c, nil
	}
	c.logger.Log(DecisionLogEntry{
		Stage:  "version_check",
		Input:  fmt.Sprintf("snapshot_base=%s log_start=%s", snap.BaseVersion, logStart),
		Output: "connected",
		Reason: "log start equals snapshot base version",
	})

	c.replayAll(actions, corruptErrs)
	return c, nil
}

func (v Version) String() string {
	return fmt.Sprintf("v(%d,%d)", v.Epoch, v.Seq)
}

// replayAll 按全局序号依次判定并应用全部动作记录。
func (c *Coordinator) replayAll(actions []Action, corruptErrs []error) {
	for i, a := range actions {
		if corruptErrs[i] != nil {
			c.judgeCorrupt(i, corruptErrs[i])
			continue
		}
		c.judgeAndApply(i, a)
	}
}

func (c *Coordinator) judgeCorrupt(index int, err error) {
	c.judgments = append(c.judgments, ActionJudgment{
		Index: index, Outcome: ActionCorrupt, Reason: err.Error(),
	})
	c.logger.Log(DecisionLogEntry{
		Stage:  "action_judge",
		Input:  fmt.Sprintf("record_index=%d", index),
		Output: string(ActionCorrupt),
		Reason: err.Error(),
	})
}

// judgeAndApply 执行原子性判定：动作涉及的全部对象必须同时满足前置条件，
// 否则整条动作对全部对象都不生效（同进同出）。
//
// 前置条件（逐对象）：该对象当前已有已知状态，或者本动作为其提供了
// 非空起点锚点 Start。任一对象不满足 => 整条动作放弃，集合中其余
// 本来状态已知的对象也一并不应用。
func (c *Coordinator) judgeAndApply(index int, a Action) {
	ids := make([]ObjectID, 0, len(a.Effects))
	for id := range a.Effects {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	// 完整记录声明的对象一律进入覆盖范围：即使动作最终被整体放弃，
	// 这些对象也属于“已覆盖但起点未知”，须报告 unknown，而非超范围。
	for _, id := range ids {
		c.covered[id] = true
	}

	var blocked []ObjectID
	for _, id := range ids {
		eff := a.Effects[id]
		_, hasKnown := c.known[id]
		if !hasKnown && eff.Start == nil {
			blocked = append(blocked, id)
		}
	}
	if len(blocked) > 0 {
		c.judgments = append(c.judgments, ActionJudgment{
			Index: index, Seq: a.Seq, Outcome: ActionBlocked,
			BlockingObjects: append([]ObjectID(nil), blocked...),
			Reason: fmt.Sprintf(
				"objects have no known starting state and action provides no start anchor: %s",
				joinIDs(blocked)),
		})
		c.logger.Log(DecisionLogEntry{
			Stage: "action_judge",
			Input: fmt.Sprintf("record_index=%d seq=%d objects=%s",
				index, a.Seq, joinIDs(ids)),
			Output: string(ActionBlocked),
			Reason: fmt.Sprintf("atomic precondition failed for %s; "+
				"whole action discarded for all involved objects", joinIDs(blocked)),
		})
		return
	}

	for _, id := range ids {
		eff := a.Effects[id]
		prev, hasKnown := c.known[id]
		if !hasKnown {
			c.known[id] = entry{
				exists:    true,
				state:     Apply(*eff.Start, eff.Change),
				source:    SourceRebuilt,
				anchorSeq: a.Seq,
			}
			continue
		}
		prev.state = Apply(prev.state, eff.Change)
		prev.exists = true
		c.known[id] = prev
	}
	c.judgments = append(c.judgments, ActionJudgment{
		Index: index, Seq: a.Seq, Outcome: ActionApplied,
		Reason: "all involved objects satisfied the atomic precondition",
	})
	c.logger.Log(DecisionLogEntry{
		Stage: "action_judge",
		Input: fmt.Sprintf("record_index=%d seq=%d objects=%s",
			index, a.Seq, joinIDs(ids)),
		Output: string(ActionApplied),
		Reason: "whole action applied atomically to all involved objects",
	})
}

func joinIDs(ids []ObjectID) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = string(id)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func (c *Coordinator) reportFor(id ObjectID) ObjectReport {
	r := ObjectReport{Object: id, AnchorSeq: -1}
	if c.unreadable[id] != "" {
		r.SnapshotUnreadable = true
	}
	if e, ok := c.known[id]; ok {
		r.Exists = e.exists
		r.State = e.state
		r.Source = e.source
		r.AnchorSeq = e.anchorSeq
		return r
	}
	r.Source = SourceUnknown
	return r
}

func reasonFor(r ObjectReport) string {
	switch r.Source {
	case SourceSnapshot:
		return "state chain starts from an intact snapshot record"
	case SourceRebuilt:
		return fmt.Sprintf("snapshot record unreadable; rebuilt from action seq %d anchor",
			r.AnchorSeq)
	default:
		return "snapshot unreadable and no applied action ever provided a start state"
	}
}

// Repair 执行修复或查询。拒绝优先级：对象超出覆盖范围 > 版本不衔接
// 即：先做覆盖范围判定，全部对象命中后才检查版本衔接；均通过时按对象
// 逐一报告，不视为错误。本方法只读，被拒绝的请求不改变任何内部状态。
func (c *Coordinator) Repair(objects []ObjectID) (*RepairReport, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	requested := append([]ObjectID(nil), objects...)
	sort.Slice(requested, func(i, j int) bool { return requested[i] < requested[j] })

	for _, id := range requested {
		if !c.covered[id] {
			c.logger.Log(DecisionLogEntry{
				Stage:  "request",
				Input:  fmt.Sprintf("object=%q", id),
				Output: "rejected",
				Reason: "object is outside snapshot/action-log coverage",
			})
			return nil, &Error{
				Kind: KindOutOfCoverage, Object: id,
				Message: fmt.Sprintf("object %q is not covered by snapshot or action log", id),
			}
		}
	}
	if c.versionMismatch {
		c.logger.Log(DecisionLogEntry{
			Stage: "request",
			Input: fmt.Sprintf("snapshot_base=%s log_start=%s objects=%s",
				c.baseVersion, c.logStart, joinIDs(requested)),
			Output: "rejected",
			Reason: "snapshot and action-log versions are not connected",
		})
		return nil, &Error{
			Kind: KindVersionMismatch,
			Message: fmt.Sprintf("snapshot base %s but action log starts at %s",
				c.baseVersion, c.logStart),
		}
	}

	report := &RepairReport{
		SnapshotBase:        c.baseVersion,
		LogStart:            c.logStart,
		Objects:             map[ObjectID]ObjectReport{},
		Judgments:           append([]ActionJudgment(nil), c.judgments...),
		SnapshotCorruptions: map[ObjectID]string{},
	}
	for id, why := range c.unreadable {
		report.SnapshotCorruptions[id] = why
	}
	for _, id := range requested {
		r := c.reportFor(id)
		report.Objects[id] = r
		c.logger.Log(DecisionLogEntry{
			Stage:  "request",
			Input:  fmt.Sprintf("object=%q", id),
			Output: string(r.Source),
			Reason: reasonFor(r),
		})
	}
	return report, nil
}

// RepairAll 对覆盖范围内全部对象生成完整报告（对象级粒度）。
func (c *Coordinator) RepairAll() (*RepairReport, error) {
	c.mu.RLock()
	ids := make([]ObjectID, 0, len(c.covered))
	for id := range c.covered {
		ids = append(ids, id)
	}
	c.mu.RUnlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return c.Repair(ids)
}

// Lookup 返回单个对象的最终状态及其来源分类，只访问物化投影，
// 不扫描任何动作记录，与日志总长度无关。
func (c *Coordinator) Lookup(id ObjectID) (ObjectReport, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.covered[id] {
		return ObjectReport{}, &Error{
			Kind: KindOutOfCoverage, Object: id,
			Message: fmt.Sprintf("object %q is not covered by snapshot or action log", id),
		}
	}
	if c.versionMismatch {
		return ObjectReport{}, &Error{
			Kind: KindVersionMismatch,
			Message: fmt.Sprintf("snapshot base %s but action log starts at %s",
				c.baseVersion, c.logStart),
		}
	}
	return c.reportFor(id), nil
}

// Append 以原子方式追加一条新到达的已编码动作记录。
// 损坏记录以 ActionCorrupt 判定计入，绝不部分采信；该操作与修复/查询
// 互斥，等价于在全局串行顺序中插入，调用返回后所有后续读看到的都是
// 一致的最终状态，不存在“部分恢复”的可见窗口。
func (c *Coordinator) Append(record []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.versionMismatch {
		return &Error{
			Kind:    KindVersionMismatch,
			Message: "cannot append to a coordinator whose log does not connect to its snapshot",
		}
	}
	index := len(c.judgments)
	a, err := DecodeAction(record, index)
	if err != nil {
		if e := AsError(err); e != nil {
			e.RecordIndex = index
		}
		c.judgeCorrupt(index, err)
		return nil
	}
	c.judgeAndApply(index, a)
	return nil
}

// AppendAction 追加一条逻辑动作（内存实现与测试使用），语义同 Append。
func (c *Coordinator) AppendAction(a Action) error {
	record, err := EncodeAction(a)
	if err != nil {
		return err
	}
	return c.Append(record)
}

// Versions 返回快照基线版本与日志起点版本。
func (c *Coordinator) Versions() (snapshotBase, logStart Version) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.baseVersion, c.logStart
}

// VersionConnected 报告快照与日志版本是否衔接。
func (c *Coordinator) VersionConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.versionMismatch
}

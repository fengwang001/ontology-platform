package lifecycle

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"
)

// Option 配置 Coordinator。
type Option func(*Coordinator)

// Stats 是“与规模无关”性能要求的可验证证据。
//
// HistoryScansDuringLinkChecks 恒为 0：判断单条链接是否满足复活条件时，
// 只读取该记录自身的 TypeID / Status / InvalidatedAt 字段，不扫描任何
// 历史区间或事件，因此检查成本与对象经历的删除复活循环次数无关（O(1)）。
type Stats struct {
	LinkChecks                   int64 // 判定链接恢复条件的次数
	HistoryScansDuringLinkChecks int64 // 这些判定中扫描历史的次数（恒为 0）
}

type objectState struct {
	id           string
	intervals    []*Interval
	events       []*Event
	alive        bool
	lastDeleteAt int64 // 删除态：最近一次删除时点；存活态为 0
}

// Coordinator 逻辑删除对象的溯源审计与复活协调器。
//
// 所有方法在同一互斥锁内完成“校验 + 变更”，对外提供可线性化语义：并发
// 调用的可观察结果等价于某个全局串行顺序，被拒绝的操作不改变任何状态。
type Coordinator struct {
	mu sync.Mutex

	clock       int64
	objects     map[string]*objectState
	permissions map[string]*PermissionEntry
	linkTypes   map[string]*LinkType
	links       map[string]*LinkRecord

	linkChecks                   int64
	historyScansDuringLinkChecks int64

	logWriter io.Writer
}

// WithLogWriter 设置每次判定的结构化日志（JSON 行）输出。
func WithLogWriter(w io.Writer) Option {
	return func(c *Coordinator) { c.logWriter = w }
}

// New 创建协调器。
func New(opts ...Option) *Coordinator {
	c := &Coordinator{
		objects:     map[string]*objectState{},
		permissions: map[string]*PermissionEntry{},
		linkTypes:   map[string]*LinkType{},
		links:       map[string]*LinkRecord{},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Coordinator) nextTickLocked() int64 {
	c.clock++
	return c.clock
}

// UpsertPermission 登记或更新权限条目。条目可随后被吊销而不影响历史快照。
func (c *Coordinator) UpsertPermission(entry *PermissionEntry) error {
	if entry == nil || entry.ID == "" {
		return fmt.Errorf("%w: permission entry requires id", ErrNotFound)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.permissions[entry.ID] = &PermissionEntry{
		ID:      entry.ID,
		Grants:  append([]string(nil), entry.Grants...),
		Version: entry.Version,
		Revoked: entry.Revoked,
	}
	c.log("upsert_permission", map[string]any{"id": entry.ID}, "allow", nil)
	return nil
}

// UpsertLinkType 登记链接类型及其删除行为声明。
func (c *Coordinator) UpsertLinkType(t *LinkType) error {
	if t == nil || t.ID == "" {
		return fmt.Errorf("%w: link type requires id", ErrNotFound)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.linkTypes[t.ID] = &LinkType{ID: t.ID, Behavior: t.Behavior}
	c.log("upsert_link_type", map[string]any{"id": t.ID, "behavior": int(t.Behavior)}, "allow", nil)
	return nil
}

// CreateObject 使对象诞生，开启第 1 段存活区间并记录 birth 事件。
func (c *Coordinator) CreateObject(id, actor string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.objects[id]; ok {
		return fmt.Errorf("%w: object %q already exists", ErrStateMismatch, id)
	}
	tick := c.nextTickLocked()
	obj := &objectState{id: id, alive: true}
	iv := &Interval{ID: fmt.Sprintf("%s#I%d", id, 1), ObjectID: id, Seq: 1, StartedAt: tick}
	obj.intervals = append(obj.intervals, iv)
	obj.events = append(obj.events, &Event{
		Seq: 1, Kind: EventBirth, At: tick, IntervalID: iv.ID,
		Actor: actor, Detail: "object created",
	})
	c.objects[id] = obj
	c.log("create", map[string]any{"object": id, "actor": actor}, "allow", nil)
	return nil
}

// AddLink 在两个对象之间建立可用链接（要求类型已登记且两端对象存在）。
func (c *Coordinator) AddLink(rec *LinkRecord) error {
	if rec == nil || rec.ID == "" {
		return fmt.Errorf("%w: link requires id", ErrNotFound)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.linkTypes[rec.TypeID]; !ok {
		return fmt.Errorf("%w: link type %q", ErrNotFound, rec.TypeID)
	}
	if _, ok := c.objects[rec.SourceID]; !ok {
		return fmt.Errorf("%w: source object %q", ErrNotFound, rec.SourceID)
	}
	if _, ok := c.objects[rec.TargetID]; !ok {
		return fmt.Errorf("%w: target object %q", ErrNotFound, rec.TargetID)
	}
	if _, dup := c.links[rec.ID]; dup {
		return fmt.Errorf("%w: link %q already exists", ErrStateMismatch, rec.ID)
	}
	c.links[rec.ID] = &LinkRecord{
		ID:       rec.ID,
		TypeID:   rec.TypeID,
		SourceID: rec.SourceID,
		TargetID: rec.TargetID,
		Status:   LinkAvailable,
	}
	c.log("add_link", map[string]any{
		"link": rec.ID, "type": rec.TypeID, "source": rec.SourceID, "target": rec.TargetID,
	}, "allow", nil)
	return nil
}

// DeleteObject 逻辑删除对象：
//  1. 权限不足优先于一切；
//  2. 对象当前必须存活；
//  3. 所有 incident 的“随删除失效”链接带上本次删除时点进入不可用状态；
//     “独立存在”类链接不触碰；更早前已失效的链接保留原失效时点，绝不覆盖。
func (c *Coordinator) DeleteObject(id, actor, permissionID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	in := map[string]any{"op": "delete", "object": id, "actor": actor, "permission": permissionID}
	obj, ok := c.objects[id]

	snap, err := c.authorizeLocked(permissionID, ActionDelete, "delete", in)
	if err != nil {
		return err
	}
	if !ok {
		err := fmt.Errorf("%w: object %q", ErrNotFound, id)
		c.log("delete", in, "deny_not_found", err)
		return err
	}
	if !obj.alive {
		err := fmt.Errorf("%w: cannot delete deleted object %q", ErrStateMismatch, id)
		c.log("delete", in, "deny_state", err)
		return err
	}

	tick := c.nextTickLocked()
	cur := obj.intervals[len(obj.intervals)-1]
	cur.EndedAt = tick
	obj.alive = false
	obj.lastDeleteAt = tick

	var invalidated []string
	for _, link := range c.incidentLinksLocked(id) {
		if c.linkTypes[link.TypeID].Behavior != LinkInvalidatesWithEndpoint {
			continue // 第二类：独立于对象存活状态继续存在
		}
		if link.Status == LinkInvalidated {
			continue // 已因其他原因提前失效：保留原失效时点，绝不覆盖
		}
		link.Status = LinkInvalidated
		link.InvalidatedAt = tick
		invalidated = append(invalidated, link.ID)
	}
	sort.Strings(invalidated)

	obj.events = append(obj.events, &Event{
		Seq: int64(len(obj.events)) + 1, Kind: EventDelete, At: tick, IntervalID: cur.ID,
		Actor: actor, Permission: snap, Detail: fmt.Sprintf("invalidated links=%v", invalidated),
	})
	c.log("delete", withResult(in, map[string]any{"interval": cur.ID, "invalidated_links": invalidated}), "allow", nil)
	return nil
}

// ReviveObject 复活对象：
// targetDeleteAt 必须精确等于最近一次删除时点；restoreLinks 为 true 时，
// linkIDs 非空则仅恢复显式列出的链接，为空则自动选取全部满足条件的链接。
//
// 拒绝优先级：权限不足 > 状态不符 > 删除指向过期 > 链接条件不满足。
// 任一链接不满足条件则整次复活整体失败，不发生任何部分恢复。
func (c *Coordinator) ReviveObject(id, actor, permissionID string, targetDeleteAt int64, restoreLinks bool, linkIDs []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	in := map[string]any{
		"op": "revive", "object": id, "actor": actor, "permission": permissionID,
		"target_delete_at": targetDeleteAt, "restore_links": restoreLinks,
		"link_ids": append([]string(nil), linkIDs...),
	}
	obj, ok := c.objects[id]

	snap, err := c.authorizeLocked(permissionID, ActionRevive, "revive", in)
	if err != nil {
		return err
	}
	if !ok {
		err := fmt.Errorf("%w: object %q", ErrNotFound, id)
		c.log("revive", in, "deny_not_found", err)
		return err
	}
	if obj.alive {
		err := fmt.Errorf("%w: cannot revive alive object %q", ErrStateMismatch, id)
		c.log("revive", in, "deny_state", err)
		return err
	}
	if targetDeleteAt != obj.lastDeleteAt {
		err := fmt.Errorf("%w: object %q target=%d latest=%d",
			ErrStaleDeleteTarget, id, targetDeleteAt, obj.lastDeleteAt)
		c.log("revive", in, "deny_stale_target", err)
		return err
	}

	// 纯校验先行：全部候选项确定后才进入变更阶段，保证拒绝时原子无副作用。
	candidates, err := c.resolveRestoreCandidatesLocked(id, obj.lastDeleteAt, restoreLinks, linkIDs, in)
	if err != nil {
		return err
	}

	tick := c.nextTickLocked()
	newSeq := obj.intervals[len(obj.intervals)-1].Seq + 1
	iv := &Interval{ID: fmt.Sprintf("%s#I%d", id, newSeq), ObjectID: id, Seq: newSeq, StartedAt: tick}
	obj.intervals = append(obj.intervals, iv)
	obj.alive = true
	obj.lastDeleteAt = 0

	var restored []string
	if restoreLinks {
		for _, link := range candidates {
			link.Status = LinkAvailable
			link.InvalidatedAt = 0
			restored = append(restored, link.ID)
		}
		sort.Strings(restored)
	}

	obj.events = append(obj.events, &Event{
		Seq: int64(len(obj.events)) + 1, Kind: EventRevive, At: tick, IntervalID: iv.ID,
		Actor: actor, Permission: snap, TargetDeleteAt: targetDeleteAt,
		RestoreLinks: restoreLinks, RestoredLinks: restored,
	})
	c.log("revive", withResult(in, map[string]any{"new_interval": iv.ID, "restored_links": restored}), "allow", nil)
	return nil
}

// Write 对存活对象执行常规写并记审计；对象已删除则返回 ErrObjectDeleted。
func (c *Coordinator) Write(id, actor, detail string) error {
	return c.mutateData(id, actor, detail, EventWrite, "write")
}

// Read 对存活对象执行常规读并记审计；对象已删除则返回 ErrObjectDeleted。
func (c *Coordinator) Read(id, actor, detail string) error {
	return c.mutateData(id, actor, detail, EventRead, "read")
}

// Audit 不受对象删除状态限制：始终返回全部存活区间与区间内全部事件的
// 深拷贝。结果对应某个全局串行顺序的完整前缀，区间边界确定且互不重叠。
func (c *Coordinator) Audit(id string) (*AuditReport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	obj, ok := c.objects[id]
	if !ok {
		return nil, fmt.Errorf("%w: object %q", ErrNotFound, id)
	}
	rep := &AuditReport{ObjectID: id}
	for _, iv := range obj.intervals {
		cp := *iv
		rep.Intervals = append(rep.Intervals, &cp)
	}
	for _, ev := range obj.events {
		cp := *ev
		if ev.Permission != nil {
			p := *ev.Permission
			p.Grants = append([]string(nil), ev.Permission.Grants...)
			cp.Permission = &p
		}
		if ev.RestoredLinks != nil {
			cp.RestoredLinks = append([]string(nil), ev.RestoredLinks...)
		}
		rep.Events = append(rep.Events, &cp)
	}
	c.log("audit", map[string]any{
		"object": id, "alive": obj.alive, "intervals": len(rep.Intervals), "events": len(rep.Events),
	}, "allow", nil)
	return rep, nil
}

// Stats 返回性能计数器快照，作为“与规模无关”的可验证证据。
func (c *Coordinator) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Stats{LinkChecks: c.linkChecks, HistoryScansDuringLinkChecks: c.historyScansDuringLinkChecks}
}

func (c *Coordinator) mutateData(id, actor, detail string, kind EventKind, op string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	in := map[string]any{"op": op, "object": id, "actor": actor, "detail": detail}
	obj, ok := c.objects[id]
	if !ok {
		err := fmt.Errorf("%w: object %q", ErrNotFound, id)
		c.log(op, in, "deny_not_found", err)
		return err
	}
	if !obj.alive {
		err := fmt.Errorf("%w: object %q", ErrObjectDeleted, id)
		c.log(op, in, "deny_deleted", err)
		return err
	}
	tick := c.nextTickLocked()
	cur := obj.intervals[len(obj.intervals)-1]
	obj.events = append(obj.events, &Event{
		Seq: int64(len(obj.events)) + 1, Kind: kind, At: tick, IntervalID: cur.ID,
		Actor: actor, Detail: detail,
	})
	c.log(op, in, "allow", nil)
	return nil
}

// authorizeLocked 校验操作权限并产出权限快照。权限不足是最高优先级拒绝：
// 无论对象状态如何，先判定权限。
func (c *Coordinator) authorizeLocked(permissionID, action, op string, in map[string]any) (*PermissionSnapshot, error) {
	entry, ok := c.permissions[permissionID]
	granted := ok && !entry.Revoked && containsGrant(entry.Grants, action)
	if !granted {
		err := fmt.Errorf("%w: permission %q lacks action %q", ErrPermissionDenied, permissionID, action)
		c.log(op, in, "deny_permission", err)
		return nil, err
	}
	return &PermissionSnapshot{
		PermissionID: entry.ID,
		Version:      entry.Version,
		Grants:       append([]string(nil), entry.Grants...),
		WasRevoked:   entry.Revoked,
		CapturedAt:   c.clock,
	}, nil
}

func containsGrant(grants []string, action string) bool {
	for _, g := range grants {
		if g == action {
			return true
		}
	}
	return false
}

// incidentLinksLocked 返回所有以 id 为端点的链接（无序，调用方需自行排序）。
func (c *Coordinator) incidentLinksLocked(id string) []*LinkRecord {
	var out []*LinkRecord
	for _, link := range c.links {
		if link.SourceID == id || link.TargetID == id {
			out = append(out, link)
		}
	}
	return out
}

// resolveRestoreCandidatesLocked 在不修改任何状态的前提下确定待恢复链接。
//
// 一条链接可被本次复活恢复，当且仅当同时满足：
//  1. 以被复活对象为端点；
//  2. 链接类型声明为 LinkInvalidatesWithEndpoint（第一类）；
//  3. 当前处于 LinkInvalidated；
//  4. InvalidatedAt 恰好等于本次删除时点 deleteAt。
//
// 第 4 条直接排除了更早已失效的链接（例如对端对象先被删除导致的失效）。
// 判定只读链接记录自身字段，O(1)，不随删除复活循环次数增长。
func (c *Coordinator) resolveRestoreCandidatesLocked(id string, deleteAt int64, restoreLinks bool, linkIDs []string, in map[string]any) ([]*LinkRecord, error) {
	if !restoreLinks {
		c.log("revive_link_decision", withResult(in, map[string]any{"checked": 0, "eligible": []string{}}), "allow", nil)
		return nil, nil
	}

	var candidates []*LinkRecord
	var checked []map[string]any

	check := func(link *LinkRecord) error {
		c.linkChecks++
		lt := c.linkTypes[link.TypeID]
		eligible := link.SourceID == id || link.TargetID == id
		reason := "incident"
		if !eligible {
			reason = "not_incident"
		} else if lt.Behavior != LinkInvalidatesWithEndpoint {
			eligible, reason = false, "independent_link_type"
		} else if link.Status != LinkInvalidated {
			eligible, reason = false, "not_invalidated"
		} else if link.InvalidatedAt != deleteAt {
			eligible, reason = false, "invalidated_at_mismatch"
		}
		checked = append(checked, map[string]any{
			"link": link.ID, "type": link.TypeID, "status": int(link.Status),
			"invalidated_at": link.InvalidatedAt, "delete_at": deleteAt,
			"eligible": eligible, "reason": reason,
		})
		if !eligible {
			err := fmt.Errorf("%w: link %q invalidated_at=%d delete_at=%d (%s)",
				ErrLinkCondition, link.ID, link.InvalidatedAt, deleteAt, reason)
			c.log("revive_link_decision", withResult(in, map[string]any{"checks": checked}), "deny_link_condition", err)
			return err
		}
		candidates = append(candidates, link)
		return nil
	}

	if len(linkIDs) > 0 {
		for _, lid := range linkIDs {
			link, ok := c.links[lid]
			if !ok {
				err := fmt.Errorf("%w: link %q", ErrNotFound, lid)
				c.log("revive_link_decision", withResult(in, map[string]any{"checks": checked}), "deny_link_not_found", err)
				return nil, err
			}
			if err := check(link); err != nil {
				return nil, err
			}
		}
	} else {
		// 自动选取：仅检查第一类且当前失效的 incident 链接；仍逐条执行同一 O(1) 判定。
		var auto []*LinkRecord
		for _, link := range c.links {
			if (link.SourceID == id || link.TargetID == id) && link.Status == LinkInvalidated {
				auto = append(auto, link)
			}
		}
		sort.Slice(auto, func(i, j int) bool { return auto[i].ID < auto[j].ID })
		for _, link := range auto {
			if err := check(link); err != nil {
				return nil, err
			}
		}
	}

	var eligibleIDs []string
	for _, link := range candidates {
		eligibleIDs = append(eligibleIDs, link.ID)
	}
	sort.Strings(eligibleIDs)
	c.log("revive_link_decision", withResult(in, map[string]any{"checks": checked, "eligible": eligibleIDs}), "allow", nil)
	return candidates, nil
}

func withResult(in map[string]any, extra map[string]any) map[string]any {
	out := make(map[string]any, len(in)+len(extra))
	for k, v := range in {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// log 输出一条判定日志：输入字段 + 判定结果 + 依据（错误信息或 allow）。
func (c *Coordinator) log(decision string, in map[string]any, result string, cause error) {
	if c.logWriter == nil {
		return
	}
	entry := map[string]any{"decision": decision, "input": in, "result": result}
	if cause != nil {
		entry["cause"] = cause.Error()
	}
	b, _ := json.Marshal(entry)
	b = append(b, '\n')
	_, _ = c.logWriter.Write(b)
}

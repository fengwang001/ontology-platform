package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// ReviveRequest 是一次复活操作的请求。
type ReviveRequest struct {
	Actor   Actor
	Object  ObjectID
	GrantID GrantID
	// ResumesDeletion 必须精确等于该对象最近一次删除事件的标识，
	// 否则以 ErrStaleDeletion 拒绝。
	ResumesDeletion EventID
	// RestoreLinks 声明是否尝试恢复删除前持有的链接关系。
	RestoreLinks bool
	// LinkIDs 非空时仅恢复列出的链接（逐条校验恢复条件，任一不满足
	// 则整次复活失败）；为空且 RestoreLinks 为真时恢复全部满足条件的链接。
	LinkIDs []LinkID
}

// objectState 是协调器内部的对象状态。
type objectState struct {
	intervals    []*Interval
	lastDeletion Event // 仅当对象当前处于删除状态时有效
	deleted      bool
}

// Coordinator 是溯源审计与复活协调器。
//
// 并发模型：全部操作（含审计查询与常规读写）在同一把互斥锁内串行执行，
// 因此整体效果天然等价于某个全局串行顺序，任一时刻的审计查询结果都反映
// 该顺序下某个前缀的完整历史。被拒绝的操作在获得锁后、任何状态变更之前
// 返回错误，不改变对象状态、区间划分或任何链接记录。
type Coordinator struct {
	mu      sync.Mutex
	clock   LogicalTime
	grants  map[GrantID]Grant
	objects map[ObjectID]*objectState
	links   map[LinkID]*Link
	// linksByObject 索引每个对象持有的链接，使删除/复活只需检查
	// 与该对象直接相连的链接。
	linksByObject map[ObjectID]map[LinkID]struct{}
	linkTypes     map[string]LinkType
}

// New 创建一个空的协调器。
func New() *Coordinator {
	return &Coordinator{
		grants:        make(map[GrantID]Grant),
		objects:       make(map[ObjectID]*objectState),
		links:         make(map[LinkID]*Link),
		linksByObject: make(map[ObjectID]map[LinkID]struct{}),
		linkTypes:     make(map[string]LinkType),
	}
}

// AddGrant 登记一条权限条目。
func (c *Coordinator) AddGrant(g Grant) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.grants[g.ID] = g
}

// RevokeGrant 吊销一条权限条目。已写入审计历史中的条目快照不受影响。
func (c *Coordinator) RevokeGrant(id GrantID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	g, ok := c.grants[id]
	if ok {
		g.Revoked = true
		c.grants[id] = g
	}
}

// RegisterLinkType 登记一种链接类型。
func (c *Coordinator) RegisterLinkType(t LinkType) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.linkTypes[t.Name] = t
}

// CreateObject 创建对象并开启其第一段存活区间。
func (c *Coordinator) CreateObject(actor Actor, id ObjectID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.createObject(actor, id)
}

// DeleteObject 逻辑删除对象：结束当前存活区间，并按链接类型声明
// 使级联失效类链接带上失效时点进入不可用状态。
func (c *Coordinator) DeleteObject(actor Actor, id ObjectID, gid GrantID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deleteObject(actor, id, gid)
}

// ReviveObject 复活对象：开启新的存活区间，延续 req.ResumesDeletion
// 指向的最近一次删除的溯源，并按请求恢复满足条件的链接。
func (c *Coordinator) ReviveObject(req ReviveRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reviveObject(req)
}

// ReadObject 常规读操作。对已删除对象返回 ErrObjectDeleted。
// 读操作同样计入审计，归属当前存活区间。
func (c *Coordinator) ReadObject(actor Actor, id ObjectID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.accessObject(actor, id, EventRead, "")
}

// WriteObject 常规写操作。对已删除对象返回 ErrObjectDeleted。
func (c *Coordinator) WriteObject(actor Actor, id ObjectID, payload string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.accessObject(actor, id, EventWrite, payload)
}

// LinkObjects 在两个存活对象之间建立链接。
func (c *Coordinator) LinkObjects(actor Actor, id LinkID, typeName string, from, to ObjectID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.linkObjects(actor, id, typeName, from, to)
}

// History 返回对象的全部存活区间及区间内事件。
// 审计查询不受对象删除状态限制，始终可用。
func (c *Coordinator) History(id ObjectID) (ObjectHistory, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.history(id)
}

// GetLink 返回链接记录的当前快照。
func (c *Coordinator) GetLink(id LinkID) (Link, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.links[id]
	if !ok {
		return Link{}, fmt.Errorf("link %q: %w", id, ErrNotFound)
	}
	return *l, nil
}

// ---- 以下为锁内实现，调用方必须已持有 c.mu ----

// nextEventID 推进逻辑时钟并生成事件标识。
// 只有被接受的操作才会推进时钟；被拒绝的操作不改变任何状态。
func (c *Coordinator) nextEventID() (EventID, LogicalTime) {
	c.clock++
	return EventID(fmt.Sprintf("evt-%d", c.clock)), c.clock
}

// checkGrant 校验操作引用的权限条目，返回条目快照。
func (c *Coordinator) checkGrant(actor Actor, action Action, obj ObjectID, gid GrantID) (Grant, error) {
	g, ok := c.grants[gid]
	if !ok {
		return Grant{}, fmt.Errorf("grant %q: %w", gid, ErrPermissionDenied)
	}
	if g.Revoked || g.Actor != actor || g.Action != action ||
		(g.Object != "" && g.Object != obj) {
		return Grant{}, fmt.Errorf("grant %q cannot authorize %s on %q by %q: %w",
			gid, action, obj, actor, ErrPermissionDenied)
	}
	return g, nil
}

// currentInterval 返回对象当前（开启中的）存活区间。
func currentInterval(st *objectState) *Interval {
	return st.intervals[len(st.intervals)-1]
}

// openInterval 开启一段新的存活区间并记录开启事件。
func (c *Coordinator) openInterval(st *objectState, id ObjectID, e Event) {
	iv := &Interval{
		ID:       IntervalID(fmt.Sprintf("%s#%d", id, len(st.intervals)+1)),
		Object:   id,
		Index:    len(st.intervals) + 1,
		OpenedBy: e.ID,
	}
	e.Interval = iv.ID
	iv.Events = append(iv.Events, e)
	st.intervals = append(st.intervals, iv)
}

func (c *Coordinator) createObject(actor Actor, id ObjectID) error {
	if _, ok := c.objects[id]; ok {
		return fmt.Errorf("object %q: %w", id, ErrAlreadyExists)
	}
	eid, ts := c.nextEventID()
	e := Event{ID: eid, Kind: EventCreate, Object: id, Actor: actor, Time: ts}
	st := &objectState{}
	c.openInterval(st, id, e)
	c.objects[id] = st
	return nil
}

func (c *Coordinator) deleteObject(actor Actor, id ObjectID, gid GrantID) error {
	st, ok := c.objects[id]
	if !ok {
		return fmt.Errorf("object %q: %w", id, ErrNotFound)
	}
	// 拒绝优先级 1：权限不足。
	grant, err := c.checkGrant(actor, ActionDelete, id, gid)
	if err != nil {
		return err
	}
	// 拒绝优先级 2：对象状态与请求操作不符。
	if st.deleted {
		return fmt.Errorf("object %q: %w", id, ErrObjectDeleted)
	}
	eid, ts := c.nextEventID()
	iv := currentInterval(st)
	e := Event{
		ID: eid, Kind: EventDelete, Object: id, Interval: iv.ID,
		Actor: actor, Time: ts, Grant: grant,
	}
	iv.Events = append(iv.Events, e)
	iv.ClosedBy = eid
	st.deleted = true
	st.lastDeletion = e
	// 级联失效：仅处理与本对象直接相连、且类型声明为随删除失效、
	// 且当前仍可用的链接。失效时点一旦写入不再覆盖（首次失效生效）。
	for lid := range c.linksByObject[id] {
		l := c.links[lid]
		if l.Available() && c.linkTypes[l.Type].Policy == CascadeInvalidate {
			l.InvalidatedAt = ts
		}
	}
	return nil
}

// restorable 报告链接是否满足复活恢复条件：
// 类型为级联失效、与本对象相连、且失效时点恰好等于本对象最近一次删除时点。
//
// 该判断是 O(1) 的字段比较：逻辑时间全局唯一，失效时点等于删除时点
// 当且仅当该链接正是被这次删除失效的，无需扫描任何历史记录，
// 与对象经历的删除复活循环总次数无关。
func (c *Coordinator) restorable(st *objectState, id ObjectID, l *Link) bool {
	if l.InvalidatedAt == 0 || l.InvalidatedAt != st.lastDeletion.Time {
		return false
	}
	if c.linkTypes[l.Type].Policy != CascadeInvalidate {
		return false
	}
	return l.From == id || l.To == id
}

func (c *Coordinator) reviveObject(req ReviveRequest) error {
	st, ok := c.objects[req.Object]
	if !ok {
		return fmt.Errorf("object %q: %w", req.Object, ErrNotFound)
	}
	// 拒绝优先级 1：权限不足。
	grant, err := c.checkGrant(req.Actor, ActionRevive, req.Object, req.GrantID)
	if err != nil {
		return err
	}
	// 拒绝优先级 2：对象状态与请求操作不符。
	if !st.deleted {
		return fmt.Errorf("object %q: %w", req.Object, ErrObjectNotDeleted)
	}
	// 复活必须精确指向紧邻的最近一次删除事件，不得跳过中间的
	// 删除复活循环去延续更早的一段。
	if req.ResumesDeletion != st.lastDeletion.ID {
		return fmt.Errorf("object %q resumes %q, latest deletion is %q: %w",
			req.Object, req.ResumesDeletion, st.lastDeletion.ID, ErrStaleDeletion)
	}
	// 拒绝优先级 3：链接恢复条件校验。全部校验通过前不做任何状态变更，
	// 任一链接不满足条件则整次复活失败，不存在部分恢复。
	var restore []LinkID
	if req.RestoreLinks {
		if len(req.LinkIDs) > 0 {
			for _, lid := range req.LinkIDs {
				l, ok := c.links[lid]
				if !ok {
					return fmt.Errorf("link %q: %w", lid, ErrNotFound)
				}
				if !c.restorable(st, req.Object, l) {
					return fmt.Errorf("link %q invalidated at %d, deletion at %d: %w",
						lid, l.InvalidatedAt, st.lastDeletion.Time, ErrLinkRestore)
				}
			}
			restore = append(restore, req.LinkIDs...)
		} else {
			for lid := range c.linksByObject[req.Object] {
				if c.restorable(st, req.Object, c.links[lid]) {
					restore = append(restore, lid)
				}
			}
			// 排序保证事件内容确定，可复现、可比较。
			sort.Slice(restore, func(i, j int) bool { return restore[i] < restore[j] })
		}
	}
	eid, ts := c.nextEventID()
	e := Event{
		ID: eid, Kind: EventRevive, Object: req.Object,
		Actor: req.Actor, Time: ts, Grant: grant,
		ResumesDeletion: req.ResumesDeletion,
		RestoreLinks:    req.RestoreLinks,
		RestoredLinks:   restore,
	}
	c.openInterval(st, req.Object, e)
	st.deleted = false
	for _, lid := range restore {
		c.links[lid].InvalidatedAt = 0
	}
	return nil
}

func (c *Coordinator) accessObject(actor Actor, id ObjectID, kind EventKind, payload string) error {
	st, ok := c.objects[id]
	if !ok {
		return fmt.Errorf("object %q: %w", id, ErrNotFound)
	}
	if st.deleted {
		return fmt.Errorf("object %q: %w", id, ErrObjectDeleted)
	}
	eid, ts := c.nextEventID()
	iv := currentInterval(st)
	iv.Events = append(iv.Events, Event{
		ID: eid, Kind: kind, Object: id, Interval: iv.ID,
		Actor: actor, Time: ts, Payload: payload,
	})
	return nil
}

func (c *Coordinator) linkObjects(actor Actor, id LinkID, typeName string, from, to ObjectID) error {
	if _, ok := c.linkTypes[typeName]; !ok {
		return fmt.Errorf("link type %q: %w", typeName, ErrNotFound)
	}
	if _, ok := c.links[id]; ok {
		return fmt.Errorf("link %q: %w", id, ErrAlreadyExists)
	}
	for _, oid := range []ObjectID{from, to} {
		st, ok := c.objects[oid]
		if !ok {
			return fmt.Errorf("object %q: %w", oid, ErrNotFound)
		}
		if st.deleted {
			return fmt.Errorf("object %q: %w", oid, ErrObjectDeleted)
		}
	}
	c.links[id] = &Link{ID: id, Type: typeName, From: from, To: to}
	for _, oid := range []ObjectID{from, to} {
		set := c.linksByObject[oid]
		if set == nil {
			set = make(map[LinkID]struct{})
			c.linksByObject[oid] = set
		}
		set[id] = struct{}{}
	}
	return nil
}

func (c *Coordinator) history(id ObjectID) (ObjectHistory, error) {
	st, ok := c.objects[id]
	if !ok {
		return ObjectHistory{}, fmt.Errorf("object %q: %w", id, ErrNotFound)
	}
	h := ObjectHistory{Object: id, Alive: !st.deleted}
	for _, iv := range st.intervals {
		cp := *iv
		cp.Events = append([]Event(nil), iv.Events...)
		h.Intervals = append(h.Intervals, cp)
	}
	return h, nil
}

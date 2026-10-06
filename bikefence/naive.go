package bikefence

import (
	"fmt"
	"time"
)

// NaiveService 是独立编写的参照模型：点归属逐围栏、点在多边形逐边判定，
// 不使用空间索引。与 Service 的差异仅在“如何找到候选围栏”，业务规则完全一致，
// 用于随机操作序列对照，证明空间索引实现的正确性。
type NaiveService struct {
	cfg        Config
	fences     map[string]*Fence
	order      []string
	bikes      map[string]*Bike
	tasks      map[string]*Task
	taskOrder  []string
	counts     map[string]int
	parent     map[string]string
	children   map[string][]string
	activeEvac map[string]string
	rewardDays map[string]bool
	lastTime   int64
	seq        int
}

// NewNaive 构造朴素模型。
func NewNaive(cfg Config) (*NaiveService, error) {
	if cfg.Timezone == nil {
		cfg.Timezone = time.UTC
	}
	if cfg.EvacNumerator <= 0 || cfg.EvacDenominator <= 0 ||
		cfg.EvacNumerator > cfg.EvacDenominator || cfg.ClaimTimeout <= 0 ||
		cfg.DefaultOperatingID == "" {
		return nil, errf(ErrInvalidArgument, "invalid config")
	}
	return &NaiveService{
		cfg:        cfg,
		fences:     map[string]*Fence{},
		bikes:      map[string]*Bike{},
		tasks:      map[string]*Task{},
		counts:     map[string]int{},
		parent:     map[string]string{},
		children:   map[string][]string{},
		activeEvac: map[string]string{},
		rewardDays: map[string]bool{},
	}, nil
}

func (n *NaiveService) logf(format string, args ...any) {
	if n.cfg.Log != nil {
		fmt.Fprintf(n.cfg.Log, "[naive] "+format+"\n", args...)
	}
}

func (n *NaiveService) checkClock(ts int64) error {
	if ts < n.lastTime {
		return errf(ErrClockBackwards, "clock backwards: %d < %d", ts, n.lastTime)
	}
	return nil
}

func (n *NaiveService) sweepExpired(now int64) {
	limit := int64(n.cfg.ClaimTimeout / time.Millisecond)
	for _, id := range n.taskOrder {
		t := n.tasks[id]
		if t.Status == TaskInProgress && t.ClaimedAt > 0 && now-t.ClaimedAt >= limit {
			t.Status = TaskPending
			t.ClaimedBy = ""
			t.ClaimedAt = 0
		}
	}
}

// classifyLinear 朴素逐点逐围栏判定。
func (n *NaiveService) classifyLinear(p Point) *Fence {
	var reward, noPark, op *Fence
	for _, id := range n.order {
		f := n.fences[id]
		if pointInPolygon(f.Vertices, p) {
			switch f.Kind {
			case KindReward:
				reward = f
			case KindNoParking:
				noPark = f
			case KindOperating:
				op = f
			}
		}
	}
	switch {
	case reward != nil:
		return reward
	case noPark != nil:
		return noPark
	default:
		return op
	}
}

// RegisterFence 朴素登记（约束判定与主服务相同，线性遍历既有围栏）。
func (n *NaiveService) RegisterFence(id string, kind FenceKind, vertices []Point, capacity int, at int64) error {
	if !validID(id) || capacity <= 0 || !validPolygon(vertices) ||
		kind < KindOperating || kind > KindReward {
		return errf(ErrInvalidArgument, "bad fence parameters")
	}
	if _, dup := n.fences[id]; dup {
		return errf(ErrInvalidArgument, "duplicate fence id: %s", id)
	}
	if err := n.checkClock(at); err != nil {
		return err
	}
	nf := &Fence{ID: id, Kind: kind, Vertices: append([]Point(nil), vertices...), Capacity: capacity}
	nf.bbox = bboxOf(nf.Vertices)
	if selfIntersects(nf.Vertices) {
		return errf(ErrFenceConstraint, "fence %s is self-intersecting", id)
	}
	containedIn := ""
	for _, eid := range n.order {
		ex := n.fences[eid]
		hit := polygonsIntersect(nf.Vertices, ex.Vertices)
		switch {
		case kind == KindOperating:
			if hit {
				return errf(ErrFenceConstraint, "operating intersects %s", eid)
			}
		case ex.Kind == KindOperating:
			if hit {
				if !polygonContains(ex.Vertices, nf.Vertices) {
					return errf(ErrFenceConstraint, "not inside operating %s", eid)
				}
				if containedIn != "" {
					return errf(ErrFenceConstraint, "inside multiple operating areas: %s", id)
				}
				containedIn = eid
			}
		default:
			if hit {
				return errf(ErrFenceConstraint, "intersects %s", eid)
			}
		}
	}
	if kind != KindOperating && containedIn == "" {
		return errf(ErrFenceConstraint, "not inside any operating area: %s", id)
	}
	n.fences[id] = nf
	n.order = append(n.order, id)
	n.counts[id] = 0
	if kind != KindOperating {
		n.parent[id] = containedIn
		n.children[containedIn] = append(n.children[containedIn], id)
	}
	n.lastTime = at
	n.logf("register %s accepted", id)
	return nil
}

func (n *NaiveService) RegisterBike(id string, at int64) error {
	if !validID(id) {
		return errf(ErrInvalidArgument, "bad bike id")
	}
	if _, dup := n.bikes[id]; dup {
		return errf(ErrInvalidArgument, "duplicate bike id: %s", id)
	}
	if err := n.checkClock(at); err != nil {
		return err
	}
	n.bikes[id] = &Bike{ID: id, Riding: true}
	n.lastTime = at
	return nil
}

func (n *NaiveService) rewardKey(user, fence string, at int64) string {
	day := time.UnixMilli(at).In(n.cfg.Timezone).Format("2006-01-02")
	return user + "|" + fence + "|" + day
}

func (n *NaiveService) createTask(kind TaskKind, bike, from, to string, at int64) *Task {
	n.seq++
	t := &Task{
		ID:          taskID(kind, n.seq),
		Kind:        kind,
		BikeID:      bike,
		FromFenceID: from,
		ToFenceID:   to,
		Status:      TaskPending,
		CreatedAt:   at,
	}
	n.tasks[t.ID] = t
	n.taskOrder = append(n.taskOrder, t.ID)
	return t
}

func (n *NaiveService) evacThreshold(capacity int) int {
	num := n.cfg.EvacNumerator
	den := n.cfg.EvacDenominator
	return (capacity*num + den - 1) / den
}

func (n *NaiveService) pickDestination(src *Fence) *Fence {
	opID := src.ID
	if src.Kind != KindOperating {
		opID = n.parent[src.ID]
	}
	var best *Fence
	bestCount := 0
	consider := func(f *Fence) {
		if f == nil || f.ID == src.ID || f.Kind == KindNoParking || n.counts[f.ID] >= f.Capacity {
			return
		}
		cnt := n.counts[f.ID]
		if best == nil || cnt < bestCount {
			best, bestCount = f, cnt
		}
	}
	consider(n.fences[opID])
	for _, cid := range n.children[opID] {
		consider(n.fences[cid])
	}
	return best
}

func (n *NaiveService) maybeEvacuate(src *Fence, bikeID string, at int64) {
	if _, busy := n.activeEvac[src.ID]; busy {
		return
	}
	if n.counts[src.ID] < n.evacThreshold(src.Capacity) {
		return
	}
	dst := n.pickDestination(src)
	if dst == nil {
		return
	}
	t := n.createTask(TaskEvacuate, bikeID, src.ID, dst.ID, at)
	n.activeEvac[src.ID] = t.ID
}

// ReturnBike 朴素还车，规则分支与 Service 严格对应。
func (n *NaiveService) ReturnBike(bikeID, userID string, p Point, at int64) (Receipt, error) {
	r := Receipt{BikeID: bikeID, UserID: userID, At: at, Point: p}
	if !validID(bikeID) || !validID(userID) {
		return Receipt{}, errf(ErrInvalidArgument, "bad bike/user id")
	}
	if err := n.checkClock(at); err != nil {
		return Receipt{}, err
	}
	n.sweepExpired(at)
	b, ok := n.bikes[bikeID]
	if !ok {
		return Receipt{}, errf(ErrBikeNotFound, "bike not found: %s", bikeID)
	}
	if !b.Riding {
		return Receipt{}, errf(ErrBikeNotRiding, "bike %s not riding", bikeID)
	}
	f := n.classifyLinear(p)
	if f != nil && f.Kind == KindNoParking {
		return Receipt{}, errf(ErrNoParkingReturn, "return in no-parking fence %s", f.ID)
	}
	if f == nil {
		r.Accepted = true
		r.Outside = true
		r.Fee = n.cfg.OutsideFee
		b.Riding = false
		t := n.createTask(TaskOutside, bikeID, "", n.cfg.DefaultOperatingID, at)
		r.TaskID = t.ID
		n.lastTime = at
		return r, nil
	}
	if n.counts[f.ID] >= f.Capacity {
		return Receipt{}, errf(ErrFenceFull, "fence %s full", f.ID)
	}
	r.Accepted = true
	r.FenceID = f.ID
	n.counts[f.ID]++
	b.Riding = false
	b.FenceID = f.ID
	if f.Kind == KindReward {
		key := n.rewardKey(userID, f.ID, at)
		if !n.rewardDays[key] {
			n.rewardDays[key] = true
			r.Reward = n.cfg.RewardAmount
			r.RewardGranted = true
		}
	}
	n.maybeEvacuate(f, bikeID, at)
	n.lastTime = at
	return r, nil
}

func (n *NaiveService) Unlock(bikeID string, at int64) error {
	if !validID(bikeID) {
		return errf(ErrInvalidArgument, "bad bike id")
	}
	if err := n.checkClock(at); err != nil {
		return err
	}
	n.sweepExpired(at)
	b, ok := n.bikes[bikeID]
	if !ok {
		return errf(ErrBikeNotFound, "bike not found: %s", bikeID)
	}
	if b.Riding {
		return errf(ErrBikeNotRiding, "bike %s already riding", bikeID)
	}
	if b.FenceID != "" {
		n.counts[b.FenceID]--
	}
	b.Riding = true
	b.FenceID = ""
	n.lastTime = at
	return nil
}

func (n *NaiveService) ClaimTask(taskID, workerID string, at int64) error {
	if !validID(taskID) || !validID(workerID) {
		return errf(ErrInvalidArgument, "bad task/worker id")
	}
	if err := n.checkClock(at); err != nil {
		return err
	}
	n.sweepExpired(at)
	t, ok := n.tasks[taskID]
	if !ok {
		return errf(ErrTaskNotFound, "task not found: %s", taskID)
	}
	if t.Status != TaskPending {
		return errf(ErrTaskClaimed, "task %s already claimed", taskID)
	}
	t.Status = TaskInProgress
	t.ClaimedBy = workerID
	t.ClaimedAt = at
	t.Claims = append(t.Claims, ClaimRecord{WorkerID: workerID, ClaimedAt: at})
	n.lastTime = at
	return nil
}

func (n *NaiveService) CompleteTask(taskID, workerID string, p Point, at int64) error {
	if !validID(taskID) || !validID(workerID) {
		return errf(ErrInvalidArgument, "bad task/worker id")
	}
	if err := n.checkClock(at); err != nil {
		return err
	}
	n.sweepExpired(at)
	t, ok := n.tasks[taskID]
	if !ok {
		return errf(ErrTaskNotFound, "task not found: %s", taskID)
	}
	if t.Status != TaskInProgress {
		return errf(ErrTaskNotInProgress, "task %s not in progress", taskID)
	}
	if t.ClaimedBy != workerID {
		return errf(ErrTaskClaimed, "task %s claimed by another worker", taskID)
	}
	dst := n.classifyLinear(p)
	if dst == nil {
		return errf(ErrIllegalDropoff, "dropoff outside any fence")
	}
	if dst.Kind == KindNoParking {
		return errf(ErrIllegalDropoff, "dropoff in no-parking fence %s", dst.ID)
	}
	if n.counts[dst.ID] >= dst.Capacity {
		return errf(ErrFenceFull, "dropoff fence %s full", dst.ID)
	}
	b := n.bikes[t.BikeID]
	if !b.Riding && b.FenceID == t.FromFenceID && t.FromFenceID != "" {
		n.counts[t.FromFenceID]--
	}
	n.counts[dst.ID]++
	b.Riding = false
	b.FenceID = dst.ID
	t.Status = TaskDone
	t.ToFenceID = dst.ID
	t.CompletedAt = at
	if t.Kind == TaskEvacuate {
		delete(n.activeEvac, t.FromFenceID)
	}
	n.lastTime = at
	return nil
}

// SnapshotState 返回朴素模型状态快照（结构与主服务一致，便于逐字段对照）。
func (n *NaiveService) SnapshotState() Snapshot {
	snap := Snapshot{Counts: map[string]int{}, Bikes: map[string]Bike{}, Tasks: map[string]Task{}}
	for id, cnt := range n.counts {
		snap.Counts[id] = cnt
	}
	for id, b := range n.bikes {
		snap.Bikes[id] = *b
	}
	for id, t := range n.tasks {
		snap.Tasks[id] = *t
	}
	return snap
}

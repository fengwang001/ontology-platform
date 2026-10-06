package bikefence

import "time"

// ReturnBike 处理还车。拒绝的还车不改变任何状态（车辆仍骑行、继续计费）。
func (s *Service) ReturnBike(bikeID, userID string, p Point, at int64) (Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := &s.c

	r := Receipt{BikeID: bikeID, UserID: userID, At: at, Point: p}
	if !validID(bikeID) || !validID(userID) {
		return Receipt{}, errf(ErrInvalidArgument, "bad bike/user id")
	}
	if err := c.checkClock(at); err != nil {
		return Receipt{}, err
	}
	c.sweepExpired(at)
	b, ok := c.bikes[bikeID]
	if !ok {
		return Receipt{}, errf(ErrBikeNotFound, "bike not found: %s", bikeID)
	}
	if !b.Riding {
		return Receipt{}, errf(ErrBikeNotRiding, "bike %s not riding", bikeID)
	}

	f, reason, visited := c.classify(p)
	r.Reason = reason

	// 禁停区：一律拒绝。
	if f != nil && f.Kind == KindNoParking {
		c.logf("[return] bike=%s user=%s p=(%d,%d) at=%d -> REJECT no-parking %s; nodes=%d",
			bikeID, userID, p.X, p.Y, at, f.ID, visited)
		return Receipt{}, errf(ErrNoParkingReturn, "return in no-parking fence %s", f.ID)
	}

	// 区外：允许还车，收调度费并立即生成搬回默认运营区的任务。
	if f == nil {
		r.Accepted = true
		r.Outside = true
		r.Fee = c.cfg.OutsideFee
		b.Riding = false
		b.FenceID = ""
		t := c.createTask(TaskOutside, bikeID, "", c.cfg.DefaultOperatingID, at)
		r.TaskID = t.ID
		c.lastTime = at
		c.logf("[return] bike=%s user=%s p=(%d,%d) at=%d -> ACCEPT outside, fee=%d, task=%s; %s",
			bikeID, userID, p.X, p.Y, at, r.Fee, t.ID, reason)
		return r, nil
	}

	// 运营区 / 奖励区：先看容量。
	if c.counts[f.ID] >= f.Capacity {
		c.logf("[return] bike=%s user=%s p=(%d,%d) at=%d -> REJECT fence-full %s (%d/%d); nodes=%d",
			bikeID, userID, p.X, p.Y, at, f.ID, c.counts[f.ID], f.Capacity, visited)
		return Receipt{}, errf(ErrFenceFull, "fence %s full", f.ID)
	}

	r.Accepted = true
	r.FenceID = f.ID
	c.counts[f.ID]++
	b.Riding = false
	b.FenceID = f.ID

	if f.Kind == KindReward {
		key := c.rewardKey(userID, f.ID, at)
		if !c.rewardDays[key] {
			c.rewardDays[key] = true
			r.Reward = c.cfg.RewardAmount
			r.RewardGranted = true
		}
	}

	c.maybeEvacuate(f, bikeID, at)
	c.lastTime = at
	c.logf("[return] bike=%s user=%s p=(%d,%d) at=%d -> ACCEPT fence=%s count=%d/%d reward=%d granted=%v; nodes=%d",
		bikeID, userID, p.X, p.Y, at, f.ID, c.counts[f.ID], f.Capacity, r.Reward, r.RewardGranted, visited)
	return r, nil
}

// Unlock 解锁骑走，释放原围栏名额；区外车辆直接进入骑行。
func (s *Service) Unlock(bikeID string, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := &s.c
	if !validID(bikeID) {
		return errf(ErrInvalidArgument, "bad bike id")
	}
	if err := c.checkClock(at); err != nil {
		return err
	}
	c.sweepExpired(at)
	b, ok := c.bikes[bikeID]
	if !ok {
		return errf(ErrBikeNotFound, "bike not found: %s", bikeID)
	}
	if b.Riding {
		return errf(ErrBikeNotRiding, "bike %s already riding", bikeID)
	}
	if b.FenceID != "" {
		c.counts[b.FenceID]--
	}
	b.Riding = true
	b.FenceID = ""
	c.lastTime = at
	c.logf("[unlock] bike=%s at=%d -> riding, slot released", bikeID, at)
	return nil
}

func (c *serviceCore) rewardKey(user, fence string, at int64) string {
	day := time.UnixMilli(at).In(c.cfg.Timezone).Format("2006-01-02")
	return user + "|" + fence + "|" + day
}

func (c *serviceCore) createTask(kind TaskKind, bike, from, to string, at int64) *Task {
	c.seq++
	t := &Task{
		ID:          taskID(kind, c.seq),
		Kind:        kind,
		BikeID:      bike,
		FromFenceID: from,
		ToFenceID:   to,
		Status:      TaskPending,
		CreatedAt:   at,
	}
	c.tasks[t.ID] = t
	c.taskOrder = append(c.taskOrder, t.ID)
	return t
}

func taskID(kind TaskKind, seq int) string {
	if kind == TaskOutside {
		return "O" + itoa(seq)
	}
	return "E" + itoa(seq)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// evacThreshold 返回触发阈值车辆数：count * denom >= cap * num 时触发（取等即触发）。
func (c *serviceCore) evacThreshold(capacity int) int {
	num := c.cfg.EvacNumerator
	den := c.cfg.EvacDenominator
	return (capacity*num + den - 1) / den
}

// maybeEvacuate 在围栏达到阈值、且没有未完成疏散任务、且存在可接收的围栏时，
// 生成一条把指定车辆搬往同运营区内最空闲其他围栏的任务。
func (c *serviceCore) maybeEvacuate(src *Fence, bikeID string, at int64) {
	if _, busy := c.activeEvac[src.ID]; busy {
		c.logf("[evac] fence=%s at threshold but active task exists, skip", src.ID)
		return
	}
	if c.counts[src.ID] < c.evacThreshold(src.Capacity) {
		return
	}
	dst := c.pickEvacDestination(src)
	if dst == nil {
		c.logf("[evac] fence=%s at threshold but no eligible destination, skip", src.ID)
		return
	}
	t := c.createTask(TaskEvacuate, bikeID, src.ID, dst.ID, at)
	c.activeEvac[src.ID] = t.ID
	c.logf("[evac] fence=%s count=%d/%d reached threshold, task=%s bike=%s -> %s",
		src.ID, c.counts[src.ID], src.Capacity, t.ID, bikeID, dst.ID)
}

// pickEvacDestination 选择同一运营区内车辆数最少的其他非禁停围栏；必须有空位。
// 数量并列时取登记次序最早者，保证可复现。
func (c *serviceCore) pickEvacDestination(src *Fence) *Fence {
	opID := src.ID
	if src.Kind != KindOperating {
		opID = c.parent[src.ID]
	}
	var best *Fence
	bestCount := 0
	consider := func(f *Fence) {
		if f == nil || f.ID == src.ID || f.Kind == KindNoParking {
			return
		}
		if c.counts[f.ID] >= f.Capacity {
			return
		}
		n := c.counts[f.ID]
		if best == nil || n < bestCount {
			best, bestCount = f, n
		}
	}
	consider(c.fences[opID])
	for _, cid := range c.children[opID] {
		consider(c.fences[cid])
	}
	return best
}

// ClaimTask 调度员认领任务；超时任务在每个写操作前已被 sweep 回待认领。
func (s *Service) ClaimTask(taskID, workerID string, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := &s.c
	if !validID(taskID) || !validID(workerID) {
		return errf(ErrInvalidArgument, "bad task/worker id")
	}
	if err := c.checkClock(at); err != nil {
		return err
	}
	c.sweepExpired(at)
	t, ok := c.tasks[taskID]
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
	c.lastTime = at
	c.logf("[claim] task=%s worker=%s at=%d -> in-progress (claim #%d)",
		taskID, workerID, at, len(t.Claims))
	return nil
}

// CompleteTask 上报实际落点并完成任务；落点满员或为禁停区时拒绝，任务保持处理中。
func (s *Service) CompleteTask(taskID, workerID string, p Point, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := &s.c
	if !validID(taskID) || !validID(workerID) {
		return errf(ErrInvalidArgument, "bad task/worker id")
	}
	if err := c.checkClock(at); err != nil {
		return err
	}
	c.sweepExpired(at)
	t, ok := c.tasks[taskID]
	if !ok {
		return errf(ErrTaskNotFound, "task not found: %s", taskID)
	}
	if t.Status != TaskInProgress {
		return errf(ErrTaskNotInProgress, "task %s not in progress", taskID)
	}
	if t.ClaimedBy != workerID {
		return errf(ErrTaskClaimed, "task %s claimed by another worker", taskID)
	}

	dst, reason, visited := c.classify(p)
	if dst == nil {
		c.logf("[complete] task=%s worker=%s at=%d -> REJECT illegal dropoff: outside; %s",
			taskID, workerID, at, reason)
		return errf(ErrIllegalDropoff, "dropoff outside any fence")
	}
	if dst.Kind == KindNoParking {
		c.logf("[complete] task=%s worker=%s at=%d -> REJECT illegal dropoff: no-parking %s; nodes=%d",
			taskID, workerID, at, dst.ID, visited)
		return errf(ErrIllegalDropoff, "dropoff in no-parking fence %s", dst.ID)
	}
	if c.counts[dst.ID] >= dst.Capacity {
		c.logf("[complete] task=%s worker=%s at=%d -> REJECT fence-full %s (%d/%d), stays in-progress; nodes=%d",
			taskID, workerID, at, dst.ID, c.counts[dst.ID], dst.Capacity, visited)
		return errf(ErrFenceFull, "dropoff fence %s full", dst.ID)
	}

	b := c.bikes[t.BikeID]
	if !b.Riding && b.FenceID == t.FromFenceID && t.FromFenceID != "" {
		c.counts[t.FromFenceID]--
	}
	c.counts[dst.ID]++
	b.Riding = false
	b.FenceID = dst.ID

	t.Status = TaskDone
	t.ToFenceID = dst.ID
	t.CompletedAt = at
	if t.Kind == TaskEvacuate {
		delete(c.activeEvac, t.FromFenceID)
	}
	c.lastTime = at
	c.logf("[complete] task=%s worker=%s p=(%d,%d) at=%d -> DONE dst=%s count=%d/%d; nodes=%d",
		taskID, workerID, p.X, p.Y, at, dst.ID, c.counts[dst.ID], dst.Capacity, visited)
	return nil
}

// Snapshot 供测试与复现核对：返回各围栏车辆数与全部任务状态。
type Snapshot struct {
	Counts map[string]int
	Bikes  map[string]Bike
	Tasks  map[string]Task
}

// SnapshotState 返回当前状态的深拷贝。
func (s *Service) SnapshotState() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := &s.c
	snap := Snapshot{
		Counts: map[string]int{},
		Bikes:  map[string]Bike{},
		Tasks:  map[string]Task{},
	}
	for id, n := range c.counts {
		snap.Counts[id] = n
	}
	for id, b := range c.bikes {
		snap.Bikes[id] = *b
	}
	for id, t := range c.tasks {
		snap.Tasks[id] = *t
	}
	return snap
}

// GetTask 读取任务当前状态（不推进时钟）。
func (s *Service) GetTask(id string) (Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.c.tasks[id]
	if !ok {
		return Task{}, false
	}
	return *t, true
}

package snapshot

// 本文件实现协调服务的全部公开操作。每个操作都遵循统一判定管线：
// 参数非法 -> 时钟回退 -> 超时检测 -> 操作自身判定，
// 且只报告次序最靠前的一类错误。

// CreateVolume 注册一个卷，写序号初始为零。queueCapacity 是冻结排队容量。
func (c *Coordinator) CreateVolume(t Time, id string, queueCapacity int) error {
	if id == "" {
		return invalidArgf("volume id must be non-empty")
	}
	if queueCapacity < 0 {
		return invalidArgf("queue capacity %d is negative", queueCapacity)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(t); err != nil {
		return err
	}
	if _, ok := c.volumes[id]; ok {
		return invalidArgf("volume %q already exists", id)
	}
	c.volumes[id] = newVolume(id, queueCapacity)
	return nil
}

// CreateGroup 创建组。卷列表含重复、数量越界或卷标识为空为参数非法；
// 任一卷不存在报卷不存在；任一卷已属别组报组冲突，且整体拒绝。
func (c *Coordinator) CreateGroup(t Time, id string, volumeIDs []string) error {
	if id == "" {
		return invalidArgf("group id must be non-empty")
	}
	if len(volumeIDs) < MinGroupSize || len(volumeIDs) > MaxGroupSize {
		return invalidArgf("group must contain %d..%d volumes, got %d", MinGroupSize, MaxGroupSize, len(volumeIDs))
	}
	seen := make(map[string]struct{}, len(volumeIDs))
	for _, vid := range volumeIDs {
		if vid == "" {
			return invalidArgf("volume id must be non-empty")
		}
		if _, dup := seen[vid]; dup {
			return invalidArgf("duplicate volume %q in group", vid)
		}
		seen[vid] = struct{}{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(t); err != nil {
		return err
	}
	if _, ok := c.groups[id]; ok {
		return invalidArgf("group %q already exists", id)
	}
	for _, vid := range volumeIDs {
		if _, ok := c.volumes[vid]; !ok {
			return notFoundf("volume %q does not exist", vid)
		}
	}
	for _, vid := range volumeIDs {
		if c.volumes[vid].group != nil {
			return groupConflictf("volume %q already belongs to group %q", vid, c.volumes[vid].group.id)
		}
	}
	g := newGroup(id)
	for _, vid := range volumeIDs {
		v := c.volumes[vid]
		g.insertMember(v)
		v.group = g
	}
	c.groups[id] = g
	return nil
}

// AddVolumeToGroup 向空闲组增加卷。组已满（16 卷）为参数非法；
// 卷已属任何组（含本组）报组冲突；组有进行中快照报状态错误。
func (c *Coordinator) AddVolumeToGroup(t Time, groupID, volumeID string) error {
	if groupID == "" || volumeID == "" {
		return invalidArgf("group id and volume id must be non-empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(t); err != nil {
		return err
	}
	g, ok := c.groups[groupID]
	if !ok {
		return notFoundf("group %q does not exist", groupID)
	}
	v, ok := c.volumes[volumeID]
	if !ok {
		return notFoundf("volume %q does not exist", volumeID)
	}
	if len(g.members) >= MaxGroupSize {
		return invalidArgf("group %q already has %d volumes", groupID, MaxGroupSize)
	}
	if v.group != nil {
		return groupConflictf("volume %q already belongs to group %q", volumeID, v.group.id)
	}
	if g.phase != PhaseIdle {
		return stateErrorf("group %q has a snapshot in progress (%s)", groupID, g.phase)
	}
	g.insertMember(v)
	v.group = g
	return nil
}

// RemoveVolumeFromGroup 从空闲组移除卷。移除后不足两卷为参数非法；
// 卷不是该组成员报卷不存在；组有进行中快照报状态错误。
func (c *Coordinator) RemoveVolumeFromGroup(t Time, groupID, volumeID string) error {
	if groupID == "" || volumeID == "" {
		return invalidArgf("group id and volume id must be non-empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(t); err != nil {
		return err
	}
	g, ok := c.groups[groupID]
	if !ok {
		return notFoundf("group %q does not exist", groupID)
	}
	v, ok := c.volumes[volumeID]
	if !ok {
		return notFoundf("volume %q does not exist", volumeID)
	}
	if v.group != g {
		return notFoundf("volume %q is not a member of group %q", volumeID, groupID)
	}
	if len(g.members)-1 < MinGroupSize {
		return invalidArgf("removing volume %q would leave group %q with fewer than %d volumes", volumeID, groupID, MinGroupSize)
	}
	if g.phase != PhaseIdle {
		return stateErrorf("group %q has a snapshot in progress (%s)", groupID, g.phase)
	}
	g.removeMember(volumeID)
	v.group = nil
	return nil
}

// StartSnapshot 发起快照。freezeDeadline 不得早于发起时刻（取等合法）。
func (c *Coordinator) StartSnapshot(t Time, groupID string, freezeDeadline Time) error {
	if groupID == "" {
		return invalidArgf("group id must be non-empty")
	}
	if freezeDeadline < t {
		return invalidArgf("freeze deadline %d is before start time %d", freezeDeadline, t)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(t); err != nil {
		return err
	}
	g, ok := c.groups[groupID]
	if !ok {
		return notFoundf("group %q does not exist", groupID)
	}
	if g.phase != PhaseIdle {
		return stateErrorf("group %q already has a snapshot in progress (%s)", groupID, g.phase)
	}
	g.phase = PhaseFreezing
	g.freezeDeadline = freezeDeadline
	g.remaining = len(g.members)
	c.active[g.id] = g
	return nil
}

// ConfirmFreeze 确认一个卷冻结完成。判定次序：组不存在 -> 卷不属于该组 ->
// 快照不在冻结中阶段 -> 重复确认。
//
// 常数开销：确认只做一次成员查找、一次标志置位与一次计数器递减；
// 是否为最后一个确认由 remaining 计数器判零得出，不遍历组内卷；
// 各卷截止点延迟到提交时捕获（已冻结阶段写序号不再变化，二者等价）。
func (c *Coordinator) ConfirmFreeze(t Time, groupID, volumeID string) (ConfirmResult, error) {
	if groupID == "" || volumeID == "" {
		return ConfirmResult{}, invalidArgf("group id and volume id must be non-empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(t); err != nil {
		return ConfirmResult{}, err
	}
	g, ok := c.groups[groupID]
	if !ok {
		return ConfirmResult{}, notFoundf("group %q does not exist", groupID)
	}
	v, ok := g.members[volumeID]
	if !ok {
		return ConfirmResult{}, notFoundf("volume %q is not a member of group %q", volumeID, groupID)
	}
	if g.phase != PhaseFreezing {
		return ConfirmResult{}, stateErrorf("group %q is not in freezing phase (%s)", groupID, g.phase)
	}
	if v.confirmed {
		return ConfirmResult{}, duplicateConfirmf("volume %q already confirmed for the in-progress snapshot", volumeID)
	}
	v.confirmed = true
	g.remaining--
	if g.remaining > 0 {
		return ConfirmResult{Completed: false}, nil
	}
	// 最后一个确认：此刻即快照点，进入已冻结阶段。
	g.phase = PhaseFrozen
	g.snapshotPoint = t
	return ConfirmResult{Completed: true, SnapshotPoint: t}, nil
}

// Write 向卷写入。卷不存在报卷不存在；需要排队而队列已满报队列已满，
// 写入被丢弃且不改变任何状态、不占用写序号、不触发中止；
// 卷不属于任何组或组空闲时直接应用并分配写序号。
func (c *Coordinator) Write(t Time, volumeID, payload string) (WriteResult, error) {
	if volumeID == "" {
		return WriteResult{}, invalidArgf("volume id must be non-empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(t); err != nil {
		return WriteResult{}, err
	}
	v, ok := c.volumes[volumeID]
	if !ok {
		return WriteResult{}, notFoundf("volume %q does not exist", volumeID)
	}
	if v.needsQueue() {
		if len(v.queue) >= v.capacity {
			return WriteResult{}, queueFullf("volume %q freeze queue is full (%d/%d)", volumeID, len(v.queue), v.capacity)
		}
		v.queue = append(v.queue, payload)
		return WriteResult{Outcome: WriteQueued}, nil
	}
	v.seq++
	return WriteResult{Outcome: WriteApplied, Seq: v.seq}, nil
}

// Commit 提交快照：生成快照记录（含快照点与各卷截止点），随后全部卷解冻，
// 排队的写入按各卷到达次序依次应用并分配写序号。只在已冻结阶段允许。
func (c *Coordinator) Commit(t Time, groupID string) (SnapshotRecord, error) {
	if groupID == "" {
		return SnapshotRecord{}, invalidArgf("group id must be non-empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(t); err != nil {
		return SnapshotRecord{}, err
	}
	g, ok := c.groups[groupID]
	if !ok {
		return SnapshotRecord{}, notFoundf("group %q does not exist", groupID)
	}
	if g.phase != PhaseFrozen {
		return SnapshotRecord{}, stateErrorf("group %q is not in frozen phase (%s)", groupID, g.phase)
	}
	// 已冻结阶段各卷写序号与快照点相同，此刻捕获即快照点截止点。
	record := SnapshotRecord{
		GroupID:     g.id,
		Index:       len(g.records),
		Point:       g.snapshotPoint,
		CommittedAt: t,
		Cutoffs:     g.cutoffs(),
	}
	g.records = append(g.records, record)
	c.drain(g)
	return record, nil
}

// Abort 手工中止快照：不生成快照记录，解冻并应用排队写入。
// 在冻结中或已冻结阶段允许。
func (c *Coordinator) Abort(t Time, groupID string) error {
	if groupID == "" {
		return invalidArgf("group id must be non-empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(t); err != nil {
		return err
	}
	g, ok := c.groups[groupID]
	if !ok {
		return notFoundf("group %q does not exist", groupID)
	}
	if g.phase != PhaseFreezing && g.phase != PhaseFrozen {
		return stateErrorf("group %q has no snapshot in progress (%s)", groupID, g.phase)
	}
	c.drain(g)
	return nil
}

// InspectVolume 查询卷状态。只读查询同样携带时刻、推进时钟并触发超时检测。
func (c *Coordinator) InspectVolume(t Time, volumeID string) (VolumeView, error) {
	if volumeID == "" {
		return VolumeView{}, invalidArgf("volume id must be non-empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(t); err != nil {
		return VolumeView{}, err
	}
	v, ok := c.volumes[volumeID]
	if !ok {
		return VolumeView{}, notFoundf("volume %q does not exist", volumeID)
	}
	return v.view(), nil
}

// InspectGroup 查询组状态。
func (c *Coordinator) InspectGroup(t Time, groupID string) (GroupView, error) {
	if groupID == "" {
		return GroupView{}, invalidArgf("group id must be non-empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(t); err != nil {
		return GroupView{}, err
	}
	g, ok := c.groups[groupID]
	if !ok {
		return GroupView{}, notFoundf("group %q does not exist", groupID)
	}
	return g.view(), nil
}

// SnapshotRecords 查询组已提交的全部快照记录。
func (c *Coordinator) SnapshotRecords(t Time, groupID string) ([]SnapshotRecord, error) {
	if groupID == "" {
		return nil, invalidArgf("group id must be non-empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(t); err != nil {
		return nil, err
	}
	g, ok := c.groups[groupID]
	if !ok {
		return nil, notFoundf("group %q does not exist", groupID)
	}
	return append([]SnapshotRecord(nil), g.records...), nil
}

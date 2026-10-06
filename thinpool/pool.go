package thinpool

import "sync"

// Config 描述一个精简配置存储池的固定参数。
type Config struct {
	PhysicalBlocks int // 物理块总数
	OvercommitPct  int // 超分配百分比：所有卷虚拟块数之和上限 = physical*pct/100
	WarningPct     int // 告警水位百分比（取等升档）
	CriticalPct    int // 严重水位百分比（取等升档）
}

// VolumeSnapshot 是某一卷在某一时刻的可观测状态。
type VolumeSnapshot struct {
	Name        string
	VirtualSize int
	Reservation int
	Allocated   int // 已占用物理块数
	Deficit     int // 欠额：max(reservation-allocated, 0)
}

// PoolSnapshot 是整个池在某一时刻的可观测状态。
type PoolSnapshot struct {
	Physical     int
	Allocated    int
	Free         int
	TotalVirtual int
	TotalReserve int
	TotalDeficit int
	Level        Level
	Volumes      []VolumeSnapshot
}

type volume struct {
	name        string
	virtual     int
	reservation int
	mapped      *runset
}

func (v *volume) allocated() int { return v.mapped.count() }

func (v *volume) deficit() int { return maxInt(v.reservation-v.allocated(), 0) }

// Pool 是线程安全的精简配置空间管理服务。
// 一把互斥锁串行化所有变更，并发调用结果等价于某个串行顺序。
type Pool struct {
	mu sync.Mutex

	cfg Config

	// 记账字段全部在 mu 保护下维护；核心不变量：
	//   totalDeficit == 各卷 deficit 之和，且恒有 totalDeficit <= free
	volumes      map[string]*volume
	order        []*volume // 稳定 Snapshot/重放顺序
	allocated    int       // 已分配物理块总数
	totalVirtual int       // 全部卷虚拟块数之和
	totalReserve int       // 全部卷保留块数之和
	totalDeficit int       // 全部卷欠额之和，增量维护
	events       []Event
	nextEventSeq int
	currentLevel Level
}

// New 按配置创建池。非法配置返回 KindInvalidArgument。
func New(cfg Config) (*Pool, error) {
	if cfg.PhysicalBlocks <= 0 ||
		cfg.OvercommitPct < 0 ||
		cfg.WarningPct < 0 || cfg.CriticalPct < 0 ||
		cfg.WarningPct > 100 || cfg.CriticalPct > 100 ||
		cfg.WarningPct > cfg.CriticalPct {
		return nil, errKind(KindInvalidArgument, "invalid pool config")
	}
	return &Pool{
		cfg:          cfg,
		volumes:      map[string]*volume{},
		currentLevel: LevelNormal,
	}, nil
}

// CreateVolume 创建精简卷。
func (p *Pool) CreateVolume(name string, virtualSize, reservationBlocks int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.createVolumeLocked(name, virtualSize, reservationBlocks)
}

// Write 首次写入虚拟块时分配一个物理块；已映射直接成功。
// 返回 true 表示本次发生了新的物理分配。
func (p *Pool) Write(name string, virtualBlock int) (allocatedNow bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if name == "" || virtualBlock < 0 {
		return false, errKind(KindInvalidArgument, "name non-empty and block >= 0")
	}
	vol, ok := p.volumes[name]
	if !ok {
		return false, errKind(KindVolumeNotFound, "volume not found")
	}
	if virtualBlock >= vol.virtual {
		return false, errKind(KindInvalidArgument, "virtual block out of bounds")
	}
	if vol.mapped.contains(virtualBlock) {
		return false, nil
	}
	// 分配后 free-1 不得小于其余各卷欠额之和
	// totalDeficit - vol.deficit()；判定为 O(1)，不随卷数增长。
	if p.freeLocked()-1 < p.totalDeficit-vol.deficit() {
		return false, errKind(KindPoolExhausted, "cannot encroach on other reservations")
	}
	vol.mapped.add(virtualBlock)
	p.allocated++
	if vol.allocated() <= vol.reservation {
		p.totalDeficit--
	}
	p.noteLevelChangeLocked()
	return true, nil
}

// Reclaim 回收 [start,start+length) 内已映射块；返回释放块数。
func (p *Pool) Reclaim(name string, start, length int) (reclaimed int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if name == "" || start < 0 || length < 0 {
		return 0, errKind(KindInvalidArgument, "invalid reclaim range")
	}
	vol, ok := p.volumes[name]
	if !ok {
		return 0, errKind(KindVolumeNotFound, "volume not found")
	}
	if start > vol.virtual || start+length > vol.virtual {
		return 0, errKind(KindInvalidArgument, "range out of volume bounds")
	}
	if length == 0 {
		return 0, nil
	}
	shortBefore := vol.deficit()
	freed := vol.mapped.removeRange(start, length)
	p.allocated -= freed
	// 欠额随释放回升，增量 = 新欠额 - 旧欠额。
	p.totalDeficit += vol.deficit() - shortBefore
	if freed > 0 {
		p.noteLevelChangeLocked()
	}
	return freed, nil
}

// DeleteVolume 删除卷并释放其全部物理块。
func (p *Pool) DeleteVolume(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if name == "" {
		return errKind(KindInvalidArgument, "name must be non-empty")
	}
	vol, ok := p.volumes[name]
	if !ok {
		return errKind(KindVolumeNotFound, "volume not found")
	}
	p.allocated -= vol.allocated()
	p.totalVirtual -= vol.virtual
	p.totalReserve -= vol.reservation
	p.totalDeficit -= vol.deficit()
	delete(p.volumes, name)
	for i, v := range p.order {
		if v == vol {
			p.order = append(p.order[:i], p.order[i+1:]...)
			break
		}
	}
	p.noteLevelChangeLocked()
	return nil
}

// Resize 将卷虚拟块数调整为 newVirtualSize（扩容或缩小）。
func (p *Pool) Resize(name string, newVirtualSize int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if name == "" || newVirtualSize < 0 {
		return errKind(KindInvalidArgument, "invalid resize arguments")
	}
	vol, ok := p.volumes[name]
	if !ok {
		return errKind(KindVolumeNotFound, "volume not found")
	}
	if vol.reservation > newVirtualSize {
		return errKind(KindReservationVolume, "reservation exceeds new virtual size")
	}
	if newVirtualSize > vol.virtual {
		if (p.totalVirtual+newVirtualSize-vol.virtual)*100 > p.cfg.PhysicalBlocks*p.cfg.OvercommitPct {
			return errKind(KindOvercommit, "virtual sum exceeds overcommit ceiling")
		}
	} else if newVirtualSize < vol.virtual {
		if vol.mapped.countIn(newVirtualSize, vol.virtual) > 0 {
			return errKind(KindDataInRange, "mapped blocks in truncated range")
		}
	}
	p.totalVirtual += newVirtualSize - vol.virtual
	vol.virtual = newVirtualSize
	return nil
}

// SetReservation 调整卷保留块数。
func (p *Pool) SetReservation(name string, newReservation int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if name == "" || newReservation < 0 {
		return errKind(KindInvalidArgument, "invalid reservation arguments")
	}
	vol, ok := p.volumes[name]
	if !ok {
		return errKind(KindVolumeNotFound, "volume not found")
	}
	if newReservation > vol.virtual {
		return errKind(KindReservationVolume, "reservation exceeds virtual size")
	}
	if p.totalReserve-vol.reservation+newReservation > p.cfg.PhysicalBlocks {
		return errKind(KindReservationPool, "total reservation exceeds pool")
	}
	newDeficit := maxInt(newReservation-vol.allocated(), 0)
	if p.totalDeficit-vol.deficit()+newDeficit > p.freeLocked() {
		return errKind(KindReservationShortfall, "free blocks cannot cover deficits")
	}
	p.totalReserve += newReservation - vol.reservation
	p.totalDeficit += newDeficit - vol.deficit()
	vol.reservation = newReservation
	return nil
}

// Snapshot 返回池状态的一致拷贝。
func (p *Pool) Snapshot() PoolSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshotLocked()
}

// Events 返回水位事件序列的拷贝。
func (p *Pool) Events() []Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Event, len(p.events))
	copy(out, p.events)
	return out
}

func (p *Pool) createVolumeLocked(name string, virtualSize, reservation int) error {
	if name == "" || virtualSize < 0 || reservation < 0 {
		return errKind(KindInvalidArgument, "name non-empty and sizes >= 0")
	}
	if _, exists := p.volumes[name]; exists {
		return errKind(KindVolumeExists, "volume already exists")
	}
	if (p.totalVirtual+virtualSize)*100 > p.cfg.PhysicalBlocks*p.cfg.OvercommitPct {
		return errKind(KindOvercommit, "virtual sum exceeds overcommit ceiling")
	}
	if p.totalReserve+reservation > p.cfg.PhysicalBlocks {
		return errKind(KindReservationPool, "total reservation exceeds pool")
	}
	if reservation > virtualSize {
		return errKind(KindReservationVolume, "reservation exceeds virtual size")
	}
	if p.totalDeficit+reservation > p.freeLocked() {
		return errKind(KindReservationShortfall, "free blocks cannot cover deficits")
	}
	vol := &volume{
		name:        name,
		virtual:     virtualSize,
		reservation: reservation,
		mapped:      newRunset(),
	}
	p.volumes[name] = vol
	p.order = append(p.order, vol)
	p.totalVirtual += virtualSize
	p.totalReserve += reservation
	p.totalDeficit += reservation
	return nil
}

func (p *Pool) freeLocked() int { return p.cfg.PhysicalBlocks - p.allocated }

// noteLevelChangeLocked 在任何成功且可能改变已分配量的操作后调用；
// 跨两档也只追加一个事件，档位不变不产生事件。
func (p *Pool) noteLevelChangeLocked() {
	newLevel := classify(p.allocated, p.cfg.PhysicalBlocks, p.cfg.WarningPct, p.cfg.CriticalPct)
	if newLevel == p.currentLevel {
		return
	}
	p.nextEventSeq++
	p.events = append(p.events, Event{
		Seq:       p.nextEventSeq,
		From:      p.currentLevel,
		To:        newLevel,
		Allocated: p.allocated,
	})
	p.currentLevel = newLevel
}

func (p *Pool) snapshotLocked() PoolSnapshot {
	snap := PoolSnapshot{
		Physical:     p.cfg.PhysicalBlocks,
		Allocated:    p.allocated,
		Free:         p.freeLocked(),
		TotalVirtual: p.totalVirtual,
		TotalReserve: p.totalReserve,
		TotalDeficit: p.totalDeficit,
		Level:        p.currentLevel,
		Volumes:      make([]VolumeSnapshot, 0, len(p.order)),
	}
	for _, v := range p.order {
		snap.Volumes = append(snap.Volumes, VolumeSnapshot{
			Name:        v.name,
			VirtualSize: v.virtual,
			Reservation: v.reservation,
			Allocated:   v.allocated(),
			Deficit:     v.deficit(),
		})
	}
	return snap
}

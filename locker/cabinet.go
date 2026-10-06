package locker

import "sync"

// DepositResult 为存件成功结果。
type DepositResult struct {
	Code Code
	Cell CellID
}

// Cabinet 是快递智能柜的并发安全入口。
//
// 所有方法可被并发调用；内部以单把互斥锁串行化，因此结果等价于某个
// 串行顺序（同一取件码并发取件恰有一次成功，由 byCode 删除原子性保证）。
type Cabinet struct {
	mu     sync.Mutex
	cfg    Config
	log    Logger
	clock  int64
	index  *CellIndex
	codes  *codePool
	active map[TrackingNo]*parcel
	byCode map[Code]*parcel
}

// NewCabinet 依据格口清单与参数创建柜体。格口编号必须为正且互不相同。
func NewCabinet(cells []Cell, cfg Config, log Logger) (*Cabinet, error) {
	if !cfg.validate() {
		return nil, ErrInvalidParam
	}
	index, ok := NewCellIndex(cells)
	if !ok {
		return nil, ErrInvalidParam
	}
	if log == nil {
		log = NopLogger{}
	}
	return &Cabinet{
		cfg:    cfg,
		log:    log,
		index:  index,
		codes:  newCodePool(cfg.CodeCount, cfg.CodeCooldown),
		active: make(map[TrackingNo]*parcel),
		byCode: make(map[Code]*parcel),
	}, nil
}

// Deposit 投递员存件。t 为操作时刻（非负整数秒）。
//
// 拒绝次序：参数非法 → 时钟回退 → 重复运单 → 柜内无足够规格格口
// → 足够格口全占用 → 无可用取件码。
func (c *Cabinet) Deposit(t int64, tracking TrackingNo, size Size, phone Phone) (res DepositResult, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	tail, ok := phone.lastFour()
	in := depositInput(t, tracking, size, phone)
	defer func() { c.record("Deposit", in, res, err) }()

	if t < 0 || tracking == "" || !size.valid() || !ok {
		return DepositResult{}, ErrInvalidParam
	}
	if t < c.clock {
		return DepositResult{}, ErrClockRollback
	}
	if _, dup := c.active[tracking]; dup {
		return DepositResult{}, ErrDuplicateTracking
	}
	if !c.index.HasFitting(size) {
		// 「柜内根本没有规格足够的格口」优先于「有但全占用」。
		return DepositResult{}, ErrNoFittingCell
	}
	cellID, got := c.index.Allocate(size)
	if !got {
		return DepositResult{}, ErrAllFittingBusy
	}
	code, hasCode := c.codes.allocate(t)
	if !hasCode {
		// 无码可用：回滚格口分配，保持格口空闲。
		c.index.Release(cellID)
		return DepositResult{}, ErrNoCodeAvailable
	}
	p := &parcel{
		tracking:    tracking,
		size:        size,
		phoneTail:   tail,
		cell:        cellID,
		code:        code,
		depositedAt: t,
	}
	c.active[tracking] = p
	c.byCode[code] = p
	c.clock = t
	return DepositResult{Code: code, Cell: cellID}, nil
}

// Pickup 收件人取件。
//
// 拒绝次序：参数非法 → 时钟回退 → 取件码不存在/已失效 → 快件已锁定
// → 手机号后四位不符 → 已超时 → 待缴费。
// 手机号后四位不符时累计错误次数（满 3 次锁定），但不推进时钟、
// 不改变其他状态——这是「被拒绝操作不改状态/时钟」的唯一例外。
func (c *Cabinet) Pickup(t int64, code Code, phone Phone) (tracking TrackingNo, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	tail, ok := phone.lastFour()
	in := pickupInput(t, code, phone)
	defer func() { c.record("Pickup", in, tracking, err) }()

	if t < 0 || code <= 0 || !ok {
		return "", ErrInvalidParam
	}
	if t < c.clock {
		return "", ErrClockRollback
	}
	p := c.byCode[code]
	if p == nil {
		return "", ErrCodeNotFound
	}
	if p.locked {
		// 锁定后即使输入正确也只报已锁定。
		return "", ErrParcelLocked
	}
	if tail != p.phoneTail {
		// 唯一例外：累计错误次数（满 3 次锁定），但不推进时钟。
		p.mismatches++
		if p.mismatches >= 3 {
			p.locked = true
		}
		return "", ErrPhoneMismatch
	}
	if p.timedOut(c.cfg, t) {
		return "", ErrTimedOut
	}
	if p.due(c.cfg, t) > 0 {
		return "", ErrUnpaidFee
	}
	tracking = p.tracking
	c.removeParcel(p, t) // 成功取件：错误计数随快件一并清除；码进入冷却。
	c.clock = t
	return tracking, nil
}

// Pay 缴纳指定运单在 t 时刻的滞留费。多缴部分留作预付（锁定/超时件也可缴）。
//
// 拒绝次序：参数非法 → 时钟回退 → 运单不存在。
func (c *Cabinet) Pay(t int64, tracking TrackingNo, amount int64) (err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	in := payInput(t, tracking, amount)
	defer func() { c.record("Pay", in, nil, err) }()

	if t < 0 || tracking == "" || amount <= 0 {
		return ErrInvalidParam
	}
	if t < c.clock {
		return ErrClockRollback
	}
	p := c.active[tracking]
	if p == nil {
		return ErrTrackingNotFound
	}
	p.paid += amount
	c.clock = t
	return nil
}

// Recycle 运营方回收：只有已超时快件允许回收；回收后格口立即可用，码进入冷却。
func (c *Cabinet) Recycle(t int64, tracking TrackingNo) (err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	in := trackingInput("Recycle", t, tracking)
	defer func() { c.record("Recycle", in, nil, err) }()

	if t < 0 || tracking == "" {
		return ErrInvalidParam
	}
	if t < c.clock {
		return ErrClockRollback
	}
	p := c.active[tracking]
	if p == nil {
		return ErrTrackingNotFound
	}
	if !p.timedOut(c.cfg, t) {
		return ErrNotTimedOut
	}
	c.removeParcel(p, t)
	c.clock = t
	return nil
}

// Unlock 运营方解除因手机号连续不符导致的锁定，并清零错误计数。
func (c *Cabinet) Unlock(t int64, tracking TrackingNo) (err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	in := trackingInput("Unlock", t, tracking)
	defer func() { c.record("Unlock", in, nil, err) }()

	if t < 0 || tracking == "" {
		return ErrInvalidParam
	}
	if t < c.clock {
		return ErrClockRollback
	}
	p := c.active[tracking]
	if p == nil {
		return ErrTrackingNotFound
	}
	p.locked = false
	p.mismatches = 0
	c.clock = t
	return nil
}

// Clock 返回最近一次被接受操作的时刻。
func (c *Cabinet) Clock() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.clock
}

// Stats 返回格口索引的可观测探测计数（用于分配开销证明）。
func (c *Cabinet) Stats() ProbeStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.index.Stats()
}

// ResetStats 清零格口索引探测计数。
func (c *Cabinet) ResetStats() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.index.ResetStats()
}

// removeParcel 将快件移出活动集合，立即释放格口并令取件码自 now 起冷却。
func (c *Cabinet) removeParcel(p *parcel, now int64) {
	delete(c.active, p.tracking)
	delete(c.byCode, p.code)
	c.index.Release(p.cell)
	c.codes.release(p.code, now)
}

func (c *Cabinet) record(op, in string, out any, err error) {
	if err != nil {
		c.log.Log(op, in, "REJECTED: "+err.Error(), rejectReason(err))
		return
	}
	c.log.Log(op, in, "ACCEPTED: "+fmtOutput(out), acceptReason(op, out))
}

// Package device 管理账号下的设备注册名额与注销冷却名额。
package device

// ErrFull 由 Register 在名额已满且不是复用自身冷却名额时返回。
var ErrFull = errFull{}

type errFull struct{}

func (errFull) Error() string { return "device: device quota full" }

type cooldown struct {
	dev   string
	until int64
}

type acctState struct {
	registered map[string]struct{}
	cooldowns  []cooldown
}

// Manager 为单账号维度之外的全量设备状态容器。
type Manager struct {
	dmax    int
	cool    int64
	accts   map[string]*acctState
	touched int
}

// New 创建设备管理器。
func New(dmax int, cool int64) *Manager {
	return &Manager{
		dmax:  dmax,
		cool:  cool,
		accts: make(map[string]*acctState),
	}
}

// AddAccount 登记账号的设备状态槽位。
func (m *Manager) AddAccount(acct string) {
	if _, ok := m.accts[acct]; !ok {
		m.accts[acct] = &acctState{registered: make(map[string]struct{})}
	}
}

// Exists 报告账号是否已登记。
func (m *Manager) Exists(acct string) bool {
	_, ok := m.accts[acct]
	return ok
}

// Registered 报告设备在账号下是否在册（不触碰冷却记录）。
func (m *Manager) Registered(acct, dev string) bool {
	st := m.accts[acct]
	_, ok := st.registered[dev]
	return ok
}

// Register 在 now 时刻为账号注册设备。
// 若该设备自身的冷却名额尚未释放，则复用该名额：不另占名额、不受满额限制。
// 其余情况下占用名额（在册设备 + 未释放冷却名额）达到 dmax 时返回 ErrFull。
// 上层须保证账号存在且设备当前不在册。拒绝（ErrFull）时不改变任何状态。
func (m *Manager) Register(now int64, acct, dev string) error {
	st := m.accts[acct]

	// 单次遍历：分出未释放（now < until）的冷却名额，并记录该设备自身是否有一个。
	// 到期（now >= until，恰等释放）的名额不计入占用。
	alive := make([]cooldown, 0, len(st.cooldowns))
	reuse := -1
	for _, c := range st.cooldowns {
		m.touched++
		if now < c.until {
			if c.dev == dev {
				reuse = len(alive)
			}
			alive = append(alive, c)
		}
	}

	// 复用自身冷却名额：不增加占用，也不受满额限制。
	if reuse >= 0 {
		alive = append(alive[:reuse], alive[reuse+1:]...)
		st.cooldowns = alive
		st.registered[dev] = struct{}{}
		return nil
	}

	used := len(st.registered) + len(alive)
	if used >= m.dmax {
		// 拒绝：连到期名额的清理也一并放弃，状态与调用前逐字节一致。
		return ErrFull
	}
	st.cooldowns = alive
	st.registered[dev] = struct{}{}
	return nil
}

// Deregister 在 now 时刻注销设备，留下 until = now + cool 的冷却名额
// （cool 为 0 时 until == now，按恰等释放规则该名额即刻可被新注册回收）。
// 上层须保证账号存在且设备在册，并负责删除该设备的全部许可。
func (m *Manager) Deregister(now int64, acct, dev string) {
	st := m.accts[acct]
	// 顺手释放已到期名额，使残留的过期名额至多包含本次新增的一个，
	// 从而 Register 单次遍历触碰数不超过“未释放名额数 + 1”。
	alive := st.cooldowns[:0]
	for _, c := range st.cooldowns {
		m.touched++
		if now < c.until {
			alive = append(alive, c)
		}
	}
	delete(st.registered, dev)
	st.cooldowns = append(alive, cooldown{dev: dev, until: now + m.cool})
}

// Touched 返回自管理器创建以来遍历/定位冷却记录的累计触碰数。
func (m *Manager) Touched() int { return m.touched }

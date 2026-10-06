package preauth

import "sync"

// authorization 是授权的内部状态。其所有字段只能在所属账户的 mu 下访问。
//
// state 取 StatusActive / StatusVoided / StatusFinalized；
// “已过期”不落盘，由 now 与 expiresDay 实时推导。
type authorization struct {
	id         string
	accountID  string
	state      Status
	authorized int64
	captured   int64
	refunded   int64
	expiresDay int64
	entry      *expiryEntry // 到期堆中的当前条目（可能已在 advance 中作废）
}

// remainingHold 是授权的当前持有：累计授权额减累计捕获额，不小于零。
func (a *authorization) remainingHold() int64 {
	r := a.authorized - a.captured
	if r < 0 {
		return 0
	}
	return r
}

// account 是单张卡账户。账户内所有状态都受 mu 保护。
type account struct {
	id      string
	credit  int64
	posted  int64
	lastNow int64
	expiry  *expirySchedule
	auths   map[string]*authorization
	mu      sync.Mutex
}

func newAccount(id string, credit int64, now int64) *account {
	return &account{
		id:      id,
		credit:  credit,
		lastNow: now,
		expiry:  newExpirySchedule(),
		auths:   make(map[string]*authorization),
	}
}

// available 是信用额度减已入账余额减全部有效持有。调用方须已将到期表
// 折叠（接受路径）或改用 expirySchedule.peekHolds 读取当前持有。
func (acc *account) available() int64 {
	return acc.credit - acc.posted - acc.expiry.holds()
}

// statusAt 在给定 now 下推导授权状态；过期由 expiresDay 与 now 实时比较。
func (a *authorization) statusAt(now int64) Status {
	if a.state != StatusActive {
		return a.state
	}
	if now > a.expiresDay {
		return StatusExpired
	}
	return StatusActive
}

// viewAt 构造授权只读视图；过期状态实时推导，不修改任何状态。
func (a *authorization) viewAt(now int64) AuthView {
	st := a.statusAt(now)
	v := AuthView{
		ID:         a.id,
		AccountID:  a.accountID,
		Status:     st,
		Authorized: a.authorized,
		Captured:   a.captured,
		Refunded:   a.refunded,
		Remaining:  0,
		ExpiresDay: a.expiresDay,
	}
	if st == StatusActive {
		v.Remaining = a.remainingHold()
	}
	return v
}

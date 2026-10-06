package ffm

import "sync"

// account 是单个会员账户的全部可变状态。
// 查询路径只依赖标量字段：O(1)，不随历史记录数量增长。
type account struct {
	mu        sync.Mutex
	cfg       *Config
	id        string
	openTime  int64
	tier      int   // 当前等级 0..3
	periodIdx int64 // 当前（最近已开始且未结算的）定级周期序号
	periodQM  int64 // 当前周期累计定级里程（扣回已反映在内）
	balance   int64 // 可兑换里程余额，恒非负
	debt      int64 // 欠账；与 balance 不同时为正
	lastOp    int64 // 上一次被接受操作时刻（时钟）
	activeAt  int64 // 最近一次被接受的入账/兑换时刻（活跃锚点）

	segments map[string]*segmentState
	records  map[string]*redemptionState
	nextSeq  int64
}

type segmentState struct {
	seg       Segment
	posted    bool
	voided    bool // 已退票；退票后不可再入账
	base      int64
	bonus     int64
	postTime  int64
	periodIdx int64
}

type redemptionState struct {
	id          string
	miles       int64
	createdAt   int64
	cancelled   bool
	cancelledAt int64
}

func newAccount(id string, openTime int64, c *Config) *account {
	return &account{
		cfg:       c,
		id:        id,
		openTime:  openTime,
		periodIdx: 0,
		segments:  make(map[string]*segmentState),
		records:   make(map[string]*redemptionState),
		lastOp:    openTime,
		activeAt:  openTime,
	}
}

// periodIndexOf 返回时刻 t 所属定级周期序号（左闭右开，自开通时刻连续划分）。
func (a *account) periodIndexOf(t int64) int64 { return (t - a.openTime) / a.cfg.PeriodLength }

// bonusRate 当前等级加成百分比。
func (a *account) bonusRate() int64 { return a.cfg.Bonuses[a.tier] }

// creditRedeemable 可兑换里程入账：先抵扣欠账，余额与欠账永不同时为正。
// 返回（实际进余额, 抵扣欠账）。
func (a *account) creditRedeemable(amount int64) (toBalance, toDebt int64) {
	if amount <= 0 {
		return 0, 0
	}
	if a.debt > 0 {
		if amount >= a.debt {
			toDebt = a.debt
			amount -= a.debt
			a.debt = 0
		} else {
			a.debt -= amount
			return 0, amount
		}
	}
	a.balance += amount
	return amount, toDebt
}

// debitRedeemable 扣减可兑换里程：不足则余额置 0、差额记欠账。
func (a *account) debitRedeemable(amount int64) {
	if amount <= 0 {
		return
	}
	if amount <= a.balance {
		a.balance -= amount
		return
	}
	a.debt += amount - a.balance
	a.balance = 0
}

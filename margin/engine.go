// Package margin 实现逐仓合约的保证金与强平引擎。
//
// 引擎管理账户保证金与单向持仓，在标记价格推进时按固定顺序判定并
// 执行强平：保险基金先吸收亏空，不足部分按盈利率排序自动减仓对手方。
package margin

import (
	"errors"
	"sync"
)

// 操作失败的可区分原因。校验顺序为：参数非法 -> 账户不存在 ->
// 持仓冲突或无仓位 -> 资金不足，只报告第一个命中的原因。
var (
	ErrInvalidParam      = errors.New("margin: invalid parameter")
	ErrAccountNotFound   = errors.New("margin: account not found")
	ErrPositionConflict  = errors.New("margin: position conflict or no position")
	ErrInsufficientFunds = errors.New("margin: insufficient funds")
)

// 数值范围限制。
const (
	maxPrice    = 1_000_000             // n、p、P 的上界（下界为 1）
	maxAmount   = 1_000_000_000_000     // x 的上界（下界为 1）
	maxPosition = 1_000_000             // 开仓后 |q| 的上界
	maxMargin   = 1_000_000_000_000_000 // 存入后 M 的上界
	rateBase    = 10_000                // 费率分母（万分比）
)

// Side 为开仓方向。
type Side int

const (
	Long  Side = 1 // 多头，持仓量为正
	Short Side = 2 // 空头，持仓量为负
)

// sign 返回该方向下带符号数量的符号。
func (s Side) sign() int64 {
	if s == Short {
		return -1
	}
	return 1
}

// Account 为账户状态：保证金 M、带符号持仓量 q、带符号成本 C。
type Account struct {
	M int64
	Q int64
	C int64
}

// ADLEntry 为一条自动减仓明细：从账户 Account 取走 Take。
type ADLEntry struct {
	Account string
	Take    int64
}

// Liquidation 为一次强平执行的记录。
type Liquidation struct {
	Account  string     // 被强平账户
	Equity   int64      // 强平时的权益 E
	Fee      int64      // 罚金 f（计入保险基金）
	FundUsed int64      // 保险基金吸收额 u
	ADL      []ADLEntry // 自动减仓明细，按取走次序
	BadDebt  int64      // 本次记入坏账 B 的金额
}

// Engine 为保证金与强平引擎。所有方法可并发调用，
// 结果等价于某个串行顺序；Mark 的三步为一个原子步骤。
type Engine struct {
	mu       sync.Mutex
	i        int64 // 初始保证金率（万分比）
	mm       int64 // 维持保证金率（万分比）
	f        int64 // 强平费率（万分比）
	accounts map[string]*Account
	z        int64 // 保险基金，恒不为负
	b        int64 // 坏账累计
	mark     int64 // 最近一次标记价格
	hasMark  bool
}

// NewEngine 构造引擎。要求 1 <= mm < i <= 10000 且 0 <= f <= 10000，
// 否则返回 ErrInvalidParam。
func NewEngine(i, mm, f int64) (*Engine, error) {
	if !(1 <= mm && mm < i && i <= rateBase) || !(0 <= f && f <= rateBase) {
		return nil, ErrInvalidParam
	}
	return &Engine{
		i:        i,
		mm:       mm,
		f:        f,
		accounts: make(map[string]*Account),
	}, nil
}

// ceilDiv 计算 ceil(a/b)，要求 a >= 0、b > 0。
func ceilDiv(a, b int64) int64 {
	return (a + b - 1) / b
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

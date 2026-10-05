// Package quote 定义做市商双边报价的状态类型。
//
// 报价整体替换：任一 Quote 操作都用新报价完全覆盖旧报价；
// Fill 只从某一侧的数量中扣减。
package quote

// Side 表示报价的买侧或卖侧。
type Side int

const (
	// Bid 买侧（做市商买入）。
	Bid Side = iota
	// Ask 卖侧（做市商卖出）。
	Ask
)

// Valid 报告 side 是否为合法取值。
func (s Side) Valid() bool { return s == Bid || s == Ask }

func (s Side) String() string {
	if s == Bid {
		return "bid"
	}
	return "ask"
}

// Quote 是某 (mm, sym) 当前的双边报价快照。
type Quote struct {
	Bid    int64
	BidQty int64
	Ask    int64
	AskQty int64
}

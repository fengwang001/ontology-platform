// Package haircut 维护债券折算率、净价，并提供标准券额度的纯算术。
package haircut

import "errors"

// Bond 描述一张债券当前的折算率与净价。
type Bond struct {
	Rate  int
	Price int64
}

// Market 是债券台账。调用方（pledge 包）负责加锁。
type Market struct {
	bonds map[string]Bond
}

// NewMarket 创建空台账。
func NewMarket() *Market {
	return &Market{bonds: make(map[string]Bond)}
}

// 哨兵错误，按拒绝次序定义；业务包统一复用以便 errors.Is 区分。
var (
	ErrInvalid     = errors.New("haircut: invalid argument")
	ErrDayBackward = errors.New("haircut: day must not go backward")
	ErrNoBond      = errors.New("haircut: bond not found")
	ErrNoAccount   = errors.New("haircut: account not found")
	ErrDupBond     = errors.New("haircut: bond already exists")
	ErrDupRepo     = errors.New("haircut: repo id already exists")
	ErrDeficit     = errors.New("haircut: account is in standard-bond deficit")
	ErrAvail       = errors.New("haircut: available holding insufficient")
	ErrStock       = errors.New("haircut: pledged stock insufficient")
	ErrCap         = errors.New("haircut: standard-bond cap insufficient")
)

// ValidDay 校验日期范围。
func ValidDay(day int) bool { return 0 <= day && day <= 1_000_000 }

// ValidRate 校验折算率（百分比，0..150）。
func ValidRate(rate int) bool { return 0 <= rate && rate <= 150 }

// ValidPrice 校验净价（每张 1..1e6）。
func ValidPrice(price int64) bool { return 1 <= price && price <= 1_000_000 }

// ValidQty 校验张数或金额（1..1e12）。
func ValidQty(n int64) bool { return 1 <= n && n <= 1_000_000_000_000 }

// ValidRateBps 校验年化利率（基点 0..10000）。
func ValidRateBps(r int) bool { return 0 <= r && r <= 10_000 }

// NonEmpty 判断标识是否为非空字节串。
func NonEmpty(id []byte) bool { return len(id) > 0 }

// StdBond 返回 n 张债券折合的标准券数：逐券向下取整 floor(n*rate/100)。
func StdBond(n int64, rate int) int64 {
	return n * int64(rate) / 100
}

// Occupy 返回融资 amount 对标准券的占用：ceil(amount/100)。
func Occupy(amount int64) int64 {
	return (amount + 99) / 100
}

// Interest 返回回购利息：ceil(amount*r*days/3650000)，r 为基点。
func Interest(amount int64, r, days int) int64 {
	return (amount*int64(r)*int64(days) + 3_649_999) / 3_650_000
}

// CeilDiv 返回 ceil(a/b)，要求 b>0。
func CeilDiv(a, b int64) int64 { return (a + b - 1) / b }

// Add 登记新债券，重复报 ErrDupBond。
func (m *Market) Add(bond string, rate int, price int64) error {
	if _, ok := m.bonds[bond]; ok {
		return ErrDupBond
	}
	m.bonds[bond] = Bond{Rate: rate, Price: price}
	return nil
}

// SetRate 调整折算率。
func (m *Market) SetRate(bond string, rate int) error {
	b, ok := m.bonds[bond]
	if !ok {
		return ErrNoBond
	}
	b.Rate = rate
	m.bonds[bond] = b
	return nil
}

// SetPrice 调整净价。
func (m *Market) SetPrice(bond string, price int64) error {
	b, ok := m.bonds[bond]
	if !ok {
		return ErrNoBond
	}
	b.Price = price
	m.bonds[bond] = b
	return nil
}

// Get 返回债券当前折算率与净价。
func (m *Market) Get(bond string) (Bond, bool) {
	b, ok := m.bonds[bond]
	return b, ok
}

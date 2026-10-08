// Package naive 是按规则直接写成的独立朴素参考模型，
// 仅用于与正式实现做随机对照测试。
//
// 它刻意采用最直白的写法：保存全部回收流水、每次操作都重新
// 遍历求和、用字符串表示参与方与方向，不依赖正式实现的任何包。
package naive

// ErrKind 错误类别（空串表示成功），与正式实现的哨兵错误一一对应。
type ErrKind string

const (
	OK               ErrKind = ""
	InvalidParam     ErrKind = "invalid_param"
	ClockRollback    ErrKind = "clock_rollback"
	CaseNotFound     ErrKind = "case_not_found"
	Expired          ErrKind = "expired"
	ExceedsTotalLoss ErrKind = "exceeds_total_loss"
	AlreadyWaived    ErrKind = "already_waived"
)

// 参与方与调整方向的字符串表示。
const (
	PInsured = "insured"
	PInsurer = "insurer"
	PThird   = "third_party"

	DPay      = "pay"
	DClawback = "clawback"
)

// Adj 一条调整记录。
type Adj struct {
	Party  string
	Dir    string
	Amount int64
	Now    int64
	Cause  string
}

// Rec 一笔回收流水。
type Rec struct {
	Gross int64
	Fee   int64
	Now   int64
}

// Case 朴素模型的案件状态：保留全部历史，每次重算。
type Case struct {
	TotalLoss int64
	Paid      int64
	Deadline  int64
	RatioBP   int64
	Waived    bool
	Recs      []Rec
	Disbursed map[string]int64
	Adjs      []Adj
}

// Sys 朴素系统。
type Sys struct {
	Cases  map[string]*Case
	last   int64
	hasNow bool
}

// New 创建朴素系统。
func New() *Sys {
	return &Sys{Cases: map[string]*Case{}}
}

func (s *Sys) clockOK(now int64) bool {
	return !s.hasNow || now >= s.last
}

func (s *Sys) accept(now int64) {
	s.last, s.hasNow = now, true
}

// Register 登记案件。
func (s *Sys) Register(now int64, id string, totalLoss, paid, deadline, ratioBP int64) ErrKind {
	if id == "" || totalLoss < 0 || paid < 0 || paid > totalLoss || ratioBP < 0 || ratioBP > 10000 {
		return InvalidParam
	}
	if _, dup := s.Cases[id]; dup {
		return InvalidParam
	}
	if !s.clockOK(now) {
		return ClockRollback
	}
	s.Cases[id] = &Case{
		TotalLoss: totalLoss,
		Paid:      paid,
		Deadline:  deadline,
		RatioBP:   ratioBP,
		Disbursed: map[string]int64{PInsured: 0, PInsurer: 0, PThird: 0},
	}
	s.accept(now)
	return OK
}

// Recover 登记一笔回收。
func (s *Sys) Recover(now int64, id string, gross, fee int64) ErrKind {
	if id == "" || gross < 0 || fee < 0 || fee > gross {
		return InvalidParam
	}
	if !s.clockOK(now) {
		return ClockRollback
	}
	c, ok := s.Cases[id]
	if !ok {
		return CaseNotFound
	}
	if now > c.Deadline {
		return Expired
	}
	c.Recs = append(c.Recs, Rec{Gross: gross, Fee: fee, Now: now})
	c.settle(now, "recover")
	s.accept(now)
	return OK
}

// AdjustRatio 调整责任比例。
func (s *Sys) AdjustRatio(now int64, id string, ratioBP int64) ErrKind {
	if id == "" || ratioBP < 0 || ratioBP > 10000 {
		return InvalidParam
	}
	if !s.clockOK(now) {
		return ClockRollback
	}
	c, ok := s.Cases[id]
	if !ok {
		return CaseNotFound
	}
	c.RatioBP = ratioBP
	c.settle(now, "adjust_ratio")
	s.accept(now)
	return OK
}

// Supplement 保险人补充赔付。
func (s *Sys) Supplement(now int64, id string, amount int64) ErrKind {
	if id == "" || amount <= 0 {
		return InvalidParam
	}
	if !s.clockOK(now) {
		return ClockRollback
	}
	c, ok := s.Cases[id]
	if !ok {
		return CaseNotFound
	}
	if c.Paid+amount > c.TotalLoss {
		return ExceedsTotalLoss
	}
	c.Paid += amount
	c.settle(now, "supplement")
	s.accept(now)
	return OK
}

// Waive 被保险人声明放弃追偿权。
func (s *Sys) Waive(now int64, id string) ErrKind {
	if id == "" {
		return InvalidParam
	}
	if !s.clockOK(now) {
		return ClockRollback
	}
	c, ok := s.Cases[id]
	if !ok {
		return CaseNotFound
	}
	if now > c.Deadline {
		return Expired
	}
	if c.Waived {
		return AlreadyWaived
	}
	c.Waived = true
	c.settle(now, "waive")
	s.accept(now)
	return OK
}

// NetTotal 遍历全部回收流水求净回收总额（朴素做法，O(历史笔数)）。
func (c *Case) NetTotal() int64 {
	var net int64
	for _, r := range c.Recs {
		net += r.Gross - r.Fee
	}
	return net
}

// Entitled 按分配瀑布直接计算三方应得。
func (c *Case) Entitled() map[string]int64 {
	net := c.NetTotal()
	cap_ := c.TotalLoss * c.RatioBP / 10000
	distributable := net
	if cap_ < distributable {
		distributable = cap_
	}
	var insured int64
	if !c.Waived {
		insured = distributable
		if u := c.TotalLoss - c.Paid; u < insured {
			insured = u
		}
	}
	insurer := distributable - insured
	if insurer > c.Paid {
		insurer = c.Paid
	}
	return map[string]int64{
		PInsured: insured,
		PInsurer: insurer,
		PThird:   net - insured - insurer,
	}
}

// settle 把应得与已发放的差逐方结清，追加最小差调整记录。
func (c *Case) settle(now int64, cause string) {
	want := c.Entitled()
	for _, p := range []string{PInsured, PInsurer, PThird} {
		diff := want[p] - c.Disbursed[p]
		if diff == 0 {
			continue
		}
		dir, amt := DPay, diff
		if diff < 0 {
			dir, amt = DClawback, -diff
		}
		c.Adjs = append(c.Adjs, Adj{Party: p, Dir: dir, Amount: amt, Now: now, Cause: cause})
		c.Disbursed[p] = want[p]
	}
}

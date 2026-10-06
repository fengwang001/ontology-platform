package ontology

import (
	"sort"
	"sync"
	"time"
)

// NaiveEngine 是完全独立、按规则逐月线性重放的朴素参照模型，仅供差分测试。
// 它不共享引擎的任何数据结构：用切片保存全部额度笔（含已用尽/已到期），
// 每次封账/预览都从首个月份开始逐月重放，逐条扫描额度，是规则的直译。
type NaiveEngine struct {
	mu sync.Mutex
	g  *TimeGrid
	cs map[string]*naiveConsumer
}

type naiveConsumer struct {
	params paramHistory[ContractParams]
	prices paramHistory[Prices]
	reads  map[MonthKey]map[time.Time]Readings
	sealed map[MonthKey]MonthlyResult
	upTo   MonthKey
	has    bool
}

type naiveCredit struct {
	id      int64
	deposit MonthKey
	expiry  MonthKey
	amount  int64
}

// NewNaiveEngine 创建朴素模型。
func NewNaiveEngine(interval time.Duration) (*NaiveEngine, error) {
	g, err := NewTimeGrid(interval)
	if err != nil {
		return nil, err
	}
	return &NaiveEngine{g: g, cs: map[string]*naiveConsumer{}}, nil
}

func (n *NaiveEngine) RegisterConsumer(id string, p ContractParams, pr Prices) error {
	if err := validateContract(p); err != nil {
		return err
	}
	if err := validatePrices(pr); err != nil {
		return err
	}
	if id == "" {
		return illegal("consumer id must not be empty")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.cs[id]; ok {
		return illegal("consumer already registered: %s", id)
	}
	c := &naiveConsumer{reads: map[MonthKey]map[time.Time]Readings{}, sealed: map[MonthKey]MonthlyResult{}}
	c.params.init(p)
	c.prices.init(pr)
	n.cs[id] = c
	return nil
}

func (n *NaiveEngine) SetParams(id string, from MonthKey, p ContractParams) error {
	if err := validateContract(p); err != nil {
		return err
	}
	c, err := n.get(id)
	if err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if c.has && from <= c.upTo {
		return sealed("parameter change at %s", from)
	}
	if from <= c.params.lastFrom() {
		return orderErr("parameter changes must be appended by effective month")
	}
	c.params.set(from, p)
	return nil
}

func (n *NaiveEngine) SetPrices(id string, from MonthKey, pr Prices) error {
	if err := validatePrices(pr); err != nil {
		return err
	}
	c, err := n.get(id)
	if err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if c.has && from <= c.upTo {
		return sealed("price change at %s", from)
	}
	if from <= c.prices.lastFrom() {
		return orderErr("price changes must be appended by effective month")
	}
	c.prices.set(from, pr)
	return nil
}

func (n *NaiveEngine) PutReading(id string, start time.Time, r Readings) error {
	if !n.g.AlignedStart(start) {
		return illegal("interval start %s is not grid-aligned", start.UTC().Format(time.RFC3339))
	}
	if r.ImportKWh < 0 || r.ExportKWh < 0 {
		return illegal("readings must be non-negative")
	}
	c, err := n.get(id)
	if err != nil {
		return err
	}
	start = start.UTC()
	m := MonthOf(start)
	n.mu.Lock()
	defer n.mu.Unlock()
	if c.has && m <= c.upTo {
		return sealed("reading at %s", start.Format(time.RFC3339))
	}
	mm := c.reads[m]
	if mm == nil {
		mm = map[time.Time]Readings{}
		c.reads[m] = mm
	}
	mm[start] = r
	return nil
}

func (n *NaiveEngine) SealMonth(id string, m MonthKey) error {
	c, err := n.get(id)
	if err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if c.has && m <= c.upTo {
		return sealed("seal %s", m)
	}
	if c.has && m != c.upTo+1 {
		return orderErr("expected seal %s before %s", c.upTo+1, m)
	}
	mm := c.reads[m]
	for _, t := range n.g.IntervalStarts(m) {
		if _, ok := mm[t]; !ok {
			return missing(t)
		}
	}
	res, _ := n.replay(c, m)
	c.sealed[m] = res
	c.upTo = m
	c.has = true
	return nil
}

func (n *NaiveEngine) MonthResult(id string, m MonthKey) (MonthlyResult, error) {
	c, err := n.get(id)
	if err != nil {
		return MonthlyResult{}, err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	res, _ := n.replay(c, m)
	return res, nil
}

func (n *NaiveEngine) CreditBalance(id string) (int64, error) {
	c, err := n.get(id)
	if err != nil {
		return 0, err
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	through := c.upTo
	if !c.has {
		return 0, nil
	}
	_, bal := n.replay(c, through)
	return bal, nil
}

func (n *NaiveEngine) get(id string) (*naiveConsumer, error) {
	c, ok := n.cs[id]
	if !ok {
		return nil, illegal("unknown consumer: %s", id)
	}
	return c, nil
}

// replay 按月份顺序重放全部已封账月；若 through 本身未封账，则最后额外对 through
// 做一次预览（不存入额度、不执行到期付款）。返回 through 月结果与重放后真实余额。
func (n *NaiveEngine) replay(c *naiveConsumer, through MonthKey) (MonthlyResult, int64) {
	cr := []naiveCredit{}
	nextID := int64(0)
	balance := int64(0)
	var target MonthlyResult
	sealedMonths := make([]MonthKey, 0, len(c.sealed))
	for m := range c.sealed {
		sealedMonths = append(sealedMonths, m)
	}
	sort.Slice(sealedMonths, func(i, j int) bool { return sealedMonths[i] < sealedMonths[j] })
	var lastSealedAtOrBefore MonthlyResult
	for _, m := range sealedMonths {
		if m > through {
			break
		}
		p := c.params.at(m)
		pr := c.prices.at(m)
		mm := c.reads[m]
		res := computeMonth(m, mm, n.g, p, pr, func(want int64) int64 {
			used := int64(0)
			for i := range cr {
				if used >= want {
					break
				}
				// 只能在存入月之后使用；到期月恰等于 m 时本月仍可用。
				if cr[i].deposit >= m || cr[i].expiry < m || cr[i].amount == 0 {
					continue
				}
				take := want - used
				if take > cr[i].amount {
					take = cr[i].amount
				}
				cr[i].amount -= take
				used += take
			}
			return used
		})
		if res.NetExportCredited > 0 {
			nextID++
			cr = append(cr, naiveCredit{id: nextID, deposit: m, expiry: m.Add(p.CreditValidMonths), amount: res.NetExportCredited})
			balance += res.NetExportCredited
		}
		expired := int64(0)
		for i := range cr {
			if cr[i].expiry <= m {
				expired += cr[i].amount
				balance -= cr[i].amount
				cr[i].amount = 0
			}
		}
		res.ExpiredCreditKWh = expired
		res.ExportPayment += expired * pr.ExportPrice
		res.Sealed = true
		res.LiveCreditBalance = balance
		lastSealedAtOrBefore = res
		if m == through {
			return res, balance
		}
	}
	if _, isSealed := c.sealed[through]; isSealed {
		return lastSealedAtOrBefore, balance
	}
	// through 晚于所有已重放月份但本身已封账（理论上已被上面覆盖），兜底直接返回冻结结果。
	if _, isSealed := c.sealed[through]; !isSealed {
		p := c.params.at(through)
		pr := c.prices.at(through)
		res := computeMonth(through, c.reads[through], n.g, p, pr, func(want int64) int64 {
			used := int64(0)
			for i := range cr {
				if used >= want {
					break
				}
				if cr[i].deposit >= through || cr[i].expiry < through || cr[i].amount == 0 {
					continue
				}
				take := want - used
				if take > cr[i].amount {
					take = cr[i].amount
				}
				cr[i].amount -= take
				used += take
			}
			return used
		})
		res.LiveCreditBalance = balance
		return res, balance
	}
	return target, balance
}

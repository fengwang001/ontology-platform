// Package clinic 实现预防接种门诊服务：历史补录、当日接种与剂次有效性评估。
package clinic

import (
	"fmt"
	"sort"
	"sync"

	"ontology/lotstock"
	"ontology/vaxrule"
)

// Kind 为操作失败的类别，拒绝时按既定次序只报第一个。
type Kind int

const (
	KindInvalidParam Kind = iota // 参数非法
	KindClock                    // 时钟回退
	KindNotFound                 // 患者/系列/批次不存在
	KindNoGrant                  // 无接种资格
	KindDuplicate                // 同患者同系列同日重复
	KindCompleted                // 系列已完成
	KindTooEarly                 // 过早（今日接种将被判无效）
	KindNoStock                  // 无库存
)

func (k Kind) String() string {
	switch k {
	case KindInvalidParam:
		return "参数非法"
	case KindClock:
		return "时钟回退"
	case KindNotFound:
		return "不存在"
	case KindNoGrant:
		return "无接种资格"
	case KindDuplicate:
		return "重复"
	case KindCompleted:
		return "系列已完成"
	case KindTooEarly:
		return "过早"
	case KindNoStock:
		return "无库存"
	}
	return "未知"
}

// OpError 为操作拒绝错误；过早时携带无效原因与最早可接种日。
type OpError struct {
	Kind     Kind
	Reason   vaxrule.Reason
	Earliest int
	Msg      string
}

func (e *OpError) Error() string { return e.Msg }

func fail(kind Kind, format string, args ...any) *OpError {
	return &OpError{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// NextDose 为某未完成系列的下一剂信息。
type NextDose struct {
	Series   string
	Dose     int
	Earliest int
}

// EvalResult 为 Evaluate 的只读结果。
type EvalResult struct {
	Judgments []vaxrule.Judgment // 按（日期，系列名）升序
	Next      []NextDose         // 按系列名字节序
}

type patient struct {
	birth     int
	recs      []vaxrule.Record
	have      map[string]map[int]bool // 系列 -> 日期集合，查重
	states    map[string]*vaxrule.SeriesState
	liveDates map[int]map[string]bool // 日期 -> 当日有记录的活疫苗系列
}

func newPatient(birth int) *patient {
	return &patient{
		birth:     birth,
		have:      make(map[string]map[int]bool),
		states:    make(map[string]*vaxrule.SeriesState),
		liveDates: make(map[int]map[string]bool),
	}
}

// stateOf 返回某系列的增量状态，不存在则初始化。
func (p *patient) stateOf(series string) *vaxrule.SeriesState {
	st, ok := p.states[series]
	if !ok {
		z := vaxrule.ZeroState()
		st = &z
		p.states[series] = st
	}
	return st
}

// maxOtherLive 返回窗口 [d−LiveGap+1, d−1] 内其他活疫苗系列记录的最大日期，无则 -1。
func (p *patient) maxOtherLive(exclude string, d int) int {
	best := -1
	lo := d - vaxrule.LiveGap + 1
	if lo < 0 {
		lo = 0
	}
	for t := lo; t <= d-1; t++ {
		for name := range p.liveDates[t] {
			if name != exclude {
				best = t
				break
			}
		}
	}
	return best
}

// Clinic 为门诊服务。全部方法可并发调用，效果等价于某个串行顺序。
type Clinic struct {
	mu       sync.Mutex
	maxNow   int
	hasNow   bool
	series   map[string]vaxrule.Series
	patients map[string]*patient
	nurses   map[string]bool
	stock    *lotstock.Stock
	touched  int // 非导出计数器：全量重算时读取的接种记录条数
}

// New 创建空门诊。
func New() *Clinic {
	return &Clinic{
		series:   make(map[string]vaxrule.Series),
		patients: make(map[string]*patient),
		nurses:   make(map[string]bool),
		stock:    lotstock.New(),
	}
}

func validDay(d int) bool { return d >= 0 && d <= vaxrule.MaxDay }

// AddSeries 注册疫苗系列。
func (c *Clinic) AddSeries(name string, live bool, n int, minAge, minInt []int, r int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, dup := c.series[name]; dup {
		return fail(KindInvalidParam, "系列 %q 已存在", name)
	}
	s, err := vaxrule.NewSeries(name, live, n, minAge, minInt, r)
	if err != nil {
		return fail(KindInvalidParam, "%v", err)
	}
	c.series[name] = s
	return nil
}

// AddPatient 登记患者。
func (c *Clinic) AddPatient(name string, birth int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if name == "" || !validDay(birth) {
		return fail(KindInvalidParam, "患者名或出生日 %d 非法", birth)
	}
	if _, dup := c.patients[name]; dup {
		return fail(KindInvalidParam, "患者 %q 已存在", name)
	}
	c.patients[name] = newPatient(birth)
	return nil
}

// AddLot 新增批次。
func (c *Clinic) AddLot(lot, series string, exp, qty int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if lot == "" || !validDay(exp) || qty < 0 {
		return fail(KindInvalidParam, "批次参数非法")
	}
	if _, ok := c.series[series]; !ok {
		return fail(KindNotFound, "系列 %q 不存在", series)
	}
	if c.stock.Has(lot) {
		return fail(KindInvalidParam, "批次 %q 已存在", lot)
	}
	return c.stock.AddLot(lot, series, exp, qty)
}

// Quarantine 隔离或解除批次。
func (c *Clinic) Quarantine(lot string, on bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if lot == "" {
		return fail(KindInvalidParam, "批号为空")
	}
	if !c.stock.Has(lot) {
		return fail(KindNotFound, "批次 %q 不存在", lot)
	}
	return c.stock.Quarantine(lot, on)
}

// Grant 授予接种资格。
func (c *Clinic) Grant(user string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if user == "" {
		return fail(KindInvalidParam, "用户名为空")
	}
	c.nurses[user] = true
	return nil
}

// checkClock 校验时钟并（在操作被接受时）推进 maxNow。
func (c *Clinic) checkClock(now int) error {
	if c.hasNow && now < c.maxNow {
		return fail(KindClock, "now=%d 小于已接受的最大 now=%d", now, c.maxNow)
	}
	return nil
}

func (c *Clinic) accept(now int) {
	if !c.hasNow || now > c.maxNow {
		c.maxNow = now
	}
	c.hasNow = true
}

// Record 补录历史记录：不扣库存、不做有效性拦截，补录后整体重算。
func (c *Clinic) Record(now int, patientName, seriesName string, d int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if patientName == "" || seriesName == "" || !validDay(now) || !validDay(d) || d > now {
		return fail(KindInvalidParam, "Record 参数非法: now=%d d=%d", now, d)
	}
	if err := c.checkClock(now); err != nil {
		return err
	}
	p, ok := c.patients[patientName]
	if !ok {
		return fail(KindNotFound, "患者 %q 不存在", patientName)
	}
	if _, ok := c.series[seriesName]; !ok {
		return fail(KindNotFound, "系列 %q 不存在", seriesName)
	}
	if d < p.birth {
		return fail(KindInvalidParam, "接种日 %d 早于出生日 %d", d, p.birth)
	}
	if p.have[seriesName][d] {
		return fail(KindDuplicate, "患者 %q 系列 %q 日期 %d 已有记录", patientName, seriesName, d)
	}
	p.recs = append(p.recs, vaxrule.Record{Date: d, Series: seriesName})
	if p.have[seriesName] == nil {
		p.have[seriesName] = make(map[int]bool)
	}
	p.have[seriesName][d] = true
	c.recompute(p)
	c.accept(now)
	return nil
}

// recompute 全量重算一名患者的判定并重建增量状态。
func (c *Clinic) recompute(p *patient) {
	judgments := vaxrule.JudgeAll(p.birth, c.series, p.recs)
	c.touched += len(p.recs)
	p.states = make(map[string]*vaxrule.SeriesState)
	p.liveDates = make(map[int]map[string]bool)
	for _, j := range judgments {
		s := c.series[j.Rec.Series]
		st := p.stateOf(j.Rec.Series)
		st.LastRecDate = j.Rec.Date
		st.LastRecInvalid = j.Status == vaxrule.StatusInvalid
		if j.Status == vaxrule.StatusValid {
			st.Valid++
			st.LastValidDate = j.Rec.Date
		}
		if s.Live {
			if p.liveDates[j.Rec.Date] == nil {
				p.liveDates[j.Rec.Date] = make(map[string]bool)
			}
			p.liveDates[j.Rec.Date][j.Rec.Series] = true
		}
	}
}

// Evaluate 只读评估：返回逐条判定与每个未完成系列的下一剂信息。
func (c *Clinic) Evaluate(patientName string) (*EvalResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.patients[patientName]
	if !ok {
		return nil, fail(KindNotFound, "患者 %q 不存在", patientName)
	}
	judgments := vaxrule.JudgeAll(p.birth, c.series, p.recs)
	c.touched += len(p.recs)
	res := &EvalResult{Judgments: judgments}
	names := make([]string, 0, len(c.series))
	for name := range c.series {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		s := c.series[name]
		st := p.stateOf(name)
		dose, base, ok := vaxrule.EarliestBase(p.birth, s, *st, c.maxNow)
		if !ok {
			continue
		}
		earliest := vaxrule.AdjustLive(s, base, func(d int) int {
			return p.maxOtherLive(name, d)
		})
		res.Next = append(res.Next, NextDose{Series: name, Dose: dose, Earliest: earliest})
	}
	return res, nil
}

// Administer 当日接种：日期为 now，成功后追加一条必为有效的记录并扣减批次。
func (c *Clinic) Administer(now int, nurse, patientName, seriesName string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if nurse == "" || patientName == "" || seriesName == "" || !validDay(now) {
		return "", fail(KindInvalidParam, "Administer 参数非法: now=%d", now)
	}
	if err := c.checkClock(now); err != nil {
		return "", err
	}
	p, ok := c.patients[patientName]
	if !ok {
		return "", fail(KindNotFound, "患者 %q 不存在", patientName)
	}
	s, ok := c.series[seriesName]
	if !ok {
		return "", fail(KindNotFound, "系列 %q 不存在", seriesName)
	}
	if !c.nurses[nurse] {
		return "", fail(KindNoGrant, "%q 无接种资格", nurse)
	}
	if p.have[seriesName][now] {
		return "", fail(KindDuplicate, "患者 %q 系列 %q 今日已有记录", patientName, seriesName)
	}
	st := p.stateOf(seriesName)
	if st.Valid >= s.N {
		return "", fail(KindCompleted, "系列 %q 已完成 %d 剂", seriesName, s.N)
	}
	conflict := p.maxOtherLive(seriesName, now) >= 0
	status, reason := vaxrule.JudgeOne(p.birth, s, *st, now, conflict)
	if status == vaxrule.StatusInvalid {
		_, base, _ := vaxrule.EarliestBase(p.birth, s, *st, now)
		earliest := vaxrule.AdjustLive(s, base, func(d int) int {
			return p.maxOtherLive(seriesName, d)
		})
		return "", &OpError{
			Kind:     KindTooEarly,
			Reason:   reason,
			Earliest: earliest,
			Msg:      fmt.Sprintf("过早：%s，最早可接种日 %d", reason, earliest),
		}
	}
	lot, ok := c.stock.Acquire(seriesName, now)
	if !ok {
		return "", fail(KindNoStock, "系列 %q 无可用批次", seriesName)
	}
	p.recs = append(p.recs, vaxrule.Record{Date: now, Series: seriesName})
	if p.have[seriesName] == nil {
		p.have[seriesName] = make(map[int]bool)
	}
	p.have[seriesName][now] = true
	st.Valid++
	st.LastValidDate = now
	st.LastRecDate = now
	st.LastRecInvalid = false
	if s.Live {
		if p.liveDates[now] == nil {
			p.liveDates[now] = make(map[string]bool)
		}
		p.liveDates[now][seriesName] = true
	}
	c.accept(now)
	return lot, nil
}

package dr

// 本文件实现一个按需求规则逐步写成的独立朴素模型（线性扫描、无索引），
// 与生产实现对照大量随机操作序列：每条输入、输出与判定依据均打印日志，
// 两侧输出逐条比对，任何分歧即失败。

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ---------- 朴素模型 ----------

type nCommit struct {
	p             string
	committed     float64
	lateWithdrawn bool
	active        bool
}

type nInvite struct {
	p         string
	requested float64
	responded bool
	accepted  bool
}

type nEvent struct {
	id         string
	params     EventParams
	cancelled  bool
	afterStart bool
	cancelTick int64
	settled    bool
	invites    []*nInvite
	commits    []*nCommit
	assess     *Assessment
}

type nKey struct {
	p    string
	tick int64
}

type naive struct {
	cfg       Config
	last      int64
	clockInit bool
	nextID    int
	events    []*nEvent
	data      map[nKey]float64
}

func newNaive(cfg Config) *naive {
	return &naive{cfg: cfg, data: map[nKey]float64{}}
}

func (m *naive) checkClock(now int64) *Error {
	if m.clockInit && now < m.last {
		return newErr(ErrKindClock, "当前时刻 %d 早于上次被接受操作的时刻 %d", now, m.last)
	}
	return nil
}

func (m *naive) advance(now int64) { m.last, m.clockInit = now, true }

func (m *naive) findEvent(id string) *nEvent {
	for _, e := range m.events {
		if e.id == id {
			return e
		}
	}
	return nil
}

func (m *naive) windowEnd(e *nEvent) int64 {
	return e.params.WindowStart + int64(e.params.WindowIntervals)*m.cfg.IntervalTicks
}

func (m *naive) effWindowEnd(e *nEvent) int64 {
	end := m.windowEnd(e)
	if e.cancelled && e.afterStart {
		t := e.cancelTick - e.cancelTick%m.cfg.IntervalTicks
		if t < e.params.WindowStart {
			t = e.params.WindowStart
		}
		if t < end {
			end = t
		}
	}
	return end
}

func (m *naive) state(e *nEvent, now int64) State {
	if e.settled {
		return StateSettled
	}
	if e.cancelled {
		return StateCancelled
	}
	if now < e.params.WindowStart {
		return StatePublished
	}
	if now < m.windowEnd(e) {
		return StateRunning
	}
	return StateEnded
}

func (m *naive) findInvite(e *nEvent, p string) *nInvite {
	for _, inv := range e.invites {
		if inv.p == p {
			return inv
		}
	}
	return nil
}

func (m *naive) activeCommit(e *nEvent, p string) *nCommit {
	for _, c := range e.commits {
		if c.p == p && c.active {
			return c
		}
	}
	return nil
}

func (m *naive) create(now int64, p EventParams) (string, error) {
	c := &m.cfg
	param := func(format string, args ...any) (string, error) {
		return "", newErr(ErrKindParam, format, args...)
	}
	if p.Day < 0 || p.WindowStart < 0 || p.WindowIntervals <= 0 {
		return param("事件日、窗口起点须非负且窗口长度须为正")
	}
	if p.WindowStart%c.IntervalTicks != 0 {
		return param("窗口起点未对齐计量间隔")
	}
	dayStart := p.Day * c.TicksPerDay
	if p.WindowStart-int64(c.AdjustmentIntervals)*c.IntervalTicks < dayStart {
		return param("调整期超出事件日边界")
	}
	if p.WindowStart+int64(p.WindowIntervals)*c.IntervalTicks > dayStart+c.TicksPerDay {
		return param("事件窗口跨日")
	}
	if !(p.ResponseDeadline < p.ExitDeadline) {
		return param("应答截止须早于免责退出截止")
	}
	if p.ExitDeadline > p.WindowStart {
		return param("免责退出截止须不晚于窗口起点")
	}
	if p.PayUnitPrice < 0 || p.PenaltyUnitPrice < 0 ||
		math.IsNaN(p.PayUnitPrice) || math.IsNaN(p.PenaltyUnitPrice) {
		return param("单价须非负")
	}
	if p.QualifiedRatio <= 0 || p.QualifiedRatio > 1 || math.IsNaN(p.QualifiedRatio) {
		return param("履约合格比例须在 (0,1] 内")
	}
	if err := m.checkClock(now); err != nil {
		return "", err
	}
	m.nextID++
	id := fmt.Sprintf("E%d", m.nextID)
	m.events = append(m.events, &nEvent{id: id, params: p})
	m.advance(now)
	return id, nil
}

func (m *naive) invite(now int64, eventID, p string, requested float64) error {
	if math.IsNaN(requested) || requested < m.cfg.MinCommitment {
		return newErr(ErrKindParam, "请求削减量小于最小承诺量")
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	e := m.findEvent(eventID)
	if e == nil {
		return newErr(ErrKindEventState, "事件不存在")
	}
	if st := m.state(e, now); st != StatePublished {
		return newErr(ErrKindEventState, "事件状态为 %s", st)
	}
	if m.findInvite(e, p) != nil {
		return newErr(ErrKindEventState, "重复邀约")
	}
	e.invites = append(e.invites, &nInvite{p: p, requested: requested})
	m.advance(now)
	return nil
}

func (m *naive) respond(now int64, eventID, p string, accept bool, amount float64) error {
	e := m.findEvent(eventID)
	var inv *nInvite
	if e != nil {
		inv = m.findInvite(e, p)
	}
	if accept {
		if math.IsNaN(amount) || amount < m.cfg.MinCommitment {
			return newErr(ErrKindParam, "承诺量小于最小承诺量")
		}
		if inv != nil && amount > inv.requested {
			return newErr(ErrKindParam, "承诺量大于请求量")
		}
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	if e == nil {
		return newErr(ErrKindEventState, "事件不存在")
	}
	if st := m.state(e, now); st != StatePublished {
		return newErr(ErrKindEventState, "事件状态为 %s", st)
	}
	if inv == nil || inv.responded {
		return newErr(ErrKindNotInvited, "无有效邀约")
	}
	if now >= e.params.ResponseDeadline {
		return newErr(ErrKindDeadline, "应答截止已过")
	}
	if accept {
		for _, oe := range m.events {
			for _, c := range oe.commits {
				if !c.active || c.p != p {
					continue
				}
				if overlap(oe.params.WindowStart, m.effWindowEnd(oe),
					e.params.WindowStart, m.windowEnd(e)) {
					return newErr(ErrKindEventConflict, "与事件 %s 窗口重叠", oe.id)
				}
			}
		}
	}
	inv.responded = true
	inv.accepted = accept
	if accept {
		e.commits = append(e.commits, &nCommit{p: p, committed: amount, active: true})
	}
	m.advance(now)
	return nil
}

func (m *naive) withdraw(now int64, eventID, p string) error {
	if err := m.checkClock(now); err != nil {
		return err
	}
	e := m.findEvent(eventID)
	if e == nil {
		return newErr(ErrKindEventState, "事件不存在")
	}
	if st := m.state(e, now); st == StateCancelled || st == StateSettled {
		return newErr(ErrKindEventState, "事件状态为 %s", st)
	}
	c := m.activeCommit(e, p)
	if c == nil {
		return newErr(ErrKindNotInvited, "无有效承诺")
	}
	switch {
	case now < e.params.ExitDeadline:
		c.active = false
	case now < m.windowEnd(e):
		c.lateWithdrawn = true
	default:
		return newErr(ErrKindDeadline, "窗口已结束")
	}
	m.advance(now)
	return nil
}

func (m *naive) cancel(now int64, eventID string) error {
	if err := m.checkClock(now); err != nil {
		return err
	}
	e := m.findEvent(eventID)
	if e == nil {
		return newErr(ErrKindEventState, "事件不存在")
	}
	if e.cancelled || e.settled {
		return newErr(ErrKindEventState, "事件已取消或已考核")
	}
	if now < e.params.WindowStart {
		e.cancelled = true
		for _, c := range e.commits {
			c.active = false
		}
	} else {
		e.cancelled = true
		e.afterStart = true
		e.cancelTick = now
	}
	m.advance(now)
	return nil
}

func (m *naive) register(now int64, p string, tick int64, value float64) error {
	if tick < 0 || tick%m.cfg.IntervalTicks != 0 || math.IsNaN(value) || value < 0 {
		return newErr(ErrKindParam, "间隔或电量非法")
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	k := nKey{p, tick}
	if old, ok := m.data[k]; ok {
		if old != value {
			return newErr(ErrKindDataConflict, "间隔 %d 已登记 %v", tick, old)
		}
		m.advance(now)
		return nil
	}
	var wm int64
	for _, e := range m.events {
		if !e.settled || m.activeCommit(e, p) == nil {
			continue
		}
		if end := m.effWindowEnd(e); end > wm {
			wm = end
		}
	}
	if tick < wm {
		return newErr(ErrKindSettled, "相关事件已考核")
	}
	m.data[k] = value
	m.advance(now)
	return nil
}

func (m *naive) dayOffsets(e *nEvent) (adjust, window []int64) {
	dayStart := e.params.Day * m.cfg.TicksPerDay
	adjStart := e.params.WindowStart - int64(m.cfg.AdjustmentIntervals)*m.cfg.IntervalTicks
	for t := adjStart; t < e.params.WindowStart; t += m.cfg.IntervalTicks {
		adjust = append(adjust, t-dayStart)
	}
	for t := e.params.WindowStart; t < m.effWindowEnd(e); t += m.cfg.IntervalTicks {
		window = append(window, t-dayStart)
	}
	return adjust, window
}

func (m *naive) qualifyingDays(p string, e *nEvent, offsets []int64) []int64 {
	excluded := map[int64]bool{}
	for _, oe := range m.events {
		if oe.cancelled && !oe.afterStart {
			continue
		}
		if m.activeCommit(oe, p) == nil {
			continue
		}
		excluded[oe.params.Day] = true
	}
	var days []int64
	for d := e.params.Day - 1; d >= 0 &&
		d >= e.params.Day-int64(m.cfg.MaxLookbackDays) &&
		len(days) < m.cfg.QualifyingDays; d-- {
		if m.cfg.workday(d) != m.cfg.workday(e.params.Day) {
			continue
		}
		if excluded[d] {
			continue
		}
		base := d * m.cfg.TicksPerDay
		complete := true
		for _, off := range offsets {
			if _, ok := m.data[nKey{p, base + off}]; !ok {
				complete = false
				break
			}
		}
		if complete {
			days = append(days, d)
		}
	}
	return days
}

func (m *naive) finish(e *nEvent, r *ParticipantResult) {
	if r.Committed > 0 {
		r.Ratio = r.Reduction / r.Committed
	}
	switch {
	case r.Ratio >= 1:
		r.Payment = r.Committed * e.params.PayUnitPrice
	case r.Ratio >= e.params.QualifiedRatio:
		r.Payment = r.Reduction * e.params.PayUnitPrice
	default:
		r.Penalty = r.Committed * e.params.PenaltyUnitPrice
	}
}

func (m *naive) settle(p string, e *nEvent, adjustOffs, windowOffs []int64) ParticipantResult {
	c := m.activeCommit(e, p)
	committed := c.committed * float64(len(windowOffs)) / float64(e.params.WindowIntervals)
	r := ParticipantResult{Participant: p, Committed: committed, LateWithdrawn: c.lateWithdrawn}
	if c.lateWithdrawn {
		m.finish(e, &r)
		return r
	}
	all := append(append([]int64{}, adjustOffs...), windowOffs...)
	days := m.qualifyingDays(p, e, all)
	r.QualifyingDays = len(days)
	if len(days) < m.cfg.MinQualifyingDays {
		return r
	}
	r.Assessable = true
	means := make([]float64, len(all))
	for _, d := range days {
		base := d * m.cfg.TicksPerDay
		for i, off := range all {
			means[i] += m.data[nKey{p, base + off}]
		}
	}
	for i := range means {
		means[i] /= float64(len(days))
	}
	dayStart := e.params.Day * m.cfg.TicksPerDay
	var actualAdj, baseAdj float64
	for i := range adjustOffs {
		actualAdj += m.data[nKey{p, dayStart + adjustOffs[i]}]
		baseAdj += means[i]
	}
	ratio := 1.0
	if baseAdj != 0 {
		ratio = actualAdj / baseAdj
		if ratio < m.cfg.AdjRatioLower {
			ratio = m.cfg.AdjRatioLower
		}
		if ratio > m.cfg.AdjRatioUpper {
			ratio = m.cfg.AdjRatioUpper
		}
	}
	r.AdjRatio = ratio
	var red float64
	for j, off := range windowOffs {
		actual := m.data[nKey{p, dayStart + off}]
		if d := means[len(adjustOffs)+j]*ratio - actual; d > 0 {
			red += d
		}
	}
	r.Reduction = red
	m.finish(e, &r)
	return r
}

func (m *naive) assess(now int64, eventID string) (*Assessment, error) {
	if err := m.checkClock(now); err != nil {
		return nil, err
	}
	e := m.findEvent(eventID)
	if e == nil {
		return nil, newErr(ErrKindEventState, "事件不存在")
	}
	st := m.state(e, now)
	if !(st == StateEnded || (st == StateCancelled && e.afterStart)) {
		return nil, newErr(ErrKindEventState, "事件状态为 %s", st)
	}
	adjustOffs, windowOffs := m.dayOffsets(e)
	all := append(append([]int64{}, adjustOffs...), windowOffs...)
	var ps []string
	for _, c := range e.commits {
		if c.active {
			ps = append(ps, c.p)
		}
	}
	sort.Strings(ps)
	dayStart := e.params.Day * m.cfg.TicksPerDay
	for _, p := range ps {
		for i, off := range all {
			if _, ok := m.data[nKey{p, dayStart + off}]; !ok {
				which := "调整期"
				if i >= len(adjustOffs) {
					which = "事件窗口"
				}
				return nil, &Error{
					Kind:        ErrKindIncomplete,
					Msg:         fmt.Sprintf("事件 %s 考核被拒绝：数据不齐全", eventID),
					Participant: p,
					Reason:      fmt.Sprintf("%s间隔 %d 用电数据缺失", which, dayStart+off),
				}
			}
		}
	}
	res := &Assessment{EventID: eventID}
	for _, p := range ps {
		res.Results = append(res.Results, m.settle(p, e, adjustOffs, windowOffs))
	}
	e.assess = res
	e.settled = true
	m.advance(now)
	return res, nil
}

// ---------- 随机操作序列对照 ----------

type opKind int

const (
	opCreate opKind = iota
	opInvite
	opRespond
	opWithdraw
	opCancel
	opRegister
	opAssess
)

type op struct {
	kind   opKind
	now    int64
	id     string
	p      string
	amount float64
	accept bool
	tick   int64
	value  float64
	params EventParams
}

func (o op) String() string {
	switch o.kind {
	case opCreate:
		return fmt.Sprintf("create(now=%d, %+v)", o.now, o.params)
	case opInvite:
		return fmt.Sprintf("invite(now=%d, %s, %s, req=%s)", o.now, o.id, o.p, f2s(o.amount))
	case opRespond:
		return fmt.Sprintf("respond(now=%d, %s, %s, accept=%v, amt=%s)", o.now, o.id, o.p, o.accept, f2s(o.amount))
	case opWithdraw:
		return fmt.Sprintf("withdraw(now=%d, %s, %s)", o.now, o.id, o.p)
	case opCancel:
		return fmt.Sprintf("cancel(now=%d, %s)", o.now, o.id)
	case opRegister:
		return fmt.Sprintf("register(now=%d, %s, tick=%d, v=%s)", o.now, o.p, o.tick, f2s(o.value))
	case opAssess:
		return fmt.Sprintf("assess(now=%d, %s)", o.now, o.id)
	}
	return "?"
}

func f2s(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

func errStr(err error) string {
	if err == nil {
		return "ok"
	}
	if e, ok := err.(*Error); ok {
		if e.Participant != "" {
			return fmt.Sprintf("err:%s:%s:%s", e.Kind, e.Participant, e.Reason)
		}
		return "err:" + e.Kind.String()
	}
	return "err:?"
}

func formatAssessment(a *Assessment) string {
	var b strings.Builder
	for _, r := range a.Results {
		fmt.Fprintf(&b, "{%s assessable=%v late=%v qdays=%d adj=%s committed=%s reduction=%s ratio=%s pay=%s penalty=%s}",
			r.Participant, r.Assessable, r.LateWithdrawn, r.QualifyingDays,
			f2s(r.AdjRatio), f2s(r.Committed), f2s(r.Reduction), f2s(r.Ratio),
			f2s(r.Payment), f2s(r.Penalty))
	}
	return b.String()
}

func applySys(s *System, o op) string {
	switch o.kind {
	case opCreate:
		id, err := s.CreateEvent(o.now, o.params)
		if err != nil {
			return errStr(err)
		}
		return "ok:" + id
	case opInvite:
		return errStr(s.Invite(o.now, o.id, o.p, o.amount))
	case opRespond:
		return errStr(s.Respond(o.now, o.id, o.p, o.accept, o.amount))
	case opWithdraw:
		return errStr(s.Withdraw(o.now, o.id, o.p))
	case opCancel:
		return errStr(s.CancelEvent(o.now, o.id))
	case opRegister:
		return errStr(s.RegisterData(o.now, o.p, o.tick, o.value))
	case opAssess:
		res, err := s.Assess(o.now, o.id)
		if err != nil {
			return errStr(err)
		}
		return "ok:" + formatAssessment(res)
	}
	return "?"
}

func applyNaive(m *naive, o op) string {
	switch o.kind {
	case opCreate:
		id, err := m.create(o.now, o.params)
		if err != nil {
			return errStr(err)
		}
		return "ok:" + id
	case opInvite:
		return errStr(m.invite(o.now, o.id, o.p, o.amount))
	case opRespond:
		return errStr(m.respond(o.now, o.id, o.p, o.accept, o.amount))
	case opWithdraw:
		return errStr(m.withdraw(o.now, o.id, o.p))
	case opCancel:
		return errStr(m.cancel(o.now, o.id))
	case opRegister:
		return errStr(m.register(o.now, o.p, o.tick, o.value))
	case opAssess:
		res, err := m.assess(o.now, o.id)
		if err != nil {
			return errStr(err)
		}
		return "ok:" + formatAssessment(res)
	}
	return "?"
}

func detValue(p string, tick int64) float64 {
	h := int64(17)
	for _, c := range p {
		h = h*31 + int64(c)
	}
	h = h*31 + tick
	if h < 0 {
		h = -h
	}
	return float64(h%1000) / 10
}

type gen struct {
	r      *rand.Rand
	cfg    Config
	cur    int64
	ids    []string
	params map[string]EventParams
	parts  []string
}

func (g *gen) randNow() int64 {
	g.cur += g.r.Int63n(180)
	if g.r.Intn(100) < 5 {
		t := g.cur - g.r.Int63n(600)
		if t < 0 {
			t = 0
		}
		return t
	}
	return g.cur
}

func (g *gen) someEvent() string {
	if len(g.ids) == 0 || g.r.Intn(100) < 5 {
		return "NOPE"
	}
	return g.ids[g.r.Intn(len(g.ids))]
}

func (g *gen) somePart() string { return g.parts[g.r.Intn(len(g.parts))] }

func (g *gen) randomParams() EventParams {
	day := g.cur/g.cfg.TicksPerDay + int64(g.r.Intn(6))
	startOff := int64(120 + 15*g.r.Intn(40))
	p := EventParams{
		Day:              day,
		WindowStart:      day*g.cfg.TicksPerDay + startOff,
		WindowIntervals:  2 + g.r.Intn(4),
		PayUnitPrice:     1 + g.r.Float64()*3,
		PenaltyUnitPrice: 1 + g.r.Float64()*3,
		QualifiedRatio:   0.3 + g.r.Float64()*0.6,
	}
	p.ResponseDeadline = p.WindowStart - 120
	p.ExitDeadline = p.WindowStart - 60
	if g.r.Intn(100) < 10 {
		switch g.r.Intn(3) {
		case 0:
			p.ExitDeadline = p.WindowStart + 15
		case 1:
			p.ResponseDeadline = p.ExitDeadline
		case 2:
			p.WindowStart++
		}
	}
	return p
}

func (g *gen) randomOp() op {
	now := g.randNow()
	roll := g.r.Intn(100)
	switch {
	case roll < 18:
		return op{kind: opCreate, now: now, params: g.randomParams()}
	case roll < 35:
		return op{kind: opInvite, now: now, id: g.someEvent(), p: g.somePart(), amount: 5 + g.r.Float64()*60}
	case roll < 55:
		return op{kind: opRespond, now: now, id: g.someEvent(), p: g.somePart(),
			accept: g.r.Intn(100) < 75, amount: 5 + g.r.Float64()*60}
	case roll < 65:
		return op{kind: opWithdraw, now: now, id: g.someEvent(), p: g.somePart()}
	case roll < 73:
		return op{kind: opCancel, now: now, id: g.someEvent()}
	case roll < 88:
		var tick int64
		if len(g.ids) > 0 && g.r.Intn(100) < 70 {
			ep := g.params[g.ids[g.r.Intn(len(g.ids))]]
			day := ep.Day - int64(g.r.Intn(13))
			if day < 0 {
				day = 0
			}
			tick = day*g.cfg.TicksPerDay + int64(15*g.r.Intn(96))
		} else {
			tick = int64(15 * g.r.Intn(96))
		}
		p := g.somePart()
		v := detValue(p, tick)
		if g.r.Intn(100) < 10 {
			v += 0.5
		}
		return op{kind: opRegister, now: now, p: p, tick: tick, value: v}
	default:
		return op{kind: opAssess, now: now, id: g.someEvent()}
	}
}

// flow 生成一段“快乐路径”：创建事件、邀约、接受、补齐数据、推进到结束后考核。
func (g *gen) flow(do func(op) string) {
	day := g.cur/g.cfg.TicksPerDay + 2 + int64(g.r.Intn(4))
	startOff := int64(600 + 15*g.r.Intn(8))
	p := EventParams{
		Day:              day,
		WindowStart:      day*g.cfg.TicksPerDay + startOff,
		WindowIntervals:  2 + g.r.Intn(3),
		PayUnitPrice:     1 + g.r.Float64()*3,
		PenaltyUnitPrice: 1 + g.r.Float64()*3,
		QualifiedRatio:   0.3 + g.r.Float64()*0.5,
	}
	p.ResponseDeadline = p.WindowStart - 120
	p.ExitDeadline = p.WindowStart - 60
	g.cur += 5
	rs := do(op{kind: opCreate, now: g.cur, params: p})
	if !strings.HasPrefix(rs, "ok:") {
		return
	}
	id := rs[3:]
	n := 1 + g.r.Intn(len(g.parts))
	for i := 0; i < n; i++ {
		part := g.parts[i]
		g.cur += 3
		if do(op{kind: opInvite, now: g.cur, id: id, p: part, amount: 40}) != "ok" {
			continue
		}
		g.cur += 3
		if g.r.Intn(100) < 80 {
			amt := 10 + float64(g.r.Intn(31))
			do(op{kind: opRespond, now: g.cur, id: id, p: part, accept: true, amount: amt})
		} else {
			do(op{kind: opRespond, now: g.cur, id: id, p: part, accept: false})
		}
	}
	if g.r.Intn(100) < 20 {
		g.cur += 10
		do(op{kind: opCancel, now: g.cur, id: id})
		return
	}
	var offs []int64
	for off := startOff - 60; off < startOff+int64(p.WindowIntervals)*15; off += 15 {
		offs = append(offs, off)
	}
	for d := day - 12; d <= day; d++ {
		if d < 0 {
			continue
		}
		for _, off := range offs {
			for _, part := range g.parts {
				g.cur++
				tick := d*g.cfg.TicksPerDay + off
				do(op{kind: opRegister, now: g.cur, p: part, tick: tick, value: detValue(part, tick)})
			}
		}
	}
	end := p.WindowStart + int64(p.WindowIntervals)*g.cfg.IntervalTicks
	if g.cur < end {
		g.cur = end
	}
	g.cur += 5
	do(op{kind: opAssess, now: g.cur, id: id})
}

func (g *gen) mkParams(day, startOff int64, intervals int) EventParams {
	p := EventParams{
		Day:              day,
		WindowStart:      day*g.cfg.TicksPerDay + startOff,
		WindowIntervals:  intervals,
		PayUnitPrice:     1 + g.r.Float64()*3,
		PenaltyUnitPrice: 1 + g.r.Float64()*3,
		QualifiedRatio:   0.3 + g.r.Float64()*0.5,
	}
	p.ResponseDeadline = p.WindowStart - 120
	p.ExitDeadline = p.WindowStart - 60
	return p
}

// conflictFlow 制造重叠窗口：同一参与者接受两个窗口重叠的事件。
func (g *gen) conflictFlow(do func(op) string) {
	day := g.cur/g.cfg.TicksPerDay + 2 + int64(g.r.Intn(4))
	p1 := g.mkParams(day, int64(600+15*g.r.Intn(8)), 4)
	p2 := g.mkParams(day, p1.WindowStart-day*g.cfg.TicksPerDay+30, 4)
	part := g.somePart()
	g.cur += 5
	r1 := do(op{kind: opCreate, now: g.cur, params: p1})
	g.cur += 2
	r2 := do(op{kind: opCreate, now: g.cur, params: p2})
	if !strings.HasPrefix(r1, "ok:") || !strings.HasPrefix(r2, "ok:") {
		return
	}
	id1, id2 := r1[3:], r2[3:]
	g.cur += 2
	if do(op{kind: opInvite, now: g.cur, id: id1, p: part, amount: 40}) != "ok" {
		return
	}
	g.cur += 2
	if do(op{kind: opInvite, now: g.cur, id: id2, p: part, amount: 40}) != "ok" {
		return
	}
	g.cur += 2
	if do(op{kind: opRespond, now: g.cur, id: id1, p: part, accept: true, amount: 30}) != "ok" {
		return
	}
	g.cur += 2
	do(op{kind: opRespond, now: g.cur, id: id2, p: part, accept: true, amount: 30})
}

// incompleteFlow 在数据不齐时发起考核，随后补齐数据重试。
func (g *gen) incompleteFlow(do func(op) string) {
	day := g.cur/g.cfg.TicksPerDay + 2 + int64(g.r.Intn(4))
	p := g.mkParams(day, int64(600+15*g.r.Intn(8)), 2+g.r.Intn(3))
	part := g.somePart()
	g.cur += 5
	rs := do(op{kind: opCreate, now: g.cur, params: p})
	if !strings.HasPrefix(rs, "ok:") {
		return
	}
	id := rs[3:]
	g.cur += 2
	if do(op{kind: opInvite, now: g.cur, id: id, p: part, amount: 40}) != "ok" {
		return
	}
	g.cur += 2
	if do(op{kind: opRespond, now: g.cur, id: id, p: part, accept: true, amount: 25}) != "ok" {
		return
	}
	end := p.WindowStart + int64(p.WindowIntervals)*g.cfg.IntervalTicks
	if g.cur < end {
		g.cur = end
	}
	g.cur += 3
	do(op{kind: opAssess, now: g.cur, id: id})
	// 补齐全部所需数据后重试。
	var offs []int64
	startOff := p.WindowStart - day*g.cfg.TicksPerDay
	for off := startOff - 60; off < startOff+int64(p.WindowIntervals)*15; off += 15 {
		offs = append(offs, off)
	}
	for d := day - 12; d <= day; d++ {
		if d < 0 {
			continue
		}
		for _, off := range offs {
			g.cur++
			tick := d*g.cfg.TicksPerDay + off
			do(op{kind: opRegister, now: g.cur, p: part, tick: tick, value: detValue(part, tick)})
		}
	}
	g.cur += 3
	do(op{kind: opAssess, now: g.cur, id: id})
}

// deadlineFlow 在截止时刻边界上应答与退出。
func (g *gen) deadlineFlow(do func(op) string) {
	day := g.cur/g.cfg.TicksPerDay + 2 + int64(g.r.Intn(4))
	p := g.mkParams(day, int64(600+15*g.r.Intn(8)), 2+g.r.Intn(3))
	part := g.somePart()
	g.cur += 5
	rs := do(op{kind: opCreate, now: g.cur, params: p})
	if !strings.HasPrefix(rs, "ok:") {
		return
	}
	id := rs[3:]
	g.cur += 2
	if do(op{kind: opInvite, now: g.cur, id: id, p: part, amount: 40}) != "ok" {
		return
	}
	// 恰在应答截止时刻应答（视为已过），再提前一刻应答。
	do(op{kind: opRespond, now: p.ResponseDeadline, id: id, p: part, accept: true, amount: 30})
	do(op{kind: opRespond, now: p.ResponseDeadline - 1, id: id, p: part, accept: true, amount: 30})
	// 恰在免责退出截止时刻退出，或窗口结束后退出。
	if g.r.Intn(100) < 50 {
		do(op{kind: opWithdraw, now: p.ExitDeadline, id: id, p: part})
	} else {
		end := p.WindowStart + int64(p.WindowIntervals)*g.cfg.IntervalTicks
		do(op{kind: opWithdraw, now: end, id: id, p: part})
	}
	if g.cur < p.WindowStart {
		g.cur = p.WindowStart
	}
}

func checkInvariants(t *testing.T, s *System) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for part, cs := range s.byPart {
		var wins [][2]int64
		for _, c := range cs {
			e := s.events[c.EventID]
			wins = append(wins, [2]int64{e.Params.WindowStart, e.effectiveWindowEnd()})
		}
		for i := 0; i < len(wins); i++ {
			for j := i + 1; j < len(wins); j++ {
				if overlap(wins[i][0], wins[i][1], wins[j][0], wins[j][1]) {
					t.Fatalf("不变量违反：参与者 %s 接受中的事件窗口重叠 %v 与 %v", part, wins[i], wins[j])
				}
			}
		}
	}
	for _, e := range s.events {
		if e.assessment == nil {
			continue
		}
		for _, r := range e.assessment.Results {
			if r.Payment > 0 && r.Penalty > 0 {
				t.Fatalf("不变量违反：事件 %s 参与者 %s 报酬与违约金同时为正", e.ID, r.Participant)
			}
		}
	}
}

func dumpSys(s *System, now int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	for _, id := range s.eventOrder {
		e := s.events[id]
		fmt.Fprintf(&b, "%s st=%s cancelled=%v afterStart=%v", id, e.state(now), e.Cancelled, e.CancelAfterStart)
		for _, p := range sortedKeys(e.commitments) {
			c := e.commitments[p]
			fmt.Fprintf(&b, " c[%s,%s,late=%v]", p, f2s(c.Committed), c.LateWithdrawn)
		}
		if e.assessment != nil {
			b.WriteString(" assess=" + formatAssessment(e.assessment))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func dumpNaive(m *naive, now int64) string {
	var b strings.Builder
	for _, e := range m.events {
		fmt.Fprintf(&b, "%s st=%s cancelled=%v afterStart=%v", e.id, m.state(e, now), e.cancelled, e.afterStart)
		var ps []string
		for _, c := range e.commits {
			if c.active {
				ps = append(ps, c.p)
			}
		}
		sort.Strings(ps)
		for _, p := range ps {
			c := m.activeCommit(e, p)
			fmt.Fprintf(&b, " c[%s,%s,late=%v]", p, f2s(c.committed), c.lateWithdrawn)
		}
		if e.assess != nil {
			b.WriteString(" assess=" + formatAssessment(e.assess))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			cfg := testConfig()
			sys, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			m := newNaive(cfg)
			g := &gen{
				r:      rand.New(rand.NewSource(seed)),
				cfg:    cfg,
				params: map[string]EventParams{},
				parts:  []string{"P1", "P2", "P3", "P4"},
			}
			do := func(o op) string {
				rs := applySys(sys, o)
				rm := applyNaive(m, o)
				if rs != rm {
					t.Fatalf("分歧 op=%s\n sys  =%s\n model=%s", o, rs, rm)
				}
				t.Logf("op=%s → %s", o, rs)
				if o.kind == opCreate && strings.HasPrefix(rs, "ok:") {
					g.ids = append(g.ids, rs[3:])
					g.params[rs[3:]] = o.params
				}
				checkInvariants(t, sys)
				return rs
			}
			for i := 0; i < 150; i++ {
				roll := g.r.Intn(100)
				switch {
				case roll < 10:
					g.flow(do)
				case roll < 15:
					g.conflictFlow(do)
				case roll < 20:
					g.incompleteFlow(do)
				case roll < 24:
					g.deadlineFlow(do)
				default:
					do(g.randomOp())
				}
			}
			ds := dumpSys(sys, g.cur)
			dm := dumpNaive(m, g.cur)
			if ds != dm {
				t.Fatalf("最终状态分歧\nsys:\n%s\nmodel:\n%s", ds, dm)
			}
			t.Logf("最终状态一致：\n%s", ds)
		})
	}
}

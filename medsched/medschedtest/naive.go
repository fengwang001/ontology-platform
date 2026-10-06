// Package medschedtest 提供独立朴素模型与随机差分测试。
//
// 朴素模型把每张非必要时医嘱在有限视野 horizon 内的全部计划点显式枚举为
// 一个点数组并逐点维护状态；它不使用生产实现的任何判定逻辑，只以最直白的
// 方式逐条实现规格，作为差分测试的独立裁判。
package medschedtest

import "fmt"

const horizon int64 = 300000
const daySecs int64 = 86400

type nStatus int

const (
	nPending nStatus = iota
	nOnTime
	nRefused
	nMadeUp
	nMissed
	nVoid
)

type nPoint struct {
	t      int64
	status nStatus
	actual int64
}

type nDrug struct {
	category string
	minSafe  int64
}

type nOrder struct {
	id        string
	patient   string
	drug      string
	kind      int // 1 间隔 2 时点 3 PRN
	first, h  int64
	times     []int64
	prnMin    int64
	prnLimit  int64
	openedAt  int64
	stoppedAt int64
	points    []nPoint
	prnDoses  []int64
	// 固定间隔重排后当前活动序列的首点锚点（等于最近一次 now+H）。
	activeAnchor int64
}

// Naive 是独立朴素系统。
type Naive struct {
	now       int64
	w         int64
	drugs     map[string]nDrug
	allDrug   map[string]map[string]bool
	allCat    map[string]map[string]bool
	orders    map[string]*nOrder
	orderList []string
	seq       int64
	lastDose  map[string]int64
}

// Error codes mirror medsched 的错误码顺序。
const (
	cInvalid = 1 + iota
	cClock
	cNotFound
	cState
	cAllergy
	cNoPoint
	cInterval
	cLimit
	cMakeup
)

// NResult 是朴素操作结果。
type NResult struct {
	OK   bool
	Code int
	ID   string
}

func bad(code int) NResult { return NResult{OK: false, Code: code} }
func good() NResult        { return NResult{OK: true} }

// NewNaive 构造朴素模型。
func NewNaive(w int64) *Naive {
	return &Naive{
		w:        w,
		drugs:    map[string]nDrug{},
		allDrug:  map[string]map[string]bool{},
		allCat:   map[string]map[string]bool{},
		orders:   map[string]*nOrder{},
		lastDose: map[string]int64{},
	}
}

func (n *Naive) newID() string {
	n.seq++
	return fmt.Sprintf("N-%d", n.seq)
}

func (n *Naive) RegisterDrug(now int64, drug, cat string, min int64) NResult {
	if drug == "" || cat == "" || min <= 0 || now < 0 {
		return bad(cInvalid)
	}
	if now < n.now {
		return bad(cClock)
	}
	if _, ok := n.drugs[drug]; ok {
		return bad(cState)
	}
	n.drugs[drug] = nDrug{category: cat, minSafe: min}
	n.now = now
	return good()
}

func (n *Naive) AddAllergyDrug(now int64, patient, drug string) NResult {
	if now < 0 {
		return bad(cInvalid)
	}
	if now < n.now {
		return bad(cClock)
	}
	if patient == "" {
		return bad(cInvalid)
	}
	if _, ok := n.drugs[drug]; !ok {
		return bad(cNotFound)
	}
	if n.allDrug[patient] == nil {
		n.allDrug[patient] = map[string]bool{}
	}
	n.allDrug[patient][drug] = true
	n.now = now
	return good()
}

func (n *Naive) AddAllergyCat(now int64, patient, cat string) NResult {
	if now < 0 || patient == "" || cat == "" {
		return bad(cInvalid)
	}
	if now < n.now {
		return bad(cClock)
	}
	if n.allCat[patient] == nil {
		n.allCat[patient] = map[string]bool{}
	}
	n.allCat[patient][cat] = true
	n.now = now
	return good()
}

func sortedUniqueTimes(in []int64) ([]int64, bool) {
	seen := map[int64]bool{}
	for _, t := range in {
		if t < 0 || t >= daySecs || seen[t] {
			return nil, false
		}
		seen[t] = true
	}
	out := append([]int64(nil), in...)
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, true
}

func (n *Naive) validateShape(kind int, first, h int64, times []int64, prnMin, prnLimit, openAt int64) bool {
	switch kind {
	case 1:
		return h > 0 && first >= openAt && h > 2*n.w
	case 2:
		ts, ok := sortedUniqueTimes(times)
		if !ok {
			return false
		}
		for i := range ts {
			j := (i + 1) % len(ts)
			gap := ts[j] - ts[i]
			if j == 0 {
				gap += daySecs
			}
			if gap <= 2*n.w {
				return false
			}
		}
		return true
	case 3:
		return prnMin > 0 && prnLimit > 0
	}
	return false
}

// OpenSpec 是开立入参。
type OpenSpec struct {
	Now              int64
	Patient, Drug    string
	Kind             int
	First, H         int64
	Times            []int64
	PRNMin, PRNLimit int64
}

func (n *Naive) enumerate(o *nOrder) {
	o.points = nil
	switch o.kind {
	case 1:
		for t := o.first; t <= horizon; t += o.h {
			o.points = append(o.points, nPoint{t: t, status: nPending})
		}
	case 2:
		startDay := o.openedAt / daySecs
		endDay := horizon/daySecs + 1
		for day := startDay; day <= endDay; day++ {
			for _, tod := range o.times {
				t := day*daySecs + tod
				if t >= o.openedAt && t <= horizon {
					o.points = append(o.points, nPoint{t: t, status: nPending})
				}
			}
		}
	}
}

func (n *Naive) Open(sp OpenSpec) NResult {
	if sp.Now < 0 || sp.Patient == "" || sp.Drug == "" {
		return bad(cInvalid)
	}
	if !n.validateShape(sp.Kind, sp.First, sp.H, sp.Times, sp.PRNMin, sp.PRNLimit, sp.Now) {
		return bad(cInvalid)
	}
	if sp.Now < n.now {
		return bad(cClock)
	}
	d, ok := n.drugs[sp.Drug]
	if !ok {
		return bad(cNotFound)
	}
	if sp.Kind == 1 && sp.H < d.minSafe {
		return bad(cInvalid)
	}
	if sp.Kind == 2 {
		ts, _ := sortedUniqueTimes(sp.Times)
		for i := range ts {
			j := (i + 1) % len(ts)
			gap := ts[j] - ts[i]
			if j == 0 {
				gap += daySecs
			}
			if gap < d.minSafe {
				return bad(cInvalid)
			}
		}
	}
	if n.allDrug[sp.Patient][sp.Drug] || n.allCat[sp.Patient][d.category] {
		return bad(cAllergy)
	}
	o := &nOrder{
		patient: sp.Patient, drug: sp.Drug, kind: sp.Kind,
		first: sp.First, h: sp.H, prnMin: sp.PRNMin, prnLimit: sp.PRNLimit,
		openedAt:     sp.Now,
		activeAnchor: sp.First,
	}
	if sp.Kind == 2 {
		o.times, _ = sortedUniqueTimes(sp.Times)
	}
	n.enumerate(o)
	id := n.newID()
	o.id = id
	n.orders[id] = o
	n.orderList = append(n.orderList, id)
	n.now = sp.Now
	return NResult{OK: true, ID: id}
}

// refreshMissed 朴素地逐点扫描，把已出窗的 pending 点变为漏给。
func (n *Naive) refreshMissed(now int64) {
	for _, o := range n.orders {
		if o.stoppedAt != 0 {
			continue // 停嘱时点状态已冻结，不再随时间变化
		}
		for i := range o.points {
			p := &o.points[i]
			if p.status == nPending && now > p.t+n.w {
				p.status = nMissed
			}
			// 漏给是随调用时刻体现的派生视图：查询时刻较早、该点仍在窗口内
			// 或尚未到窗口时，应回退为待给（它从未被真正处理）。
			if p.status == nMissed && now <= p.t+n.w {
				p.status = nPending
			}
		}
	}
}

// findWindowPoint 返回窗口内可处理点下标；-2 表示窗口内点已处理（状态不符）；-1 无点。
func (n *Naive) findWindowPoint(o *nOrder, now int64) int {
	idx := -1
	for i := range o.points {
		p := &o.points[i]
		if p.t >= now-n.w && p.t <= now+n.w {
			if p.status == nVoid {
				continue // 重排产生的作废旧点不参与匹配
			}
			if p.status == nPending || p.status == nMissed {
				if idx == -1 || p.t < o.points[idx].t {
					idx = i
				}
			} else {
				return -2
			}
		}
	}
	return idx
}

func (n *Naive) loadActive(now int64, id string) (*nOrder, int) {
	if id == "" {
		return nil, cInvalid
	}
	o, ok := n.orders[id]
	if !ok {
		return nil, cNotFound
	}
	if o.stoppedAt != 0 && now >= o.stoppedAt {
		return nil, cState
	}
	return o, 0
}

func (n *Naive) checkSafety(patient, drug string, now int64) bool {
	if last, ok := n.lastDose[patient+"\x00"+drug]; ok {
		if now-last < n.drugs[drug].minSafe {
			return false
		}
	}
	return true
}

func (n *Naive) commitDose(patient, drug string, now int64) {
	k := patient + "\x00" + drug
	if last, ok := n.lastDose[k]; !ok || now > last {
		n.lastDose[k] = now
	}
}

func (n *Naive) Administer(now int64, id string) NResult {
	if now < 0 {
		return bad(cInvalid)
	}
	if now < n.now {
		return bad(cClock)
	}
	o, code := n.loadActive(now, id)
	if code != 0 {
		return bad(code)
	}
	if o.kind == 3 {
		return bad(cState)
	}
	n.refreshMissed(now)
	idx := n.findWindowPoint(o, now)
	if idx == -2 {
		return bad(cState)
	}
	if idx == -1 {
		return bad(cNoPoint)
	}
	if !n.checkSafety(o.patient, o.drug, now) {
		return bad(cInterval)
	}
	o.points[idx].status = nOnTime
	o.points[idx].actual = now
	n.commitDose(o.patient, o.drug, now)
	n.now = now
	return good()
}

func (n *Naive) Refuse(now int64, id string) NResult {
	if now < 0 {
		return bad(cInvalid)
	}
	if now < n.now {
		return bad(cClock)
	}
	o, code := n.loadActive(now, id)
	if code != 0 {
		return bad(code)
	}
	if o.kind == 3 {
		return bad(cState)
	}
	n.refreshMissed(now)
	idx := n.findWindowPoint(o, now)
	if idx == -2 {
		return bad(cState)
	}
	if idx == -1 {
		return bad(cNoPoint)
	}
	o.points[idx].status = nRefused
	o.points[idx].actual = now
	n.now = now
	return good()
}

func (n *Naive) pointIndex(o *nOrder, t int64) int {
	for i := range o.points {
		if o.points[i].t == t {
			return i
		}
	}
	return -1
}

// nextPoint 返回严格晚于 planned 的第一个 pending/missed 点时刻。
func (n *Naive) nextPoint(o *nOrder, planned int64) (int64, bool) {
	for i := range o.points {
		p := &o.points[i]
		if p.t > planned && p.status != nVoid {
			return o.points[i].t, true
		}
	}
	return 0, false
}

func (n *Naive) MakeUp(now int64, id string, planned int64) NResult {
	if now < 0 {
		return bad(cInvalid)
	}
	if now < n.now {
		return bad(cClock)
	}
	o, code := n.loadActive(now, id)
	if code != 0 {
		return bad(code)
	}
	if o.kind == 3 {
		return bad(cState)
	}
	n.refreshMissed(now)
	idx := n.pointIndex(o, planned)
	if idx < 0 {
		return bad(cNoPoint)
	}
	if o.kind == 1 && planned < o.activeAnchor {
		return bad(cNoPoint) // 属于已关闭代，活动序列中不存在
	}
	if o.kind == 1 && (planned-o.activeAnchor)%o.h != 0 {
		return bad(cNoPoint)
	}
	st := o.points[idx].status
	if st == nVoid {
		return bad(cNoPoint)
	}
	if st == nOnTime || st == nRefused || st == nMadeUp {
		return bad(cState)
	}
	// 间隔不足优先于补给不允许。
	if !n.checkSafety(o.patient, o.drug, now) {
		return bad(cInterval)
	}
	if st == nPending { // 尚未漏给
		return bad(cMakeup)
	}
	next, ok := n.nextPoint(o, planned)
	if !ok {
		return bad(cMakeup)
	}
	if now >= next-n.w {
		return bad(cMakeup)
	}
	// 提交补给。
	o.points[idx].status = nMadeUp
	o.points[idx].actual = now
	n.commitDose(o.patient, o.drug, now)
	if o.kind == 1 {
		// 规格字面语义：补给点之后尚未处理的旧点全部作废；
		// 从 now+H 起另起新序列（可能与作废旧点在同一时刻并存）。
		anchor := now + o.h
		o.activeAnchor = anchor
		for i := idx + 1; i < len(o.points); i++ {
			if o.points[i].status == nPending || o.points[i].status == nMissed {
				o.points[i].status = nVoid
			}
		}
		for t := anchor; t <= horizon; t += o.h {
			o.points = append(o.points, nPoint{t: t, status: nPending})
		}
	}
	n.now = now
	return good()
}

func (n *Naive) Stop(now int64, id string) NResult {
	if now < 0 {
		return bad(cInvalid)
	}
	if now < n.now {
		return bad(cClock)
	}
	o, ok := n.orders[id]
	if !ok {
		return bad(cNotFound)
	}
	if o.stoppedAt != 0 {
		return bad(cState)
	}
	o.stoppedAt = now
	for i := range o.points {
		p := &o.points[i]
		if p.status == nPending || p.status == nMissed {
			// 只按停嘱时刻分界：planned+W < now 保持漏给，其余作废。
			if p.t+n.w < now {
				p.status = nMissed
			} else {
				p.status = nVoid
			}
		}
	}
	n.now = now
	return good()
}

func (n *Naive) Revise(now int64, oldID string, sp OpenSpec) NResult {
	if now < 0 || sp.Patient == "" || sp.Drug == "" {
		return bad(cInvalid)
	}
	if !n.validateShape(sp.Kind, sp.First, sp.H, sp.Times, sp.PRNMin, sp.PRNLimit, now) {
		return bad(cInvalid)
	}
	if now < n.now {
		return bad(cClock)
	}
	o, ok := n.orders[oldID]
	if !ok {
		return bad(cNotFound)
	}
	if o.stoppedAt != 0 {
		return bad(cState)
	}
	sp.Now = now
	r := n.Open(sp)
	if !r.OK {
		return r // 新嘱失败：未停旧嘱（Open 在提交前全部校验）
	}
	// 新嘱已开，再停旧嘱。
	o.stoppedAt = now
	for i := range o.points {
		p := &o.points[i]
		if p.status == nPending || p.status == nMissed {
			if p.t+n.w < now {
				p.status = nMissed
			} else {
				p.status = nVoid
			}
		}
	}
	return r
}

func (n *Naive) PRN(now int64, id string) NResult {
	if now < 0 {
		return bad(cInvalid)
	}
	if now < n.now {
		return bad(cClock)
	}
	o, code := n.loadActive(now, id)
	if code != 0 {
		return bad(code)
	}
	if o.kind != 3 {
		return bad(cState)
	}
	if m := len(o.prnDoses); m > 0 {
		if now-o.prnDoses[m-1] < o.prnMin {
			return bad(cInterval)
		}
	}
	if !n.checkSafety(o.patient, o.drug, now) {
		return bad(cInterval)
	}
	cnt := 0
	for _, d := range o.prnDoses {
		if d > now-daySecs && d <= now {
			cnt++
		}
	}
	if int64(cnt) >= o.prnLimit {
		return bad(cLimit)
	}
	o.prnDoses = append(o.prnDoses, now)
	n.commitDose(o.patient, o.drug, now)
	n.now = now
	return good()
}

// NPointView 是朴素查询的一个点视图。
type NPointView struct {
	OrderID string
	Planned int64
	Status  int
	Actual  int64
}

// Query 返回患者在 [lo,hi] 内的点视图，按 (时刻, 医嘱) 排序。
func (n *Naive) Query(now int64, patient string, lo, hi int64) ([]NPointView, NResult) {
	if now < 0 {
		return nil, bad(cInvalid)
	}
	if now < n.now {
		return nil, bad(cClock)
	}
	if patient == "" || lo < 0 || hi < lo {
		return nil, bad(cInvalid)
	}
	// 漏给是随调用时刻体现的派生视图（在途医嘱）；停嘱医嘱在 refreshMissed 中跳过。
	n.refreshMissed(now)
	n.now = now
	var out []NPointView
	for _, id := range n.orderList {
		o := n.orders[id]
		if o.patient != patient || o.kind == 3 {
			continue
		}
		for i := range o.points {
			p := &o.points[i]
			if p.t >= lo && p.t <= hi {
				out = append(out, NPointView{OrderID: id, Planned: p.t, Status: int(p.status), Actual: p.actual})
			}
		}
	}
	// 朴素排序
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Planned < out[i].Planned ||
				(out[j].Planned == out[i].Planned && out[j].OrderID < out[i].OrderID) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, good()
}

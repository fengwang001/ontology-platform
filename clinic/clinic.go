package clinic

import (
	"sort"
	"sync"

	"ontology/lotstock"
	"ontology/vaxrule"
)

type Status int

const (
	StatusValid Status = iota
	StatusInvalid
	StatusExtra
)

// Error 携带固定错误码；Administer 过早拒绝另用 AdministerResult 返回原因与最早日。
type Error struct {
	Code string
}

func (e *Error) Error() string { return e.Code }

// 错误码（拒绝次序固定）。
const (
	CodeInvalid     = "invalid parameter"
	CodeClockRewind = "clock rewind"
	CodeMissing     = "patient or series not found"
	CodeNotGranted  = "nurse not granted"
	CodeDuplicate   = "duplicate record"
	CodeComplete    = "series complete"
	CodeTooEarly    = "too early"
	CodeNoStock     = "no stock"
)

const maxDate = 1_000_000

type RecordJudgment struct {
	Series string
	Date   int
	Dose   int // 评估当时剂号（该系列有效剂数+1）；多余为 n+1
	Status Status
	Reason vaxrule.InvalidReason
}

type NextDose struct {
	Series   string
	Dose     int
	Earliest int
}

type Evaluation struct {
	Records []RecordJudgment
	Next    []NextDose
}

// AdministerResult 成功时 Lot 为扣减批号；过早失败时 Reason/Earliest 有效。
type AdministerResult struct {
	Lot      string
	Reason   vaxrule.InvalidReason
	Earliest int
}

type rec struct {
	series string
	date   int
}

type seriesState struct {
	validCount      int
	lastValidDate   int
	lastRecordDate  int
	hasRecord       bool
	lastRecordValid bool
}

type patientData struct {
	birth     int
	records   map[rec]bool
	liveDates map[int]map[string]bool // 日期 -> 当日活疫苗系列名集合（含无效/多余记录）
	summaries map[string]*seriesState // Record 后全量重建；Administer 增量更新
}

type Clinic struct {
	mu       sync.Mutex
	catalog  *vaxrule.Catalog
	store    *lotstock.Store
	now      int
	patients map[string]*patientData
	granted  map[string]bool
}

func New() *Clinic {
	return &Clinic{
		catalog:  vaxrule.NewCatalog(),
		store:    lotstock.NewStore(),
		patients: map[string]*patientData{},
		granted:  map[string]bool{},
	}
}

func (c *Clinic) Catalog() *vaxrule.Catalog { return c.catalog }
func (c *Clinic) Store() *lotstock.Store    { return c.store }

func (c *Clinic) AddSeries(name string, live bool, n int, minAge, minInt []int, r int) error {
	if err := c.catalog.AddSeries(name, live, n, minAge, minInt, r); err != nil {
		return &Error{Code: CodeInvalid}
	}
	return nil
}

func (c *Clinic) AddPatient(now int, patient string, birth int) error {
	if patient == "" || birth < 0 || birth > maxDate || now < 0 || now > maxDate {
		return &Error{Code: CodeInvalid}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.now {
		return &Error{Code: CodeClockRewind}
	}
	if _, ok := c.patients[patient]; ok {
		return &Error{Code: CodeInvalid}
	}
	c.now = now
	c.patients[patient] = &patientData{
		birth:     birth,
		records:   map[rec]bool{},
		liveDates: map[int]map[string]bool{},
		summaries: map[string]*seriesState{},
	}
	return nil
}

func (c *Clinic) AddLot(now int, lot, series string, exp, qty int) error {
	if lot == "" || series == "" || exp < 0 || exp > maxDate || qty < 0 ||
		now < 0 || now > maxDate {
		return &Error{Code: CodeInvalid}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.now {
		return &Error{Code: CodeClockRewind}
	}
	if _, ok := c.catalog.Get(series); !ok {
		return &Error{Code: CodeMissing}
	}
	if err := c.store.AddLot(lot, series, exp, qty); err != nil {
		return &Error{Code: CodeInvalid}
	}
	c.now = now
	return nil
}

func (c *Clinic) Quarantine(now int, lot string, on bool) error {
	if lot == "" || now < 0 || now > maxDate {
		return &Error{Code: CodeInvalid}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.now {
		return &Error{Code: CodeClockRewind}
	}
	if err := c.store.Quarantine(lot, on); err != nil {
		return &Error{Code: CodeMissing}
	}
	c.now = now
	return nil
}

func (c *Clinic) Grant(now int, user string) error {
	if user == "" || now < 0 || now > maxDate {
		return &Error{Code: CodeInvalid}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.now {
		return &Error{Code: CodeClockRewind}
	}
	c.now = now
	c.granted[user] = true
	return nil
}

func (c *Clinic) Record(now int, patient, series string, d int) error {
	if patient == "" || series == "" || d < 0 || d > maxDate ||
		now < 0 || now > maxDate {
		return &Error{Code: CodeInvalid}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.now {
		return &Error{Code: CodeClockRewind}
	}
	p, ok := c.patients[patient]
	if !ok {
		return &Error{Code: CodeMissing}
	}
	s, ok := c.catalog.Get(series)
	if !ok {
		return &Error{Code: CodeMissing}
	}
	if d < p.birth || d > now {
		return &Error{Code: CodeInvalid}
	}
	key := rec{series: series, date: d}
	if p.records[key] {
		return &Error{Code: CodeDuplicate}
	}
	c.now = now
	p.records[key] = true
	if s.Live {
		set := p.liveDates[d]
		if set == nil {
			set = map[string]bool{}
			p.liveDates[d] = set
		}
		set[series] = true
	}
	rebuildSummaries(c, p)
	return nil
}

// rebuildSummaries 对该患者全部记录做一次全量重算，生成各系列汇总。
// 仅在允许整体重算的 Record 路径调用。
func rebuildSummaries(c *Clinic, p *patientData) {
	ordered := orderedRecords(p)
	states := map[string]*seriesState{}
	for _, r := range ordered {
		s := mustGet(c.catalog, r.series)
		st := states[r.series]
		if st == nil {
			st = &seriesState{}
			states[r.series] = st
		}
		k := st.validCount + 1
		valid := false
		if k <= s.N {
			reason := s.Check(p.birth, r.date, k, st.lastValidDate,
				st.hasRecord, st.lastRecordDate, st.lastRecordValid,
				hasLiveConflict(p, r.series, r.date, nil))
			valid = reason == vaxrule.ReasonNone
		}
		if valid {
			st.validCount++
			st.lastValidDate = r.date
		}
		st.hasRecord = true
		st.lastRecordDate = r.date
		st.lastRecordValid = valid
	}
	p.summaries = states
}

// orderedRecords 返回（日期, 系列名字节序）升序的全部记录。
func orderedRecords(p *patientData) []rec {
	out := make([]rec, 0, len(p.records))
	for r := range p.records {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].date != out[j].date {
			return out[i].date < out[j].date
		}
		return out[i].series < out[j].series
	})
	return out
}

// hasLiveConflict 判断活疫苗系列 series 在日期 d 是否与另一活疫苗系列的
// 既有记录冲突：存在 d' 满足 0 < d-d' < 28。同日、同系列均不冲突。
// touched 非 nil 时累加窗口内实际存在的记录条数。
func hasLiveConflict(p *patientData, series string, d int, touched *int) bool {
	for delta := 1; delta < vaxrule.LiveGap; delta++ {
		set := p.liveDates[d-delta]
		if touched != nil {
			*touched += len(set)
		}
		for other := range set {
			if other != series {
				return true
			}
		}
	}
	return false
}

func mustGet(cat *vaxrule.Catalog, name string) *vaxrule.Series {
	s, _ := cat.Get(name)
	return s
}

// reevaluate 全量重算一名患者，返回逐条判定与各系列汇总。
func reevaluate(c *Clinic, p *patientData) ([]RecordJudgment, map[string]*seriesState) {
	ordered := orderedRecords(p)
	states := map[string]*seriesState{}
	judgments := make([]RecordJudgment, 0, len(ordered))
	for _, r := range ordered {
		s := mustGet(c.catalog, r.series)
		st := states[r.series]
		if st == nil {
			st = &seriesState{}
			states[r.series] = st
		}
		k := st.validCount + 1
		j := RecordJudgment{Series: r.series, Date: r.date, Dose: k}
		valid := false
		if k > s.N {
			j.Status = StatusExtra
		} else {
			reason := s.Check(p.birth, r.date, k, st.lastValidDate,
				st.hasRecord, st.lastRecordDate, st.lastRecordValid,
				hasLiveConflict(p, r.series, r.date, nil))
			if reason == vaxrule.ReasonNone {
				j.Status = StatusValid
				valid = true
			} else {
				j.Status = StatusInvalid
				j.Reason = reason
			}
		}
		if valid {
			st.validCount++
			st.lastValidDate = r.date
		}
		st.hasRecord = true
		st.lastRecordDate = r.date
		st.lastRecordValid = valid
		judgments = append(judgments, j)
	}
	return judgments, states
}

// earliestFor 求系列 s 下一剂在 now 及以后“按现有记录接种后会判有效”的最小日期。
// 候选记录始终在全部现有记录之后，故不改变既有判定。
func earliestFor(c *Clinic, p *patientData, s *vaxrule.Series, st *seriesState, now int) int {
	k := st.validCount + 1
	lb := now
	if v := p.birth + s.MinAge[k-1] - vaxrule.Grace; v > lb {
		lb = v
	}
	if k > 1 {
		if v := st.lastValidDate + s.MinInt[k-1] - vaxrule.Grace; v > lb {
			lb = v
		}
	}
	if st.hasRecord && !st.lastRecordValid {
		if v := st.lastRecordDate + s.R; v > lb { // 重打不享受宽限
			lb = v
		}
	}
	d := lb
	if d < 0 {
		d = 0
	}
	if s.Live {
		for hasLiveConflict(p, s.Name, d, nil) {
			d++
		}
	}
	return d
}

func (c *Clinic) Evaluate(now int, patient string) (*Evaluation, error) {
	if patient == "" || now < 0 || now > maxDate {
		return nil, &Error{Code: CodeInvalid}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.now {
		return nil, &Error{Code: CodeClockRewind}
	}
	p, ok := c.patients[patient]
	if !ok {
		return nil, &Error{Code: CodeMissing}
	}
	judgments, states := reevaluate(c, p)
	ev := &Evaluation{Records: judgments}
	for _, name := range c.catalog.Names() {
		s := mustGet(c.catalog, name)
		st := states[name]
		valid := 0
		if st != nil {
			valid = st.validCount
		}
		if valid < s.N {
			if st == nil {
				st = &seriesState{}
			}
			ev.Next = append(ev.Next, NextDose{
				Series:   name,
				Dose:     valid + 1,
				Earliest: earliestFor(c, p, s, st, now),
			})
		}
	}
	return ev, nil
}

func (c *Clinic) Administer(now int, nurse, patient, series string) (*AdministerResult, error) {
	res, _, err := c.administerTouched(now, nurse, patient, series)
	return res, err
}

// administerTouched 与 Administer 等价，额外返回判定时触达的既有记录条数。
// 该数只来自固定 27 日活疫苗窗口，不随患者记录总数增长。
func (c *Clinic) administerTouched(now int, nurse, patient, series string) (*AdministerResult, int, error) {
	if nurse == "" || patient == "" || series == "" || now < 0 || now > maxDate {
		return nil, 0, &Error{Code: CodeInvalid}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.now {
		return nil, 0, &Error{Code: CodeClockRewind}
	}
	p, ok := c.patients[patient]
	if !ok {
		return nil, 0, &Error{Code: CodeMissing}
	}
	s, ok := c.catalog.Get(series)
	if !ok {
		return nil, 0, &Error{Code: CodeMissing}
	}
	if !c.granted[nurse] {
		return nil, 0, &Error{Code: CodeNotGranted}
	}
	if p.records[rec{series: series, date: now}] {
		return nil, 0, &Error{Code: CodeDuplicate}
	}
	st := p.summaries[series]
	if st == nil {
		st = &seriesState{}
	}
	if st.validCount >= s.N {
		return nil, 0, &Error{Code: CodeComplete}
	}
	touched := 0
	k := st.validCount + 1
	conflict := false
	if s.Live {
		conflict = hasLiveConflict(p, series, now, &touched)
	}
	reason := s.Check(p.birth, now, k, st.lastValidDate,
		st.hasRecord, st.lastRecordDate, st.lastRecordValid, conflict)
	if reason != vaxrule.ReasonNone {
		c.now = now
		return &AdministerResult{
			Reason:   reason,
			Earliest: earliestFor(c, p, s, st, now),
		}, touched, &Error{Code: CodeTooEarly}
	}
	lot, ok := c.store.PickAndConsume(series, now)
	if !ok {
		return nil, touched, &Error{Code: CodeNoStock}
	}
	c.now = now
	p.records[rec{series: series, date: now}] = true
	if s.Live {
		set := p.liveDates[now]
		if set == nil {
			set = map[string]bool{}
			p.liveDates[now] = set
		}
		set[series] = true
	}
	// 追加的记录已被证明有效，直接增量维护该系列汇总（不触碰其它记录）。
	st.validCount++
	st.lastValidDate = now
	st.hasRecord = true
	st.lastRecordDate = now
	st.lastRecordValid = true
	p.summaries[series] = st
	return &AdministerResult{Lot: lot}, touched, nil
}

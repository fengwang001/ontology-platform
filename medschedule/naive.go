package medschedule

// naive.go 是与生产实现完全独立的“朴素参考模型”：
// 把每条非 PRN 医嘱到当前时刻为止的计划点全部物化为切片，逐点重放规则。
// 刻意不做分段/二分/历史裁剪，用于与高效实现做差分对照。

type nvStatus int

const (
	nvPending nvStatus = iota
	nvGiven
	nvMadeUp
	nvRefused
	// 漏给不落库：未记录点在查询时由 p+W 与 now/stop 即时判定。
)

type nvPoint struct {
	t      int64
	status nvStatus
	alive  bool // 重排后旧网格点置 false（等价作废）
}

type nvOrder struct {
	id, patient, drug, kind string
	createdAt, stoppedAt    int64
	h, first                int64
	gridStart               int64 // 固定间隔：当前网格起点（补给重排后变化）
	genUpto                 int64 // 固定间隔：当前网格已物化到的时刻（-1 表示尚未生成）
	times                   []int64
	prnGap, prnMax          int64
	points                  []nvPoint
	prnDoses                []int64
}

// NaiveModel 朴素参考系统，API 与 System 平行，返回同构错误码。
type NaiveModel struct {
	w       int64
	now     int64
	drugs   map[string]*Drug
	allergy map[string]map[string]bool
	orders  map[string]*nvOrder
	lastAdm map[string]int64
}

func NewNaiveModel(w int64) *NaiveModel {
	return &NaiveModel{
		w:       w,
		now:     -1,
		drugs:   map[string]*Drug{},
		allergy: map[string]map[string]bool{},
		orders:  map[string]*nvOrder{},
		lastAdm: map[string]int64{},
	}
}

func (m *NaiveModel) checkClock(now int64) error {
	if m.now >= 0 && now < m.now {
		return errRollback("clock rollback")
	}
	return nil
}

func (m *NaiveModel) RegisterDrug(now int64, name, category string, minInterval int64) error {
	if !validNow(now) || name == "" || category == "" || minInterval <= 0 {
		return errInvalid("bad")
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	m.drugs[name] = &Drug{Category: category, MinIntervalSec: minInterval}
	m.now = now
	return nil
}

func (m *NaiveModel) SetAllergy(now int64, patient, item string, active bool) error {
	if !validNow(now) || patient == "" || item == "" {
		return errInvalid("bad")
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	set := m.allergy[patient]
	if set == nil {
		set = map[string]bool{}
		m.allergy[patient] = set
	}
	if active {
		set[item] = true
	} else {
		delete(set, item)
	}
	m.now = now
	return nil
}

func nvSpacing(kind string, h int64, times []int64, w, minGap int64) bool {
	switch kind {
	case "interval":
		return h > 2*w && h >= minGap
	case "daily":
		for i := range times {
			a := times[i]
			b := times[(i+1)%len(times)]
			gap := b - a
			if i == len(times)-1 {
				gap = b + dayLen - a
			}
			if gap <= 2*w || gap < minGap {
				return false
			}
		}
	}
	return true
}

func (m *NaiveModel) validSpec(spec Spec, now int64) bool {
	if !validNow(now) || spec.ID == "" || spec.Patient == "" || spec.Drug == "" {
		return false
	}
	switch spec.Kind {
	case "interval":
		return spec.H > 0 && spec.FirstTime >= now
	case "daily":
		if len(spec.TimesOfDay) == 0 {
			return false
		}
		seen := map[int64]bool{}
		for _, t := range spec.TimesOfDay {
			if t < 0 || t >= dayLen || seen[t] {
				return false
			}
			seen[t] = true
		}
		return true
	case "prn":
		return spec.PRNMinGap > 0 && spec.PRNMax24 > 0
	}
	return false
}

func (m *NaiveModel) CreateOrder(now int64, spec Spec) error {
	if !m.validSpec(spec, now) {
		return errInvalid("bad spec")
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	d, ok := m.drugs[spec.Drug]
	if !ok {
		return errNotFound("no drug")
	}
	if _, dup := m.orders[spec.ID]; dup {
		return errBadState("dup")
	}
	if set := m.allergy[spec.Patient]; set != nil && (set[spec.Drug] || set[d.Category]) {
		return errAllergy("allergy")
	}
	if !nvSpacing(spec.Kind, spec.H, spec.TimesOfDay, m.w, d.MinIntervalSec) {
		return errInvalid("spacing")
	}
	m.orders[spec.ID] = m.buildOrder(spec, now)
	m.now = now
	return nil
}

func nvSortInts(a []int64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

func (m *NaiveModel) buildOrder(spec Spec, now int64) *nvOrder {
	o := &nvOrder{
		id: spec.ID, patient: spec.Patient, drug: spec.Drug, kind: spec.Kind,
		createdAt: now, stoppedAt: -1, h: spec.H, first: spec.FirstTime,
		prnGap: spec.PRNMinGap, prnMax: spec.PRNMax24,
		gridStart: spec.FirstTime, genUpto: -1,
	}
	if spec.Kind == "daily" {
		o.times = append([]int64(nil), spec.TimesOfDay...)
		nvSortInts(o.times)
	}
	m.materialize(o, now)
	return o
}

// materialize 把计划点物化到 horizon+W（覆盖当前可能进入窗口的点）。
func (m *NaiveModel) materialize(o *nvOrder, horizon int64) {
	if o.kind == "prn" {
		return
	}
	upto := horizon + m.w + 1
	if o.kind == "interval" {
		start := o.gridStart
		if o.genUpto >= 0 {
			// 从当前网格已生成的最后时刻之后续算，永不重新生成已杀死点。
			k := (o.genUpto-start)/o.h + 1
			if o.genUpto < start {
				k = 0
			}
			t := start + k*o.h
			for ; t <= upto; t += o.h {
				o.points = append(o.points, nvPoint{t: t, status: nvPending, alive: true})
			}
			o.genUpto = max64(o.genUpto, upto)
			return
		}
		for t := start; t <= upto; t += o.h {
			o.points = append(o.points, nvPoint{t: t, status: nvPending, alive: true})
		}
		o.genUpto = upto
		return
	}
	var last int64 = -1
	if len(o.points) > 0 {
		last = o.points[len(o.points)-1].t
	}
	dailyCandidates(o.times, o.createdAt, 0, upto, func(t int64) {
		if t <= last {
			return
		}
		o.points = append(o.points, nvPoint{t: t, status: nvPending, alive: true})
	})
}

func (m *NaiveModel) advance(now int64) {
	for _, o := range m.orders {
		m.materialize(o, now)
	}
	m.now = now
}

// materializeAll 只补齐物化点，不推进时钟（供可能失败的操作做只读判定）。
func (m *NaiveModel) materializeAll(now int64) {
	for _, o := range m.orders {
		m.materialize(o, now)
	}
}

package medsched

import "sync"

const maxInt64 = int64(1<<63 - 1)

const orderIDPrefix = "ORD-"

// System 是住院护理给药时间表系统。所有方法在单一互斥锁下串行化，
// 因而并发调用等价于某个串行顺序，且结果可由操作序列确定性重放。
type System struct {
	mu        sync.Mutex
	now       int64
	w         int64
	catalog   *Catalog
	allergies *Allergies
	orders    map[string]*order

	// 同一患者同一药品的最近一次实际给药时刻（按时/补给/必要时统一计入）。
	lastDose map[string]int64

	orderSeq int64
}

// New 创建系统。W 为按时窗口半宽（正整数秒），非法返回错误。
func New(w int64) (*System, error) {
	if w <= 0 {
		return nil, errf(ErrInvalidParam, "窗口半宽 W 必须为正整数")
	}
	return &System{
		w:         w,
		catalog:   NewCatalog(),
		allergies: NewAllergies(),
		orders:    map[string]*order{},
		lastDose:  map[string]int64{},
	}, nil
}

// checkTimeParam 校验 now 的取值范围（参数非法，优先级最高）。
func checkTimeParam(now int64) error {
	if now < 0 {
		return errf(ErrInvalidParam, "now 越界")
	}
	return nil
}

func (s *System) checkClock(now int64) error {
	if now < s.now {
		return errf(ErrClockRollback, "时钟回退: now=%d < %d", now, s.now)
	}
	return nil
}

func (s *System) acceptClock(now int64) {
	if now > s.now {
		s.now = now
	}
}

func doseKey(patient, drug string) string { return patient + "\x00" + drug }

func minSafeOf(s *System, drug string) int64 {
	d, err := s.catalog.Get(drug)
	if err != nil {
		return 0
	}
	return d.MinInterval
}

// checkSafety 校验同一患者同一药品两次实际给药的最小安全间隔。
func (s *System) checkSafety(patient, drug string, at int64) error {
	if last, ok := s.lastDose[doseKey(patient, drug)]; ok {
		if at-last < minSafeOf(s, drug) {
			return errf(ErrIntervalTooShort, "同患者同药品给药间隔不足")
		}
	}
	return nil
}

func (s *System) commitDose(patient, drug string, at int64) {
	k := doseKey(patient, drug)
	if last, ok := s.lastDose[k]; !ok || at > last {
		s.lastDose[k] = at
	}
}

func (s *System) newOrderID() string {
	s.orderSeq++
	return orderIDPrefix + itoa(s.orderSeq)
}

// Now 返回系统最近接受操作的时钟。
func (s *System) Now() int64 { return s.now }

// validateOpenShape 只依赖入参形状：参数非法优先级最高。
func (s *System) validateOpenShape(patient, drug string, f Frequency, openAt int64) error {
	if patient == "" || drug == "" {
		return errf(ErrInvalidParam, "患者或药品标识为空")
	}
	return validateFreqShape(f, openAt, s.w)
}

// validateOpenRefs 在时钟检查之后执行：对象存在 -> 安全间隔 -> 过敏。
func (s *System) validateOpenRefs(patient, drug string, f Frequency) error {
	d, err := s.catalog.Get(drug)
	if err != nil {
		return err // ErrNotFound
	}
	if err := validateFreqSafety(f, d.MinInterval); err != nil {
		return err
	}
	if s.allergies.HasAllergy(patient, drug, d) {
		return errf(ErrAllergy, "患者对药品 %s 或其类别过敏", drug)
	}
	return nil
}

func (s *System) buildOrder(patient, drug string, f Frequency, openAt int64) *order {
	o := &order{
		patient:  patient,
		drug:     drug,
		freq:     f,
		openedAt: openAt,
	}
	switch f.Kind {
	case FreqInterval:
		o.gens = []gen{{anchor: f.First, cutoffK: maxInt64}}
		o.genRecords = [][]record{{}}
	case FreqDaily:
		o.dailyTimes = sortDailyTimes(f.Times)
		o.genRecords = [][]record{{}}
	}
	return o
}

// RegisterDrug 登记药品目录。
func (s *System) RegisterDrug(now int64, drugID, category string, minInterval int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if drugID == "" || category == "" || minInterval <= 0 {
		return errf(ErrInvalidParam, "药品登记参数非法")
	}
	if err := checkTimeParam(now); err != nil {
		return err
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if err := s.catalog.RegisterDrug(drugID, category, minInterval); err != nil {
		return err // 重复登记 -> 状态不符
	}
	s.acceptClock(now)
	return nil
}

// AddAllergyDrug 登记患者对某药品过敏。
func (s *System) AddAllergyDrug(now int64, patient, drug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkTimeParam(now); err != nil {
		return err
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if patient == "" {
		return errf(ErrInvalidParam, "患者标识为空")
	}
	if _, err := s.catalog.Get(drug); err != nil {
		return err
	}
	if err := s.allergies.AddDrug(patient, drug); err != nil {
		return err
	}
	s.acceptClock(now)
	return nil
}

// AddAllergyCategory 登记患者对某类别过敏。
func (s *System) AddAllergyCategory(now int64, patient, category string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkTimeParam(now); err != nil {
		return err
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if patient == "" || category == "" {
		return errf(ErrInvalidParam, "过敏登记参数非法")
	}
	if err := s.allergies.AddCategory(patient, category); err != nil {
		return err
	}
	s.acceptClock(now)
	return nil
}

// OpenOrderInput 为开立医嘱的入参。
type OpenOrderInput struct {
	Now       int64
	Patient   string
	Drug      string
	Frequency Frequency
}

// OpenOrder 开立医嘱，返回系统生成的医嘱标识。
func (s *System) OpenOrder(in OpenOrderInput) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkTimeParam(in.Now); err != nil {
		return "", err
	}
	if err := s.validateOpenShape(in.Patient, in.Drug, in.Frequency, in.Now); err != nil {
		return "", err
	}
	if err := s.checkClock(in.Now); err != nil {
		return "", err
	}
	if err := s.validateOpenRefs(in.Patient, in.Drug, in.Frequency); err != nil {
		return "", err
	}
	o := s.buildOrder(in.Patient, in.Drug, in.Frequency, in.Now)
	id := s.newOrderID()
	s.orders[id] = o
	s.acceptClock(in.Now)
	return id, nil
}

func (s *System) loadActiveOrder(now int64, orderID string) (*order, error) {
	if orderID == "" {
		return nil, errf(ErrInvalidParam, "医嘱标识为空")
	}
	o, ok := s.orders[orderID]
	if !ok {
		return nil, errf(ErrNotFound, "医嘱不存在: %s", orderID)
	}
	if o.isStopped(now) {
		return nil, errf(ErrInvalidState, "医嘱已停嘱")
	}
	return o, nil
}

// matchPoint 找到按时窗口含 now 且尚未处理的计划点时刻。
// 窗口内含点但已处理 -> 状态不符；窗口内根本无点 -> 无对应计划点。
func (s *System) matchPoint(o *order, now int64) (int64, error) {
	switch o.freq.Kind {
	case FreqInterval:
		g := o.gens[o.activeGenIdx()]
		lo, hi := intervalIndexRange(g.anchor, o.freq.H, now-s.w, now+s.w)
		gi := o.activeGenIdx()
		for k := lo; k <= hi; k++ {
			t := intervalPoint(g.anchor, o.freq.H, k)
			if t < now-s.w || t > now+s.w {
				continue
			}
			if findIn(o.genRecords[gi], t) >= 0 {
				return 0, errf(ErrInvalidState, "计划点已处理: %d", t)
			}
			return t, nil
		}
		return 0, errf(ErrNoScheduledPoint, "窗口内无未处理计划点")
	case FreqDaily:
		var hit int64
		found := false
		dailyPointsBetween(o.dailyTimes, now-s.w, now+s.w, func(t int64) {
			if !found {
				hit, found = t, true
			}
		})
		if !found {
			return 0, errf(ErrNoScheduledPoint, "窗口内无计划点")
		}
		if findIn(o.genRecords[0], hit) >= 0 {
			return 0, errf(ErrInvalidState, "计划点已处理: %d", hit)
		}
		return hit, nil
	default:
		return 0, errf(ErrInvalidState, "必要时医嘱无计划点")
	}
}

// Administer 在 now 为某医嘱登记一次按时给药。
func (s *System) Administer(now int64, orderID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkTimeParam(now); err != nil {
		return err
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	o, err := s.loadActiveOrder(now, orderID)
	if err != nil {
		return err
	}
	t, err := s.matchPoint(o, now)
	if err != nil {
		return err
	}
	if err := s.checkSafety(o.patient, o.drug, now); err != nil {
		return err
	}
	gi := o.activeGenIdx()
	if o.freq.Kind == FreqDaily {
		gi = 0
	}
	o.genRecords[gi] = insertRecord(o.genRecords[gi], record{at: t, done: now, kind: rkOnTime})
	s.commitDose(o.patient, o.drug, now)
	s.acceptClock(now)
	return nil
}

// Refuse 在 now 登记患者拒服（计为处理，但不算实际给药，不参与间隔判定）。
func (s *System) Refuse(now int64, orderID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkTimeParam(now); err != nil {
		return err
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	o, err := s.loadActiveOrder(now, orderID)
	if err != nil {
		return err
	}
	t, err := s.matchPoint(o, now)
	if err != nil {
		return err
	}
	rgi := o.activeGenIdx()
	if o.freq.Kind == FreqDaily {
		rgi = 0
	}
	o.genRecords[rgi] = insertRecord(o.genRecords[rgi], record{at: t, done: now, kind: rkRefused})
	s.acceptClock(now)
	return nil
}

// nextPlannedAfter 返回严格晚于 ref 的下一个计划点时刻（固定间隔只看活动代）。
func (s *System) nextPlannedAfter(o *order, ref int64) (int64, bool) {
	switch o.freq.Kind {
	case FreqInterval:
		g := o.gens[o.activeGenIdx()]
		k := intervalIndexAt(g.anchor, o.freq.H, ref) + 1
		return intervalPoint(g.anchor, o.freq.H, k), true
	case FreqDaily:
		var best int64
		found := false
		dailyPointsBetween(o.dailyTimes, ref+1, ref+secondsPerDay, func(t int64) {
			if !found || t < best {
				best, found = t, true
			}
		})
		return best, found
	default:
		return 0, false
	}
}

// pointExists 判断 plannedAt 是否为当前活动序列中的计划点（且不早于开立时刻）。
func (s *System) pointExists(o *order, plannedAt int64) bool {
	switch o.freq.Kind {
	case FreqInterval:
		g := o.gens[o.activeGenIdx()]
		if plannedAt < g.anchor {
			return false
		}
		k := intervalIndexAt(g.anchor, o.freq.H, plannedAt)
		return intervalPoint(g.anchor, o.freq.H, k) == plannedAt
	case FreqDaily:
		if plannedAt < o.openedAt || plannedAt < 0 {
			return false
		}
		tod := plannedAt % secondsPerDay
		for _, t := range o.dailyTimes {
			if t == tod {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// MakeUp 在 now 对漏给计划点 plannedAt 补给。
func (s *System) MakeUp(now int64, orderID string, plannedAt int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkTimeParam(now); err != nil {
		return err
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	o, err := s.loadActiveOrder(now, orderID)
	if err != nil {
		return err
	}
	if o.freq.Kind == FreqPRN {
		return errf(ErrInvalidState, "必要时医嘱无计划点")
	}
	tgi := o.activeGenIdx()
	if o.freq.Kind == FreqDaily {
		tgi = 0
	}
	if o.freq.Kind == FreqInterval {
		if !s.pointExists(o, plannedAt) {
			return errf(ErrNoScheduledPoint, "计划点不存在: %d", plannedAt)
		}
	} else if !s.pointExists(o, plannedAt) {
		return errf(ErrNoScheduledPoint, "计划点不存在: %d", plannedAt)
	}
	if findIn(o.genRecords[tgi], plannedAt) >= 0 {
		return errf(ErrInvalidState, "计划点已处理: %d", plannedAt)
	}
	// 间隔不足优先于补给不允许。
	if err := s.checkSafety(o.patient, o.drug, now); err != nil {
		return err
	}
	if now <= plannedAt+s.w {
		return errf(ErrMakeupNotAllowed, "计划点尚未漏给，不可补给")
	}
	next, ok := s.nextPlannedAfter(o, plannedAt)
	if !ok {
		return errf(ErrMakeupNotAllowed, "无下一计划点")
	}
	if now >= next-s.w {
		return errf(ErrMakeupNotAllowed, "已进入下一计划点窗口，不可补给")
	}

	// 提交：记录补给，并在固定间隔下开启新一代。
	o.genRecords[tgi] = insertRecord(o.genRecords[tgi], record{at: plannedAt, done: now, kind: rkMadeUp})
	s.commitDose(o.patient, o.drug, now)
	if o.freq.Kind == FreqInterval {
		gi := o.activeGenIdx()
		h := o.freq.H
		targetK := (plannedAt - o.gens[gi].anchor) / h
		o.gens[gi].cutoffK = targetK
		o.gens = append(o.gens, gen{anchor: now + h, cutoffK: maxInt64})
		o.genRecords = append(o.genRecords, []record{})
	}
	s.acceptClock(now)
	return nil
}

// StopOrder 在 now 停嘱：此后不再接受给药/拒服/补给；
// 尚无记录的点中 planned+W < now 的保持漏给，其余作废。
func (s *System) StopOrder(now int64, orderID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkTimeParam(now); err != nil {
		return err
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return errf(ErrNotFound, "医嘱不存在: %s", orderID)
	}
	if o.stoppedAt != 0 {
		return errf(ErrInvalidState, "医嘱已停嘱")
	}
	o.stoppedAt = now
	s.acceptClock(now)
	return nil
}

// ReviseOrder 改嘱：在 now 停旧嘱并开新嘱，两步原子地成功或失败。
// 任一步校验失败均不改变任何状态与时钟。
func (s *System) ReviseOrder(now int64, oldOrderID, patient, drug string, f Frequency) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkTimeParam(now); err != nil {
		return "", err
	}
	if err := s.validateOpenShape(patient, drug, f, now); err != nil {
		return "", err
	}
	if err := s.checkClock(now); err != nil {
		return "", err
	}
	o, ok := s.orders[oldOrderID]
	if !ok {
		return "", errf(ErrNotFound, "医嘱不存在: %s", oldOrderID)
	}
	if o.stoppedAt != 0 {
		return "", errf(ErrInvalidState, "旧医嘱已停嘱，不能改嘱")
	}
	if err := s.validateOpenRefs(patient, drug, f); err != nil {
		return "", err
	}
	// 全部校验通过后一并提交。
	o.stoppedAt = now
	no := s.buildOrder(patient, drug, f, now)
	id := s.newOrderID()
	s.orders[id] = no
	s.acceptClock(now)
	return id, nil
}

// AdministerPRN 在 now 对必要时医嘱提出给药请求。
func (s *System) AdministerPRN(now int64, orderID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkTimeParam(now); err != nil {
		return err
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	o, err := s.loadActiveOrder(now, orderID)
	if err != nil {
		return err
	}
	if o.freq.Kind != FreqPRN {
		return errf(ErrInvalidState, "非必要时医嘱")
	}
	// 同医嘱最小间隔。
	if n := len(o.prnDoses); n > 0 {
		if now-o.prnDoses[n-1] < o.freq.PRNMin {
			return errf(ErrIntervalTooShort, "必要时同医嘱间隔不足")
		}
	}
	// 同药品最小安全间隔（间隔不足优先于次数超限）。
	if err := s.checkSafety(o.patient, o.drug, now); err != nil {
		return err
	}
	// 滚动 24h：区间 (now-86400, now] 内已实际给药次数须 < 上限。
	cutoff := now - secondsPerDay
	count := 0
	for i := len(o.prnDoses) - 1; i >= 0; i-- {
		if o.prnDoses[i] <= cutoff {
			break
		}
		count++
	}
	if int64(count) >= o.freq.PRNLimit {
		return errf(ErrLimitExceeded, "必要时滚动24小时次数超限")
	}
	o.prnDoses = append(o.prnDoses, now)
	s.commitDose(o.patient, o.drug, now)
	s.acceptClock(now)
	return nil
}

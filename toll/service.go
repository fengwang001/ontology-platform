package toll

import (
	"fmt"
	"sync"
	"time"
)

// Service 是门架计费服务。所有方法可并发调用,
// 内部以互斥锁串行化,结果等价于某个串行顺序。
type Service struct {
	mu       sync.Mutex
	net      *Network
	cfg      Config
	vehicles map[string]*Vehicle
	journeys map[string]*Journey // 行程 ID 直查,查询开销与历史行程数无关
	ledgers  map[MonthKey]*MonthLedger
	lastOp   time.Time
	hasOp    bool
	seq      int
}

func NewService(net *Network, cfg Config) *Service {
	if cfg.Location == nil {
		cfg.Location = time.UTC
	}
	return &Service{
		net:      net,
		cfg:      cfg,
		vehicles: make(map[string]*Vehicle),
		journeys: make(map[string]*Journey),
		ledgers:  make(map[MonthKey]*MonthLedger),
	}
}

// checkClock 校验操作时刻;被拒绝的操作不改变任何状态与时钟。
func (s *Service) checkClock(now time.Time) error {
	if now.IsZero() {
		return ErrInvalidParams
	}
	if s.hasOp && now.Before(s.lastOp) {
		return fmt.Errorf("%w: %s < %s", ErrClockRollback, now, s.lastOp)
	}
	return nil
}

func (s *Service) commitClock(now time.Time) {
	s.lastOp = now
	s.hasOp = true
}

func (s *Service) nextSeq() int {
	s.seq++
	return s.seq
}

// RegisterVehicle 登记车辆及初始车型。
func (s *Service) RegisterVehicle(now time.Time, id, typ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || typ == "" {
		return ErrInvalidParams
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if _, ok := s.vehicles[id]; ok {
		return fmt.Errorf("%w: 车辆已存在 %s", ErrInvalidParams, id)
	}
	s.vehicles[id] = newVehicle(id, typ)
	s.commitClock(now)
	return nil
}

// ChangeVehicleType 变更车型,effective 为生效时刻(恰等于经过时刻按新车型)。
func (s *Service) ChangeVehicleType(now time.Time, id, typ string, effective time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || typ == "" || effective.IsZero() {
		return ErrInvalidParams
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	v, ok := s.vehicles[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrVehicleNotFound, id)
	}
	v.addTypeChange(TypeChange{Effective: effective, Type: typ, Seq: s.nextSeq()})
	s.commitClock(now)
	return nil
}

// Entry 在入口门架登记,开启一次行程,返回行程 ID。
func (s *Service) Entry(now time.Time, vehicleID, gantry string, t time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if vehicleID == "" || gantry == "" || t.IsZero() {
		return "", ErrInvalidParams
	}
	if err := s.checkClock(now); err != nil {
		return "", err
	}
	if !s.net.HasGantry(gantry) {
		return "", fmt.Errorf("%w: %s", ErrGantryNotFound, gantry)
	}
	v, ok := s.vehicles[vehicleID]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrVehicleNotFound, vehicleID)
	}
	if v.Open != nil {
		return "", fmt.Errorf("%w: 车辆 %s 存在未结算行程", ErrInvalidParams, vehicleID)
	}
	rec := &GantryRecord{Gantry: gantry, Time: t, Seq: s.nextSeq(), Status: StatusValid}
	j := &Journey{
		ID:      fmt.Sprintf("%s#%d", vehicleID, len(v.Journeys)+1),
		Vehicle: v,
		Entry:   rec,
	}
	v.Journeys = append(v.Journeys, j)
	v.Open = j
	v.keep(gantry, t)
	s.journeys[j.ID] = j
	s.commitClock(now)
	return j.ID, nil
}

// Record 登记一条中间门架记录,必要时触发重算。
func (s *Service) Record(now time.Time, vehicleID, gantry string, t time.Time) (RecordResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if vehicleID == "" || gantry == "" || t.IsZero() {
		return RecordResult{}, ErrInvalidParams
	}
	if err := s.checkClock(now); err != nil {
		return RecordResult{}, err
	}
	if !s.net.HasGantry(gantry) {
		return RecordResult{}, fmt.Errorf("%w: %s", ErrGantryNotFound, gantry)
	}
	v, ok := s.vehicles[vehicleID]
	if !ok {
		return RecordResult{}, fmt.Errorf("%w: %s", ErrVehicleNotFound, vehicleID)
	}
	rec := &GantryRecord{Gantry: gantry, Time: t, Seq: s.nextSeq()}
	// 重复记录:只登记,不触发任何重算。
	if v.isDuplicate(gantry, t, s.cfg.DuplicateWindow) {
		rec.Status = StatusDuplicate
		v.Orphans = append(v.Orphans, rec)
		s.commitClock(now)
		return RecordResult{Status: StatusDuplicate}, nil
	}
	v.keep(gantry, t)
	// 已结算行程的迟到记录(时刻落在入口与出口之间,含端点)。
	for i := len(v.Journeys) - 1; i >= 0; i-- {
		j := v.Journeys[i]
		if !j.Settled || t.Before(j.Entry.Time) || t.After(j.Exit.Time) {
			continue
		}
		j.Records = append(j.Records, rec)
		if now.Sub(j.Exit.Time) > s.cfg.BackchargeWindow {
			rec.Status = StatusExpired
			s.commitClock(now)
			return RecordResult{Status: StatusExpired, JourneyID: j.ID, Note: "超过补扣期限,仅登记"}, nil
		}
		rec.Status = StatusValid
		adj, note := s.recompute(j, now, fmt.Sprintf("迟到记录 %s@%s", gantry, t.Format(time.RFC3339Nano)))
		s.commitClock(now)
		return RecordResult{Status: StatusLate, JourneyID: j.ID, Adjustment: adj, Note: note}, nil
	}
	// 未结算行程的中间记录。
	if v.Open != nil && !t.Before(v.Open.Entry.Time) {
		rec.Status = StatusValid
		v.Open.Records = append(v.Open.Records, rec)
		s.commitClock(now)
		return RecordResult{Status: StatusAccepted, JourneyID: v.Open.ID}, nil
	}
	// 不属于本行程:孤立登记。
	rec.Status = StatusOrphan
	v.Orphans = append(v.Orphans, rec)
	s.commitClock(now)
	return RecordResult{Status: StatusOrphan}, nil
}

// Exit 在出口门架触发结算。路径不可达时报错,行程保持未结算。
func (s *Service) Exit(now time.Time, vehicleID, gantry string, t time.Time) (ExitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if vehicleID == "" || gantry == "" || t.IsZero() {
		return ExitResult{}, ErrInvalidParams
	}
	if err := s.checkClock(now); err != nil {
		return ExitResult{}, err
	}
	if !s.net.HasGantry(gantry) {
		return ExitResult{}, fmt.Errorf("%w: %s", ErrGantryNotFound, gantry)
	}
	v, ok := s.vehicles[vehicleID]
	if !ok {
		return ExitResult{}, fmt.Errorf("%w: %s", ErrVehicleNotFound, vehicleID)
	}
	j := v.Open
	if j == nil {
		if n := len(v.Journeys); n > 0 && v.Journeys[n-1].Settled {
			return ExitResult{}, fmt.Errorf("%w: %s", ErrJourneySettled, v.Journeys[n-1].ID)
		}
		return ExitResult{}, fmt.Errorf("%w: 车辆 %s", ErrExitWithoutEntry, vehicleID)
	}
	if t.Before(j.Entry.Time) {
		return ExitResult{}, fmt.Errorf("%w: 出口时刻早于入口时刻", ErrInvalidParams)
	}
	exit := &GantryRecord{Gantry: gantry, Time: t, Seq: s.nextSeq(), Status: StatusValid}
	j.Exit = exit
	path, fee, err := inferPath(s.net, v, j)
	if err != nil {
		j.Exit = nil
		return ExitResult{}, err // 行程保持未结算,时钟不前进
	}
	// 出口时刻之后的记录不属于本行程,改判孤立。
	kept := j.Records[:0]
	for _, r := range j.Records {
		if r.Status == StatusValid && r.Time.After(t) {
			r.Status = StatusOrphan
			v.Orphans = append(v.Orphans, r)
			continue
		}
		kept = append(kept, r)
	}
	j.Records = kept
	j.Path = path
	j.Fee = fee
	j.Settled = true
	v.Open = nil
	v.keep(gantry, t)
	adj := s.applyDelta(j, now, true, 0, fmt.Sprintf("出口结算:路径 %v", path))
	s.commitClock(now)
	res := ExitResult{JourneyID: j.ID, Path: append([]string(nil), path...), Fee: fee}
	if adj != nil {
		res.Charged = adj.Amount
		res.CappedUncollected = adj.CappedUncollected
	}
	return res, nil
}

// CloseJourney 人工关闭未结算行程(如路径不可达)。
func (s *Service) CloseJourney(now time.Time, vehicleID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if vehicleID == "" {
		return ErrInvalidParams
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	v, ok := s.vehicles[vehicleID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrVehicleNotFound, vehicleID)
	}
	if v.Open == nil {
		return fmt.Errorf("%w: 车辆 %s 无未结算行程", ErrJourneyNotFound, vehicleID)
	}
	v.Open.Closed = true
	v.Open = nil
	s.commitClock(now)
	return nil
}

// QueryJourney 查询任意行程的当前计费路径、已收金额与调整明细。
// 直查行程 ID 索引,开销不随该车辆历史行程总数增长。
func (s *Service) QueryJourney(journeyID string) (JourneyView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.journeys[journeyID]
	if !ok {
		return JourneyView{}, fmt.Errorf("%w: %s", ErrJourneyNotFound, journeyID)
	}
	view := JourneyView{
		ID:        j.ID,
		VehicleID: j.Vehicle.ID,
		Settled:   j.Settled,
		Closed:    j.Closed,
		Path:      append([]string(nil), j.Path...),
		Fee:       j.Fee,
		Received:  j.Received,
	}
	for _, a := range j.Adjustments {
		cp := *a
		cp.Path = append([]string(nil), a.Path...)
		view.Adjustments = append(view.Adjustments, cp)
	}
	return view, nil
}

// QueryAdjustments 补扣/调整明细查询;行程未结算时报错。
func (s *Service) QueryAdjustments(journeyID string) ([]Adjustment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.journeys[journeyID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrJourneyNotFound, journeyID)
	}
	if !j.Settled {
		return nil, fmt.Errorf("%w: %s", ErrJourneyNotSettled, journeyID)
	}
	out := make([]Adjustment, 0, len(j.Adjustments))
	for _, a := range j.Adjustments {
		cp := *a
		cp.Path = append([]string(nil), a.Path...)
		out = append(out, cp)
	}
	return out, nil
}

// QueryMonth 查询车辆某时刻所属自然月的台账(实收、封顶未收、退款溢出)。
func (s *Service) QueryMonth(vehicleID string, t time.Time) (MonthLedger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.vehicles[vehicleID]; !ok {
		return MonthLedger{}, fmt.Errorf("%w: %s", ErrVehicleNotFound, vehicleID)
	}
	lt := t.In(s.cfg.Location)
	key := MonthKey{VehicleID: vehicleID, Year: lt.Year(), Month: lt.Month()}
	if l, ok := s.ledgers[key]; ok {
		return *l, nil
	}
	return MonthLedger{}, nil
}

package congestion

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// Service 拥堵收费与豁免结算服务。所有方法并发安全，可串行化。
type Service struct {
	mu       sync.Mutex
	cfg      Config
	zones    *registry
	vehicles map[string]*vehicleState
	seq      int64
	last     time.Time
	logger   io.Writer // 可选：逐条操作审计日志
}

type plateChange struct {
	at    time.Time
	plate string
}

type vehicleState struct {
	id       string
	plates   []plateChange         // 按 at 升序
	quals    []Qualification       // 按 Start 升序
	entries  []RawEntry            // 全量存档，按 Seq 升序（仅重算/导出用）
	dayEntry map[string][]RawEntry // 按配置时区自然日分组，查询不依赖全量历史
	days     map[string]*dayState
}

type dayState struct {
	payable     int64
	lines       []ChargeLine
	frozenLines []ChargeLine // 冻结瞬间的计费依据快照
	adjustments []Adjustment
	frozen      bool
	frozenValue int64
}

// New 创建服务。cfg.Location 为必填，DiscountBasis 缺省为 10000。
func New(cfg Config) *Service {
	if cfg.Location == nil {
		cfg.Location = time.UTC
	}
	if cfg.DiscountBasis <= 0 {
		cfg.DiscountBasis = 10000
	}
	return &Service{
		cfg:      cfg,
		zones:    newRegistry(),
		vehicles: map[string]*vehicleState{},
		logger:   io.Discard,
	}
}

// SetLogger 设置审计日志输出（nil 表示关闭）。
func (s *Service) SetLogger(w io.Writer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w == nil {
		w = io.Discard
	}
	s.logger = w
}

// AddZone 登记收费区。区域结构一旦被事件引用即不可变（本实现不提供修改）。
func (s *Service) AddZone(z Zone) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validZone(&z) {
		return s.fail("AddZone", ErrInvalid, z)
	}
	if _, exists := s.zones.get(z.ID); exists {
		return s.fail("AddZone", ErrInvalid, z)
	}
	if err := s.zones.add(&z); err != nil {
		return s.fail("AddZone", err, z)
	}
	s.ok("AddZone", nil, z)
	return nil
}

// RegisterVehicle 注册车辆，plate 为初始车牌，at 为该车牌生效时刻。
func (s *Service) RegisterVehicle(id, plate string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || plate == "" || at.IsZero() {
		return s.fail("RegisterVehicle", ErrInvalid, id, plate, at)
	}
	if at.Before(s.last) {
		return s.fail("RegisterVehicle", ErrClockBack, id, plate, at)
	}
	if _, exists := s.vehicles[id]; exists {
		return s.fail("RegisterVehicle", ErrInvalid, id, plate, at)
	}
	s.vehicles[id] = &vehicleState{
		id:       id,
		plates:   []plateChange{{at: at, plate: plate}},
		dayEntry: map[string][]RawEntry{},
		days:     map[string]*dayState{},
	}
	s.advance(at)
	s.ok("RegisterVehicle", nil, id, plate, at)
	return nil
}

// ChangePlate 更换车牌；at 恰等于某次进入时刻时该次进入归新车牌。
func (s *Service) ChangePlate(id, plate string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || plate == "" || at.IsZero() {
		return s.fail("ChangePlate", ErrInvalid, id, plate, at)
	}
	if at.Before(s.last) {
		return s.fail("ChangePlate", ErrClockBack, id, plate, at)
	}
	v, ok := s.vehicles[id]
	if !ok {
		return s.fail("ChangePlate", ErrVehicleNotFound, id, plate, at)
	}
	if plate == v.plateAt(at) || (len(v.plates) > 0 && at.Equal(v.plates[len(v.plates)-1].at)) {
		return s.fail("ChangePlate", ErrInvalid, id, plate, at)
	}
	v.plates = append(v.plates, plateChange{at: at, plate: plate})
	sortPlates(v.plates)
	s.advance(at)
	s.ok("ChangePlate", nil, id, plate, at)
	return nil
}

// Enter 记录一次进入。仅在收费时段且当日首次时产生计费。
func (s *Service) Enter(vehicleID, zoneID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if vehicleID == "" || zoneID == "" || at.IsZero() {
		return s.fail("Enter", ErrInvalid, vehicleID, zoneID, at)
	}
	if at.Before(s.last) {
		return s.fail("Enter", ErrClockBack, vehicleID, zoneID, at)
	}
	v, ok := s.vehicles[vehicleID]
	if !ok {
		return s.fail("Enter", ErrVehicleNotFound, vehicleID, zoneID, at)
	}
	if _, ok := s.zones.get(zoneID); !ok {
		return s.fail("Enter", ErrZoneNotFound, vehicleID, zoneID, at)
	}
	s.seq++
	entry := RawEntry{
		Seq:       s.seq,
		VehicleID: vehicleID,
		ZoneID:    zoneID,
		At:        at,
		Plate:     v.plateAt(at),
	}
	v.entries = append(v.entries, entry)
	day := dayKey(at, s.cfg.Location)
	v.dayEntry[day] = append(v.dayEntry[day], entry)
	s.advance(at)
	s.settle(v, day, at, "entry")
	s.ok("Enter", nil, vehicleID, zoneID, at)
	return nil
}

// AddQualification 登记资格（可追溯）。生效区间为左闭右开。
func (s *Service) AddQualification(vehicleID string, q Qualification, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if vehicleID == "" || at.IsZero() || !q.Kind.valid() || q.End.Before(q.Start) || q.End.Equal(q.Start) {
		return s.fail("AddQualification", ErrInvalid, vehicleID, q, at)
	}
	if q.Discount < 0 || q.Discount > s.cfg.DiscountBasis {
		return s.fail("AddQualification", ErrInvalid, vehicleID, q, at)
	}
	if at.Before(s.last) {
		return s.fail("AddQualification", ErrClockBack, vehicleID, q, at)
	}
	v, ok := s.vehicles[vehicleID]
	if !ok {
		return s.fail("AddQualification", ErrVehicleNotFound, vehicleID, q, at)
	}
	if q.Kind == Resident {
		if q.ZoneID == "" {
			return s.fail("AddQualification", ErrInvalid, vehicleID, q, at)
		}
		if _, ok := s.zones.get(q.ZoneID); !ok {
			return s.fail("AddQualification", ErrZoneNotFound, vehicleID, q, at)
		}
	}
	for _, ex := range v.quals {
		if ex.Kind != q.Kind {
			continue
		}
		if q.Kind == Resident && ex.ZoneID != q.ZoneID {
			continue
		}
		if intervalsOverlap(ex.Start, ex.End, q.Start, q.End) {
			return s.fail("AddQualification", ErrIntervalOverlap, vehicleID, q, at)
		}
	}

	v.quals = append(v.quals, q)
	sortQuals(v.quals)
	s.advance(at)

	// 重新结算区间覆盖到的、且已有事件的自然日（冻结日照常记录、不改冻结值）。
	// 追溯期仅约束这些“确有事件的受影响日”：最早一日的日结束距 at 不得超过 RetroDays。
	cutoff := at.AddDate(0, 0, -s.cfg.RetroDays)
	earliest := ""
	for day := range v.dayEntry {
		dStart := s.dayEnd(day).Add(-24 * time.Hour)
		dEnd := s.dayEnd(day)
		if s.cfg.RetroDays >= 0 && q.Start.Before(dEnd) && q.End.After(dStart) {
			if earliest == "" || day < earliest {
				earliest = day
			}
		}
	}
	if earliest != "" && s.cfg.RetroDays >= 0 && s.dayEnd(earliest).Before(cutoff) {
		v.quals = v.quals[:len(v.quals)-1]
		return s.fail("AddQualification", ErrTooLate, vehicleID, q, at)
	}
	for day := range v.dayEntry {
		dStart := s.dayEnd(day).Add(-24 * time.Hour)
		dEnd := s.dayEnd(day)
		if q.Start.Before(dEnd) && q.End.After(dStart) {
			s.settle(v, day, at, "retroactive:"+qualName(q.Kind))
		}
	}
	s.ok("AddQualification", nil, vehicleID, q, at)
	return nil
}

// OpenDispute 冻结某车某日；重复争议被拒绝。
func (s *Service) OpenDispute(vehicleID, day string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if vehicleID == "" || !validDay(day) || at.IsZero() {
		return s.fail("OpenDispute", ErrInvalid, vehicleID, day, at)
	}
	if at.Before(s.last) {
		return s.fail("OpenDispute", ErrClockBack, vehicleID, day, at)
	}
	v, ok := s.vehicles[vehicleID]
	if !ok {
		return s.fail("OpenDispute", ErrVehicleNotFound, vehicleID, day, at)
	}
	d := v.ensureDay(day)
	if d.frozen {
		return s.fail("OpenDispute", ErrDisputeExists, vehicleID, day, at)
	}
	d.frozen = true
	d.frozenValue = d.payable
	d.frozenLines = append([]ChargeLine(nil), d.lines...)
	s.advance(at)
	s.ok("OpenDispute", nil, vehicleID, day, at)
	return nil
}

// CloseDispute 按全部事件与资格一次性重算，差额作为单独一笔调整。
func (s *Service) CloseDispute(vehicleID, day string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if vehicleID == "" || !validDay(day) || at.IsZero() {
		return s.fail("CloseDispute", ErrInvalid, vehicleID, day, at)
	}
	if at.Before(s.last) {
		return s.fail("CloseDispute", ErrClockBack, vehicleID, day, at)
	}
	v, ok := s.vehicles[vehicleID]
	if !ok {
		return s.fail("CloseDispute", ErrVehicleNotFound, vehicleID, day, at)
	}
	d, ok := v.days[day]
	if !ok || !d.frozen {
		return s.fail("CloseDispute", ErrNoDispute, vehicleID, day, at)
	}
	s.advance(at)
	pay, lines := s.recomputeDay(v, day)
	delta := pay - d.frozenValue
	if delta != 0 {
		d.adjustments = append(d.adjustments, Adjustment{
			Seq: s.nextAdjSeq(), At: at, Amount: delta,
			Reason: "dispute_close:recompute", Before: d.frozenValue, After: pay,
		})
	}
	d.frozen = false
	d.frozenValue = 0
	d.frozenLines = nil
	d.payable = pay
	d.lines = lines
	s.ok("CloseDispute", nil, vehicleID, day, at)
	return nil
}

// Query 返回某车某日应付与全部依据。开销只取决于该日事件数，与历史总量无关。
func (s *Service) Query(vehicleID, day string) (DayReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if vehicleID == "" || !validDay(day) {
		return DayReport{}, s.fail("Query", ErrInvalid, vehicleID, day)
	}
	v, ok := s.vehicles[vehicleID]
	if !ok {
		return DayReport{}, s.fail("Query", ErrVehicleNotFound, vehicleID, day)
	}
	d := v.ensureDay(day)
	rep := DayReport{
		VehicleID:   vehicleID,
		Day:         day,
		Payable:     d.payable,
		Lines:       append([]ChargeLine(nil), d.lines...),
		Adjustments: append([]Adjustment(nil), d.adjustments...),
		Frozen:      d.frozen,
	}
	if d.frozen {
		rep.Lines = append([]ChargeLine(nil), d.frozenLines...)
	}
	s.ok("Query", nil, vehicleID, day)
	return rep, nil
}

// VehicleEntries 导出某车全部原始进入（测试/审计使用）。
func (s *Service) VehicleEntries(id string) []RawEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.vehicles[id]; ok {
		out := make([]RawEntry, len(v.entries))
		copy(out, v.entries)
		return out
	}
	return nil
}

func (s *Service) advance(at time.Time) {
	if at.After(s.last) {
		s.last = at
	}
}

func (s *Service) nextAdjSeq() int64 {
	s.seq++
	return s.seq
}

func (v *vehicleState) ensureDay(day string) *dayState {
	d, ok := v.days[day]
	if !ok {
		d = &dayState{}
		v.days[day] = d
	}
	return d
}

// plateAt 解析某时刻应归属的车牌：变更时刻恰等于进入时刻时归新车牌。
func (v *vehicleState) plateAt(at time.Time) string {
	plate := v.plates[0].plate
	for _, c := range v.plates {
		if !at.Before(c.at) {
			plate = c.plate
		}
	}
	return plate
}

// recomputeDay 以该车该日全部事件与当前全部资格一次性重算（纯函数包装）。
func (s *Service) recomputeDay(v *vehicleState, day string) (int64, []ChargeLine) {
	entries := append([]RawEntry(nil), v.dayEntry[day]...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Seq < entries[j].Seq })
	return recompute(recalcInput{
		day:     day,
		cfg:     s.cfg,
		zones:   s.zones,
		entries: entries,
		quals:   v.quals,
	})
}

// settle 重算某车某日并记账。冻结日只记录、不动冻结值；
// 非冻结日把应付差额作为一笔带符号调整（退款为负），满足：
// payable == 初始入账 + Σ adjustments。
func (s *Service) settle(v *vehicleState, day string, at time.Time, reason string) {
	d := v.ensureDay(day)
	pay, lines := s.recomputeDay(v, day)
	if d.frozen {
		return
	}
	delta := pay - d.payable
	// 入账与退款都记账，保证 payable = Σ adjustments，每笔金额均可复现。
	if delta != 0 {
		d.adjustments = append(d.adjustments, Adjustment{
			Seq: s.nextAdjSeq(), At: at, Amount: delta,
			Reason: reason, Before: d.payable, After: pay,
		})
	}
	d.payable = pay
	d.lines = lines
}

// dayEnd 返回某自然日（配置时区）的右开结束时刻。
func (s *Service) dayEnd(day string) time.Time {
	t, err := time.ParseInLocation("2006-01-02", day, s.cfg.Location)
	if err != nil {
		return time.Time{}
	}
	return t.AddDate(0, 0, 1)
}

func validDay(day string) bool {
	_, err := time.Parse("2006-01-02", day)
	return err == nil && len(day) == 10
}

// 左闭右开区间重叠判定：[as,ae) 与 [bs,be)。
func intervalsOverlap(as, ae, bs, be time.Time) bool {
	return as.Before(be) && bs.Before(ae)
}

func sortPlates(p []plateChange) {
	sort.SliceStable(p, func(i, j int) bool { return p[i].at.Before(p[j].at) })
}

func sortQuals(q []Qualification) {
	sort.SliceStable(q, func(i, j int) bool { return q[i].Start.Before(q[j].Start) })
}

func qualName(k QualificationKind) string {
	switch k {
	case Disabled:
		return "disabled"
	case NewEnergy:
		return "new_energy"
	default:
		return "resident"
	}
}

// fail / ok 打印逐条操作审计日志：输入、输出与判定依据。
func (s *Service) fail(op string, err error, args ...any) error {
	s.logf("OP %-18s REJECT kind=%-18s input=%s", op, Kind(err), joinArgs(args))
	return err
}

func (s *Service) ok(op string, result any, args ...any) {
	s.logf("OP %-18s ACCEPT input=%s result=%v", op, joinArgs(args), result)
}

func (s *Service) logf(format string, a ...any) {
	if s.logger != nil {
		fmt.Fprintf(s.logger, format+"\n", a...)
	}
}

func joinArgs(args []any) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = fmt.Sprintf("%v", a)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

package scheduler

import (
	"sort"
	"sync"
)

// engine.go 为引擎入口：持有全局锁并对外提供
// 安排、移动、互换、撤销与查询操作。
//
// 所有写操作在同一把互斥锁内完成「校验 + 提交」，因而并发调用的
// 结果等价于某个串行顺序；校验先在纯内存视图上进行，只有全部通过
// 后才改动账目，任何拒绝都不留下部分写入（含监考占用账目）。

// placed 是一门考试被接受后的内部安排记录。
type placed struct {
	examID   string
	start    int
	baseEnd  int
	extEnd   int
	rooms    []RoomAssign
	proctors map[int]map[string]string // slot -> room -> proctor
}

// candidate 是事务中一门待落地考试的新位置描述。
type candidate struct {
	examID  string
	start   int
	roomIDs []string
}

// Engine 是排考与调考引擎。
type Engine struct {
	mu       sync.Mutex
	cfg      Config
	cal      *Calendar
	venue    *venueBook
	proctors *proctorBook
	exams    map[string]examInfo
	placed   map[string]placed
	sidx     *studentIndex
}

// NewEngine 创建一个空引擎。配置非法返回 invalid_param。
func NewEngine(cfg Config, rooms []Room, proctors []Proctor) (*Engine, error) {
	if cfg.Days <= 0 || cfg.SlotsPerDay <= 0 || cfg.MaxExamsPerStudent <= 0 ||
		cfg.MaxProctorSlotsPerDay <= 0 || cfg.MinGapSlots < 0 || cfg.ExtensionRatio <= 0 {
		return nil, &SchedError{Code: ReasonInvalidParam, Message: "invalid config"}
	}
	roomSeen := map[string]struct{}{}
	for _, r := range rooms {
		if r.ID == "" || r.Capacity < 0 {
			return nil, &SchedError{Code: ReasonInvalidParam, Message: "invalid room"}
		}
		if _, ok := roomSeen[r.ID]; ok {
			return nil, &SchedError{Code: ReasonInvalidParam, Message: "duplicate room"}
		}
		roomSeen[r.ID] = struct{}{}
	}
	proctorSeen := map[string]struct{}{}
	for _, p := range proctors {
		if p.ID == "" {
			return nil, &SchedError{Code: ReasonInvalidParam, Message: "invalid proctor"}
		}
		if _, ok := proctorSeen[p.ID]; ok {
			return nil, &SchedError{Code: ReasonInvalidParam, Message: "duplicate proctor"}
		}
		proctorSeen[p.ID] = struct{}{}
	}
	return &Engine{
		cfg:      cfg,
		cal:      newCalendar(cfg),
		venue:    newVenueBook(rooms),
		proctors: newProctorBook(proctors, cfg.MaxProctorSlotsPerDay, cfg.SlotsPerDay),
		exams:    map[string]examInfo{},
		placed:   map[string]placed{},
		sidx:     newStudentIndex(),
	}, nil
}

// AddExam 注册一门考试的静态信息。
func (e *Engine) AddExam(ex Exam) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ex.ID == "" || ex.StandardSlots <= 0 || len(ex.Students) == 0 {
		return &SchedError{Code: ReasonInvalidParam, Message: "invalid exam", ExamID: ex.ID}
	}
	studentSeen := map[string]struct{}{}
	for _, s := range ex.Students {
		if s == "" {
			return &SchedError{Code: ReasonInvalidParam, Message: "invalid student", ExamID: ex.ID}
		}
		studentSeen[s] = struct{}{}
	}
	for _, s := range ex.ExtendedStudents {
		if _, ok := studentSeen[s]; !ok {
			return &SchedError{Code: ReasonInvalidParam, Message: "extended student not in exam", ExamID: ex.ID}
		}
	}
	if !ex.AllowExtension && len(ex.ExtendedStudents) > 0 {
		return &SchedError{Code: ReasonInvalidParam, Message: "extension not allowed", ExamID: ex.ID}
	}
	if _, exists := e.exams[ex.ID]; exists {
		return &SchedError{Code: ReasonInvalidParam, Message: "duplicate exam", ExamID: ex.ID}
	}
	e.exams[ex.ID] = newExamInfo(ex)
	return nil
}

// validateCandidates 是所有写操作共用的事务判定。
// remove 为判定时视为已移除的考试（移动/互换的旧位置）。
// 拒绝优先级：非法参数 > 跨日越界 > 考场时段占用 > 容量不足 >
// 学生冲突 > 监考不足。成功返回可直接落地的 placed（按 candidate 顺序）。
func (e *Engine) validateCandidates(cands []candidate, remove map[string]struct{}) ([]placed, *SchedError) {
	result := make([]placed, len(cands))

	// 阶段 0b：时段跨日 / 越界。
	for i, c := range cands {
		info := e.exams[c.examID]
		total := actualSlots(info, e.cfg.ExtensionRatio)
		end := c.start + total - 1
		result[i] = placed{
			examID:  c.examID,
			start:   c.start,
			baseEnd: c.start + info.exam.StandardSlots - 1,
			extEnd:  end,
			rooms:   e.venue.seatStudents(c.roomIDs, len(info.students), info.extendedCount),
		}
	}
	ordered0 := append([]candidate(nil), cands...)
	sort.Slice(ordered0, func(i, j int) bool { return ordered0[i].examID < ordered0[j].examID })
	for _, c := range ordered0 {
		info := e.exams[c.examID]
		end := c.start + actualSlots(info, e.cfg.ExtensionRatio) - 1
		if _, ok := e.cal.checkRange(c.start, end); !ok {
			return nil, &SchedError{Code: ReasonCrossDayOrOutOfRange, Message: "cross day or out of range", ExamID: c.examID}
		}
	}

	// 阶段 1：考场时段占用（已提交账目 + 候选之间互查）。
	tempBusy := map[string]map[int]string{}
	ordered := append([]placed(nil), result...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].examID < ordered[j].examID })
	for _, p := range ordered {
		for _, a := range p.rooms {
			last := p.extEnd
			if !a.Extended {
				last = p.baseEnd
			}
			for slot := p.start; slot <= last; slot++ {
				if owner, ok := e.venue.busy[a.RoomID][slot]; ok {
					if _, skip := remove[owner]; !skip {
						return nil, &SchedError{Code: ReasonRoomSlotBusy, Message: "room slot occupied", ExamID: p.examID}
					}
				}
				if owner, ok := tempBusy[a.RoomID][slot]; ok && owner != p.examID {
					return nil, &SchedError{Code: ReasonRoomSlotBusy, Message: "room slot occupied by co-candidate", ExamID: p.examID}
				}
				if tempBusy[a.RoomID] == nil {
					tempBusy[a.RoomID] = map[int]string{}
				}
				tempBusy[a.RoomID][slot] = p.examID
			}
		}
	}

	// 阶段 2：考场容量之和。
	for _, p := range ordered {
		totalCap := 0
		for _, a := range p.rooms {
			totalCap += e.venue.rooms[a.RoomID].capacity
		}
		if totalCap < len(e.exams[p.examID].students) {
			return nil, &SchedError{Code: ReasonCapacityInsufficient, Message: "rooms too small", ExamID: p.examID}
		}
	}

	// 阶段 3：学生冲突，按 重叠 > 超门数 > 间隔不足 归因，同类取最小学生。
	best := studentConflict{}
	bestExam := ""
	priority := func(cat ConflictCategory) int {
		switch cat {
		case ConflictOverlap:
			return 0
		case ConflictOverCount:
			return 1
		default:
			return 2
		}
	}
	candByStudent := map[string][]interval{}
	for _, p := range result {
		for _, s := range e.exams[p.examID].students {
			candByStudent[s] = append(candByStudent[s], interval{examID: p.examID, start: p.start, end: p.extEnd})
		}
	}
	for s, civs := range candByStudent {
		base := make([]interval, 0, len(e.sidx.per[s]))
		for _, iv := range e.sidx.per[s] {
			if _, skip := remove[iv.examID]; !skip {
				base = append(base, iv)
			}
		}
		merged := append(base, civs...)
		cat := detectStudent(merged, e.cal, e.cfg.MaxExamsPerStudent, e.cfg.MinGapSlots)
		if cat == "" {
			continue
		}
		// 找出该学生候选区间中起始时段最早、标识最小的一门，归因到不合法候选。
		offender := ""
		offenderStart := 0
		hasOffender := false
		for _, iv := range civs {
			if !hasOffender || iv.start < offenderStart || (iv.start == offenderStart && iv.examID < offender) {
				offender = iv.examID
				offenderStart = iv.start
				hasOffender = true
			}
		}
		if best.category == "" || priority(cat) < priority(best.category) ||
			(priority(cat) == priority(best.category) && (s < best.student || (s == best.student && offender < best.exam))) {
			best = studentConflict{student: s, category: cat}
			bestExam = offender
		}
	}
	if best.category != "" {
		return nil, &SchedError{
			Code:     ReasonStudentConflict,
			Message:  "student conflict: " + string(best.category),
			Student:  best.student,
			Category: best.category,
			ExamID:   bestExam,
		}
	}

	// 阶段 4：监考人力，按日「保留的已提交岗位 + 本事务新岗位」精确匹配。
	chosen := map[string]map[int]map[string]string{}
	byDay := map[int][]proctorJob{}
	for _, p := range result {
		chosen[p.examID] = map[int]map[string]string{}
		for _, a := range p.rooms {
			last := p.baseEnd
			if a.Extended {
				last = p.extEnd
			}
			for slot := p.start; slot <= last; slot++ {
				day := e.cal.dayOf(slot)
				byDay[day] = append(byDay[day], proctorJob{day: day, slot: slot, roomID: a.RoomID})
			}
		}
	}
	days := make([]int, 0, len(byDay))
	for d := range byDay {
		days = append(days, d)
	}
	sort.Ints(days)
	for _, day := range days {
		prob := dayProblem{day: day, baseCount: map[string]int{}, baseBusy: map[int]map[string]bool{}}
		for examID, p := range e.placed {
			if _, skip := remove[examID]; skip || e.cal.dayOf(p.start) != day {
				continue
			}
			for slot, rooms := range p.proctors {
				if e.cal.dayOf(slot) != day {
					continue
				}
				if prob.baseBusy[slot] == nil {
					prob.baseBusy[slot] = map[string]bool{}
				}
				for _, proctor := range rooms {
					prob.baseBusy[slot][proctor] = true
					prob.baseCount[proctor]++
				}
			}
		}
		prob.jobs = byDay[day]
		dayChosen := e.proctors.solveDay(prob)
		if dayChosen == nil {
			return nil, &SchedError{Code: ReasonProctorInsufficient, Message: "not enough proctors", ExamID: minExamID(cands)}
		}
		for slot, rooms := range dayChosen {
			for room, proctor := range rooms {
				owner := ""
				for _, p := range result {
					for _, a := range p.rooms {
						last := p.baseEnd
						if a.Extended {
							last = p.extEnd
						}
						if a.RoomID == room && slot >= p.start && slot <= last {
							owner = p.examID
						}
					}
				}
				if chosen[owner][slot] == nil {
					chosen[owner][slot] = map[string]string{}
				}
				chosen[owner][slot][room] = proctor
			}
		}
	}
	for i := range result {
		result[i].proctors = chosen[result[i].examID]
	}
	return result, nil
}

// commitPlaced 将通过判定的安排写入全部账目与学生索引。
func (e *Engine) commitPlaced(ps []placed) {
	for _, p := range ps {
		info := e.exams[p.examID]
		e.venue.commit(p.examID, p.rooms, p.start, p.baseEnd, p.extEnd)
		e.proctors.commit(p.examID, p)
		e.sidx.add(info.students, interval{examID: p.examID, start: p.start, end: p.extEnd})
		e.placed[p.examID] = p
	}
}

func toPlacement(p placed) *Placement {
	return &Placement{StartSlot: p.start, EndSlot: p.extEnd, Rooms: p.rooms, Proctors: p.proctors}
}

// minExamID 返回候选考试中标识最小者，用于跨候选错误的确定性归因。
func minExamID(cands []candidate) string {
	m := cands[0].examID
	for _, c := range cands[1:] {
		if c.examID < m {
			m = c.examID
		}
	}
	return m
}

// validateParams 只做纯参数合法性检查（不触碰已安排状态），
// 以便「参数非法」在操作专属的状态错误之前被优先报出。
func (e *Engine) validateParams(cands []candidate) *SchedError {
	for _, c := range cands {
		if _, ok := e.exams[c.examID]; !ok {
			return &SchedError{Code: ReasonInvalidParam, Message: "unknown exam", ExamID: c.examID}
		}
		if len(c.roomIDs) == 0 {
			return &SchedError{Code: ReasonInvalidParam, Message: "no room", ExamID: c.examID}
		}
		seen := map[string]struct{}{}
		for _, rid := range c.roomIDs {
			if !e.venue.roomExists(rid) {
				return &SchedError{Code: ReasonInvalidParam, Message: "unknown room", ExamID: c.examID}
			}
			if _, dup := seen[rid]; dup {
				return &SchedError{Code: ReasonInvalidParam, Message: "duplicate room in request", ExamID: c.examID}
			}
			seen[rid] = struct{}{}
		}
	}
	return nil
}

// Schedule 安排一门尚未安排的考试。
func (e *Engine) Schedule(examID string, startSlot int, roomIDs []string) (*Placement, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cand := candidate{examID: examID, start: startSlot, roomIDs: roomIDs}
	if schedErr := e.validateParams([]candidate{cand}); schedErr != nil {
		return nil, schedErr
	}
	if _, ok := e.placed[examID]; ok {
		return nil, &SchedError{Code: ReasonExamAlreadyPlaced, Message: "exam already placed", ExamID: examID}
	}
	ps, schedErr := e.validateCandidates([]candidate{cand}, map[string]struct{}{})
	if schedErr != nil {
		return nil, schedErr
	}
	e.commitPlaced(ps)
	return toPlacement(ps[0]), nil
}

// Move 把一门已安排考试整体挪到新的时段与考场组合。
// 判定以该考试自身已被移除的状态为基准；失败时旧安排完整保留。
func (e *Engine) Move(examID string, startSlot int, roomIDs []string) (*Placement, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cand := candidate{examID: examID, start: startSlot, roomIDs: roomIDs}
	if schedErr := e.validateParams([]candidate{cand}); schedErr != nil {
		return nil, schedErr
	}
	old, ok := e.placed[examID]
	if !ok {
		return nil, &SchedError{Code: ReasonExamNotPlaced, Message: "exam not placed", ExamID: examID}
	}
	ps, schedErr := e.validateCandidates(
		[]candidate{cand},
		map[string]struct{}{examID: {}},
	)
	if schedErr != nil {
		return nil, schedErr
	}
	e.rollbackOne(old)
	e.commitPlaced(ps)
	return toPlacement(ps[0]), nil
}

// Swap 交换两门考试的时段与考场组合；任一方不合法则整体拒绝，
// 错误中 ExamID 为不合法一方（两方均不合法时取标识较小者）。
func (e *Engine) Swap(examA, examB string) (*Placement, *Placement, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if examA == examB {
		return nil, nil, &SchedError{Code: ReasonInvalidParam, Message: "cannot swap exam with itself", ExamID: examA}
	}
	pa, oka := e.placed[examA]
	pb, okb := e.placed[examB]
	if !oka || !okb {
		missing := examA
		switch {
		case !oka && okb:
			missing = examA
		case oka && !okb:
			missing = examB
		case examB < examA:
			missing = examB
		}
		return nil, nil, &SchedError{Code: ReasonExamNotPlaced, Message: "exam not placed", ExamID: missing}
	}
	cands := []candidate{
		{examID: examA, start: pb.start, roomIDs: roomIDsOf(pb.rooms)},
		{examID: examB, start: pa.start, roomIDs: roomIDsOf(pa.rooms)},
	}
	ps, schedErr := e.validateCandidates(cands, map[string]struct{}{examA: {}, examB: {}})
	if schedErr != nil {
		return nil, nil, schedErr
	}
	e.rollbackOne(pa)
	e.rollbackOne(pb)
	e.commitPlaced(ps)
	return toPlacement(ps[0]), toPlacement(ps[1]), nil
}

func roomIDsOf(assigns []RoomAssign) []string {
	out := make([]string, len(assigns))
	for i, a := range assigns {
		out[i] = a.RoomID
	}
	return out
}

// rollbackOne 从全部账目与学生索引中移除一门考试（供事务切换位置）。
func (e *Engine) rollbackOne(p placed) {
	info := e.exams[p.examID]
	e.venue.release(p.examID, p.rooms, p.start, p.baseEnd, p.extEnd)
	e.proctors.release(p)
	e.sidx.remove(info.students, p.start)
	delete(e.placed, p.examID)
}

// Cancel 释放某考试的全部占用（含延长时段与监考人员）。
func (e *Engine) Cancel(examID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.placed[examID]
	if !ok {
		return &SchedError{Code: ReasonExamNotPlaced, Message: "exam not placed", ExamID: examID}
	}
	e.rollbackOne(p)
	return nil
}

// GetPlacement 查询一门考试当前安排；未安排返回 (nil, false)。
func (e *Engine) GetPlacement(examID string) (*Placement, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.placed[examID]
	if !ok {
		return nil, false
	}
	return toPlacement(p), true
}

// StudentSlots 返回某学生在各时段被哪门考试占用（升序）。
func (e *Engine) StudentSlots(studentID string) []Occupy {
	e.mu.Lock()
	defer e.mu.Unlock()
	list := e.sidx.per[studentID]
	out := make([]Occupy, 0)
	for _, iv := range list {
		for slot := iv.start; slot <= iv.end; slot++ {
			out = append(out, Occupy{Slot: slot, Day: e.cal.dayOf(slot), ExamID: iv.examID})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Slot != out[j].Slot {
			return out[i].Slot < out[j].Slot
		}
		return out[i].ExamID < out[j].ExamID
	})
	return out
}

// RoomSlots 返回某考场被占用的时段列表（升序）。
func (e *Engine) RoomSlots(roomID string) ([]Occupy, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.venue.roomExists(roomID) {
		return nil, &SchedError{Code: ReasonInvalidParam, Message: "unknown room"}
	}
	out := e.venue.slotsOfRoom(roomID)
	for i := range out {
		out[i].Day = e.cal.dayOf(out[i].Slot)
	}
	return out, nil
}

// ProctorSlots 返回某监考人员被占用的时段列表（升序）。
func (e *Engine) ProctorSlots(proctorID string) []Occupy {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := e.proctors.slotsOfProctor(proctorID)
	for i := range out {
		out[i].Day = e.cal.dayOf(out[i].Slot)
	}
	return out
}

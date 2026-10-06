package exam

import (
	"fmt"
	"sort"
	"sync"
)

// RoomAssign 描述一门考试在某考场的占用终点（闭区间右端）。
// 含延长学生的考场其 EndSlot 为延长后的终点，否则为标准终点。
type RoomAssign struct {
	RoomID  string
	EndSlot int
}

// Schedule 是一门已安排考试的完整占用记录。
type Schedule struct {
	ExamID      string
	Start       int
	Rooms       []RoomAssign
	Staff       map[int][]string // 时段 -> 该考场群所需监考（已按编号升序）
	studentRoom map[string]string
}

// Config 为全局排考参数。
type Config struct {
	MaxExamsPerDay   int // 同一学生同日考试门数上限
	MinGapSlots      int // 同一学生相邻两门考试间最少空闲时段数
	ProctorsPerRoom  int // 每个被占用考场每个时段所需监考人数
	MaxProctorPerDay int // 每名监考每日监考时段总数上限
}

// Occupancy 为查询返回的单条占用，按时段升序。
type Occupancy struct {
	Slot   int
	ExamID string
	RoomID string
}

// Engine 是考场排考与调考引擎；互斥锁保证所有操作可线性化串行。
type Engine struct {
	mu sync.Mutex

	cal    *Calendar
	rooms  *VenueRegistry
	enroll *Enrollment
	staff  *StaffRegistry
	cfg    Config
	w      *world
}

// world 是可变状态。所有判定先在 clone 上进行，成功后整体替换，失败不触碰原状态。
type world struct {
	sched        map[string]*Schedule
	roomBusy     map[int]map[string]string // slot -> roomID -> examID
	slotStaff    map[int]map[string]bool   // slot -> staff 占用集合
	staffDayLoad map[string]map[int]int    // staffID -> day -> 已监考时段数
	studentExams map[string][]string       // 学生 -> 其已参加的考试（倒排索引）
}

func newWorld() *world {
	return &world{
		sched:        map[string]*Schedule{},
		roomBusy:     map[int]map[string]string{},
		slotStaff:    map[int]map[string]bool{},
		staffDayLoad: map[string]map[int]int{},
		studentExams: map[string][]string{},
	}
}

func (w *world) clone() *world {
	c := newWorld()
	for id, s := range w.sched {
		ns := &Schedule{
			ExamID:      s.ExamID,
			Start:       s.Start,
			Rooms:       append([]RoomAssign(nil), s.Rooms...),
			Staff:       map[int][]string{},
			studentRoom: map[string]string{},
		}
		for k, v := range s.Staff {
			ns.Staff[k] = append([]string(nil), v...)
		}
		for k, v := range s.studentRoom {
			ns.studentRoom[k] = v
		}
		c.sched[id] = ns
	}
	for slot, m := range w.roomBusy {
		nm := make(map[string]string, len(m))
		for r, e := range m {
			nm[r] = e
		}
		c.roomBusy[slot] = nm
	}
	for slot, m := range w.slotStaff {
		nm := make(map[string]bool, len(m))
		for p := range m {
			nm[p] = true
		}
		c.slotStaff[slot] = nm
	}
	for p, dm := range w.staffDayLoad {
		nm := make(map[int]int, len(dm))
		for d, n := range dm {
			nm[d] = n
		}
		c.staffDayLoad[p] = nm
	}
	for st, ids := range w.studentExams {
		c.studentExams[st] = append([]string(nil), ids...)
	}
	return c
}

// NewEngine 构造引擎。
func NewEngine(cal *Calendar, rooms *VenueRegistry, enroll *Enrollment, staff *StaffRegistry, cfg Config) (*Engine, error) {
	if cal == nil || rooms == nil || enroll == nil || staff == nil {
		return nil, fmt.Errorf("nil dependency")
	}
	if cfg.MaxExamsPerDay <= 0 || cfg.MinGapSlots < 0 || cfg.ProctorsPerRoom <= 0 || cfg.MaxProctorPerDay <= 0 {
		return nil, fmt.Errorf("invalid config: %+v", cfg)
	}
	return &Engine{
		cal: cal, rooms: rooms, enroll: enroll, staff: staff, cfg: cfg, w: newWorld(),
	}, nil
}

func errf(code ReasonCode, examID, detail string) *Error {
	return &Error{Code: code, ExamID: examID, Detail: detail}
}

func (g *Engine) markBusy(w *world, s *Schedule) {
	for _, ra := range s.Rooms {
		for slot := s.Start; slot <= ra.EndSlot; slot++ {
			if w.roomBusy[slot] == nil {
				w.roomBusy[slot] = map[string]string{}
			}
			w.roomBusy[slot][ra.RoomID] = s.ExamID
		}
	}
	for slot, ps := range s.Staff {
		if w.slotStaff[slot] == nil {
			w.slotStaff[slot] = map[string]bool{}
		}
		day := g.cal.DayOf(slot)
		for _, p := range ps {
			w.slotStaff[slot][p] = true
			if w.staffDayLoad[p] == nil {
				w.staffDayLoad[p] = map[int]int{}
			}
			w.staffDayLoad[p][day]++
		}
	}
	// 维护学生->考试倒排索引（按考试编号有序插入）。
	x := g.enroll.Get(s.ExamID)
	for _, st := range x.students {
		w.studentExams[st] = insertSorted(w.studentExams[st], s.ExamID)
	}
}

func insertSorted(list []string, v string) []string {
	i := sort.SearchStrings(list, v)
	list = append(list, "")
	copy(list[i+1:], list[i:])
	list[i] = v
	return list
}

func removeSorted(list []string, v string) []string {
	i := sort.SearchStrings(list, v)
	if i < len(list) && list[i] == v {
		return append(list[:i], list[i+1:]...)
	}
	return list
}

func (g *Engine) unmarkBusy(w *world, s *Schedule) {
	for _, ra := range s.Rooms {
		for slot := s.Start; slot <= ra.EndSlot; slot++ {
			if m := w.roomBusy[slot]; m != nil {
				delete(m, ra.RoomID)
				if len(m) == 0 {
					delete(w.roomBusy, slot)
				}
			}
		}
	}
	for slot, ps := range s.Staff {
		day := g.cal.DayOf(slot)
		for _, p := range ps {
			if m := w.slotStaff[slot]; m != nil {
				delete(m, p)
				if len(m) == 0 {
					delete(w.slotStaff, slot)
				}
			}
			if dm := w.staffDayLoad[p]; dm != nil {
				dm[day]--
				if dm[day] == 0 {
					delete(dm, day)
				}
			}
		}
	}
	x2 := g.enroll.Get(s.ExamID)
	for _, st := range x2.students {
		w.studentExams[st] = removeSorted(w.studentExams[st], s.ExamID)
	}
}

// validateCandidate 执行“已安排/参数/跨日”之外不依赖现有占用的参数检查，
// 并返回按调用顺序确定的考场列表与各自终点。
// 返回的 roomEnd 与 roomIDs 一一对应。
func (g *Engine) validateCandidate(x *examInfo, start int, roomIDs []string) ([]RoomAssign, int, *Error) {
	if start < 0 || len(roomIDs) == 0 {
		return nil, 0, errf(ReasonInvalidArgument, x.id, "empty rooms or negative start")
	}
	seen := map[string]bool{}
	var capSum int
	for _, rid := range roomIDs {
		if rid == "" {
			return nil, 0, errf(ReasonInvalidArgument, x.id, "empty room id")
		}
		if seen[rid] {
			return nil, 0, errf(ReasonInvalidArgument, x.id, fmt.Sprintf("duplicated room %q", rid))
		}
		seen[rid] = true
		c := g.rooms.Capacity(rid)
		if c < 0 {
			return nil, 0, errf(ReasonInvalidArgument, x.id, fmt.Sprintf("unknown room %q", rid))
		}
		capSum += c
	}
	dur := x.DurationSlots()
	end := start + dur - 1
	if !g.cal.InRange(start) || !g.cal.InRange(end) || !g.cal.SameDay(start, end) {
		return nil, 0, errf(ReasonCrossDayOrOutOfRange, x.id,
			fmt.Sprintf("slots [%d,%d] cross day or out of range", start, end))
	}
	if capSum < len(x.students) {
		// 容量不足：容量错误在“考场占用”之后报告，这里仍需让全部候选考场
		// 按最长占用参与考场占用检查，故直接返回全长占位。
		assigns := make([]RoomAssign, len(roomIDs))
		for i, rid := range roomIDs {
			assigns[i] = RoomAssign{RoomID: rid, EndSlot: end}
		}
		return assigns, capSum, nil
	}
	// 确定性座位分配：延长学生优先（编号升序），再排普通学生；
	// 按调用给定的考场顺序与容量依次填满。凡承接延长学生的考场占用到延长终点。
	order := make([]string, 0, len(x.students))
	for _, st := range x.students {
		if x.allowExtended && x.extended[st] {
			order = append(order, st)
		}
	}
	extCount := len(order)
	for _, st := range x.students {
		if !(x.allowExtended && x.extended[st]) {
			order = append(order, st)
		}
	}
	longRoom := map[string]bool{}
	studentRoom := map[string]string{}
	ri, used := 0, 0
	for i, st := range order {
		for used >= g.rooms.Capacity(roomIDs[ri]) {
			ri++
			used = 0
		}
		rid := roomIDs[ri]
		studentRoom[st] = rid
		used++
		if i < extCount {
			longRoom[rid] = true
		}
	}
	stdEnd := start + x.standardSlots - 1
	// 调用方显式列出的每个考场都视为被该考试占用（含未分到学生的预留考场），
	// 未分到学生的考场按标准终点占用。
	assigns := make([]RoomAssign, 0, len(roomIDs))
	for _, rid := range roomIDs {
		ra := RoomAssign{RoomID: rid, EndSlot: stdEnd}
		if longRoom[rid] {
			ra.EndSlot = end
		}
		assigns = append(assigns, ra)
	}
	return assigns, capSum, nil
}

// candidateView 汇总一门候选考试对单个学生的占用形态。
type candidateView struct {
	start   int
	stdEnd  int
	long    bool
	fullEnd int
	day     int
}

// studentConflicts 检查候选考试相对于 w 中现有安排的全部学生冲突。
// 返回按 重叠 > 超门数 > 间隔不足、同类取最小学生标识 的首个冲突。
//
// 开销说明：对候选考试的每个学生 s，只遍历 s 已参加的考试集合
// （大小 k_s，即该学生所参加考试总数），与考试总数/考场总数/时段总数无关。
func (g *Engine) studentConflicts(w *world, x *examInfo, start int) *Error {
	dur := x.DurationSlots()
	fullEnd := start + dur - 1
	stdEnd := start + x.standardSlots - 1
	day := g.cal.DayOf(start)

	var overlapID, tooManyID, gapID string
	for _, s := range x.students {
		long := x.allowExtended && x.extended[s]
		candEnd := stdEnd
		if long {
			candEnd = fullEnd
		}
		var sameDay []int // 该学生同日已有考试的（个人）区间端点对
		overlap, tooMany, gap := false, false, false
		count := 0
		// O(k_s)：仅遍历学生 s 的倒排索引（其参加的考试），与 N/考场数/时段数无关。
		for _, eid := range w.studentExams[s] {
			other := w.sched[eid]
			ox := g.enroll.Get(other.ExamID)
			oStart := other.Start
			oEnd := ox.StudentEnd(oStart, s)
			if g.cal.DayOf(oStart) != day {
				continue
			}
			count++
			sameDay = append(sameDay, oStart, oEnd)
			// 重叠：左闭右闭整时段集合有交集
			if start <= oEnd && oStart <= candEnd {
				overlap = true
			}
		}
		if count+1 > g.cfg.MaxExamsPerDay {
			tooMany = true
		}
		// 间隔：把同日区间按起点排序，检查插入候选区间后相邻区间空闲时段是否 >= MinGapSlots。
		if !overlap {
			gap = g.hasGapViolation(sameDay, start, candEnd, day)
		}
		if overlap && (overlapID == "" || s < overlapID) {
			overlapID = s
		}
		if tooMany && (tooManyID == "" || s < tooManyID) {
			tooManyID = s
		}
		if gap && (gapID == "" || s < gapID) {
			gapID = s
		}
	}
	if overlapID != "" {
		return &Error{Code: ReasonStudentConflict, ExamID: x.id, StudentID: overlapID, Kind: StudentConflictOverlap,
			Detail: fmt.Sprintf("student %q overlap", overlapID)}
	}
	if tooManyID != "" {
		return &Error{Code: ReasonStudentConflict, ExamID: x.id, StudentID: tooManyID, Kind: StudentConflictTooMany,
			Detail: fmt.Sprintf("student %q exceeds %d exams/day", tooManyID, g.cfg.MaxExamsPerDay)}
	}
	if gapID != "" {
		return &Error{Code: ReasonStudentConflict, ExamID: x.id, StudentID: gapID, Kind: StudentConflictGap,
			Detail: fmt.Sprintf("student %q gap < %d", gapID, g.cfg.MinGapSlots)}
	}
	return nil
}

// hasGapViolation 判断同日区间集合中插入候选 [cs,ce] 后是否出现间隔不足。
// pairs 为成对的 (start,end)。间隔恰好等于下限视为满足。
func (g *Engine) hasGapViolation(pairs []int, cs, ce, day int) bool {
	type iv struct{ l, r int }
	vs := make([]iv, 0, len(pairs)/2+1)
	for i := 0; i < len(pairs); i += 2 {
		vs = append(vs, iv{pairs[i], pairs[i+1]})
	}
	vs = append(vs, iv{cs, ce})
	sort.Slice(vs, func(i, j int) bool {
		if vs[i].l != vs[j].l {
			return vs[i].l < vs[j].l
		}
		return vs[i].r < vs[j].r
	})
	for i := 1; i < len(vs); i++ {
		// 相邻考试之间的空闲时段数 = 左区间右端到右区间左端之间的整时段数
		gapSlots := vs[i].l - vs[i-1].r - 1
		if gapSlots < g.cfg.MinGapSlots {
			return true
		}
	}
	return false
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// checkRoomBusy 检查候选考场区间在 w 中是否与现有占用冲突（含延长部分）。
func (g *Engine) checkRoomBusy(w *world, start int, assigns []RoomAssign) *Error {
	for _, ra := range assigns {
		for slot := start; slot <= ra.EndSlot; slot++ {
			if m := w.roomBusy[slot]; m != nil {
				if _, busy := m[ra.RoomID]; busy {
					return &Error{Code: ReasonRoomSlotBusy, Detail: fmt.Sprintf("room %q busy at slot %d", ra.RoomID, slot)}
				}
			}
		}
	}
	return nil
}

// selectStaff 为候选考试确定性选取监考：逐时段按监考人员编号升序，
// 挑选该时段空闲且当日监考时段数未超上限者。每个被占用考场每个时段
// 需要 ProctorsPerRoom 人。结果只依赖当前 w 与输入，与调用历史无关。
func (g *Engine) selectStaff(w *world, start int, assigns []RoomAssign) (map[int][]string, *Error) {
	need := len(assigns) * g.cfg.ProctorsPerRoom
	day := g.cal.DayOf(start)
	maxEnd := start
	for _, ra := range assigns {
		if ra.EndSlot > maxEnd {
			maxEnd = ra.EndSlot
		}
	}
	chosen := map[int][]string{}
	for slot := start; slot <= maxEnd; slot++ {
		// 该时段仍被占用的考场数（延长部分可能少于全部考场）
		activeRooms := 0
		for _, ra := range assigns {
			if slot <= ra.EndSlot {
				activeRooms++
			}
		}
		needSlot := activeRooms * g.cfg.ProctorsPerRoom
		picked := make([]string, 0, needSlot)
		for _, p := range g.staff.List() {
			if len(picked) >= needSlot {
				break
			}
			if m := w.slotStaff[slot]; m != nil && m[p] {
				continue
			}
			if w.staffDayLoad[p][day] >= g.cfg.MaxProctorPerDay {
				continue
			}
			picked = append(picked, p)
		}
		if len(picked) < needSlot {
			return nil, &Error{Code: ReasonStaffShortage,
				Detail: fmt.Sprintf("need %d proctors at slot %d, got %d", needSlot, slot, len(picked))}
		}
		chosen[slot] = picked
	}
	_ = need
	return chosen, nil
}

// tryAdd 在给定 world 上尝试完整加入一门考试，执行全部判定并真正写入占用。
// 拒绝优先级：参数非法 > 已安排 > 跨日越界 > 考场占用 > 容量不足 > 学生冲突 > 监考不足。
// 注意容量不足在 validateCandidate 中先于学生/监考，但考场时段占用需读 w。
func (g *Engine) tryAdd(w *world, examID string, start int, roomIDs []string, checkScheduled bool) *Error {
	x := g.enroll.Get(examID)
	if x == nil {
		return errf(ReasonInvalidArgument, examID, "unknown exam")
	}
	if checkScheduled {
		if _, ok := w.sched[examID]; ok {
			return errf(ReasonExamAlreadyScheduled, examID, "already scheduled")
		}
	}
	assigns, capSum, verr := g.validateCandidate(x, start, roomIDs)
	if verr != nil {
		return verr // 参数非法 / 跨日越界
	}
	if rerr := g.checkRoomBusy(w, start, assigns); rerr != nil {
		rerr.ExamID = examID
		return rerr
	}
	if capSum < len(x.students) {
		return errf(ReasonRoomCapacity, examID,
			fmt.Sprintf("capacity %d < students %d", capSum, len(x.students)))
	}
	if serr := g.studentConflicts(w, x, start); serr != nil {
		return serr
	}
	chosen, serr := g.selectStaff(w, start, assigns)
	if serr != nil {
		serr.ExamID = examID
		return serr
	}
	studentRoom := g.computeStudentRoom(x, roomIDs)
	s := &Schedule{
		ExamID:      examID,
		Start:       start,
		Rooms:       assigns,
		Staff:       chosen,
		studentRoom: studentRoom,
	}
	w.sched[examID] = s
	g.markBusy(w, s)
	return nil
}

func (g *Engine) computeStudentRoom(x *examInfo, roomIDs []string) map[string]string {
	order := make([]string, 0, len(x.students))
	for _, st := range x.students {
		if x.allowExtended && x.extended[st] {
			order = append(order, st)
		}
	}
	for _, st := range x.students {
		if !(x.allowExtended && x.extended[st]) {
			order = append(order, st)
		}
	}
	out := map[string]string{}
	ri, used := 0, 0
	for _, st := range order {
		for used >= g.rooms.Capacity(roomIDs[ri]) {
			ri++
			used = 0
		}
		out[st] = roomIDs[ri]
		used++
	}
	return out
}

// studentConflictsIndexed 为基准测试暴露的 O(k_s) 判定入口，与正式判定相同。
func (g *Engine) studentConflictsIndexed(w *world, x *examInfo, start int) *Error {
	return g.studentConflicts(w, x, start)
}

// ScheduleExam 安排一门考试；被拒绝时不改动任何状态（含监考账目）。
func (g *Engine) ScheduleExam(examID string, startSlot int, roomIDs []string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	w := g.w.clone()
	if e := g.tryAdd(w, examID, startSlot, append([]string(nil), roomIDs...), true); e != nil {
		return e
	}
	g.w = w
	return nil
}

// Move 把已安排考试整体移动；判定基准为“自身已被移除”的状态，
// 失败时旧安排完整保留。
func (g *Engine) Move(examID string, startSlot int, roomIDs []string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.enroll.Get(examID) == nil {
		return errf(ReasonInvalidArgument, examID, "unknown exam")
	}
	old, ok := g.w.sched[examID]
	if !ok {
		return errf(ReasonExamNotScheduled, examID, "not scheduled")
	}
	w := g.w.clone()
	g.unmarkBusy(w, old)
	delete(w.sched, examID)
	if e := g.tryAdd(w, examID, startSlot, append([]string(nil), roomIDs...), false); e != nil {
		return e // g.w 未被替换，旧安排与账目完整保留
	}
	g.w = w
	return nil
}

// Swap 原子交换两门考试的时段与考场组合。
// 两门考试在新位置须同时合法；任一不合法则整体拒绝。
// 先按考试编号升序试放，不合法一方的标识与原因原样报出。
func (g *Engine) Swap(examA, examB string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if examA == examB || examA == "" || examB == "" {
		return errf(ReasonInvalidArgument, examA, "swap requires two distinct exam ids")
	}
	if g.enroll.Get(examA) == nil || g.enroll.Get(examB) == nil {
		bad := examA
		if g.enroll.Get(examA) != nil {
			bad = examB
		}
		return errf(ReasonInvalidArgument, bad, "unknown exam")
	}
	sa, oka := g.w.sched[examA]
	sb, okb := g.w.sched[examB]
	if !oka || !okb {
		bad := examA
		if oka {
			bad = examB
		}
		return errf(ReasonExamNotScheduled, bad, "not scheduled")
	}
	startA, roomsA := sa.Start, roomIDsOf(sa)
	startB, roomsB := sb.Start, roomIDsOf(sb)

	// A 落到 B 的旧位置，B 落到 A 的旧位置；按考试编号升序试放以确定归因。
	type move struct {
		id          string
		start       int
		rooms       []string
		counterpart string
	}
	moves := []move{
		{examA, startB, roomsB, examB},
		{examB, startA, roomsA, examA},
	}
	sort.Slice(moves, func(i, j int) bool { return moves[i].id < moves[j].id })

	w := g.w.clone()
	g.unmarkBusy(w, sa)
	g.unmarkBusy(w, sb)
	delete(w.sched, examA)
	delete(w.sched, examB)
	for _, mv := range moves {
		if e := g.tryAdd(w, mv.id, mv.start, mv.rooms, false); e != nil {
			e.ConflictID = mv.counterpart
			return e
		}
	}
	g.w = w
	return nil
}

func roomIDsOf(s *Schedule) []string {
	ids := make([]string, len(s.Rooms))
	for i, ra := range s.Rooms {
		ids[i] = ra.RoomID
	}
	sort.Strings(ids)
	return ids
}

// Cancel 撤销一门已安排考试的全部占用（含延长时段与监考人员）。
func (g *Engine) Cancel(examID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	s, ok := g.w.sched[examID]
	if !ok {
		return errf(ReasonExamNotScheduled, examID, "not scheduled")
	}
	g.unmarkBusy(g.w, s)
	delete(g.w.sched, examID)
	return nil
}

// QueryByStudent 返回某学生参加的考试占用时段列表，按时段升序。
func (g *Engine) QueryByStudent(studentID string) []Occupancy {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []Occupancy
	for _, s := range g.w.sched {
		rid, ok := s.studentRoom[studentID]
		if !ok {
			continue
		}
		x := g.enroll.Get(s.ExamID)
		end := x.StudentEnd(s.Start, studentID)
		for slot := s.Start; slot <= end; slot++ {
			out = append(out, Occupancy{Slot: slot, ExamID: s.ExamID, RoomID: rid})
		}
	}
	sortOccupancy(out)
	return out
}

// QueryByRoom 返回某考场全部被占用时段列表，按时段升序。
func (g *Engine) QueryByRoom(roomID string) []Occupancy {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []Occupancy
	for slot, m := range g.w.roomBusy {
		if examID, ok := m[roomID]; ok {
			out = append(out, Occupancy{Slot: slot, ExamID: examID, RoomID: roomID})
		}
	}
	sortOccupancy(out)
	return out
}

// QueryByStaff 返回某监考人员被安排的时段列表，按时段升序。
func (g *Engine) QueryByStaff(staffID string) []Occupancy {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []Occupancy
	for _, s := range g.w.sched {
		for slot, ps := range s.Staff {
			for _, p := range ps {
				if p == staffID {
					out = append(out, Occupancy{Slot: slot, ExamID: s.ExamID})
				}
			}
		}
	}
	sortOccupancy(out)
	return out
}

func sortOccupancy(out []Occupancy) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].Slot != out[j].Slot {
			return out[i].Slot < out[j].Slot
		}
		if out[i].ExamID != out[j].ExamID {
			return out[i].ExamID < out[j].ExamID
		}
		return out[i].RoomID < out[j].RoomID
	})
}

// Scheduled 返回某考试当前安排的只读副本（便于测试与外部复现）。
func (g *Engine) Scheduled(examID string) (Schedule, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s, ok := g.w.sched[examID]
	if !ok {
		return Schedule{}, false
	}
	cp := Schedule{
		ExamID: s.ExamID, Start: s.Start,
		Rooms: append([]RoomAssign(nil), s.Rooms...),
		Staff: map[int][]string{},
	}
	for k, v := range s.Staff {
		cp.Staff[k] = append([]string(nil), v...)
	}
	return cp, true
}

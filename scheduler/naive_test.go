package scheduler

// naive_test.go 用独立写成的朴素模型对随机操作序列做差分测试。
// 每一步同时作用于引擎与朴素模型，比对接受/拒绝、原因码、归因学生/类别/
// 不合法考试方，以及接受后的全部可见占用。日志打印每步输入、输出与判定依据。

import (
	"flag"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

var verboseDiff = flag.Bool("verbose-diff", false, "print every differential step input/output/basis")

type naiveExam struct {
	students []string
	extended map[string]struct{}
	stdSlots int
	allowExt bool
}

type naivePlaced struct {
	start    int
	end      int
	baseEnd  int
	rooms    []string
	extRooms map[string]bool
}

type naiveState struct {
	cfg      Config
	rooms    map[string]int
	proctors []string
	exams    map[string]naiveExam
	placed   map[string]naivePlaced
}

func newNaive(cfg Config, rooms []Room, proctors []Proctor) *naiveState {
	n := &naiveState{cfg: cfg, rooms: map[string]int{}, exams: map[string]naiveExam{}, placed: map[string]naivePlaced{}}
	for _, r := range rooms {
		n.rooms[r.ID] = r.Capacity
	}
	for _, p := range proctors {
		n.proctors = append(n.proctors, p.ID)
	}
	sort.Strings(n.proctors)
	return n
}

func (n *naiveState) add(ex Exam) {
	e := naiveExam{students: append([]string(nil), ex.Students...), extended: map[string]struct{}{}, stdSlots: ex.StandardSlots, allowExt: ex.AllowExtension}
	if ex.AllowExtension {
		for _, s := range ex.ExtendedStudents {
			e.extended[s] = struct{}{}
		}
	}
	n.exams[ex.ID] = e
}

func (n *naiveState) actualSlots(ex naiveExam) int {
	if !ex.allowExt || len(ex.extended) == 0 {
		return ex.stdSlots
	}
	return ex.stdSlots + ceilSlots(ex.stdSlots, n.cfg.ExtensionRatio)
}

func (n *naiveState) seatPlan(ex naiveExam, reqRooms []string) []RoomAssign {
	ids := append([]string(nil), reqRooms...)
	sort.Strings(ids)
	out := []RoomAssign{}
	seated := 0
	for _, id := range ids {
		cap := n.rooms[id]
		use := cap
		if seated+use > len(ex.students) {
			use = len(ex.students) - seated
		}
		out = append(out, RoomAssign{RoomID: id, CapacityUsed: use, Extended: seated < len(ex.extended) && use > 0})
		seated += use
	}
	return out
}

// naiveResult 是朴素模型对一步操作的判定。
type naiveResult struct {
	ok       bool
	code     ReasonCode
	student  string
	category ConflictCategory
	exam     string
}

// eval 评估 mods（examID -> start/rooms）在删除 del 后的假想状态。
func (n *naiveState) eval(mods map[string]naiveMod, del map[string]struct{}) naiveResult {
	type pinfo struct {
		ex       naiveExam
		start    int
		end      int
		baseEnd  int
		rooms    []string
		extRooms map[string]bool
	}
	current := map[string]pinfo{}
	modOrder := make([]string, 0, len(mods))
	for eid := range mods {
		modOrder = append(modOrder, eid)
	}
	sort.Strings(modOrder)

	for _, eid := range modOrder {
		m := mods[eid]
		ex, ok := n.exams[eid]
		if !ok {
			return naiveResult{code: ReasonInvalidParam, exam: eid}
		}
		if len(m.rooms) == 0 {
			return naiveResult{code: ReasonInvalidParam, exam: eid}
		}
		seen := map[string]struct{}{}
		for _, rid := range m.rooms {
			if _, exists := n.rooms[rid]; !exists {
				return naiveResult{code: ReasonInvalidParam, exam: eid}
			}
			if _, dup := seen[rid]; dup {
				return naiveResult{code: ReasonInvalidParam, exam: eid}
			}
			seen[rid] = struct{}{}
		}
		end := m.start + n.actualSlots(ex) - 1
		if m.start < 0 || end >= n.cfg.Days*n.cfg.SlotsPerDay || end < m.start ||
			m.start/n.cfg.SlotsPerDay != end/n.cfg.SlotsPerDay {
			return naiveResult{code: ReasonCrossDayOrOutOfRange, exam: eid}
		}
		plan := n.seatPlan(ex, m.rooms)
		extRooms := map[string]bool{}
		for _, ra := range plan {
			if ra.Extended {
				extRooms[ra.RoomID] = true
			}
		}
		current[eid] = pinfo{ex: ex, start: m.start, end: end, baseEnd: m.start + ex.stdSlots - 1, rooms: append([]string(nil), m.rooms...), extRooms: extRooms}
	}
	for eid, p := range n.placed {
		if _, d := del[eid]; d {
			continue
		}
		if _, isMod := current[eid]; isMod {
			continue
		}
		current[eid] = pinfo{ex: n.exams[eid], start: p.start, end: p.end, baseEnd: p.baseEnd, rooms: append([]string(nil), p.rooms...), extRooms: p.extRooms}
	}

	// 考场时段占用。已存在安排先入账（无归因歧义），候选随后按标识顺序入账。
	busy := map[int]map[string]string{}
	eids := make([]string, 0, len(current))
	for eid := range current {
		eids = append(eids, eid)
	}
	sort.Strings(eids)
	for _, eid := range eids {
		if _, isMod := mods[eid]; isMod {
			continue
		}
		p := current[eid]
		for _, rid := range p.rooms {
			last := p.baseEnd
			if p.extRooms[rid] {
				last = p.end
			}
			for slot := p.start; slot <= last; slot++ {
				if busy[slot] == nil {
					busy[slot] = map[string]string{}
				}
				if owner, exists := busy[slot][rid]; exists && owner != eid {
					return naiveResult{code: ReasonProctorInsufficient, exam: eid} // 不可能：已接受状态自洽
				}
				busy[slot][rid] = eid
			}
		}
	}
	for _, eid := range modOrder {
		p := current[eid]
		for _, rid := range p.rooms {
			last := p.baseEnd
			if p.extRooms[rid] {
				last = p.end
			}
			for slot := p.start; slot <= last; slot++ {
				if busy[slot] == nil {
					busy[slot] = map[string]string{}
				}
				if owner, exists := busy[slot][rid]; exists && owner != eid {
					return naiveResult{code: ReasonRoomSlotBusy, exam: eid}
				}
				busy[slot][rid] = eid
			}
		}
	}

	// 考场容量。
	for _, eid := range modOrder {
		p := current[eid]
		capSum := 0
		for _, rid := range p.rooms {
			capSum += n.rooms[rid]
		}
		if capSum < len(p.ex.students) {
			return naiveResult{code: ReasonCapacityInsufficient, exam: eid}
		}
	}

	// 学生冲突。
	byStudent := map[string][]interval{}
	for eid, p := range current {
		for _, s := range p.ex.students {
			byStudent[s] = append(byStudent[s], interval{examID: eid, start: p.start, end: p.end})
		}
	}
	students := make([]string, 0, len(byStudent))
	for s := range byStudent {
		students = append(students, s)
	}
	sort.Strings(students)
	cal := newCalendar(n.cfg)
	for _, cat := range []ConflictCategory{ConflictOverlap, ConflictOverCount, ConflictGapTooSmall} {
		for _, s := range students {
			if detectStudent(byStudent[s], cal, n.cfg.MaxExamsPerStudent, n.cfg.MinGapSlots) == cat {
				offender := ""
				offStart := 0
				has := false
				for _, iv := range byStudent[s] {
					if _, isMod := mods[iv.examID]; isMod {
						if !has || iv.start < offStart || (iv.start == offStart && iv.examID < offender) {
							offender = iv.examID
							offStart = iv.start
							has = true
						}
					}
				}
				return naiveResult{ok: false, code: ReasonStudentConflict, student: s, category: cat, exam: offender}
			}
		}
	}

	// 监考：按日回溯精确匹配（独立于引擎的最大流实现）。
	// 岗位按时段、考场排序，监考按标识排序；每人每日最多 maxPerDay 段、
	// 同一时段唯一。回溯成功即存在可行人选，可行性与具体选谁无关。
	maxDay := 0
	for _, p := range current {
		if d := p.end / n.cfg.SlotsPerDay; d > maxDay {
			maxDay = d
		}
	}
	for day := 0; day <= maxDay; day++ {
		type job struct {
			slot int
			room string
		}
		jobs := []job{}
		for _, eid := range eids {
			p := current[eid]
			if p.start/n.cfg.SlotsPerDay != day {
				continue
			}
			for _, rid := range p.rooms {
				last := p.baseEnd
				if p.extRooms[rid] {
					last = p.end
				}
				for slot := p.start; slot <= last; slot++ {
					jobs = append(jobs, job{slot: slot, room: rid})
				}
			}
		}
		if len(jobs) == 0 {
			continue
		}
		sort.Slice(jobs, func(i, j int) bool {
			if jobs[i].slot != jobs[j].slot {
				return jobs[i].slot < jobs[j].slot
			}
			return jobs[i].room < jobs[j].room
		})
		count := map[string]int{}
		busyAt := map[int]map[string]bool{}
		var backtrack func(idx int) bool
		backtrack = func(idx int) bool {
			if idx == len(jobs) {
				return true
			}
			jb := jobs[idx]
			for _, pr := range n.proctors {
				if count[pr] >= n.cfg.MaxProctorSlotsPerDay || busyAt[jb.slot][pr] {
					continue
				}
				count[pr]++
				if busyAt[jb.slot] == nil {
					busyAt[jb.slot] = map[string]bool{}
				}
				busyAt[jb.slot][pr] = true
				if backtrack(idx + 1) {
					return true
				}
				busyAt[jb.slot][pr] = false
				count[pr]--
			}
			return false
		}
		if !backtrack(0) {
			return naiveResult{code: ReasonProctorInsufficient, exam: modOrder[0]}
		}
	}

	return naiveResult{ok: true}
}

func (n *naiveState) commit(eid string, start int, rooms []string) {
	ex := n.exams[eid]
	plan := n.seatPlan(ex, rooms)
	extRooms := map[string]bool{}
	for _, ra := range plan {
		if ra.Extended {
			extRooms[ra.RoomID] = true
		}
	}
	n.placed[eid] = naivePlaced{
		start:    start,
		end:      start + n.actualSlots(ex) - 1,
		baseEnd:  start + ex.stdSlots - 1,
		rooms:    append([]string(nil), rooms...),
		extRooms: extRooms,
	}
}

func (n *naiveState) cancel(eid string) { delete(n.placed, eid) }

func (n *naiveState) studentSlots(s string) []int {
	out := []int{}
	for eid, p := range n.placed {
		for _, st := range n.exams[eid].students {
			if st == s {
				for slot := p.start; slot <= p.end; slot++ {
					out = append(out, slot)
				}
			}
		}
	}
	sort.Ints(out)
	return out
}

type modSpec struct {
	op    string
	exam  string
	examB string
	start int
	rooms []string
}

// TestNaiveDifferential 随机生成操作序列，与朴素模型逐步比对并打印日志。
func TestNaiveDifferential(t *testing.T) {
	const iterations = 60
	const seqLen = 250
	rng := rand.New(rand.NewSource(20261006))

	for iter := 0; iter < iterations; iter++ {
		cfg := Config{
			SlotsPerDay:           3 + rng.Intn(3),
			Days:                  2 + rng.Intn(2),
			MaxExamsPerStudent:    1 + rng.Intn(2),
			MinGapSlots:           rng.Intn(2),
			ExtensionRatio:        []float64{0.25, 0.5, 1.0}[rng.Intn(3)],
			MaxProctorSlotsPerDay: 2 + rng.Intn(4),
		}
		nRooms := 2 + rng.Intn(4)
		rooms := make([]Room, nRooms)
		for i := range rooms {
			rooms[i] = Room{ID: "R" + letter(i), Capacity: 1 + rng.Intn(6)}
		}
		nProctors := 1 + rng.Intn(6)
		proctors := make([]Proctor, nProctors)
		for i := range proctors {
			proctors[i] = Proctor{ID: "P" + letter(i)}
		}
		eng, err := NewEngine(cfg, rooms, proctors)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		nv := newNaive(cfg, rooms, proctors)

		nExams := 3 + rng.Intn(6)
		examIDs := []string{}
		for i := 0; i < nExams; i++ {
			id := "E" + letter(i)
			examIDs = append(examIDs, id)
			nStu := 1 + rng.Intn(6)
			students := []string{}
			for k := 0; k < nStu; k++ {
				students = append(students, "s"+letter(k))
			}
			allowExt := rng.Intn(2) == 0
			ext := []string{}
			if allowExt && rng.Intn(2) == 0 {
				ext = append(ext, students[rng.Intn(len(students))])
			}
			ex := Exam{ID: id, Students: students, ExtendedStudents: ext, StandardSlots: 1 + rng.Intn(3), AllowExtension: allowExt}
			if err := eng.AddExam(ex); err != nil {
				t.Fatalf("iter %d add: %v", iter, err)
			}
			nv.add(ex)
		}

		var log []string
		compareErr := func(step int, spec modSpec, engErr error, nr naiveResult) bool {
			if nr.ok {
				if engErr != nil {
					t.Fatalf("iter %d step %d %+v: engine rejected %v but naive accepted\n%s", iter, step, spec, engErr, joinLog(log))
				}
				return true
			}
			if engErr == nil {
				t.Fatalf("iter %d step %d %+v: engine accepted but naive rejected %s\n%s", iter, step, spec, nr.code, joinLog(log))
			}
			se := engErr.(*SchedError)
			if se.Code != nr.code {
				t.Fatalf("iter %d step %d %+v: code engine=%s naive=%s\n%s", iter, step, spec, se.Code, nr.code, joinLog(log))
			}
			if nr.code == ReasonStudentConflict {
				if se.Student != nr.student || se.Category != nr.category || se.ExamID != nr.exam {
					t.Fatalf("iter %d step %d: attribution engine=(%s,%s,%s) naive=(%s,%s,%s)\n%s",
						iter, step, se.Student, se.Category, se.ExamID, nr.student, nr.category, nr.exam, joinLog(log))
				}
			}
			if (nr.code == ReasonProctorInsufficient || nr.code == ReasonRoomSlotBusy || nr.code == ReasonCapacityInsufficient) && se.ExamID != nr.exam {
				t.Fatalf("iter %d step %d: exam attr engine=%s naive=%s\n%s", iter, step, se.ExamID, nr.exam, joinLog(log))
			}
			return true
		}

		for step := 0; step < seqLen; step++ {
			spec := modSpec{}
			kind := rng.Intn(10)
			switch {
			case kind < 6:
				spec.op = "schedule"
				spec.exam = examIDs[rng.Intn(len(examIDs))]
				spec.start = rng.Intn(cfg.Days*cfg.SlotsPerDay+2) - 1
				k := 1 + rng.Intn(nRooms)
				spec.rooms = pickRooms(rng, rooms, k)
			case kind < 8:
				spec.op = "move"
				spec.exam = examIDs[rng.Intn(len(examIDs))]
				spec.start = rng.Intn(cfg.Days*cfg.SlotsPerDay+2) - 1
				k := 1 + rng.Intn(nRooms)
				spec.rooms = pickRooms(rng, rooms, k)
			case kind == 8:
				spec.op = "swap"
				a := examIDs[rng.Intn(len(examIDs))]
				b := examIDs[rng.Intn(len(examIDs))]
				spec.exam, spec.examB = a, b
			default:
				spec.op = "cancel"
				spec.exam = examIDs[rng.Intn(len(examIDs))]
			}

			var engErr error
			var engP *Placement
			var engP2 *Placement
			switch spec.op {
			case "schedule":
				engP, engErr = eng.Schedule(spec.exam, spec.start, spec.rooms)
			case "move":
				engP, engErr = eng.Move(spec.exam, spec.start, spec.rooms)
			case "swap":
				engP, engP2, engErr = eng.Swap(spec.exam, spec.examB)
			case "cancel":
				engErr = eng.Cancel(spec.exam)
			}

			nr := nv.evalStep(spec)
			basis := nrBasis(nr)
			log = append(log, fmt.Sprintf("step %d %s in=%v -> engine=%v naive=%s %s", step, spec.op, spec, errName(engErr), nr.code, basis))
			if *verboseDiff {
				t.Logf("iter %d %s", iter, log[len(log)-1])
			}
			compareErr(step, spec, engErr, nr)

			// 接受后同步朴素状态。
			if nr.ok {
				switch spec.op {
				case "schedule", "move":
					nv.commit(spec.exam, spec.start, spec.rooms)
				case "cancel":
					nv.cancel(spec.exam)
				case "swap":
					pa := nv.placed[spec.exam]
					pb := nv.placed[spec.examB]
					nv.commit(spec.exam, pb.start, pb.rooms)
					nv.commit(spec.examB, pa.start, pa.rooms)
				}
			}

			// 接受后抽查：学生占用列表两边一致。
			if nr.ok && spec.op != "swap" {
				for stu := 0; stu < 7; stu++ {
					sid := "s" + letter(stu)
					got := eng.StudentSlots(sid)
					want := nv.studentSlots(sid)
					if len(got) != len(want) {
						t.Fatalf("iter %d step %d student %s slots diverge: engine=%v naive=%v\n%s", iter, step, sid, got, want, joinLog(log))
					}
					for i := range got {
						if got[i].Slot != want[i] {
							t.Fatalf("iter %d step %d student %s slot %d diverge\n%s", iter, step, sid, i, joinLog(log))
						}
					}
				}
			}
			_ = engP
			_ = engP2
		}
	}
}

func (n *naiveState) evalStep(spec modSpec) naiveResult {
	switch spec.op {
	case "schedule":
		// 参数优先级：未知等在 eval 内处理；已安排须在跨日之前。
		if _, ok := n.exams[spec.exam]; !ok {
			return naiveResult{code: ReasonInvalidParam, exam: spec.exam}
		}
		pre := n.paramOnly(spec)
		if pre.code != "" {
			return pre
		}
		if _, placed := n.placed[spec.exam]; placed {
			return naiveResult{code: ReasonExamAlreadyPlaced, exam: spec.exam}
		}
		return n.eval(map[string]naiveMod{spec.exam: {start: spec.start, rooms: spec.rooms}}, map[string]struct{}{})
	case "move":
		if _, ok := n.exams[spec.exam]; !ok {
			return naiveResult{code: ReasonInvalidParam, exam: spec.exam}
		}
		pre := n.paramOnly(spec)
		if pre.code != "" {
			return pre
		}
		if _, placed := n.placed[spec.exam]; !placed {
			return naiveResult{code: ReasonExamNotPlaced, exam: spec.exam}
		}
		return n.eval(map[string]naiveMod{spec.exam: {start: spec.start, rooms: spec.rooms}}, map[string]struct{}{spec.exam: {}})
	case "cancel":
		if _, ok := n.exams[spec.exam]; !ok {
			return naiveResult{code: ReasonInvalidParam, exam: spec.exam}
		}
		if _, placed := n.placed[spec.exam]; !placed {
			return naiveResult{code: ReasonExamNotPlaced, exam: spec.exam}
		}
		return naiveResult{ok: true}
	case "swap":
		if spec.exam == spec.examB {
			return naiveResult{code: ReasonInvalidParam, exam: spec.exam}
		}
		pa, oka := n.placed[spec.exam]
		pb, okb := n.placed[spec.examB]
		if !oka || !okb {
			missing := spec.exam
			if !oka && okb {
				missing = spec.exam
			} else if oka && !okb {
				missing = spec.examB
			} else if spec.examB < spec.exam {
				missing = spec.examB
			}
			return naiveResult{code: ReasonExamNotPlaced, exam: missing}
		}
		return n.eval(map[string]naiveMod{
			spec.exam:  {start: pb.start, rooms: pb.rooms},
			spec.examB: {start: pa.start, rooms: pa.rooms},
		}, map[string]struct{}{spec.exam: {}, spec.examB: {}})
	}
	return naiveResult{code: ReasonInvalidParam}
}

type naiveMod struct {
	start int
	rooms []string
}

func (n *naiveState) paramOnly(spec modSpec) naiveResult {
	if len(spec.rooms) == 0 {
		return naiveResult{code: ReasonInvalidParam, exam: spec.exam}
	}
	seen := map[string]struct{}{}
	for _, rid := range spec.rooms {
		if _, ok := n.rooms[rid]; !ok {
			return naiveResult{code: ReasonInvalidParam, exam: spec.exam}
		}
		if _, dup := seen[rid]; dup {
			return naiveResult{code: ReasonInvalidParam, exam: spec.exam}
		}
		seen[rid] = struct{}{}
	}
	return naiveResult{}
}

func pickRooms(rng *rand.Rand, rooms []Room, k int) []string {
	perm := rng.Perm(len(rooms))
	out := []string{}
	for i := 0; i < k && i < len(perm); i++ {
		out = append(out, rooms[perm[i]].ID)
	}
	return out
}

func letter(i int) string {
	if i < 26 {
		return string(rune('A' + i))
	}
	return string(rune('a'+(i/26)-1)) + string(rune('A'+(i%26)))
}

func joinLog(log []string) string {
	out := ""
	for _, l := range log {
		out += "\n  " + l
	}
	return out
}

func errName(err error) string {
	if err == nil {
		return "ACCEPT"
	}
	return err.Error()
}

func nrBasis(nr naiveResult) string {
	if nr.ok {
		return "basis=all constraints satisfied"
	}
	return "basis=" + string(nr.code)
}

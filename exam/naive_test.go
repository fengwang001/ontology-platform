package exam

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"
)

// 本文件以完全独立的“朴素模型”重写同一套规则，对随机操作序列做差分对照。
// 朴素模型直接保存每个考场/学生/监考的逐时段占用，逐步蛮力校验，
// 与引擎内部任何数据结构无共享，仅用于结果对账。

type nExam struct {
	id       string
	students []string
	extended map[string]bool
	std      int
	allowExt bool
	rNum     int
	rDen     int
	duration int
}

type nPlaced struct {
	id    string
	start int
	rooms []string
	// roomEnd[r] 为该考场的右端（闭），roomStd 记录是否延长考场
	roomEnd map[string]int
	staff   map[int][]string
}

type naiveModel struct {
	days, perDay int
	cfg          Config
	roomCap      map[string]int
	exams        map[string]*nExam
	staff        []string
	placed       map[string]*nPlaced
	roomSlot     map[int]map[string]string
	staffSlot    map[int]map[string]bool
	staffDay     map[string]map[int]int
}

func newNaive(days, perDay int, cfg Config, roomCap map[string]int, exams map[string]*nExam, staff []string) *naiveModel {
	return &naiveModel{
		days: days, perDay: perDay, cfg: cfg, roomCap: roomCap, exams: exams,
		staff:     append([]string(nil), staff...),
		placed:    map[string]*nPlaced{},
		roomSlot:  map[int]map[string]string{},
		staffSlot: map[int]map[string]bool{},
		staffDay:  map[string]map[int]int{},
	}
}

func (n *naiveModel) duration(x *nExam) int {
	extra := 0
	if x.allowExt && len(x.extended) > 0 {
		extra = (x.std*x.rNum + x.rDen - 1) / x.rDen
	}
	return x.std + extra
}

// roomPlan 复刻引擎的确定性座位分配，返回 roomEnd 与学生->考场。
func (n *naiveModel) roomPlan(x *nExam, start int, rooms []string) (map[string]int, map[string]string, int) {
	ext, normal := []string{}, []string{}
	for _, s := range x.students {
		if x.allowExt && x.extended[s] {
			ext = append(ext, s)
		} else {
			normal = append(normal, s)
		}
	}
	order := append(ext, normal...)
	capSum := 0
	for _, r := range rooms {
		capSum += n.roomCap[r]
	}
	if capSum < len(x.students) {
		// 容量不足：不做座位填充，全部考场按全长参与占用检查。
		re := map[string]int{}
		for _, r := range rooms {
			re[r] = start + n.duration(x) - 1
		}
		return re, map[string]string{}, capSum
	}
	sr := map[string]string{}
	long := map[string]bool{}
	ri, used := 0, 0
	for i, s := range order {
		for used >= n.roomCap[rooms[ri]] {
			ri++
			used = 0
		}
		sr[s] = rooms[ri]
		used++
		if i < len(ext) {
			long[rooms[ri]] = true
		}
	}
	// 显式列出的每个考场都视为占用；未分到学生的考场按标准终点。
	re := map[string]int{}
	for _, r := range rooms {
		re[r] = start + x.std - 1
		if long[r] {
			re[r] = start + n.duration(x) - 1
		}
	}
	return re, sr, capSum
}

// check 返回错误类别（与 ReasonCode 对齐）；成功返回 -1。
func (n *naiveModel) check(id string, start int, rooms []string) ReasonCode {
	x, ok := n.exams[id]
	if !ok || start < 0 || len(rooms) == 0 {
		return ReasonInvalidArgument
	}
	seen := map[string]bool{}
	for _, r := range rooms {
		if r == "" || seen[r] || n.roomCap[r] == 0 {
			return ReasonInvalidArgument
		}
		seen[r] = true
	}
	dur := n.duration(x)
	end := start + dur - 1
	if start < 0 || start >= n.days*n.perDay || end >= n.days*n.perDay || start/n.perDay != end/n.perDay {
		return ReasonCrossDayOrOutOfRange
	}
	re, _, capSum := n.roomPlan(x, start, rooms)
	enoughCap := capSum >= len(x.students)
	roomBusy := false
	for _, r := range rooms {
		lim := re[r] // roomPlan 返回全局右端
		for slot := start; slot <= lim; slot++ {
			if owner := n.roomSlot[slot][r]; owner != "" && owner != id {
				roomBusy = true
			}
		}
	}
	_ = dur
	if roomBusy {
		return ReasonRoomSlotBusy
	}
	if !enoughCap {
		return ReasonRoomCapacity
	}
	// 学生冲突
	var overlap, many, gap []string
	for _, s := range x.students {
		long := x.allowExt && x.extended[s]
		ce := start + x.std - 1
		if long {
			ce = end
		}
		count := 0
		type iv struct{ l, r int }
		var ivs []iv
		ov := false
		for _, p := range n.placed {
			px := n.exams[p.id]
			found := false
			for _, st := range px.students {
				if st == s {
					found = true
				}
			}
			if !found || p.start/n.perDay != start/n.perDay {
				continue
			}
			count++
			pe := p.start + px.std - 1
			if px.allowExt && px.extended[s] {
				pe = p.start + n.duration(px) - 1
			}
			ivs = append(ivs, iv{p.start, pe})
			if start <= pe && p.start <= ce {
				ov = true
			}
		}
		if ov {
			overlap = append(overlap, s)
			continue
		}
		if count+1 > n.cfg.MaxExamsPerDay {
			many = append(many, s)
		}
		ivs = append(ivs, iv{start, ce})
		sort.Slice(ivs, func(a, b int) bool {
			if ivs[a].l != ivs[b].l {
				return ivs[a].l < ivs[b].l
			}
			return ivs[a].r < ivs[b].r
		})
		badGap := false
		for i := 1; i < len(ivs); i++ {
			if ivs[i].l-ivs[i-1].r-1 < n.cfg.MinGapSlots {
				badGap = true
			}
		}
		if badGap {
			gap = append(gap, s)
		}
	}
	if len(overlap) > 0 {
		return ReasonStudentConflict
	}
	if len(many) > 0 {
		return ReasonStudentConflict
	}
	if len(gap) > 0 {
		return ReasonStudentConflict
	}
	// 监考：逐时段确定性挑选
	day := start / n.perDay
	maxEnd := start
	for _, r := range rooms {
		if re[r] > maxEnd {
			maxEnd = re[r]
		}
	}
	for slot := start; slot <= maxEnd; slot++ {
		active := 0
		for _, r := range rooms {
			if slot <= re[r] {
				active++
			}
		}
		need := active * n.cfg.ProctorsPerRoom
		got := 0
		for _, p := range n.staff {
			if got >= need {
				break
			}
			if n.staffSlot[slot][p] {
				continue
			}
			if n.staffDay[p][day] >= n.cfg.MaxProctorPerDay {
				continue
			}
			got++
		}
		if got < need {
			return ReasonStaffShortage
		}
	}
	return -1
}

// reindex 从 placed 记录全量重算房间与监考索引。
// 朴素模型优先选择“简单且显然正确”：每次成功写操作后重建，杜绝增量删改导致的别名残留。
func (n *naiveModel) reindex() {
	n.roomSlot = map[int]map[string]string{}
	n.staffSlot = map[int]map[string]bool{}
	n.staffDay = map[string]map[int]int{}
	ids := make([]string, 0, len(n.placed))
	for id := range n.placed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := n.placed[id]
		day := p.start / n.perDay
		for _, r := range p.rooms {
			for slot := p.start; slot <= p.start+p.roomEnd[r]; slot++ {
				if n.roomSlot[slot] == nil {
					n.roomSlot[slot] = map[string]string{}
				}
				n.roomSlot[slot][r] = id
			}
		}
		for slot, ps := range p.staff {
			if n.staffSlot[slot] == nil {
				n.staffSlot[slot] = map[string]bool{}
			}
			for _, pp := range ps {
				n.staffSlot[slot][pp] = true
				if n.staffDay[pp] == nil {
					n.staffDay[pp] = map[int]int{}
				}
				n.staffDay[pp][day]++
			}
		}
	}
}

func (n *naiveModel) place(id string, start int, rooms []string) {
	x := n.exams[id]
	re, _, _ := n.roomPlan(x, start, rooms)
	day := start / n.perDay
	maxEnd := start
	for _, e := range re {
		if e > maxEnd {
			maxEnd = e
		}
	}
	staffPick := map[int][]string{}
	for slot := start; slot <= maxEnd; slot++ {
		active := 0
		for _, e := range re {
			if slot <= e {
				active++
			}
		}
		need := active * n.cfg.ProctorsPerRoom
		picked := []string{}
		for _, p := range n.staff {
			if len(picked) >= need {
				break
			}
			if n.staffSlot[slot][p] || n.staffDay[p][day] >= n.cfg.MaxProctorPerDay {
				continue
			}
			picked = append(picked, p)
		}
		staffPick[slot] = picked
	}
	used := []string{}
	rel := map[string]int{}
	for _, r := range rooms {
		if _, ok := re[r]; ok {
			used = append(used, r)
			rel[r] = re[r] - start
		}
	}
	n.placed[id] = &nPlaced{id: id, start: start, rooms: used, roomEnd: rel, staff: staffPick}
	n.reindex()
}

func (n *naiveModel) remove(id string) bool {
	if _, ok := n.placed[id]; !ok {
		return false
	}
	delete(n.placed, id)
	n.reindex()
	return true
}

func TestRandomDifferential(t *testing.T) {
	seed := timeSeed()
	if s := os.Getenv("SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	rng := rand.New(rand.NewSource(seed))
	t.Logf("random seed = %d (复现: go test -run TestRandomDifferential -v)", seed)

	const days, perDay = 3, 10
	cfg := Config{MaxExamsPerDay: 2, MinGapSlots: 1, ProctorsPerRoom: 1, MaxProctorPerDay: 6}

	roomCap := map[string]int{"R1": 2, "R2": 3, "R3": 4}
	roomIDs := []string{"R1", "R2", "R3"}
	staffIDs := []string{"p1", "p2", "p3", "p4"}

	nExams := 10
	examDefs := map[string]*nExam{}
	var examObjs []*Exam
	examIDs := []string{}
	for i := 0; i < nExams; i++ {
		id := "E" + itoa(i)
		ns := 1 + rng.Intn(2)
		students := []string{}
		for s := 0; s < 1+rng.Intn(4); s++ {
			students = append(students, "s"+itooa(rng.Intn(8)))
		}
		students = dedup(students)
		ext := map[string]bool{}
		allowExt := rng.Intn(2) == 0
		if allowExt && rng.Intn(2) == 0 && len(students) > 0 {
			ext[students[rng.Intn(len(students))]] = true
		}
		rn, rd := 1, 4
		x := &Exam{ID: id, Students: students, Extended: ext, StandardSlots: ns,
			AllowExtended: allowExt, RatioNum: rn, RatioDen: rd}
		examObjs = append(examObjs, x)
		examIDs = append(examIDs, id)
		examDefs[id] = &nExam{id: id, students: append([]string(nil), students...),
			extended: cloneSet(ext), std: ns, allowExt: allowExt, rNum: rn, rDen: rd}
	}
	g := mustEngine(t, days, perDay, roomCap, examObjs, staffIDs, cfg)
	nm := newNaive(days, perDay, cfg, roomCap, examDefs, staffIDs)

	codeName := func(e error) string {
		if e == nil {
			return "ACCEPT"
		}
		return e.(*Error).Code.String()
	}

	applyNaive := func(op string, a, b string, start int, rooms []string) (string, bool) {
		switch op {
		case "schedule":
			if _, ok := nm.placed[a]; ok {
				return ReasonExamAlreadyScheduled.String(), false
			}
			c := nm.check(a, start, rooms)
			if c != -1 {
				return c.String(), false
			}
			nm.place(a, start, rooms)
			return "ACCEPT", true
		case "move":
			if _, ok := nm.placed[a]; !ok {
				return ReasonExamNotScheduled.String(), false
			}
			snap := snapshotNaive(nm)
			nm.remove(a)
			c := nm.check(a, start, rooms)
			if c != -1 {
				restoreNaive(nm, snap) // 原子性：失败完整保留旧安排
				return c.String(), false
			}
			nm.place(a, start, rooms)
			return "ACCEPT", false
		case "cancel":
			if !nm.remove(a) {
				return ReasonExamNotScheduled.String(), false
			}
			return "ACCEPT", false
		}
		return "?", false
	}

	var history []string
	for step := 0; step < 3000; step++ {
		op := []string{"schedule", "move", "cancel", "swap"}[rng.Intn(4)]
		a := examIDs[rng.Intn(len(examIDs))]
		b := examIDs[rng.Intn(len(examIDs))]
		start := rng.Intn(days * perDay)
		rooms := pickRooms(rng, roomIDs)

		var got error
		desc := ""
		switch op {
		case "schedule":
			desc = fmt.Sprintf("Schedule(%s,%d,%v)", a, start, rooms)
			got = g.ScheduleExam(a, start, rooms)
		case "move":
			desc = fmt.Sprintf("Move(%s,%d,%v)", a, start, rooms)
			got = g.Move(a, start, rooms)
		case "cancel":
			desc = fmt.Sprintf("Cancel(%s)", a)
			got = g.Cancel(a)
		case "swap":
			desc = fmt.Sprintf("Swap(%s,%s)", a, b)
			got = g.Swap(a, b)
		}
		// 朴素对照
		var want string
		switch op {
		case "swap":
			want = naiveSwap(nm, a, b)
		default:
			want, _ = applyNaive(op, a, "", start, rooms)
		}
		gotS := codeName(got)
		if gotS != want {
			for _, l := range history {
				t.Log(l)
			}
			if op != "swap" {
				for _, r := range rooms {
					t.Logf("naive room %q occupancy: %v", r, nm.roomSlotDebug(start, r))
				}
			}
			t.Fatalf("step %d %s: engine=%s naive=%s (seed=%d)", step, desc, gotS, want, seed)
		}
		basis := ""
		if got != nil {
			basis = got.(*Error).Detail
		}
		line := fmt.Sprintf("step=%03d %-28s -> %-9s (naive=%-9s) %s", step, desc, gotS, want, basis)
		history = append(history, line)
		if len(history) > 60 {
			history = history[1:]
		}
		if testing.Verbose() {
			t.Log(line)
		}
		// 每 500 步全量对账一次放置集合
		if step%500 == 0 {
			reconcile(t, g, nm, examIDs)
		}
	}
	reconcile(t, g, nm, examIDs)
}

// naiveSwap 在朴素模型上原子互换，返回结果类别；失败不改动。
func naiveSwap(nm *naiveModel, a, b string) string {
	if a == b {
		return ReasonInvalidArgument.String()
	}
	pa, oka := nm.placed[a]
	pb, okb := nm.placed[b]
	if !oka || !okb {
		return ReasonExamNotScheduled.String()
	}
	ra, rb := append([]string(nil), pa.rooms...), append([]string(nil), pb.rooms...)
	sa, sb := pa.start, pb.start
	snap := snapshotNaive(nm)
	nm.remove(a)
	nm.remove(b)
	first, second := a, b
	fs, fr, ss, sr2 := sb, rb, sa, ra
	if b < a {
		first, second = b, a
		fs, fr, ss, sr2 = sa, ra, sb, rb
	}
	if c := nm.check(first, fs, fr); c != -1 {
		restoreNaive(nm, snap)
		return c.String()
	}
	nm.place(first, fs, fr)
	if c := nm.check(second, ss, sr2); c != -1 {
		restoreNaive(nm, snap)
		return c.String()
	}
	nm.place(second, ss, sr2)
	return "ACCEPT"
}

func reconcile(t *testing.T, g *Engine, nm *naiveModel, ids []string) {
	t.Helper()
	for _, id := range ids {
		s, ok1 := g.Scheduled(id)
		p, ok2 := nm.placed[id]
		if ok1 != ok2 {
			t.Fatalf("reconcile %s: engine placed=%v naive=%v", id, ok1, ok2)
		}
		if ok1 && (s.Start != p.start || len(s.Rooms) != len(p.rooms)) {
			t.Fatalf("reconcile %s: engine start=%d rooms=%v naive start=%d rooms=%v",
				id, s.Start, s.Rooms, p.start, p.rooms)
		}
	}
}

func pickRooms(rng *rand.Rand, roomIDs []string) []string {
	n := 1 + rng.Intn(len(roomIDs))
	perm := rng.Perm(len(roomIDs))[:n]
	out := []string{}
	for _, i := range perm {
		out = append(out, roomIDs[i])
	}
	sort.Strings(out)
	return out
}

func dedup(in []string) []string {
	m := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if !m[s] {
			m[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func cloneSet(m map[string]bool) map[string]bool {
	o := map[string]bool{}
	for k, v := range m {
		o[k] = v
	}
	return o
}

func itooa(n int) string {
	if n == 0 {
		return "0"
	}
	b := ""
	for n > 0 {
		b = string(rune('0'+n%10)) + b
		n /= 10
	}
	return b
}

func timeSeed() int64 { return time.Now().UnixNano() }

var reasonNames = map[ReasonCode]string{
	ReasonInvalidArgument:      "INVALID",
	ReasonExamAlreadyScheduled: "ALREADY",
	ReasonCrossDayOrOutOfRange: "CROSSDAY",
	ReasonRoomSlotBusy:         "ROOMBUSY",
	ReasonRoomCapacity:         "CAPACITY",
	ReasonStudentConflict:      "STUDENT",
	ReasonStaffShortage:        "STAFF",
	ReasonExamNotScheduled:     "NOTSCHED",
}

func (r ReasonCode) String() string {
	if n, ok := reasonNames[r]; ok {
		return n
	}
	return "OK"
}

type snap struct {
	placed    map[string]*nPlaced
	roomSlot  map[int]map[string]string
	staffSlot map[int]map[string]bool
	staffDay  map[string]map[int]int
}

func snapshotNaive(nm *naiveModel) snap {
	s := snap{
		placed:    map[string]*nPlaced{},
		roomSlot:  map[int]map[string]string{},
		staffSlot: map[int]map[string]bool{},
		staffDay:  map[string]map[int]int{},
	}
	for id, p := range nm.placed {
		np := &nPlaced{
			id: p.id, start: p.start,
			rooms:   append([]string(nil), p.rooms...),
			roomEnd: map[string]int{}, staff: map[int][]string{},
		}
		for k, v := range p.roomEnd {
			np.roomEnd[k] = v
		}
		for k, v := range p.staff {
			np.staff[k] = append([]string(nil), v...)
		}
		s.placed[id] = np
	}
	// 防止 restore 后 placed 记录与后续 map 写入别名共享：恢复时再整体复制。
	for slot, m := range nm.roomSlot {
		s.roomSlot[slot] = map[string]string{}
		for k, v := range m {
			s.roomSlot[slot][k] = v
		}
	}
	for slot, m := range nm.staffSlot {
		s.staffSlot[slot] = map[string]bool{}
		for k, v := range m {
			s.staffSlot[slot][k] = v
		}
	}
	for p, dm := range nm.staffDay {
		s.staffDay[p] = map[int]int{}
		for d, v := range dm {
			s.staffDay[p][d] = v
		}
	}
	return s
}

func restoreNaive(nm *naiveModel, s snap) {
	nm.placed = map[string]*nPlaced{}
	for id, p := range s.placed {
		np := &nPlaced{id: p.id, start: p.start,
			rooms:   append([]string(nil), p.rooms...),
			roomEnd: map[string]int{}, staff: map[int][]string{}}
		for k, v := range p.roomEnd {
			np.roomEnd[k] = v
		}
		for k, v := range p.staff {
			np.staff[k] = append([]string(nil), v...)
		}
		nm.placed[id] = np
	}
	nm.roomSlot = map[int]map[string]string{}
	for sl, m := range s.roomSlot {
		nm.roomSlot[sl] = map[string]string{}
		for k, v := range m {
			nm.roomSlot[sl][k] = v
		}
	}
	nm.staffSlot = map[int]map[string]bool{}
	for sl, m := range s.staffSlot {
		nm.staffSlot[sl] = map[string]bool{}
		for k, v := range m {
			nm.staffSlot[sl][k] = v
		}
	}
	nm.staffDay = map[string]map[int]int{}
	for p, dm := range s.staffDay {
		nm.staffDay[p] = map[int]int{}
		for d, v := range dm {
			nm.staffDay[p][d] = v
		}
	}
}

func (n *naiveModel) roomSlotDebug(start int, r string) map[int]string {
	out := map[int]string{}
	day := start / n.perDay
	for slot := day * n.perDay; slot < (day+1)*n.perDay; slot++ {
		if o := n.roomSlot[slot][r]; o != "" {
			out[slot] = o
		}
	}
	return out
}

func cloneBoolMap(m map[string]bool) map[string]bool {
	o := make(map[string]bool, len(m)+1)
	for k, v := range m {
		o[k] = v
	}
	return o
}

func cloneIntMap(m map[int]int) map[int]int {
	o := make(map[int]int, len(m)+1)
	for k, v := range m {
		o[k] = v
	}
	return o
}

func cloneStringMap(m map[string]string) map[string]string {
	o := make(map[string]string, len(m)+1)
	for k, v := range m {
		o[k] = v
	}
	return o
}

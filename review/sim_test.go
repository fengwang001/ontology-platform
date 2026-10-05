package review

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"

	"ontology/formulary"
	"ontology/interact"
)

// ---------- 朴素模拟：按规则逐日累加，与引擎实现相互独立 ----------

type simItem struct {
	ing  string
	dose int64
	s, e int
}

type simRx struct {
	doctor, patient string
	items           []simItem
	submitDay       int
	status          Status
}

type sim struct {
	t         int
	maxNow    int
	hasNow    bool
	frozen    bool
	drugs     map[string]formulary.Drug
	limits    map[string]int64
	grades    map[[2]string]int
	allergies map[string]map[string]bool
	doctors   map[string]int
	pharms    map[string]bool
	patients  map[string]bool
	rxs       []*simRx
}

func newSim(t int) *sim {
	return &sim{
		t:         t,
		drugs:     make(map[string]formulary.Drug),
		limits:    make(map[string]int64),
		grades:    make(map[[2]string]int),
		allergies: make(map[string]map[string]bool),
		doctors:   make(map[string]int),
		pharms:    make(map[string]bool),
		patients:  make(map[string]bool),
	}
}

func simPairKey(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}

// ----- 配置镜像（与引擎同序检查：参数 > 冻结）-----

func (s *sim) addDrug(id, ing string, mg int64, level int) error {
	if id == "" || ing == "" || mg < 1 || mg > 1_000_000 || level < 1 || level > 3 {
		return formulary.ErrParam
	}
	if s.frozen {
		return formulary.ErrFrozen
	}
	if _, dup := s.drugs[id]; dup {
		return formulary.ErrParam
	}
	s.drugs[id] = formulary.Drug{ID: id, Ing: ing, Mg: mg, Level: level}
	return nil
}

func (s *sim) setMax(ing string, maxDay int64) error {
	if ing == "" || maxDay < 1 || maxDay > 1_000_000_000 {
		return formulary.ErrParam
	}
	if s.frozen {
		return formulary.ErrFrozen
	}
	s.limits[ing] = maxDay
	return nil
}

func (s *sim) setPair(a, b string, grade int) error {
	if a == "" || b == "" || a == b || grade < 1 || grade > 3 {
		return interact.ErrParam
	}
	if s.frozen {
		return interact.ErrFrozen
	}
	s.grades[simPairKey(a, b)] = grade
	return nil
}

func (s *sim) setAllergy(patient, ing string) error {
	if patient == "" || ing == "" {
		return interact.ErrParam
	}
	if s.frozen {
		return interact.ErrFrozen
	}
	m := s.allergies[patient]
	if m == nil {
		m = make(map[string]bool)
		s.allergies[patient] = m
	}
	m[ing] = true
	return nil
}

func (s *sim) addDoctor(id string, level int) Code {
	if id == "" || level < 1 || level > 3 {
		return ErrParam
	}
	if s.frozen {
		return ErrFrozen
	}
	s.doctors[id] = level
	return OK
}

func (s *sim) addPharm(id string) Code {
	if id == "" {
		return ErrParam
	}
	if s.frozen {
		return ErrFrozen
	}
	s.pharms[id] = true
	return OK
}

func (s *sim) addPatient(id string) Code {
	if id == "" {
		return ErrParam
	}
	if s.frozen {
		return ErrFrozen
	}
	s.patients[id] = true
	return OK
}

// ----- 状态镜像 -----

func (s *sim) effStatus(r *simRx, now int) Status {
	if r.status == StatusPending && now >= r.submitDay+s.t {
		return StatusExpired
	}
	return r.status
}

func (s *sim) expire(now int) {
	for _, r := range s.rxs {
		if r.status == StatusPending && now >= r.submitDay+s.t {
			r.status = StatusExpired
		}
	}
}

type simRef struct {
	ing  string
	s, e int
	dose int64
}

func (s *sim) refItems(patient string, now int) []simRef {
	var refs []simRef
	for _, r := range s.rxs {
		if r.patient != patient {
			continue
		}
		st := s.effStatus(r, now)
		if st != StatusActive && st != StatusPending {
			continue
		}
		for _, it := range r.items {
			if it.s >= it.e {
				continue
			}
			refs = append(refs, simRef{ing: it.ing, s: it.s, e: it.e, dose: it.dose})
		}
	}
	return refs
}

// submit 朴素实现：重叠按逐日判定，极量按逐日累加。
func (s *sim) submit(now int, doctor, patient string, items []Item) SubmitResult {
	if now < 0 || now > maxDate || len(items) < 1 || len(items) > 20 {
		return reject(ErrParam)
	}
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		if it.Pills < 1 || it.Pills > 20 || it.Times < 1 || it.Times > 12 ||
			it.S < 0 || it.E > maxDate || it.S >= it.E || now > it.S || seen[it.Drug] {
			return reject(ErrParam)
		}
		seen[it.Drug] = true
	}
	if s.hasNow && now < s.maxNow {
		return reject(ErrClock)
	}
	docLevel, ok := s.doctors[doctor]
	if !ok || !s.patients[patient] {
		return reject(ErrNotFound)
	}
	ings := make([]string, len(items))
	doses := make([]int64, len(items))
	for i, it := range items {
		d, ok := s.drugs[it.Drug]
		if !ok {
			return reject(ErrNotFound)
		}
		ings[i] = d.Ing
		doses[i] = d.Mg * int64(it.Pills) * int64(it.Times)
	}
	for i, it := range items {
		if s.drugs[it.Drug].Level > docLevel {
			return rejectAt(ErrPermission, i)
		}
	}
	for i := range items {
		if s.allergies[patient][ings[i]] {
			return rejectAt(ErrAllergy, i)
		}
	}

	refs := s.refItems(patient, now)

	// 相互作用：逐日判定区间重叠
	daySets := make([]map[int]bool, len(items))
	for i, it := range items {
		m := make(map[int]bool, it.E-it.S)
		for d := it.S; d < it.E; d++ {
			m[d] = true
		}
		daySets[i] = m
	}
	overlapDays := func(i, s2, e2 int) bool {
		for d := s2; d < e2; d++ {
			if daySets[i][d] {
				return true
			}
		}
		return false
	}
	contra := make([]bool, len(items))
	warnSet := make(map[[2]string]int)
	checkPair := func(i, s2, e2 int, ing2 string) {
		if ings[i] == ing2 || !overlapDays(i, s2, e2) {
			return
		}
		g := s.grades[simPairKey(ings[i], ing2)]
		if g == 3 {
			contra[i] = true
		} else if g > 0 {
			warnSet[simPairKey(ings[i], ing2)] = g
		}
	}
	for i := range items {
		for j := range items {
			if j != i {
				checkPair(i, items[j].S, items[j].E, ings[j])
			}
		}
		for _, r := range refs {
			checkPair(i, r.s, r.e, r.ing)
		}
	}
	for i := range items {
		if contra[i] {
			return rejectAt(ErrContra, i)
		}
	}

	// 超日极量：逐日累加
	done := make(map[string]bool)
	for i := range items {
		g := ings[i]
		if done[g] {
			continue
		}
		done[g] = true
		lim, ok := s.limits[g]
		if !ok {
			continue
		}
		sum := make(map[int]int64)
		for j := range items {
			if ings[j] == g {
				for d := items[j].S; d < items[j].E; d++ {
					sum[d] += doses[j]
				}
			}
		}
		for _, r := range refs {
			if r.ing == g {
				for d := r.s; d < r.e; d++ {
					sum[d] += r.dose
				}
			}
		}
		for _, v := range sum {
			if v > lim {
				return reject(ErrOverdose)
			}
		}
	}

	// 接受：落地到期、冻结、推进时钟、登记处方
	s.expire(now)
	s.frozen = true
	s.maxNow, s.hasNow = now, true

	warnings := make([]Warning, 0, len(warnSet))
	pending := false
	for p, g := range warnSet {
		if g == 2 {
			pending = true
		}
		warnings = append(warnings, Warning{A: p[0], B: p[1], Grade: g})
	}
	sort.Slice(warnings, func(i, j int) bool {
		if warnings[i].Grade != warnings[j].Grade {
			return warnings[i].Grade > warnings[j].Grade
		}
		if warnings[i].A != warnings[j].A {
			return warnings[i].A < warnings[j].A
		}
		return warnings[i].B < warnings[j].B
	})
	status := StatusActive
	if pending {
		status = StatusPending
	}
	id := len(s.rxs)
	sitems := make([]simItem, len(items))
	for i, it := range items {
		sitems[i] = simItem{ing: ings[i], dose: doses[i], s: it.S, e: it.E}
	}
	s.rxs = append(s.rxs, &simRx{
		doctor: doctor, patient: patient, items: sitems, submitDay: now, status: status,
	})
	return SubmitResult{Code: OK, Index: -1, RxID: id, Status: status, Warnings: warnings}
}

func (s *sim) judge(now int, pharmacist string, rxID int, to Status) Code {
	if now < 0 || now > maxDate || pharmacist == "" || rxID < 0 {
		return ErrParam
	}
	if s.hasNow && now < s.maxNow {
		return ErrClock
	}
	if !s.pharms[pharmacist] || rxID >= len(s.rxs) {
		return ErrNotFound
	}
	r := s.rxs[rxID]
	if s.effStatus(r, now) != StatusPending {
		return ErrState
	}
	s.expire(now)
	s.maxNow, s.hasNow = now, true
	r.status = to
	return OK
}

func (s *sim) stop(now int, doctor string, rxID int) Code {
	if now < 0 || now > maxDate || doctor == "" || rxID < 0 {
		return ErrParam
	}
	if s.hasNow && now < s.maxNow {
		return ErrClock
	}
	level, ok := s.doctors[doctor]
	if !ok || rxID >= len(s.rxs) {
		return ErrNotFound
	}
	r := s.rxs[rxID]
	if r.doctor != doctor && level != 3 {
		return ErrPermission
	}
	st := s.effStatus(r, now)
	if st != StatusActive && st != StatusPending {
		return ErrState
	}
	s.expire(now)
	s.maxNow, s.hasNow = now, true
	for i := range r.items {
		if r.items[i].e > now {
			r.items[i].e = now
		}
	}
	return OK
}

// ---------- 随机操作序列：引擎 vs 朴素模拟 ----------

type rndOp struct {
	kind   string // submit/approve/deny/stop/adddrug/setmax/setpair/setallergy/adddoctor/addpharm/addpatient
	now    int
	a, b   string // who/patient 或配置参数
	items  []Item
	rx     int
	mg     int64
	n1, n2 int // level/grade
}

type rndEnv struct {
	t        int
	ings     []string
	drugIDs  []string
	doctors  []string
	pharms   []string
	patients []string
}

// genSequence 生成确定性随机的配置与操作序列。
func genSequence(seed int64) (*rndEnv, []rndOp, func(*formulary.Store, *interact.Store, *Engine), func(*sim)) {
	r := rand.New(rand.NewSource(seed))
	env := &rndEnv{
		t:        1 + r.Intn(5),
		ings:     []string{"A", "B", "C", "D", "E"},
		doctors:  []string{"doc0", "doc1", "doc2"},
		pharms:   []string{"ph0", "ph1"},
		patients: []string{"pat0", "pat1"},
	}
	nDrugs := 2 + r.Intn(5)
	type drugSpec struct {
		id, ing string
		mg      int64
		level   int
	}
	var drugs []drugSpec
	for k := 0; k < nDrugs; k++ {
		id := fmt.Sprintf("D%d", k)
		env.drugIDs = append(env.drugIDs, id)
		drugs = append(drugs, drugSpec{id, env.ings[r.Intn(len(env.ings))], int64(1 + r.Intn(6)), 1 + r.Intn(3)})
	}
	type limitSpec struct {
		ing string
		max int64
	}
	var limits []limitSpec
	for _, ing := range env.ings {
		if r.Intn(2) == 0 {
			limits = append(limits, limitSpec{ing, int64(5 + r.Intn(40))})
		}
	}
	type pairSpec struct {
		a, b  string
		grade int
	}
	var pairs []pairSpec
	for i := 0; i < len(env.ings); i++ {
		for j := i + 1; j < len(env.ings); j++ {
			if r.Intn(10) < 3 {
				pairs = append(pairs, pairSpec{env.ings[i], env.ings[j], 1 + r.Intn(3)})
			}
		}
	}
	type allergySpec struct{ patient, ing string }
	var allergies []allergySpec
	for _, p := range env.patients {
		for _, ing := range env.ings {
			if r.Intn(10) < 2 {
				allergies = append(allergies, allergySpec{p, ing})
			}
		}
	}
	docLevels := make([]int, len(env.doctors))
	for i := range docLevels {
		docLevels[i] = 1 + r.Intn(3)
	}

	setupEngine := func(fm *formulary.Store, ix *interact.Store, e *Engine) {
		for _, d := range drugs {
			if err := fm.AddDrug(d.id, d.ing, d.mg, d.level); err != nil {
				panic(err)
			}
		}
		for _, l := range limits {
			if err := fm.SetMax(l.ing, l.max); err != nil {
				panic(err)
			}
		}
		for _, p := range pairs {
			if err := ix.SetPair(p.a, p.b, p.grade); err != nil {
				panic(err)
			}
		}
		for _, a := range allergies {
			if err := ix.SetAllergy(a.patient, a.ing); err != nil {
				panic(err)
			}
		}
		for i, id := range env.doctors {
			if c := e.AddDoctor(id, docLevels[i]); c != OK {
				panic(c)
			}
		}
		for _, id := range env.pharms {
			if c := e.AddPharmacist(id); c != OK {
				panic(c)
			}
		}
		for _, id := range env.patients {
			if c := e.AddPatient(id); c != OK {
				panic(c)
			}
		}
	}
	setupSim := func(s *sim) {
		for _, d := range drugs {
			if err := s.addDrug(d.id, d.ing, d.mg, d.level); err != nil {
				panic(err)
			}
		}
		for _, l := range limits {
			if err := s.setMax(l.ing, l.max); err != nil {
				panic(err)
			}
		}
		for _, p := range pairs {
			if err := s.setPair(p.a, p.b, p.grade); err != nil {
				panic(err)
			}
		}
		for _, a := range allergies {
			if err := s.setAllergy(a.patient, a.ing); err != nil {
				panic(err)
			}
		}
		for i, id := range env.doctors {
			if c := s.addDoctor(id, docLevels[i]); c != OK {
				panic(c)
			}
		}
		for _, id := range env.pharms {
			if c := s.addPharm(id); c != OK {
				panic(c)
			}
		}
		for _, id := range env.patients {
			if c := s.addPatient(id); c != OK {
				panic(c)
			}
		}
	}

	nOps := 15 + r.Intn(15)
	ops := make([]rndOp, 0, nOps)
	cur := 0
	for k := 0; k < nOps; k++ {
		if r.Intn(10) < 8 {
			cur += r.Intn(3)
		} else if cur > 0 {
			cur -= 1 + r.Intn(2) // 制造时钟回退
			if cur < 0 {
				cur = 0
			}
		}
		now := cur
		pick := r.Intn(100)
		switch {
		case pick < 55:
			doctor := env.doctors[r.Intn(len(env.doctors))]
			if r.Intn(20) == 0 {
				doctor = "dx"
			}
			patient := env.patients[r.Intn(len(env.patients))]
			if r.Intn(20) == 0 {
				patient = "px"
			}
			n := 1 + r.Intn(3)
			items := make([]Item, n)
			for i := range items {
				drug := env.drugIDs[r.Intn(len(env.drugIDs))]
				if r.Intn(20) == 0 {
					drug = "DZ"
				}
				if i > 0 && r.Intn(15) == 0 {
					drug = items[0].Drug // 重复药品
				}
				pills, times := 1+r.Intn(3), 1+r.Intn(3)
				s := now + r.Intn(3)
				e := s + 1 + r.Intn(6)
				if r.Intn(25) == 0 {
					pills = 0
				}
				if r.Intn(25) == 0 {
					e = s
				}
				items[i] = it(drug, pills, times, s, e)
			}
			ops = append(ops, rndOp{kind: "submit", now: now, a: doctor, b: patient, items: items})
		case pick < 70:
			who := env.pharms[r.Intn(len(env.pharms))]
			if r.Intn(10) == 0 {
				who = "doc0"
			}
			ops = append(ops, rndOp{kind: "approve", now: now, a: who, rx: r.Intn(8) - 1})
		case pick < 80:
			ops = append(ops, rndOp{kind: "deny", now: now, a: env.pharms[r.Intn(len(env.pharms))], rx: r.Intn(8) - 1})
		case pick < 90:
			who := env.doctors[r.Intn(len(env.doctors))]
			if r.Intn(10) == 0 {
				who = "dx"
			}
			ops = append(ops, rndOp{kind: "stop", now: now, a: who, rx: r.Intn(8) - 1})
		default:
			// 配置操作：首次被接受的 Submit 后应报已冻结
			switch r.Intn(7) {
			case 0:
				mg := int64(1 + r.Intn(10))
				if r.Intn(10) == 0 {
					mg = 0
				}
				ops = append(ops, rndOp{kind: "adddrug", a: fmt.Sprintf("N%d", k), b: env.ings[r.Intn(len(env.ings))], mg: mg, n1: 1 + r.Intn(3)})
			case 1:
				ops = append(ops, rndOp{kind: "adddrug", a: env.drugIDs[0], b: env.ings[0], mg: 10, n1: 1}) // 重复 id
			case 2:
				ops = append(ops, rndOp{kind: "setmax", a: env.ings[r.Intn(len(env.ings))], mg: int64(1 + r.Intn(50))})
			case 3:
				i, j := r.Intn(len(env.ings)), r.Intn(len(env.ings))
				if i == j {
					j = (j + 1) % len(env.ings)
				}
				ops = append(ops, rndOp{kind: "setpair", a: env.ings[i], b: env.ings[j], n1: 1 + r.Intn(3)})
			case 4:
				ops = append(ops, rndOp{kind: "setallergy", a: env.patients[r.Intn(len(env.patients))], b: env.ings[r.Intn(len(env.ings))]})
			case 5:
				ops = append(ops, rndOp{kind: "adddoctor", a: fmt.Sprintf("nd%d", k), n1: 1 + r.Intn(3)})
			default:
				ops = append(ops, rndOp{kind: "addpatient", a: fmt.Sprintf("np%d", k)})
			}
		}
	}
	return env, ops, setupEngine, setupSim
}

// applyBoth 在引擎与朴素模拟上施加同一操作，返回两侧结果。
func applyBoth(e *Engine, fm *formulary.Store, ix *interact.Store, s *sim, op rndOp) (engRes, simRes any) {
	switch op.kind {
	case "submit":
		return e.Submit(op.now, op.a, op.b, op.items), s.submit(op.now, op.a, op.b, op.items)
	case "approve":
		return e.Approve(op.now, op.a, op.rx), s.judge(op.now, op.a, op.rx, StatusActive)
	case "deny":
		return e.Deny(op.now, op.a, op.rx), s.judge(op.now, op.a, op.rx, StatusDenied)
	case "stop":
		return e.Stop(op.now, op.a, op.rx), s.stop(op.now, op.a, op.rx)
	case "adddrug":
		return normErr(fm.AddDrug(op.a, op.b, op.mg, op.n1)), normErr(s.addDrug(op.a, op.b, op.mg, op.n1))
	case "setmax":
		return normErr(fm.SetMax(op.a, op.mg)), normErr(s.setMax(op.a, op.mg))
	case "setpair":
		return normErr(ix.SetPair(op.a, op.b, op.n1)), normErr(s.setPair(op.a, op.b, op.n1))
	case "setallergy":
		return normErr(ix.SetAllergy(op.a, op.b)), normErr(s.setAllergy(op.a, op.b))
	case "adddoctor":
		return e.AddDoctor(op.a, op.n1), s.addDoctor(op.a, op.n1)
	case "addpharm":
		return e.AddPharmacist(op.a), s.addPharm(op.a)
	case "addpatient":
		return e.AddPatient(op.a), s.addPatient(op.a)
	}
	panic("unknown op " + op.kind)
}

func normErr(err error) any {
	if err == nil {
		return nil
	}
	return err.Error()
}

func normSubmit(r SubmitResult) SubmitResult {
	if len(r.Warnings) == 0 {
		r.Warnings = nil
	}
	return r
}

// checkInvariant 校验全局不变量：任一患者任一成分任一天，生效与待审项
// 日剂量之和不超过 maxDay，且无区间重叠的禁忌成分对。
func checkInvariant(t *testing.T, e *Engine, fm *formulary.Store, ix *interact.Store, now int, tag string) {
	t.Helper()
	byPatient := make(map[string][]refItem)
	for _, r := range e.rxs {
		st := e.effStatus(r, now)
		if st != StatusActive && st != StatusPending {
			continue
		}
		for i, item := range r.items {
			if item.S >= item.E {
				continue
			}
			d, _ := fm.Drug(item.Drug)
			byPatient[r.patient] = append(byPatient[r.patient],
				refItem{ing: d.Ing, s: item.S, e: item.E, dose: r.dose[i]})
		}
	}
	for patient, items := range byPatient {
		daySum := make(map[int]map[string]int64)
		for _, item := range items {
			for d := item.s; d < item.e; d++ {
				m := daySum[d]
				if m == nil {
					m = make(map[string]int64)
					daySum[d] = m
				}
				m[item.ing] += item.dose
			}
		}
		for day, m := range daySum {
			for ing, v := range m {
				if lim, ok := fm.MaxDay(ing); ok && v > lim {
					t.Errorf("%s: 不变量违反：患者 %s 成分 %s 第 %d 日合计 %d > 极量 %d",
						tag, patient, ing, day, v, lim)
				}
			}
		}
		for i := 0; i < len(items); i++ {
			for j := i + 1; j < len(items); j++ {
				a, b := items[i], items[j]
				if a.ing != b.ing && overlap(a.s, a.e, b.s, b.e) && ix.Grade(a.ing, b.ing) == 3 {
					t.Errorf("%s: 不变量违反：患者 %s 存在重叠禁忌对 %s-%s", tag, patient, a.ing, b.ing)
				}
			}
		}
	}
}

// TestRandomVsNaive 用 1500 组随机操作序列对照引擎与朴素逐日模拟，
// 并对每组序列做重放一致性校验。
func TestRandomVsNaive(t *testing.T) {
	const sequences = 1500
	for seq := 0; seq < sequences; seq++ {
		env, ops, setupEngine, setupSim := genSequence(int64(seq))

		fm := formulary.NewStore()
		ix := interact.NewStore()
		e, err := NewEngine(fm, ix, env.t)
		if err != nil {
			t.Fatalf("seq %d: NewEngine: %v", seq, err)
		}
		setupEngine(fm, ix, e)
		sm := newSim(env.t)
		setupSim(sm)

		engResults := make([]any, 0, len(ops))
		for k, op := range ops {
			var probesBefore int64
			var nRef int
			if op.kind == "submit" {
				probesBefore = ix.Probes()
				nRef = len(sm.refItems(op.b, op.now))
			}
			engRes, simRes := applyBoth(e, fm, ix, sm, op)
			if op.kind == "submit" {
				engRes = normSubmit(engRes.(SubmitResult))
				simRes = normSubmit(simRes.(SubmitResult))
				// probes 上界：新项数×(新项数+参照项数)
				used := ix.Probes() - probesBefore
				bound := int64(len(op.items) * (len(op.items) + nRef))
				if used > bound {
					t.Fatalf("seq %d op %d: probes=%d 超过上界 %d", seq, k, used, bound)
				}
			}
			engResults = append(engResults, engRes)
			if fmt.Sprintf("%v", engRes) != fmt.Sprintf("%v", simRes) {
				t.Fatalf("seq %d op %d 不一致:\n输入: %+v\n引擎: %+v\n模拟: %+v",
					seq, k, op, engRes, simRes)
			}
			t.Logf("seq=%d op=%d 输入=%+v 输出=%+v 判定依据=%v", seq, k, op, engRes, resultReason(engRes))
			if codeOf(engRes) == OK {
				checkInvariant(t, e, fm, ix, op.now, fmt.Sprintf("seq %d op %d", seq, k))
			}
		}
		// 终态一致性：处方数与各处方生效状态
		if len(e.rxs) != len(sm.rxs) {
			t.Fatalf("seq %d: 处方数 %d != %d", seq, len(e.rxs), len(sm.rxs))
		}
		for i, r := range e.rxs {
			if got, want := e.effStatus(r, e.maxNow), sm.effStatus(sm.rxs[i], sm.maxNow); got != want {
				t.Fatalf("seq %d: 处方 %d 状态 %v != %v", seq, i, got, want)
			}
		}

		// 重放一致性：相同操作序列重放结果相同
		fm2 := formulary.NewStore()
		ix2 := interact.NewStore()
		e2, err := NewEngine(fm2, ix2, env.t)
		if err != nil {
			t.Fatalf("seq %d replay: NewEngine: %v", seq, err)
		}
		setupEngine(fm2, ix2, e2)
		for k, op := range ops {
			engRes, _ := applyBoth(e2, fm2, ix2, newSim(env.t), op)
			if op.kind == "submit" {
				engRes = normSubmit(engRes.(SubmitResult))
			}
			if fmt.Sprintf("%v", engRes) != fmt.Sprintf("%v", engResults[k]) {
				t.Fatalf("seq %d op %d 重放不一致: %+v != %+v", seq, k, engRes, engResults[k])
			}
		}
	}
}

func codeOf(res any) Code {
	switch v := res.(type) {
	case SubmitResult:
		return v.Code
	case Code:
		return v
	}
	return OK
}

func resultReason(res any) string {
	switch v := res.(type) {
	case SubmitResult:
		if v.Code == OK {
			return fmt.Sprintf("接受(%s)", v.Status)
		}
		if v.Index >= 0 {
			return fmt.Sprintf("拒绝(%s, 下标%d)", v.Code, v.Index)
		}
		return fmt.Sprintf("拒绝(%s)", v.Code)
	case Code:
		return v.String()
	}
	return "配置"
}

// TestProbesIndependentOfCatalog 证明相互作用表查询次数与目录规模、
// 其他患者处方数无关：目录 100 与 10000 种两档对照。
func TestProbesIndependentOfCatalog(t *testing.T) {
	var counts []int64
	for _, n := range []int{100, 10000} {
		fm := formulary.NewStore()
		ix := interact.NewStore()
		e, err := NewEngine(fm, ix, 5)
		if err != nil {
			t.Fatalf("NewEngine: %v", err)
		}
		for _, c := range []struct {
			id string
			lv int
		}{{"d", 3}} {
			if code := e.AddDoctor(c.id, c.lv); code != OK {
				t.Fatalf("AddDoctor: %v", code)
			}
		}
		if code := e.AddPharmacist("ph"); code != OK {
			t.Fatalf("AddPharmacist: %v", code)
		}
		for _, p := range []string{"p", "q"} {
			if code := e.AddPatient(p); code != OK {
				t.Fatalf("AddPatient: %v", code)
			}
		}
		pool := make([]string, 30)
		for k := range pool {
			pool[k] = fmt.Sprintf("I%02d", k)
		}
		for k := 0; k < n; k++ {
			if err := fm.AddDrug(fmt.Sprintf("D%d", k), pool[k%30], 10, 1); err != nil {
				t.Fatalf("AddDrug: %v", err)
			}
		}
		// 参照药（成分 GA..GC）与新药（成分 HA..HD）
		for _, ing := range []string{"GA", "GB", "GC"} {
			if err := fm.AddDrug("R"+ing, ing, 10, 1); err != nil {
				t.Fatalf("AddDrug ref: %v", err)
			}
		}
		for _, ing := range []string{"HA", "HB", "HC", "HD"} {
			if err := fm.AddDrug("N"+ing, ing, 10, 1); err != nil {
				t.Fatalf("AddDrug new: %v", err)
			}
		}
		// 其他患者 50 张处方：不应影响 probes
		for k := 0; k < 50; k++ {
			if got := e.Submit(0, "d", "q", []Item{it(fmt.Sprintf("D%d", k), 1, 1, 0, 5)}); got.Code != OK {
				t.Fatalf("seed submit %d: %v", k, got.Code)
			}
		}
		// 本患者 3 个参照项
		ref := e.Submit(0, "d", "p", []Item{it("RGA", 1, 1, 0, 100), it("RGB", 1, 1, 0, 100), it("RGC", 1, 1, 0, 100)})
		if ref.Code != OK {
			t.Fatalf("ref submit: %v", ref.Code)
		}
		ix.ResetProbes()
		got := e.Submit(0, "d", "p", []Item{
			it("NHA", 1, 1, 0, 100), it("NHB", 1, 1, 0, 100),
			it("NHC", 1, 1, 0, 100), it("NHD", 1, 1, 0, 100),
		})
		if got.Code != OK {
			t.Fatalf("probe submit: %v", got.Code)
		}
		// 上界：新项数×(新项数+参照项数) = 4×(4+3) = 28
		if p := ix.Probes(); p > 28 {
			t.Fatalf("目录 %d：probes=%d 超过上界 28", n, p)
		}
		counts = append(counts, ix.Probes())
		t.Logf("目录 %d 种：probes=%d（上界 28）", n, ix.Probes())
	}
	if counts[0] != counts[1] {
		t.Fatalf("probes 随目录规模变化：%v", counts)
	}
}

// TestConcurrentSmoke 并发调用等价于某串行顺序：竞态检测下不出现数据竞争，
// 且最终状态满足全局不变量。
func TestConcurrentSmoke(t *testing.T) {
	fm := formulary.NewStore()
	ix := interact.NewStore()
	e, err := NewEngine(fm, ix, 3)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	for k := 0; k < 6; k++ {
		if err := fm.AddDrug(fmt.Sprintf("D%d", k), string(rune('A'+k%3)), 10, 1); err != nil {
			t.Fatalf("AddDrug: %v", err)
		}
	}
	if err := fm.SetMax("A", 1000); err != nil {
		t.Fatalf("SetMax: %v", err)
	}
	if err := ix.SetPair("A", "B", 2); err != nil {
		t.Fatalf("SetPair: %v", err)
	}
	if c := e.AddDoctor("d", 3); c != OK {
		t.Fatalf("AddDoctor: %v", c)
	}
	if c := e.AddPharmacist("ph"); c != OK {
		t.Fatalf("AddPharmacist: %v", c)
	}
	for _, p := range []string{"p0", "p1"} {
		if c := e.AddPatient(p); c != OK {
			t.Fatalf("AddPatient: %v", c)
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(g)))
			for k := 0; k < 200; k++ {
				now := r.Intn(40)
				switch r.Intn(4) {
				case 0:
					s := now + r.Intn(3)
					e.Submit(now, "d", fmt.Sprintf("p%d", g%2),
						[]Item{it(fmt.Sprintf("D%d", r.Intn(6)), 1, 1, s, s+1+r.Intn(5))})
				case 1:
					e.Approve(now, "ph", r.Intn(50))
				case 2:
					e.Deny(now, "ph", r.Intn(50))
				case 3:
					e.Stop(now, "d", r.Intn(50))
				}
			}
		}(g)
	}
	wg.Wait()
	now := 0
	if e.hasNow {
		now = e.maxNow
	}
	checkInvariant(t, e, fm, ix, now, "concurrent-final")
}

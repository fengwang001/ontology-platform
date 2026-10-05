// Package review 实现处方前置审核：提交、药师复核、停药与用药时间线。
package review

import (
	"fmt"
	"sort"
	"sync"

	"ontology/formulary"
	"ontology/interact"
)

// maxDate 为日期上界（0..10^6 的整数日）。
const maxDate = 1_000_000

// Code 为操作结果码；除 OK 外均为拒绝，拒绝不落地、不推进时钟。
type Code int

const (
	OK            Code = iota
	ErrParam           // 参数非法
	ErrClock           // 时钟回退
	ErrNotFound        // 医生、患者、药品、药师或处方不存在
	ErrPermission      // 无处方权 / 无权限
	ErrAllergy         // 过敏
	ErrContra          // 禁忌
	ErrOverdose        // 超日极量
	ErrState           // 状态不符
	ErrFrozen          // 配置已冻结
)

func (c Code) String() string {
	switch c {
	case OK:
		return "OK"
	case ErrParam:
		return "参数非法"
	case ErrClock:
		return "时钟回退"
	case ErrNotFound:
		return "不存在"
	case ErrPermission:
		return "无权限"
	case ErrAllergy:
		return "过敏"
	case ErrContra:
		return "禁忌"
	case ErrOverdose:
		return "超日极量"
	case ErrState:
		return "状态不符"
	case ErrFrozen:
		return "已冻结"
	}
	return "未知"
}

// Status 为处方状态。
type Status int

const (
	StatusNone    Status = iota // 被拒绝，未生成处方
	StatusActive                // 生效
	StatusPending               // 待审
	StatusDenied                // 药师作废
	StatusExpired               // 到期自动作废
)

func (s Status) String() string {
	switch s {
	case StatusNone:
		return "拒绝"
	case StatusActive:
		return "生效"
	case StatusPending:
		return "待审"
	case StatusDenied:
		return "作废"
	case StatusExpired:
		return "到期作废"
	}
	return "未知"
}

// Item 为处方项：药品、每次片数、每日次数与左闭右开服药区间 [S,E)。
type Item struct {
	Drug  string
	Pills int
	Times int
	S, E  int
}

// Warning 为一对重叠成分的提示，A < B（字节序）。
type Warning struct {
	A, B  string
	Grade int
}

// SubmitResult 为 Submit 的完整判定结果。
type SubmitResult struct {
	Code     Code      // 判定码
	Index    int       // 出错项下标（无处方权/过敏/禁忌），否则 -1
	RxID     int       // 接受时的处方号，拒绝为 -1
	Status   Status    // 接受时为生效或待审
	Warnings []Warning // 全部等级 1/2 重叠成分对
}

type rx struct {
	id        int
	doctor    string
	patient   string
	items     []Item
	dose      []int64 // 每项日剂量
	submitDay int
	status    Status
}

// Engine 为审核引擎；全部操作由单互斥锁串行化，等价于某串行顺序。
type Engine struct {
	mu       sync.Mutex
	fm       *formulary.Store
	ix       *interact.Store
	t        int // 待审有效期 T（天）
	maxNow   int
	hasNow   bool
	frozen   bool
	doctors  map[string]int
	pharms   map[string]bool
	patients map[string]bool
	rxs      []*rx
}

// NewEngine 创建引擎，T 为待审有效期（1..100 天）。
func NewEngine(fm *formulary.Store, ix *interact.Store, t int) (*Engine, error) {
	if fm == nil || ix == nil || t < 1 || t > 100 {
		return nil, fmt.Errorf("review: 参数非法")
	}
	return &Engine{
		fm:       fm,
		ix:       ix,
		t:        t,
		doctors:  make(map[string]int),
		pharms:   make(map[string]bool),
		patients: make(map[string]bool),
	}, nil
}

// AddDoctor 注册医生（等级 1..3）。
func (e *Engine) AddDoctor(id string, level int) Code {
	if id == "" || level < 1 || level > 3 {
		return ErrParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.frozen {
		return ErrFrozen
	}
	e.doctors[id] = level
	return OK
}

// AddPharmacist 注册药师（与医生为另一类身份）。
func (e *Engine) AddPharmacist(id string) Code {
	if id == "" {
		return ErrParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.frozen {
		return ErrFrozen
	}
	e.pharms[id] = true
	return OK
}

// AddPatient 注册患者。
func (e *Engine) AddPatient(id string) Code {
	if id == "" {
		return ErrParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.frozen {
		return ErrFrozen
	}
	e.patients[id] = true
	return OK
}

func validNow(now int) bool { return now >= 0 && now <= maxDate }

func reject(code Code) SubmitResult {
	return SubmitResult{Code: code, Index: -1, RxID: -1, Status: StatusNone}
}

func rejectAt(code Code, index int) SubmitResult {
	return SubmitResult{Code: code, Index: index, RxID: -1, Status: StatusNone}
}

// Submit 提交处方，全有或全无；检查次序见 DESIGN.md。
func (e *Engine) Submit(now int, doctor, patient string, items []Item) SubmitResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 1. 参数非法
	if !validNow(now) || len(items) < 1 || len(items) > 20 {
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
	// 2. 时钟回退
	if e.hasNow && now < e.maxNow {
		return reject(ErrClock)
	}
	// 3. 医生、患者或药品不存在
	docLevel, ok := e.doctors[doctor]
	if !ok || !e.patients[patient] {
		return reject(ErrNotFound)
	}
	drugs := make([]formulary.Drug, len(items))
	doses := make([]int64, len(items))
	for i, it := range items {
		d, ok := e.fm.Drug(it.Drug)
		if !ok {
			return reject(ErrNotFound)
		}
		drugs[i] = d
		doses[i] = d.Mg * int64(it.Pills) * int64(it.Times)
	}
	// 4. 无处方权（报下标最小项）
	for i, d := range drugs {
		if d.Level > docLevel {
			return rejectAt(ErrPermission, i)
		}
	}
	// 5. 过敏（报下标最小项）
	for i, d := range drugs {
		if e.ix.Allergic(patient, d.Ing) {
			return rejectAt(ErrAllergy, i)
		}
	}

	refs := e.refItems(patient, now)

	// 相互作用：仅对成分不同且区间重叠的（新,新）与（新,参照）对查表，
	// 成分对去重缓存，probes <= 新项数×(新项数+参照项数)。
	type pair struct{ a, b string }
	cache := make(map[pair]int)
	gradeOf := func(x, y string) int {
		if x > y {
			x, y = y, x
		}
		k := pair{x, y}
		if g, hit := cache[k]; hit {
			return g
		}
		g := e.ix.Grade(x, y)
		cache[k] = g
		return g
	}
	contra := make([]bool, len(items))
	warnSet := make(map[pair]int)
	checkPair := func(i int, ingB string, sB, eB int) {
		ingA := drugs[i].Ing
		if ingA == ingB || !overlap(items[i].S, items[i].E, sB, eB) {
			return
		}
		g := gradeOf(ingA, ingB)
		if g == 0 {
			return
		}
		if g == 3 {
			contra[i] = true
			return
		}
		a, b := ingA, ingB
		if a > b {
			a, b = b, a
		}
		warnSet[pair{a, b}] = g
	}
	for i := range items {
		for j := range items {
			if j != i {
				checkPair(i, drugs[j].Ing, items[j].S, items[j].E)
			}
		}
		for _, r := range refs {
			checkPair(i, r.ing, r.s, r.e)
		}
	}
	// 6. 禁忌（报下标最小的新项）
	for i := range items {
		if contra[i] {
			return rejectAt(ErrContra, i)
		}
	}
	// 7. 超日极量（逐日合计，严格大于才拒绝）
	if overdose(e.fm, drugs, doses, items, refs) {
		return reject(ErrOverdose)
	}

	// 接受：先落地到期作废，再冻结配置、推进时钟、登记处方。
	e.expire(now)
	e.freeze()
	e.maxNow, e.hasNow = now, true

	warnings := make([]Warning, 0, len(warnSet))
	pending := false
	for p, g := range warnSet {
		if g == 2 {
			pending = true
		}
		warnings = append(warnings, Warning{A: p.a, B: p.b, Grade: g})
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
	id := len(e.rxs)
	cp := make([]Item, len(items))
	copy(cp, items)
	e.rxs = append(e.rxs, &rx{
		id: id, doctor: doctor, patient: patient,
		items: cp, dose: doses, submitDay: now, status: status,
	})
	return SubmitResult{Code: OK, Index: -1, RxID: id, Status: status, Warnings: warnings}
}

func overlap(s1, e1, s2, e2 int) bool { return s1 < e2 && s2 < e1 }

// refItem 为参照项：本患者生效与待审（未到期）处方中区间非空的项。
type refItem struct {
	ing  string
	s, e int
	dose int64
}

func (e *Engine) refItems(patient string, now int) []refItem {
	var refs []refItem
	for _, r := range e.rxs {
		if r.patient != patient {
			continue
		}
		st := e.effStatus(r, now)
		if st != StatusActive && st != StatusPending {
			continue
		}
		for i, it := range r.items {
			if it.S >= it.E {
				continue // Stop 截断为空的项不再占额
			}
			d, _ := e.fm.Drug(it.Drug)
			refs = append(refs, refItem{ing: d.Ing, s: it.S, e: it.E, dose: r.dose[i]})
		}
	}
	return refs
}

// overdose 判定：存在某天某成分，新项+参照项日剂量之和严格大于 maxDay。
func overdose(fm *formulary.Store, drugs []formulary.Drug, doses []int64, items []Item, refs []refItem) bool {
	checked := make(map[string]bool)
	for _, d := range drugs {
		ing := d.Ing
		if checked[ing] {
			continue
		}
		checked[ing] = true
		limit, ok := fm.MaxDay(ing)
		if !ok {
			continue // 未设视为无上限
		}
		delta := make(map[int]int64)
		for j, it := range items {
			if drugs[j].Ing == ing {
				delta[it.S] += doses[j]
				delta[it.E] -= doses[j]
			}
		}
		for _, r := range refs {
			if r.ing == ing {
				delta[r.s] += r.dose
				delta[r.e] -= r.dose
			}
		}
		days := make([]int, 0, len(delta))
		for day := range delta {
			days = append(days, day)
		}
		sort.Ints(days)
		var sum int64
		for _, day := range days {
			sum += delta[day]
			if sum > limit {
				return true
			}
		}
	}
	return false
}

// effStatus 计算处方在时刻 now 的生效状态（待审到期视为已作废）。
func (e *Engine) effStatus(r *rx, now int) Status {
	if r.status == StatusPending && now >= r.submitDay+e.t {
		return StatusExpired
	}
	return r.status
}

// expire 在接受的操作开头按（到期日，处方号）升序落地到期作废。
func (e *Engine) expire(now int) {
	var due []*rx
	for _, r := range e.rxs {
		if r.status == StatusPending && now >= r.submitDay+e.t {
			due = append(due, r)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].submitDay != due[j].submitDay {
			return due[i].submitDay < due[j].submitDay
		}
		return due[i].id < due[j].id
	})
	for _, r := range due {
		r.status = StatusExpired
	}
}

// freeze 在首次被接受的 Submit 时冻结全部配置。
func (e *Engine) freeze() {
	if e.frozen {
		return
	}
	e.frozen = true
	e.fm.Freeze()
	e.ix.Freeze()
}

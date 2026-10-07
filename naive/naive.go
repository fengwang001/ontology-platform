// Package naive 是接触者追踪系统的独立朴素参考实现。
//
// 它不使用任何索引、缓存或增量失效结构：保存全部登记，
// 每次查询都从头扫描全部住宿与病例记录，按定义直接计算。
// 它用于与主引擎做逐操作差分对照，以证明主引擎的增量优化
// 不改变语义；两者共享 ontology 包中的结果类型与错误类别，
// 但推导与存储代码完全独立。
package naive

import (
	"sync"

	"ontology"
)

type stayRec struct {
	patient, ward string
	in, out       int64
	open          bool
}

// end 返回住宿区间在 now 下的结束时刻（开区间端点）。
func (s *stayRec) end(now int64) int64 {
	if s.open {
		return now
	}
	return s.out
}

type caseRec struct {
	id, patient  string
	onset        int64
	registeredAt int64
	isolated     bool
	isolatedAt   int64
	revoked      bool
}

func (c *caseRec) infectiousStart() int64 {
	if c.onset < ontology.InfectiousLeadMinutes {
		return 0
	}
	return c.onset - ontology.InfectiousLeadMinutes
}

// Engine 朴素引擎：仅保存全部登记，查询时暴力重算。
type Engine struct {
	mu    sync.Mutex
	clock int64
	stays []*stayRec
	cases []*caseRec
	byID  map[string]*caseRec
}

func NewEngine() *Engine {
	return &Engine{byID: make(map[string]*caseRec)}
}

func validTime(t int64) bool { return t >= 0 && t <= ontology.MaxTimeMinutes }

func errOf(op string, kind ontology.Kind, msg string) error {
	return &ontology.Error{Kind: kind, Op: op, Msg: msg}
}

func (e *Engine) checkClock(op string, now int64) error {
	if now < e.clock {
		return errOf(op, ontology.KindClockRollback, "now 小于上一次被接受操作的 now")
	}
	return nil
}

// acceptClock 在操作被接受时推进时钟；被拒绝的操作不改变时钟。
func (e *Engine) acceptClock(now int64) {
	e.clock = now
}

func (e *Engine) openStayOf(patient string) *stayRec {
	for _, s := range e.stays {
		if s.patient == patient && s.open {
			return s
		}
	}
	return nil
}

func (e *Engine) hasStayOf(patient string) bool {
	for _, s := range e.stays {
		if s.patient == patient {
			return true
		}
	}
	return false
}

// overlaps 患者既有区间是否与 [in, out) 重叠；开区间结束时刻按 +∞ 处理。
func (e *Engine) overlaps(patient string, in, out int64) bool {
	const inf = int64(1) << 62
	for _, s := range e.stays {
		if s.patient != patient {
			continue
		}
		se := s.out
		if s.open {
			se = inf
		}
		if s.in < out && in < se {
			return true
		}
	}
	return false
}

// Admit 登记入住。
func (e *Engine) Admit(patient, ward string, at, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "Admit"
	if patient == "" || ward == "" {
		return errOf(op, ontology.KindInvalidParam, "标识不能为空字符串")
	}
	if !validTime(at) || !validTime(now) || at > now {
		return errOf(op, ontology.KindInvalidParam, "时刻越界或晚于 now")
	}
	if err := e.checkClock(op, now); err != nil {
		return err
	}
	if e.openStayOf(patient) != nil {
		return errOf(op, ontology.KindStateConflict, "该患者存在未出住的住宿记录")
	}
	if e.overlaps(patient, at, int64(1)<<62) {
		return errOf(op, ontology.KindStayConflict, "与既有住宿区间重叠")
	}
	e.stays = append(e.stays, &stayRec{patient: patient, ward: ward, in: at, open: true})
	e.acceptClock(now)
	return nil
}

// Discharge 登记出住。
func (e *Engine) Discharge(patient string, at, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "Discharge"
	if patient == "" {
		return errOf(op, ontology.KindInvalidParam, "标识不能为空字符串")
	}
	if !validTime(at) || !validTime(now) || at > now {
		return errOf(op, ontology.KindInvalidParam, "时刻越界或晚于 now")
	}
	if err := e.checkClock(op, now); err != nil {
		return err
	}
	if !e.hasStayOf(patient) {
		return errOf(op, ontology.KindNotFound, "该患者没有任何住宿记录")
	}
	open := e.openStayOf(patient)
	if open == nil {
		return errOf(op, ontology.KindStateConflict, "该患者没有未出住的住宿记录")
	}
	if at <= open.in {
		return errOf(op, ontology.KindInvalidParam, "出住时刻须晚于入住时刻")
	}
	open.open = false
	open.out = at
	e.acceptClock(now)
	return nil
}

// BackfillStay 追补一段已结束的住宿。
func (e *Engine) BackfillStay(patient, ward string, in, out, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "BackfillStay"
	if patient == "" || ward == "" {
		return errOf(op, ontology.KindInvalidParam, "标识不能为空字符串")
	}
	if !validTime(in) || !validTime(out) || !validTime(now) || out <= in || out > now {
		return errOf(op, ontology.KindInvalidParam, "时刻越界、出住不晚于入住或晚于 now")
	}
	if err := e.checkClock(op, now); err != nil {
		return err
	}
	if e.overlaps(patient, in, out) {
		return errOf(op, ontology.KindStayConflict, "与既有住宿区间重叠")
	}
	e.stays = append(e.stays, &stayRec{patient: patient, ward: ward, in: in, out: out})
	e.acceptClock(now)
	return nil
}

// RegisterCase 登记病例。
func (e *Engine) RegisterCase(caseID, patient string, onset, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "RegisterCase"
	if caseID == "" || patient == "" {
		return errOf(op, ontology.KindInvalidParam, "标识不能为空字符串")
	}
	if !validTime(onset) || !validTime(now) || onset > now {
		return errOf(op, ontology.KindInvalidParam, "发病时刻越界或晚于 now")
	}
	if err := e.checkClock(op, now); err != nil {
		return err
	}
	if e.byID[caseID] != nil {
		return errOf(op, ontology.KindStateConflict, "病例 ID 已存在")
	}
	c := &caseRec{id: caseID, patient: patient, onset: onset, registeredAt: now}
	e.cases = append(e.cases, c)
	e.byID[caseID] = c
	e.acceptClock(now)
	return nil
}

// RegisterIsolation 登记病例隔离时刻。
func (e *Engine) RegisterIsolation(caseID string, at, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "RegisterIsolation"
	if caseID == "" {
		return errOf(op, ontology.KindInvalidParam, "标识不能为空字符串")
	}
	if !validTime(at) || !validTime(now) || at > now {
		return errOf(op, ontology.KindInvalidParam, "时刻越界或晚于 now")
	}
	if err := e.checkClock(op, now); err != nil {
		return err
	}
	c := e.byID[caseID]
	if c == nil {
		return errOf(op, ontology.KindNotFound, "病例不存在")
	}
	if at < c.infectiousStart() {
		return errOf(op, ontology.KindInvalidParam, "隔离时刻早于传染期起点")
	}
	if c.revoked {
		return errOf(op, ontology.KindStateConflict, "病例已撤销")
	}
	if c.isolated {
		return errOf(op, ontology.KindStateConflict, "该病例已登记过隔离")
	}
	c.isolated = true
	c.isolatedAt = at
	e.acceptClock(now)
	return nil
}

// CorrectOnset 改正发病时刻。
func (e *Engine) CorrectOnset(caseID string, onset, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "CorrectOnset"
	if caseID == "" {
		return errOf(op, ontology.KindInvalidParam, "标识不能为空字符串")
	}
	if !validTime(onset) || !validTime(now) || onset > now {
		return errOf(op, ontology.KindInvalidParam, "发病时刻越界或晚于 now")
	}
	if err := e.checkClock(op, now); err != nil {
		return err
	}
	c := e.byID[caseID]
	if c == nil {
		return errOf(op, ontology.KindNotFound, "病例不存在")
	}
	if c.revoked {
		return errOf(op, ontology.KindStateConflict, "病例已撤销")
	}
	c.onset = onset
	e.acceptClock(now)
	return nil
}

// RevokeCase 撤销病例。
func (e *Engine) RevokeCase(caseID string, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "RevokeCase"
	if caseID == "" {
		return errOf(op, ontology.KindInvalidParam, "标识不能为空字符串")
	}
	if !validTime(now) {
		return errOf(op, ontology.KindInvalidParam, "now 越界")
	}
	if err := e.checkClock(op, now); err != nil {
		return err
	}
	c := e.byID[caseID]
	if c == nil {
		return errOf(op, ontology.KindNotFound, "病例不存在")
	}
	if c.revoked {
		return errOf(op, ontology.KindStateConflict, "病例已撤销")
	}
	c.revoked = true
	e.acceptClock(now)
	return nil
}

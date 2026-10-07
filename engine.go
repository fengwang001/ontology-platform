package ontology

import "sync"

// Engine 住院病区感染病例接触者追踪与隔离期判定引擎。
// 所有公开方法可并发调用，内部以单互斥锁串行化，
// 结果等价于按实际获得锁的串行顺序执行。
type Engine struct {
	mu      sync.Mutex
	clock   int64 // 上一次被接受操作的 now
	stays   *stayStore
	cases   *caseStore
	closeOf map[string]map[string]bool // 患者 -> 其为密切接触者的病例集合（失效提示）
	stats   OpStats
}

func NewEngine() *Engine {
	return &Engine{
		stays:   newStayStore(),
		cases:   newCaseStore(),
		closeOf: make(map[string]map[string]bool),
	}
}

// LastOpStats 返回上一次被处理操作（无论接受或拒绝）的开销统计。
func (e *Engine) LastOpStats() OpStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.stats
	s.InvalidatedIDs = append([]string(nil), s.InvalidatedIDs...)
	return s
}

// Clock 返回当前时钟（上一次被接受操作的 now）。
func (e *Engine) Clock() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clock
}

func (e *Engine) resetStats() {
	e.stats = OpStats{}
}

// --- 住宿记录 ---

// Admit 登记入住：患者在 at 时刻入住病房，出住时刻未登记。
func (e *Engine) Admit(patient, ward string, at, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resetStats()
	return e.admit(patient, ward, at, now)
}

// Discharge 登记出住：关闭患者当前未出住的住宿记录。
func (e *Engine) Discharge(patient string, at, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resetStats()
	return e.discharge(patient, at, now)
}

// BackfillStay 一次性追补一段已结束的住宿 [in, out)。
func (e *Engine) BackfillStay(patient, ward string, in, out, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resetStats()
	return e.backfill(patient, ward, in, out, now)
}

// --- 病例 ---

// RegisterCase 登记病例：患者、发病时刻；登记时刻 now 即确诊登记时刻。
func (e *Engine) RegisterCase(caseID, patient string, onset, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resetStats()
	return e.registerCase(caseID, patient, onset, now)
}

// RegisterIsolation 登记病例被隔离的时刻，每个病例只可登记一次。
func (e *Engine) RegisterIsolation(caseID string, at, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resetStats()
	return e.registerIsolation(caseID, at, now)
}

// CorrectOnset 改正发病时刻，传染期随之改变。
func (e *Engine) CorrectOnset(caseID string, onset, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resetStats()
	return e.correctOnset(caseID, onset, now)
}

// RevokeCase 撤销病例：撤销后不再产生任何接触者。
func (e *Engine) RevokeCase(caseID string, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resetStats()
	return e.revokeCase(caseID, now)
}

// --- 查询 ---

// Status 查询患者在 now 的隔离状态。
func (e *Engine) Status(patient string, now int64) (StatusResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resetStats()
	return e.status(patient, now)
}

// CaseContacts 查询某病例当前的密切接触者与次密接清单。
func (e *Engine) CaseContacts(caseID string, now int64) (ContactsResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resetStats()
	return e.caseContacts(caseID, now)
}

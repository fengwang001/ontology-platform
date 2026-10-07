package lab

import "sync"

// System 是标本采集、送检、签收与拒收系统的门面。
// 所有公开操作在内部互斥锁下串行执行，因此并发调用的结果
// 等价于某个串行顺序（可线性化）；相同操作序列重放结果完全相同。
type System struct {
	mu       sync.Mutex
	clock    Clock
	catalog  *Catalog
	patients map[string]*Patient
	tubes    map[string]*Tube
	appIDs   map[string]struct{}
	Metrics  Metrics
}

// NewSystem 创建一个空系统。
func NewSystem() *System {
	return &System{
		catalog:  newCatalog(),
		patients: make(map[string]*Patient),
		tubes:    make(map[string]*Tube),
		appIDs:   make(map[string]struct{}),
	}
}

// RegisterItem 登记或变更一个检验项目的目录信息。
// 变更只对变更之后提交的申请生效。
func (s *System) RegisterItem(now int64, itemID, tubeType string, maxDeliverySec int64, requireCold bool, maxHemolysis int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.registerItem(now, itemID, tubeType, maxDeliverySec, requireCold, maxHemolysis)
}

// SubmitApplication 提交一次申请：一名患者、1..10 个不同项目、一个优先级。
// 同一患者同一项目已有未终结申请项时整体拒绝并报重复申请。
func (s *System) SubmitApplication(now int64, appID, patientID string, itemIDs []string, priority int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.submitApplication(now, appID, patientID, itemIDs, priority)
}

// Collect 采集登记一支标本管：管内项目须属同一患者、管类别与登记一致、
// 都处于待采集；collectTime 不得晚于 now，也不得早于项目最近一次进入待采集的时刻。
func (s *System) Collect(now int64, tubeID, tubeType, patientID string, itemIDs []string, collectTime int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.collect(now, tubeID, tubeType, patientID, itemIDs, collectTime)
}

// RegisterTransport 登记标本管此次运送方式（cold=true 为冷藏）。
// 只能在签收前登记，可重复登记，以最后一次为准。
func (s *System) RegisterTransport(now int64, tubeID string, cold bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.registerTransport(now, tubeID, cold)
}

// Sign 签收标本管，按项目逐个独立判定，返回每个项目的判定结果（按项目标识排序）。
func (s *System) Sign(now int64, tubeID string, hemolysis int) ([]ItemVerdict, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sign(now, tubeID, hemolysis)
}

// Cancel 取消一个待采集或已采集待签收的项目。
func (s *System) Cancel(now int64, patientID, itemID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancel(now, patientID, itemID)
}

// QueryPatient 查询患者全部未终结项目（按项目标识排序）。
func (s *System) QueryPatient(now int64, patientID string) ([]ItemView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queryPatient(now, patientID)
}

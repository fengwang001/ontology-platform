package toollife

import "sync"

// reqState 是申请的生命周期状态。
type reqState int

const (
	reqOpen    reqState = iota // 已预占，待记账/中止
	reqSettled                 // 已记账
	reqAborted                 // 已中止
)

// requestRecord 记录申请的内容与归属，保证幂等与冲突检测。
type requestRecord struct {
	id        string
	groupID   string
	estimated uint64
	tool      *Tool
	state     reqState
}

// Service 是刀具寿命管理服务的对外门面，持有全部可变状态。
type Service struct {
	mag *Magazine

	mu    sync.Mutex
	reqs  map[string]*requestRecord // 全局申请编号台账
	wares []WarningEvent            // 已发出的预警
}

// NewService 创建服务。
func NewService() *Service {
	return &Service{
		mag:  NewMagazine(),
		reqs: map[string]*requestRecord{},
	}
}

// Magazine 暴露存储层用于配置刀库。
func (s *Service) Magazine() *Magazine { return s.mag }

// lockGroup 持有 s.mu 后再加存储层锁并定位刀组：固定加锁顺序 service -> magazine。
func (s *Service) lockGroup(groupID string) (*Group, error) {
	s.mag.mu.Lock()
	g, err := s.mag.lookupGroup(groupID)
	if err != nil {
		s.mag.mu.Unlock()
		return nil, err
	}
	return g, nil
}

// unlockGroup 释放存储层锁（s.mu 持有至操作结束）。
func (s *Service) unlockGroup() { s.mag.mu.Unlock() }

// findRequest 在全局台账中按编号定位申请；map 命中开销与历史长度无关。
func (s *Service) findRequest(reqID string) (*requestRecord, error) {
	r, ok := s.reqs[reqID]
	if !ok {
		return nil, errf(ErrRequestNotFound, "request %q not found", reqID)
	}
	return r, nil
}

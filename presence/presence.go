// Package presence 实现即时通讯的多设备在线状态聚合与订阅通知服务。
package presence

import "sync"

// Status 为设备或聚合后的可见状态。
type Status int

const (
	StatusOffline Status = iota
	StatusOnline
	StatusBusy
	StatusAway
)

func (s Status) String() string {
	switch s {
	case StatusOnline:
		return "online"
	case StatusBusy:
		return "busy"
	case StatusAway:
		return "away"
	default:
		return "offline"
	}
}

// DeviceStatus 为 Query 本人时返回的单台设备视图。
type DeviceStatus struct {
	Device string
	Status Status
}

// Notification 是观察者 Drain 得到的一条可见状态变更通知。
type Notification struct {
	Target      string
	Status      Status
	EffectiveAt int64
}

// QueryResult 为 Query 的返回。
type QueryResult struct {
	Status      Status
	ActiveCount int
	Devices     []DeviceStatus // 仅本人查询时非 nil，按设备标识排序
}

// DrainResult 为 Drain 的返回。
type DrainResult struct {
	Notifications []Notification
	Dropped       int // 自上一次 Drain 以来因队列溢出丢弃的最早通知条数
}

// Service 是状态聚合与订阅通知服务。
type Service struct {
	mu   sync.Mutex
	impl *svc
}

// New 创建服务，leaseSeconds 为固定租约时长（1..3600 秒）。
func New(leaseSeconds int64) (*Service, error) {
	impl, err := newService(leaseSeconds)
	if err != nil {
		return nil, err
	}
	return &Service{impl: impl}, nil
}

func (s *Service) Report(user, device string, status Status, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.impl.Report(user, device, status, now)
}

func (s *Service) Offline(user, device string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.impl.Offline(user, device, now)
}

func (s *Service) SetInvisible(user string, on bool, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.impl.SetInvisible(user, on, now)
}

func (s *Service) Block(owner, who string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.impl.Block(owner, who, now)
}

func (s *Service) Unblock(owner, who string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.impl.Unblock(owner, who, now)
}

func (s *Service) Subscribe(viewer, target string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.impl.Subscribe(viewer, target, now)
}

func (s *Service) Query(viewer, target string, now int64) (QueryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.impl.Query(viewer, target, now)
}

func (s *Service) Drain(viewer string, now int64) (DrainResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.impl.Drain(viewer, now)
}

// LeaseSeconds 返回固定租约时长。
func (s *Service) LeaseSeconds() int64 { return s.impl.t }

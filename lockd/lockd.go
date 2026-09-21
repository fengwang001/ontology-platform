// Package lockd 是对外网关：管理多个资源的租约，注入时钟，暴露写入校验。
//
// 每个资源拥有独立的租约状态机与围栏，令牌跨资源互不回退约束。
// 所有时间判定只走构造时注入的 now。
package lockd

import (
	"sync"
	"time"

	"ontology/fence"
	"ontology/lease"
)

// 重新导出哨兵错误，调用方只需依赖 lockd 即可判定。
var (
	ErrHeld       = lease.ErrHeld
	ErrNotHolder  = lease.ErrNotHolder
	ErrNotHeld    = lease.ErrNotHeld
	ErrStaleToken = fence.ErrStaleToken
)

// Info 是某资源租约状态的只读快照。
type Info = lease.Info

// Service 管理多个资源的租约与写入围栏，并发安全。
type Service struct {
	now func() time.Time

	mu        sync.Mutex
	resources map[string]*lease.Lease
}

// New 以注入时钟 now 创建服务。
func New(now func() time.Time) *Service {
	return &Service{now: now, resources: make(map[string]*lease.Lease)}
}

// resource 返回（必要时创建）某资源的租约状态机。
func (s *Service) resource(name string) *lease.Lease {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.resources[name]
	if !ok {
		l = lease.New(s.now, fence.New())
		s.resources[name] = l
	}
	return l
}

// Acquire 尝试获取资源的独占租约，成功时返回新的围栏令牌。
func (s *Service) Acquire(resource, holder string, ttl time.Duration) (fence.Token, error) {
	return s.resource(resource).Acquire(holder, ttl)
}

// Renew 由持有者续期，到期时间重置为 now+ttl，令牌不变。
func (s *Service) Renew(resource, holder string, ttl time.Duration) (fence.Token, error) {
	return s.resource(resource).Renew(holder, ttl)
}

// Release 由持有者主动释放租约。
func (s *Service) Release(resource, holder string) error {
	return s.resource(resource).Release(holder)
}

// Write 校验一次携带围栏令牌的写入。
// 资源当前无人持有时返回 ErrNotHeld；令牌小于已见最大值时返回 ErrStaleToken。
func (s *Service) Write(resource string, token fence.Token, data []byte) error {
	return s.resource(resource).CheckWrite(token)
}

// Info 返回某资源当前租约状态的只读快照。
func (s *Service) Info(resource string) Info {
	return s.resource(resource).Info()
}

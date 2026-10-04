// Package identity 维护设备与用户的绑定关系及登录时的账目合并。
package identity

import (
	"errors"

	"ontology/meter"
)

var (
	// ErrAlreadyBound 表示设备已绑定某个用户。
	ErrAlreadyBound = errors.New("identity: device already bound")
	// ErrNotBound 表示设备当前未绑定任何用户。
	ErrNotBound = errors.New("identity: device not bound")
)

// Service 管理设备到用户的绑定。
type Service struct {
	mt    *meter.Meter
	bound map[string]string // 设备键 -> 用户（字节串不可变拷贝）
}

// New 创建身份服务。
func New(mt *meter.Meter) *Service {
	return &Service{mt: mt, bound: make(map[string]string)}
}

// Login 将设备绑定到用户并合并双方本月账。设备已绑定时返回 ErrAlreadyBound。
// 参数合法性与时钟回退由 paywall 门面在调用前保证。
func (s *Service) Login(now int64, device, user []byte) error {
	dk := string(device)
	if _, ok := s.bound[dk]; ok {
		return ErrAlreadyBound
	}
	s.mt.MergeInto(user, device, now)
	s.bound[dk] = string(user)
	return nil
}

// Logout 解除设备绑定。
func (s *Service) Logout(device []byte) error {
	delete(s.bound, string(device))
	return nil
}

// IsBound 报告设备是否已绑定某用户。
func (s *Service) IsBound(device []byte) bool {
	_, ok := s.bound[string(device)]
	return ok
}

// Principal 返回设备当前计量主体：绑定则为用户，否则为设备自身。
// 第二个返回值表示设备是否已绑定用户。
func (s *Service) Principal(device []byte) ([]byte, bool) {
	if u, ok := s.bound[string(device)]; ok {
		return []byte(u), true
	}
	return device, false
}

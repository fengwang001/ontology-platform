package riderassess

import "sync"

// System 是骑手考核与申诉系统的并发安全入口。
type System struct {
	mu sync.Mutex
}

func NewSystem(cfg Config) (*System, error) { return nil, nil }

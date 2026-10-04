// Package matrix 维护产品族之间序列相关的不对称换型时长。
package matrix

import "errors"

const maxMinutes = 100_000

var (
	// ErrInvalidArgument 表示参数取值或字节串长度非法。
	ErrInvalidArgument = errors.New("matrix: invalid argument")
	// ErrInvalidState 表示计划序列非空时仍尝试修改换型矩阵。
	ErrInvalidState = errors.New("matrix: invalid state")
)

// Gate 由计划层实现，供矩阵配置前检查序列是否为空。
type Gate interface{ Empty() bool }

// Matrix 是不对称的产品族换型矩阵，未设定的组合时长视为 0。
type Matrix struct {
	gate  Gate
	times map[[2]string]int64
}

// New 创建换型矩阵，gate 为关联计划的空序列检查入口。
func New(gate Gate) *Matrix { return &Matrix{gate: gate, times: map[[2]string]int64{}} }

// SetChangeover 设定 a→b 的换型分钟数，仅允许在计划为空时调用。
func (m *Matrix) SetChangeover(a, b string, minutes int64) error {
	if !validFamily(a) || !validFamily(b) || minutes < 0 || minutes > maxMinutes {
		return ErrInvalidArgument
	}
	if a == b && minutes != 0 {
		return ErrInvalidArgument
	}
	if m.gate != nil && !m.gate.Empty() {
		return ErrInvalidState
	}
	key := [2]string{a, b}
	if minutes == 0 {
		delete(m.times, key)
	} else {
		m.times[key] = minutes
	}
	return nil
}

// Get 返回 a→b 的换型时长，未设定时为 0。
func (m *Matrix) Get(a, b string) int64 {
	if a == b {
		return 0
	}
	return m.times[[2]string{a, b}]
}

// Empty 判断族名是否为 1..32 字节的非空字节串。
func validFamily(f string) bool { return len(f) >= 1 && len(f) <= 32 }

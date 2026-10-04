// Package matrix 维护产品族之间不对称的序列相关换型时长。
package matrix

import "errors"

var (
	// ErrArgument 表示参数非法（族名/时长越界、同族换型非 0）。
	ErrArgument = errors.New("matrix: invalid argument")
	// ErrState 表示状态不符（如计划非空时设定换型）。
	ErrState = errors.New("matrix: invalid state")
)

const (
	maxFamilyLen = 32
	maxMinutes   = 100_000
)

// Matrix 是产品族换型矩阵，时长以分钟计。
type Matrix struct {
	d map[string]map[string]int
}

// New 创建空换型矩阵。
func New() *Matrix {
	return &Matrix{}
}

// SetChangeover 设定从族 a 换到族 b 的换型时长。
// a 与 b 相同时时长必须为 0；未设定的组合视为 0。
func (m *Matrix) SetChangeover(a, b string, minutes int) error {
	if !ValidChangeover(a, b, minutes) {
		return ErrArgument
	}
	if a == b && minutes != 0 {
		return ErrArgument
	}
	if m.d == nil {
		m.d = make(map[string]map[string]int)
	}
	row, ok := m.d[a]
	if !ok {
		row = make(map[string]int)
		m.d[a] = row
	}
	if minutes == 0 {
		delete(row, b)
	} else {
		row[b] = minutes
	}
	return nil
}

// Changeover 返回从族 a 换到族 b 的换型时长，未设定为 0。
func (m *Matrix) Changeover(a, b string) int {
	if m.d == nil {
		return 0
	}
	return m.d[a][b]
}

func validFamily(f string) bool {
	n := len(f)
	return n >= 1 && n <= maxFamilyLen
}

// ValidChangeover 报告换型设定参数是否合法。
func ValidChangeover(a, b string, minutes int) bool {
	if !validFamily(a) || !validFamily(b) || minutes < 0 || minutes > maxMinutes {
		return false
	}
	if a == b && minutes != 0 {
		return false
	}
	return true
}

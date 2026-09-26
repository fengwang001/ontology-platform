// Package api 是对外接口：进程内存中维护一份半平面交区域。
package api

import (
	"sync"

	"ontology/hp"
	"ontology/hpi"
)

// Point 是区域顶点，坐标为精确有理数。
type Point = hp.Point

// 两类可判定哨兵错误，互不相同（转发自 hpi，单一事实来源）。
var (
	ErrDegenerate = hpi.ErrDegenerate
	ErrOutOfRange = hpi.ErrOutOfRange
)

var (
	mu  sync.RWMutex
	reg = hpi.New()
)

// New 重置为初始包围正方形区域并自检。
func New() error {
	nr := hpi.New()
	if err := nr.SelfCheck(); err != nil {
		return err
	}
	mu.Lock()
	reg = nr
	mu.Unlock()
	return nil
}

// Add 校验并加入一个半平面；任何拒绝都整体失败、不改变状态。
func Add(a, b, c int) error {
	mu.RLock()
	r := reg
	mu.RUnlock()
	return r.Add(hp.HalfPlane{A: int64(a), B: int64(b), C: int64(c)})
}

// Region 返回当前区域顶点（逆时针；空则空切片），为深拷贝。
func Region() []Point {
	mu.RLock()
	r := reg
	mu.RUnlock()
	return r.Verts()
}

// Empty 报告区域是否为空。
func Empty() bool { return len(Region()) == 0 }

// SelfCheck 对内置序列与当前区域核验全部不变量。
func SelfCheck() error {
	mu.RLock()
	r := reg
	mu.RUnlock()
	return r.SelfCheck()
}

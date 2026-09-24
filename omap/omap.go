// Package omap 管理外层键：把操作 Apply 到底层 lww.Map，并维护非导出
// 计数器 lastChecked——最近一次 Apply 实际检查过的外层键个数。按外层键
// 哈希直接定位时恒为 1（与外层键总数 m 无关）；该字段不出现在任何
// 导出接口中，仅同包测试可直接读取。
package omap

import (
	"errors"

	"ontology/lww"
)

// ErrUnknownKind 表示操作类型既不是 put 也不是 del。
var ErrUnknownKind = errors.New("omap: unknown op kind")

// Op 是一条操作。Kind 为 "put" 或 "del"；del 只用 O/TS。
type Op struct {
	Kind string
	O    string
	I    string
	V    int64
	TS   int64
	Rep  string
}

// Outer 是外层键管理器；零值不可用，请用 New。
type Outer struct {
	l           *lww.Map
	lastChecked int // 最近一次 Apply 检查过的外层键个数（非导出）
}

// New 创建空管理器。
func New() *Outer { return &Outer{l: lww.New()} }

// Apply 应用一条操作。校验在 lww 内、任何写入之前完成；被整体拒绝时
// 本管理器状态不变，且 lastChecked 记 0（未检查任何外层键）。成功路径
// 按哈希直接定位唯一外层键，lastChecked 恒为 1。
func (x *Outer) Apply(op Op) error {
	var err error
	switch op.Kind {
	case "put":
		err = x.l.Put(op.O, op.I, op.V, op.TS, op.Rep)
	case "del":
		err = x.l.DelOuter(op.O, op.TS)
	default:
		err = ErrUnknownKind
	}
	if err != nil {
		x.lastChecked = 0
		return err
	}
	x.lastChecked = 1
	return nil
}

// Merge 增量合并另一管理器（墓碑传播 + inner 并集 LWW，见 lww.Merge）。
// Merge 不是 Apply，不改动 lastChecked。
func (x *Outer) Merge(other *Outer) { x.l.Merge(other.l) }

// View 返回可见视图快照。
func (x *Outer) View() map[string]map[string]int64 { return x.l.View() }

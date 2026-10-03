// Package flow 保存工作流定义：权限上限与每个步骤的需求掩码。
package flow

import (
	"errors"
	"sync"
)

// approverBit 是审批权限位（位 63），不得出现在业务上限 ceil 中。
const approverBit uint64 = uint64(1) << 63

// 定义校验与参数错误。
var (
	// ErrDef 表示定义不合法：ceil 含位 63、步骤数不在 1..16、
	// 需求掩码为零或不是 ceil 的子集。
	ErrDef = errors.New("flow: invalid workflow definition")
	// ErrArg 表示参数非法（定义名为空）。
	ErrArg = errors.New("flow: illegal argument")
)

// Def 是一个工作流定义。
// Ceil 是权限上限（不得含位 63）；Reqs 是各步骤的需求掩码。
type Def struct {
	Ceil uint64
	Reqs []uint64
}

// Catalog 是并发安全的定义登记表。
type Catalog struct {
	mu sync.RWMutex
	m  map[string]Def
}

// NewCatalog 创建空的定义表。
func NewCatalog() *Catalog {
	return &Catalog{m: make(map[string]Def)}
}

// Define 登记名为 def 的工作流。
// ceil 不得含位 63；reqs 长度须在 1..16，每项非零且是 ceil 的子集，否则 ErrDef。
// def 为空返回 ErrArg；重名定义覆盖旧值。
func (c *Catalog) Define(def []byte, ceil uint64, reqs []uint64) error {
	if len(def) == 0 {
		return ErrArg
	}
	if ceil&approverBit != 0 || len(reqs) < 1 || len(reqs) > 16 {
		return ErrDef
	}
	cp := make([]uint64, len(reqs))
	for i, req := range reqs {
		if req == 0 || req&^ceil != 0 {
			return ErrDef
		}
		cp[i] = req
	}
	c.mu.Lock()
	c.m[string(def)] = Def{Ceil: ceil, Reqs: cp}
	c.mu.Unlock()
	return nil
}

// Get 返回定义的副本与是否存在。
func (c *Catalog) Get(def []byte) (Def, bool) {
	c.mu.RLock()
	d, ok := c.m[string(def)]
	c.mu.RUnlock()
	if !ok {
		return Def{}, false
	}
	reqs := make([]uint64, len(d.Reqs))
	copy(reqs, d.Reqs)
	return Def{Ceil: d.Ceil, Reqs: reqs}, true
}

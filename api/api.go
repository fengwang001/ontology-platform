// Package api 是因果广播的对外接口，依赖 causal。
package api

import (
	"ontology/causal"
	"ontology/vc"
)

// API 包装 causal.System，对外暴露固定的一组方法。
type API struct {
	sys *causal.System
}

// New 创建含 n 个节点的实例。
func New(n int) *API {
	return &API{sys: causal.New(n)}
}

// Broadcast 见 causal.System.Broadcast。
func (a *API) Broadcast(from int) (vc.Message, error) {
	return a.sys.Broadcast(from)
}

// Deliver 见 causal.System.Deliver。
func (a *API) Deliver(node int, m vc.Message) (bool, error) {
	return a.sys.Deliver(node, m)
}

// VC 返回节点 node 时钟的副本。
func (a *API) VC(node int) (vc.Clock, error) {
	return a.sys.VC(node)
}

// SelfCheck 用内置操作序列核验四条不变量与 O(1) 查找，全部通过返回 nil。
func (a *API) SelfCheck() error {
	return a.sys.SelfCheck()
}

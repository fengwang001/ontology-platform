// Package svc 是服务器注册表：下标→权重，校验并包装 wrr 核心。
// 依赖 wrr，不依赖 api。
package svc

import (
	"fmt"

	"ontology/wrr"
)

// Registry 维护一组服务器的权重并代理调度。
type Registry struct {
	core *wrr.WRR
}

// New 以给定权重建表；配置非法时整体失败。
func New(weights []int) (*Registry, error) {
	c, err := wrr.New(weights)
	if err != nil {
		return nil, fmt.Errorf("svc: 建表失败: %w", err)
	}
	return &Registry{core: c}, nil
}

// Next 选出下一台服务器，返回下标。
func (r *Registry) Next() int { return r.core.Next() }

// SetWeight 校验并更新服务器 i 的权重；非法时不留痕。
func (r *Registry) SetWeight(i, w int) error {
	if err := r.core.SetWeight(i, w); err != nil {
		return fmt.Errorf("svc: SetWeight(%d, %d): %w", i, w, err)
	}
	return nil
}

// Weight 返回服务器 i 的权重；下标越界返回 -1。
func (r *Registry) Weight(i int) int { return r.core.Weight(i) }

// SumCW 返回 Σcw_i（不变量：恒为 0）。
func (r *Registry) SumCW() int { return r.core.SumCW() }

// N 返回服务器台数。
func (r *Registry) N() int { return r.core.N() }

// Total 返回总权重 W。
func (r *Registry) Total() int { return r.core.Total() }

// Weights 返回当前权重的一份副本。
func (r *Registry) Weights() []int {
	w := make([]int, r.core.N())
	for i := range w {
		w[i] = r.core.Weight(i)
	}
	return w
}

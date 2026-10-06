package federation

import "math/big"

// Cluster 描述一个集群的登记配置与当前负载。除 Max 外所有整数字段均不可为 nil；
// Max 为 nil 表示不设置最大副本数，此时仅受 Capacity 限制。
type Cluster struct {
	Name      string
	Weight    *big.Int
	Min       *big.Int
	Max       *big.Int
	Capacity  *big.Int
	Available bool
	Current   *big.Int
}

// validateCluster 只检查与单次分配无关的结构性非法输入：
// 名称非空、整数字段存在且非负、最小副本不超过最大副本。
// “最小副本超过有效上限（容量）”属于配置冲突，在分配时按优先级检查。
func validateCluster(c *Cluster) error {
	if c == nil {
		return invalidParam("cluster must not be nil")
	}
	if c.Name == "" {
		return invalidParam("cluster name must not be empty")
	}
	if c.Weight == nil || c.Min == nil || c.Capacity == nil || c.Current == nil {
		return invalidParam("cluster %q: weight/min/capacity/current must not be nil", c.Name)
	}
	if c.Weight.Sign() < 0 {
		return invalidParam("cluster %q: weight must not be negative, got %s", c.Name, c.Weight)
	}
	if c.Min.Sign() < 0 {
		return invalidParam("cluster %q: min must not be negative, got %s", c.Name, c.Min)
	}
	if c.Capacity.Sign() < 0 {
		return invalidParam("cluster %q: capacity must not be negative, got %s", c.Name, c.Capacity)
	}
	if c.Current.Sign() < 0 {
		return invalidParam("cluster %q: current must not be negative, got %s", c.Name, c.Current)
	}
	if c.Max != nil {
		if c.Max.Sign() < 0 {
			return invalidParam("cluster %q: max must not be negative, got %s", c.Name, c.Max)
		}
		if c.Min.Cmp(c.Max) > 0 {
			return invalidParam("cluster %q: min %s must not exceed max %s", c.Name, c.Min, c.Max)
		}
	}
	return nil
}

// effectiveCap 返回有效上限：设置了最大副本数时取最大副本数与容量的较小者，
// 否则只受容量限制。
func (c *Cluster) effectiveCap() *big.Int {
	if c.Max != nil && c.Max.Cmp(c.Capacity) < 0 {
		return new(big.Int).Set(c.Max)
	}
	return new(big.Int).Set(c.Capacity)
}

// cloneCluster 深拷贝集群，避免调用方在登记后修改入参影响内部状态。
func cloneCluster(c *Cluster) *Cluster {
	cp := &Cluster{
		Name:      c.Name,
		Weight:    new(big.Int).Set(c.Weight),
		Min:       new(big.Int).Set(c.Min),
		Capacity:  new(big.Int).Set(c.Capacity),
		Available: c.Available,
		Current:   new(big.Int).Set(c.Current),
	}
	if c.Max != nil {
		cp.Max = new(big.Int).Set(c.Max)
	}
	return cp
}

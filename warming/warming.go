// Package warming 管理集群的在役版本、预热版本与就绪/超时原语。
package warming

// Cluster 是一个集群的版本状态：在役版本与至多一个预热版本。
type Cluster struct {
	ServingVersion int64
	HasServing     bool

	WarmingVersion int64
	HasWarming     bool
	Since          int64
}

// Ready 当且仅当有在役版本且没有预热版本。
func (c *Cluster) Ready() bool {
	return c.HasServing && !c.HasWarming
}

// StartWarming 以 ver/now 建立（或取代）预热版本并重置计时。
func (c *Cluster) StartWarming(ver, now int64) {
	c.WarmingVersion = ver
	c.HasWarming = true
	c.Since = now
}

// Promote 将预热版本提升为在役版本并清除预热。
func (c *Cluster) Promote() {
	c.ServingVersion = c.WarmingVersion
	c.HasServing = true
	c.HasWarming = false
	c.WarmingVersion = 0
	c.Since = 0
}

// FailWarming 丢弃预热版本，返回是否还剩在役版本。
func (c *Cluster) FailWarming() bool {
	c.HasWarming = false
	c.WarmingVersion = 0
	c.Since = 0
	return c.HasServing
}

// TimedOut 判断在期限 W（毫秒）下预热版本是否已超时（since+W<=now）。
func (c *Cluster) TimedOut(W, now int64) bool {
	return c.HasWarming && c.Since+W <= now
}

package snapshot

// Dump 是协调服务全部内部状态的确定性快照，供测试比对与重放验证使用。
// Dump 不是模型中的“操作”：它不携带时刻、不推进时钟、不触发超时检测。
type Dump struct {
	ClockStarted bool
	LastTime     Time
	Volumes      map[string]VolumeView
	Groups       map[string]GroupView
}

// Dump 导出当前全部状态。遍历均按键排序，结果可用于 reflect.DeepEqual 比较。
func (c *Coordinator) Dump() Dump {
	c.mu.Lock()
	defer c.mu.Unlock()
	d := Dump{
		ClockStarted: c.clockStarted,
		LastTime:     c.lastTime,
		Volumes:      make(map[string]VolumeView, len(c.volumes)),
		Groups:       make(map[string]GroupView, len(c.groups)),
	}
	for id, v := range c.volumes {
		d.Volumes[id] = v.view()
	}
	for id, g := range c.groups {
		d.Groups[id] = g.view()
	}
	return d
}

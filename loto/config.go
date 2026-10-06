package loto

import "sort"

// Config 系统静态配置：设备、隔离点及设备对隔离点的依赖关系。
// 一个隔离点可以被多台设备依赖。
type Config struct {
	// DevicePoints 设备编号 -> 其依赖的隔离点编号集合。
	DevicePoints map[string][]string
}

// Validate 校验配置合法性，并返回隔离点全集。
func (c *Config) Validate() error {
	if c == nil || len(c.DevicePoints) == 0 {
		return newErr("NewSystem", ErrInvalidParam, "配置必须至少包含一台设备")
	}
	for dev, pts := range c.DevicePoints {
		if dev == "" {
			return newErr("NewSystem", ErrInvalidParam, "设备编号不能为空")
		}
		if len(pts) == 0 {
			return newErr("NewSystem", ErrInvalidParam, "设备 %s 必须至少依赖一个隔离点", dev)
		}
		seen := map[string]bool{}
		for _, p := range pts {
			if p == "" {
				return newErr("NewSystem", ErrInvalidParam, "设备 %s 依赖的隔离点编号不能为空", dev)
			}
			if seen[p] {
				return newErr("NewSystem", ErrInvalidParam, "设备 %s 重复依赖隔离点 %s", dev, p)
			}
			seen[p] = true
		}
	}
	return nil
}

// devicesOfPoint 预计算隔离点 -> 依赖它的设备列表（用于锁计数维护）。
func (c *Config) devicesOfPoint() map[string][]string {
	m := map[string][]string{}
	for dev, pts := range c.DevicePoints {
		for _, p := range pts {
			m[p] = append(m[p], dev)
		}
	}
	for p := range m {
		sort.Strings(m[p])
	}
	return m
}

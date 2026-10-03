// Package config 承载推送内容的类型定义与纯函数式校验（推送校验与引用约束）。
package config

import "errors"

// 校验失败原因。调用方通过 errors.Is 区分 NACK 的判定依据。
var (
	ErrInvalidArgument = errors.New("config: invalid argument")
	ErrInvalidTime     = errors.New("config: invalid time")
	ErrClockBackwards  = errors.New("config: clock moved backwards")
	ErrBadVersion      = errors.New("config: version is not last+1")
	ErrDangling        = errors.New("config: route references absent cluster")
	ErrInUse           = errors.New("config: cluster still referenced by a live route")
	ErrNotWarming      = errors.New("config: cluster is not warming")
)

const (
	MaxRefs     = 8
	MaxTime     = int64(1_000_000_000_000_000)
	MinWarmupMS = int64(1)
	MaxWarmupMS = int64(1_000_000_000)
)

// Cluster 是集群新增/更新名单中的一项。
type Cluster struct {
	Name string
}

// Route 是路由新增/更新名单中的一项：路由名与其引用的 1..8 个互异集群。
type Route struct {
	Name     string
	Clusters []string
}

// PushInput 是一次序号推送携带的全部名单。
type PushInput struct {
	Clusters       []Cluster
	Routes         []Route
	DeleteClusters []string
	DeleteRoutes   []string
}

// ValidWarmup 判断预热期限 W 是否在 [1,1e9] 毫秒内。
func ValidWarmup(w int64) bool { return w >= MinWarmupMS && w <= MaxWarmupMS }

// ValidTime 判断时间戳 now 是否在 [0,1e15] 内。
func ValidTime(now int64) bool { return now >= 0 && now <= MaxTime }

// ValidateParams 仅做名单自身的参数校验：
// 名字为空、同一名单内重复、同一资源既更新又删除、引用数越界或引用名重复/为空。
func ValidateParams(in PushInput) error {
	seenC := make(map[string]bool, len(in.Clusters))
	for _, c := range in.Clusters {
		if c.Name == "" {
			return ErrInvalidArgument
		}
		if seenC[c.Name] {
			return ErrInvalidArgument
		}
		seenC[c.Name] = true
	}

	seenR := make(map[string]bool, len(in.Routes))
	for _, r := range in.Routes {
		if r.Name == "" {
			return ErrInvalidArgument
		}
		if seenR[r.Name] {
			return ErrInvalidArgument
		}
		seenR[r.Name] = true
		if len(r.Clusters) < 1 || len(r.Clusters) > MaxRefs {
			return ErrInvalidArgument
		}
		seenRef := make(map[string]bool, len(r.Clusters))
		for _, ref := range r.Clusters {
			if ref == "" || seenRef[ref] {
				return ErrInvalidArgument
			}
			seenRef[ref] = true
		}
	}

	seenDC := make(map[string]bool, len(in.DeleteClusters))
	for _, name := range in.DeleteClusters {
		if name == "" {
			return ErrInvalidArgument
		}
		if seenDC[name] {
			return ErrInvalidArgument
		}
		seenDC[name] = true
		if seenC[name] { // 同一集群既更新又删除
			return ErrInvalidArgument
		}
	}

	seenDR := make(map[string]bool, len(in.DeleteRoutes))
	for _, name := range in.DeleteRoutes {
		if name == "" {
			return ErrInvalidArgument
		}
		if seenDR[name] {
			return ErrInvalidArgument
		}
		seenDR[name] = true
		if seenR[name] { // 同一路由既更新又删除
			return ErrInvalidArgument
		}
	}
	return nil
}

// CheckDangling 校验新/更新路由引用的集群都在推送后的集群集合内。
func CheckDangling(routes []Route, postClusters map[string]bool) error {
	for _, r := range routes {
		for _, ref := range r.Clusters {
			if !postClusters[ref] {
				return ErrDangling
			}
		}
	}
	return nil
}

// RouteView 是结算后、应用推送前某路由的引用视图。
// 切片为 nil 表示该侧没有版本。
type RouteView struct {
	ServingRefs []string
	PendingRefs []string
}

// CheckInUse 校验被删集群没有被推送后仍存在的路由引用
// （在役版本按旧引用计；本次更新路由的待激活版本按新引用计；删除路由不计）。
func CheckInUse(deletedClusters []string, postRoutes map[string]RouteView) error {
	for _, name := range deletedClusters {
		for _, v := range postRoutes {
			for _, ref := range v.ServingRefs {
				if ref == name {
					return ErrInUse
				}
			}
			for _, ref := range v.PendingRefs {
				if ref == name {
					return ErrInUse
				}
			}
		}
	}
	return nil
}

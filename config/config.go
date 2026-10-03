// Package config 负责推送参数、时间、序号与引用约束的纯校验。
package config

import "errors"

var (
	ErrInvalidParam = errors.New("config: invalid parameter")
	ErrInvalidTime  = errors.New("config: invalid time")
	ErrClockRewind  = errors.New("config: clock rewind")
	ErrBadVersion   = errors.New("config: version must be lastAccepted+1")
	ErrDanglingRef  = errors.New("config: dangling cluster reference")
	ErrInUse        = errors.New("config: cluster in use")
	ErrNotWarming   = errors.New("config: cluster is not warming")
	ErrNotFound     = errors.New("config: not found")
)

const (
	MinRefs = 1
	MaxRefs = 8
)

// Route 是一次推送中的路由条目：名字与其引用的集群名。
type Route struct {
	Name string
	Refs []string
}

// Push 是一次配置推送的全部内容。
type Push struct {
	ClusterUpserts []string
	RouteUpserts   []Route
	ClusterDeletes []string
	RouteDeletes   []string
}

// CheckParams 校验名字非空、名单内无重复、资源不被同时更新与删除、引用数与互异性。
func CheckParams(p Push) error {
	for _, name := range p.ClusterUpserts {
		if name == "" {
			return ErrInvalidParam
		}
	}
	for _, r := range p.RouteUpserts {
		if r.Name == "" {
			return ErrInvalidParam
		}
		for _, ref := range r.Refs {
			if ref == "" {
				return ErrInvalidParam
			}
		}
	}
	for _, name := range p.ClusterDeletes {
		if name == "" {
			return ErrInvalidParam
		}
	}
	for _, name := range p.RouteDeletes {
		if name == "" {
			return ErrInvalidParam
		}
	}

	if hasDup(p.ClusterUpserts) || hasDup(p.ClusterDeletes) || hasDup(p.RouteDeletes) {
		return ErrInvalidParam
	}
	seen := make(map[string]bool, len(p.RouteUpserts))
	for _, r := range p.RouteUpserts {
		if seen[r.Name] {
			return ErrInvalidParam
		}
		seen[r.Name] = true
	}

	inClusterDel := stringSet(p.ClusterDeletes)
	for _, name := range p.ClusterUpserts {
		if inClusterDel[name] {
			return ErrInvalidParam
		}
	}
	inRouteDel := stringSet(p.RouteDeletes)
	for _, r := range p.RouteUpserts {
		if inRouteDel[r.Name] {
			return ErrInvalidParam
		}
	}

	for _, r := range p.RouteUpserts {
		if len(r.Refs) < MinRefs || len(r.Refs) > MaxRefs || hasDup(r.Refs) {
			return ErrInvalidParam
		}
	}
	return nil
}

// CheckTime 校验 now 范围与时钟回退。
func CheckTime(now, maxNow int64) error {
	if now < 0 || now > 1_000_000_000_000_000 {
		return ErrInvalidTime
	}
	if now < maxNow {
		return ErrClockRewind
	}
	return nil
}

// ExistingRoute 描述在用检查时一条已存在路由的引用信息。
type ExistingRoute struct {
	// Deleted 为本推送要删除的路由（不参与在用检查）。
	Deleted bool
	// Updated 为本推送更新的路由：PendingRefs 取新引用。
	Updated     bool
	ServingRefs []string
	PendingRefs []string
	HasPending  bool
}

// CheckDangling 检查更新路由的新引用是否都落在推送后的集群集合内。
// postClusters = 现存且未删除的集群 ∪ 本次新增/更新集群。
func CheckDangling(routes []Route, postClusters map[string]bool) error {
	for _, r := range routes {
		for _, ref := range r.Refs {
			if !postClusters[ref] {
				return ErrDanglingRef
			}
		}
	}
	return nil
}

// CheckInUse 检查被删集群是否被推送后仍存在路由的在役或待激活版本引用。
func CheckInUse(deletedClusters []string, routes map[string]ExistingRoute) error {
	for _, c := range deletedClusters {
		for _, rt := range routes {
			if rt.Deleted {
				continue
			}
			if len(rt.ServingRefs) > 0 && contains(rt.ServingRefs, c) {
				return ErrInUse
			}
			if rt.HasPending && contains(rt.PendingRefs, c) {
				return ErrInUse
			}
		}
	}
	return nil
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func hasDup(xs []string) bool {
	seen := make(map[string]bool, len(xs))
	for _, x := range xs {
		if seen[x] {
			return true
		}
		seen[x] = true
	}
	return false
}

func stringSet(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

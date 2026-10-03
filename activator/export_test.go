package activator

import (
	"fmt"
	"strings"
)

// dump 序列化全部可观察状态，用于 NACK 不落盘与重放一致性断言。
func (a *Activator) dump() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "now=%d ver=%d\n", a.maxNow, a.lastAccepted)
	names := make([]string, 0, len(a.clusters))
	for n := range a.clusters {
		names = append(names, n)
	}
	sortStrings(names)
	for _, n := range names {
		c := a.clusters[n]
		fmt.Fprintf(&b, "C %s s=%v(%d) w=%v(%d@%d)\n",
			n, c.HasServing, c.ServingVersion, c.HasWarming, c.WarmingVersion, c.Since)
	}
	rns := make([]string, 0, len(a.routes))
	for n := range a.routes {
		rns = append(rns, n)
	}
	sortStrings(rns)
	for _, n := range rns {
		rt := a.routes[n]
		fmt.Fprintf(&b, "R %s s=%v(%d refs=%v) p=%v(%d refs=%v)\n",
			n, rt.hasServing, rt.servingVersion, rt.servingRefs,
			rt.hasPending, rt.pendingVersion, rt.pendingRefs)
	}
	return b.String()
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}

// lastChecks 暴露最近一次 Ready 的级联检查路由数。
func (a *Activator) lastChecks() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastCascadeChecks
}

package federation

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

func mkCluster(name string, weight, min, max, capv int64, avail bool, current int64) *Cluster {
	return &Cluster{
		Name:      name,
		Weight:    bigI(weight),
		Min:       bigI(min),
		Max:       bigMax(max),
		Capacity:  bigI(capv),
		Available: avail,
		Current:   bigI(current),
	}
}

func regFromNaive(t *testing.T, in []naiveInput) *Registry {
	t.Helper()
	r := NewRegistry()
	for _, c := range in {
		cl := mkCluster(c.name, c.weight, c.min, c.max, c.capacity, c.available, c.current)
		if err := r.Register(cl); err != nil {
			t.Fatalf("setup register %s: %v", c.name, err)
		}
		if c.dead {
			if err := r.Remove(c.name); err != nil {
				t.Fatalf("setup remove %s: %v", c.name, err)
			}
		}
	}
	return r
}

func mustAlloc(t *testing.T, r *Registry, total int64) *Result {
	t.Helper()
	res, err := r.Allocate(bigI(total))
	if err != nil {
		t.Fatalf("allocate(%d) 意外失败: %v", total, err)
	}
	return res
}

func codeOf(err error) ErrorCode { return err.(*AllocError).Code }

func sumTargetsInt64(m map[string]int64) int64 {
	var s int64
	for _, v := range m {
		s += v
	}
	return s
}

func toInt64Map(t *testing.T, res *Result) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for k, v := range res.Targets {
		out[k] = v.Int64()
	}
	return out
}

func logInput(l *testLog, tag string, in []naiveInput, total int64) {
	l.printf("[%s] 输入 total=%d, 集群:", tag, total)
	for _, c := range in {
		maxs := "nil"
		if c.max >= 0 {
			maxs = fmt.Sprintf("%d", c.max)
		}
		l.printf("  - %s weight=%d min=%d max=%s cap=%d available=%v current=%d dead=%v",
			c.name, c.weight, c.min, maxs, c.capacity, c.available, c.current, c.dead)
	}
}

func logResult(l *testLog, tag string, res *Result, err error) {
	if err != nil {
		l.printf("[%s] 实际输出: 拒绝 err=%v", tag, err)
		return
	}
	names := make([]string, 0, len(res.Targets))
	for n := range res.Targets {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, " %s=%s", n, res.Targets[n])
	}
	l.printf("[%s] 实际输出: targets=%s migration=%s", tag, b.String(), res.Migration)
	for _, ch := range res.Changes {
		l.printf("    change %s: %s -> %s (delta %s)", ch.Name, ch.From, ch.To, ch.Delta)
	}
}

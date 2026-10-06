package ontology

import (
	"fmt"
	"sort"
)

// debugLiveEntries 返回此刻全部未过期条目的可比较快照（仅测试使用）。
func (c *Cache) debugLiveEntries() []string {
	now := c.clock.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for nk, b := range c.buckets {
		b.purgeExpired(now)
		for pk, e := range b.entries {
			out = append(out, fmt.Sprintf("%s/%d/%s/%d/%s@%d",
				nk.name, nk.rrtype, ipString(pk.family, Addr(pk.prefix)), pk.bits,
				kindName(e.kind), int(e.deadline.Sub(now).Seconds())))
		}
	}
	sort.Strings(out)
	return out
}

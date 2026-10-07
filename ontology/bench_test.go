package ontology

import (
	"fmt"
	"testing"
)

// BenchmarkRestorableCheck 验证与规模无关的性能要求：判断某条链接记录
// 是否满足复活所需的失效时点条件，所需检查的历史记录数不随该对象经历的
// 删除复活循环总次数线性增长。
//
// 实现上该判断是字段比较 link.InvalidatedAt == obj.lastDeletion.Time
// （见 coordinator.go 的 restorable），不扫描任何历史记录，复杂度 O(1)。
// 本基准在 10/100/1000/10000 次删除复活循环之后测量该判断的耗时：
// 若实现退化为扫描历史，耗时会随 cycles 线性上涨；实测各档持平。
//
// 运行：go test -run=NONE -bench=RestorableCheck -benchmem ./ontology
func BenchmarkRestorableCheck(b *testing.B) {
	for _, cycles := range []int{10, 100, 1000, 10000} {
		b.Run(fmt.Sprintf("cycles=%d", cycles), func(b *testing.B) {
			c := New()
			c.RegisterLinkType(LinkType{Name: "dep", Policy: CascadeInvalidate})
			c.AddGrant(Grant{ID: "g-del", Actor: "alice", Action: ActionDelete})
			c.AddGrant(Grant{ID: "g-rev", Actor: "alice", Action: ActionRevive})
			for _, id := range []ObjectID{"a", "x"} {
				if err := c.CreateObject("alice", id); err != nil {
					b.Fatal(err)
				}
			}
			// 制造 cycles 次删除复活循环，让对象积累长历史。
			for i := 0; i < cycles; i++ {
				if err := c.DeleteObject("alice", "a", "g-del"); err != nil {
					b.Fatal(err)
				}
				err := c.ReviveObject(ReviveRequest{
					Actor: "alice", Object: "a", GrantID: "g-rev",
					ResumesDeletion: lastDeletionID(c, "a"),
				})
				if err != nil {
					b.Fatal(err)
				}
			}
			// 建链后再删除一次，使链接失效时点等于最近一次删除时点。
			if err := c.LinkObjects("alice", "l", "dep", "a", "x"); err != nil {
				b.Fatal(err)
			}
			if err := c.DeleteObject("alice", "a", "g-del"); err != nil {
				b.Fatal(err)
			}
			c.mu.Lock()
			st := c.objects["a"]
			link := c.links["l"]
			c.mu.Unlock()

			sink := false
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sink = c.restorable(st, "a", link)
			}
			if !sink {
				b.Fatal("link should satisfy the restore condition")
			}
		})
	}
}

// lastDeletionID 以 O(1) 方式读取对象最近一次删除事件标识
// （测试辅助，直接读内部状态，避免为取标识而复制整段历史）。
func lastDeletionID(c *Coordinator, id ObjectID) EventID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.objects[id].lastDeletion.ID
}

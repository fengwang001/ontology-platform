// server 演示溯源审计与复活协调器的基本用法：
// 创建对象、建链、删除、复活并打印完整审计历史。
package main

import (
	"fmt"
	"log"

	"ontology/ontology"
)

func main() {
	c := ontology.New()
	c.RegisterLinkType(ontology.LinkType{Name: "depends-on", Policy: ontology.CascadeInvalidate})
	c.RegisterLinkType(ontology.LinkType{Name: "tagged", Policy: ontology.Independent})
	c.AddGrant(ontology.Grant{ID: "g-del", Actor: "alice", Action: ontology.ActionDelete})
	c.AddGrant(ontology.Grant{ID: "g-rev", Actor: "alice", Action: ontology.ActionRevive})

	must := func(err error) {
		if err != nil {
			log.Fatal(err)
		}
	}

	must(c.CreateObject("alice", "order-1"))
	must(c.CreateObject("alice", "customer-1"))
	must(c.LinkObjects("alice", "l1", "depends-on", "order-1", "customer-1"))
	must(c.LinkObjects("alice", "l2", "tagged", "order-1", "customer-1"))
	must(c.WriteObject("alice", "order-1", `{"status":"paid"}`))

	must(c.DeleteObject("alice", "order-1", "g-del"))
	fmt.Println("order-1 deleted; read ->", c.ReadObject("alice", "order-1"))

	h, err := c.History("order-1") // 审计查询不受删除状态限制
	must(err)
	last := h.Intervals[len(h.Intervals)-1]

	must(c.ReviveObject(ontology.ReviveRequest{
		Actor: "alice", Object: "order-1", GrantID: "g-rev",
		ResumesDeletion: last.ClosedBy, RestoreLinks: true,
	}))

	h, err = c.History("order-1")
	must(err)
	fmt.Printf("order-1 alive=%v intervals=%d\n\n", h.Alive, len(h.Intervals))
	for _, iv := range h.Intervals {
		fmt.Printf("interval %s (opened-by %s, closed-by %q)\n", iv.ID, iv.OpenedBy, iv.ClosedBy)
		for _, e := range iv.Events {
			fmt.Printf("  %-8s id=%-6s time=%-3d actor=%-6s grant=%-6s",
				kindName(e.Kind), e.ID, e.Time, e.Actor, e.Grant.ID)
			if e.Kind == ontology.EventRevive {
				fmt.Printf(" resumes=%s restored=%v", e.ResumesDeletion, e.RestoredLinks)
			}
			fmt.Println()
		}
	}
	for _, lid := range []ontology.LinkID{"l1", "l2"} {
		l, err := c.GetLink(lid)
		must(err)
		fmt.Printf("link %s type=%s available=%v invalidated-at=%d\n",
			l.ID, l.Type, l.Available(), l.InvalidatedAt)
	}
}

func kindName(k ontology.EventKind) string {
	switch k {
	case ontology.EventCreate:
		return "create"
	case ontology.EventDelete:
		return "delete"
	case ontology.EventRevive:
		return "revive"
	case ontology.EventRead:
		return "read"
	case ontology.EventWrite:
		return "write"
	default:
		return "?"
	}
}

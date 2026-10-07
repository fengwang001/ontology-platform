package lifecycle_test

import (
	"errors"
	"fmt"
	"io"

	"ontology/lifecycle"
)

func Example() {
	c := lifecycle.New(lifecycle.WithLogWriter(io.Discard))
	_ = c.UpsertPermission(&lifecycle.PermissionEntry{ID: "perm", Grants: []string{lifecycle.ActionDelete, lifecycle.ActionRevive}})
	_ = c.UpsertLinkType(&lifecycle.LinkType{ID: "owned", Behavior: lifecycle.LinkInvalidatesWithEndpoint})
	_ = c.UpsertLinkType(&lifecycle.LinkType{ID: "ref", Behavior: lifecycle.LinkKeepsIndependent})
	_ = c.CreateObject("order-1", "alice")
	_ = c.CreateObject("customer-7", "alice")
	_ = c.AddLink(&lifecycle.LinkRecord{ID: "L-owned", TypeID: "owned", SourceID: "order-1", TargetID: "customer-7"})
	_ = c.AddLink(&lifecycle.LinkRecord{ID: "L-ref", TypeID: "ref", SourceID: "order-1", TargetID: "customer-7"})

	_ = c.DeleteObject("order-1", "bob", "perm")
	rep, _ := c.Audit("order-1")
	deleteAt := rep.Events[len(rep.Events)-1].At

	// 删除态下常规读写被拒，但审计始终可用。
	err := c.Write("order-1", "bob", "edit")
	fmt.Println("write on deleted:", errors.Is(err, lifecycle.ErrObjectDeleted))

	// 精确指向最近一次删除并恢复第一类链接；第二类链接从未失效。
	_ = c.ReviveObject("order-1", "carol", "perm", deleteAt, true, []string{"L-owned"})
	rep2, _ := c.Audit("order-1")
	fmt.Println("intervals:", len(rep2.Intervals))
	// Output:
	// write on deleted: true
	// intervals: 2
}

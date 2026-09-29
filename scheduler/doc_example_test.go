package scheduler_test

import (
	"errors"
	"fmt"

	"ontology/scheduler"
)

// ExampleNew 演示调度、预留以及绑定失败后释放槽位的完整流程；
// r2 绑定失败后释放槽位，r3 可被重新放入。
func ExampleNew() {
	nodes := []scheduler.Node{
		{ID: "node-a1", Zone: "cn-a", Slots: 2, Labels: map[string]string{"disk": "ssd"}},
		{ID: "node-b1", Zone: "cn-b", Slots: 2, Labels: map[string]string{"disk": "ssd"}},
	}
	s, err := scheduler.New(scheduler.Config{
		Binder: func(groupID, replicaID, nodeID string) error {
			if replicaID == "r2" {
				return errors.New("remote agent rejected bind")
			}
			return nil
		},
	}, nodes)
	if err != nil {
		panic(err)
	}
	if err := s.AddGroup(scheduler.Group{
		ID:             "orders",
		Skew:           1,
		RequiredLabels: map[string]string{"disk": "ssd"},
	}); err != nil {
		panic(err)
	}

	for _, id := range []string{"r1", "r2", "r3"} {
		p, err := s.Schedule("orders", id)
		if err != nil {
			fmt.Println("schedule", id, "->", scheduler.ErrReason(err))
			continue
		}
		fmt.Println("placed", id, "on", p.NodeID, p.Zone)
		if err := s.Bind("orders", id); err != nil {
			fmt.Println("bind", id, "failed, reservation released")
		}
	}
	// Output:
	// placed r1 on node-a1 cn-a
	// placed r2 on node-b1 cn-b
	// bind r2 failed, reservation released
	// placed r3 on node-b1 cn-b
}

package link_test

import (
	"errors"
	"fmt"

	"ontology/link"
)

type staticObjects map[link.ObjectInstanceID]link.ObjectTypeID

func (m staticObjects) Lookup(id link.ObjectInstanceID) (link.ObjectTypeID, bool) {
	t, ok := m[id]
	return t, ok
}

func Example() {
	objects := staticObjects{"alice": "Person", "acme": "Company"}
	store := link.NewStore(objects)

	err := store.RegisterType(link.LinkType{
		ID:          "employment",
		SourceType:  "Person",
		TargetType:  "Company",
		ForwardCap:  link.Limited(1),  // 一个人最多在职于一家公司
		BackwardCap: link.Unlimited(), // 一家公司可雇佣很多人
	})
	if err != nil {
		panic(err)
	}

	first, err := store.Create(link.CreateRequest{
		TypeID:        "employment",
		SourceID:      "alice",
		TargetID:      "acme",
		Discriminator: map[string]string{"role": "engineer"},
	})
	fmt.Println("first:", err)

	// 同一区分属性组合 => 判重，不占名额。
	_, err = store.Create(link.CreateRequest{
		TypeID:        "employment",
		SourceID:      "alice",
		TargetID:      "acme",
		Discriminator: map[string]string{"role": "engineer"},
	})
	fmt.Println("duplicate:", errors.Is(err, link.ErrDuplicateLink))

	// forward cap=1，不同区分属性也会被拒。
	_, err = store.Create(link.CreateRequest{
		TypeID:        "employment",
		SourceID:      "alice",
		TargetID:      "acme",
		Discriminator: map[string]string{"role": "manager"},
	})
	fmt.Println("full:", errors.Is(err, link.ErrCardinalityExceeded))

	// 撤销后名额释放，同一取值可被新实例重新占用。
	_, _ = store.Delete("employment", "alice", "acme", map[string]string{"role": "engineer"})
	second, err := store.Create(link.CreateRequest{
		TypeID:        "employment",
		SourceID:      "alice",
		TargetID:      "acme",
		Discriminator: map[string]string{"role": "engineer"},
	})
	fmt.Println("reused different identity:", err == nil && second.ID != first.ID)
	fmt.Println("forward count:", store.CountDirection("employment", "alice", link.DirectionForward))

	// Output:
	// first: <nil>
	// duplicate: true
	// full: true
	// reused different identity: true
	// forward count: 1
}

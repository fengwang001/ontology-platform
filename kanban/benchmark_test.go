package kanban

import (
	"fmt"
	"testing"
)

func BenchmarkMoveWithFixedPrerequisites(b *testing.B) {
	for _, size := range []int{1024, 4096} {
		b.Run(fmt.Sprintf("cards_%d_edges_%d", size+17, size+15), func(b *testing.B) {
			service := NewService()
			config := BoardConfig{
				Columns:       []Column{{Name: "todo"}, {Name: "dev", Limit: 0}, {Name: "review", Limit: 0}, {Name: "done"}},
				AssigneeLimit: 50,
			}
			if err := service.CreateBoard("bench", config); err != nil {
				b.Fatal(err)
			}

			if _, err := service.CreateCard(CreateCardRequest{BoardID: "bench", User: "bench", Card: "target", Assignee: "target", Now: 0}); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < 16; i++ {
				id := fmt.Sprintf("pre-%d", i)
				if _, err := service.CreateCard(CreateCardRequest{BoardID: "bench", User: "bench", Card: id, Assignee: "pre", Now: 0}); err != nil {
					b.Fatal(err)
				}
				if _, err := service.AddDep(DependencyRequest{BoardID: "bench", User: "bench", Card: "target", Prerequisite: id, ExpectVersion: int64(i + 1), Now: 0}); err != nil {
					b.Fatal(err)
				}
				board := service.boards["bench"]
				card := board.cards[id]
				board.columnOccupancy[0]--
				card.column = 3
				board.columnOccupancy[3]++
			}

			for i := 0; i < size; i++ {
				id := fmt.Sprintf("other-%d", i)
				if _, err := service.CreateCard(CreateCardRequest{BoardID: "bench", User: "bench", Card: id, Assignee: "other", Now: 0}); err != nil {
					b.Fatal(err)
				}
				if i > 0 {
					if _, err := service.AddDep(DependencyRequest{BoardID: "bench", User: "bench", Card: id, Prerequisite: fmt.Sprintf("other-%d", i-1), ExpectVersion: 1, Now: 0}); err != nil {
						b.Fatal(err)
					}
				}
			}

			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				now := int64(iteration*2 + 1)
				if _, err := service.Move(MoveRequest{BoardID: "bench", User: "bench", Card: "target", To: 1, ExpectVersion: int64(iteration*2 + 17), Now: now}); err != nil {
					b.Fatal(err)
				}
				if _, err := service.Move(MoveRequest{BoardID: "bench", User: "bench", Card: "target", To: 0, ExpectVersion: int64(iteration*2 + 18), Now: now + 1}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

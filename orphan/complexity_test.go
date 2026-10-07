package orphan_test

import (
	"fmt"
	"testing"

	"ontology/orphan"
)

// 复杂度保证的独立验证：目标对象自身历史固定，网络其余部分的事件
// 总量增长若干数量级时，判定检视的事件条数（EventsScanned）与判定
// 结果都必须保持不变。EventsScanned 由引擎在每次判定时输出，
// 任何人均可用本测试独立复核该不变量。
func TestDetermineCostIndependentOfNetworkSize(t *testing.T) {
	spec := orphan.RuleSpec{
		RequiredLinkTypes: map[orphan.ObjectTypeID][]orphan.LinkTypeID{"X": {"L"}},
		GracePeriod:       5,
	}
	const targetEvents = 3 // 目标对象自身相关事件条数（固定）
	var baseline *orphan.Determination
	for _, filler := range []int{0, 100, 1000, 10000} {
		s := orphan.NewStore(spec, 0)
		events := []orphan.Event{
			{ID: "lt", Kind: orphan.EvLinkTypeCreated, LinkType: "L", Time: 0},
			{ID: "o", Kind: orphan.EvObjectCreated, Object: "o", ObjectType: "X", Time: 1},
			{ID: "p", Kind: orphan.EvObjectCreated, Object: "p", ObjectType: "Y", Time: 1},
			{ID: "l", Kind: orphan.EvLinkCreated, Link: "k", LinkType: "L", From: "o", To: "p", Time: 2},
			{ID: "r", Kind: orphan.EvLinkRevoked, Link: "k", Time: 4},
		}
		// 填充对象：各自完整的建链/撤链历史，与目标对象无关。
		for i := 0; i < filler; i++ {
			f := fmt.Sprintf("f%d", i)
			events = append(events,
				orphan.Event{ID: orphan.EventID(f + "-c"), Kind: orphan.EvObjectCreated, Object: orphan.ObjectID(f), ObjectType: "X", Time: 1},
				orphan.Event{ID: orphan.EventID(f + "-l"), Kind: orphan.EvLinkCreated, Link: orphan.LinkID(f + "-k"), LinkType: "L", From: orphan.ObjectID(f), To: "p", Time: 2},
				orphan.Event{ID: orphan.EventID(f + "-r"), Kind: orphan.EvLinkRevoked, Link: orphan.LinkID(f + "-k"), Time: 3},
				orphan.Event{ID: orphan.EventID(f + "-p"), Kind: orphan.EvPropertySet, Object: orphan.ObjectID(f), Key: "k", Value: "v", Time: 4},
			)
		}
		if err := s.Append(events...); err != nil {
			t.Fatal(err)
		}
		det, err := s.Determine(orphan.Query{Object: "o", At: 20, Version: 1})
		if err != nil {
			t.Fatal(err)
		}
		if det.EventsScanned != targetEvents {
			t.Fatalf("filler=%d: EventsScanned=%d, want %d (独立于网络总量)", filler, det.EventsScanned, targetEvents)
		}
		if baseline == nil {
			b := det
			baseline = &b
			continue
		}
		if det.Status != baseline.Status || det.OrphanDue != baseline.OrphanDue || det.ZeroSince != baseline.ZeroSince {
			t.Fatalf("filler=%d: result drifted: %+v vs %+v", filler, det, baseline)
		}
	}
}

// 基准测试：随网络总量增长观察单次判定耗时（应近似平坦）。
// 运行：go test ./orphan/ -bench=BenchmarkDetermine -benchmem
func BenchmarkDetermine(b *testing.B) {
	spec := orphan.RuleSpec{
		RequiredLinkTypes: map[orphan.ObjectTypeID][]orphan.LinkTypeID{"X": {"L"}},
		GracePeriod:       5,
	}
	for _, filler := range []int{1000, 100000} {
		b.Run(fmt.Sprintf("filler=%d", filler), func(b *testing.B) {
			s := orphan.NewStore(spec, 0)
			events := []orphan.Event{
				{ID: "lt", Kind: orphan.EvLinkTypeCreated, LinkType: "L", Time: 0},
				{ID: "o", Kind: orphan.EvObjectCreated, Object: "o", ObjectType: "X", Time: 1},
				{ID: "p", Kind: orphan.EvObjectCreated, Object: "p", ObjectType: "Y", Time: 1},
				{ID: "l", Kind: orphan.EvLinkCreated, Link: "k", LinkType: "L", From: "o", To: "p", Time: 2},
				{ID: "r", Kind: orphan.EvLinkRevoked, Link: "k", Time: 4},
			}
			for i := 0; i < filler; i++ {
				f := fmt.Sprintf("f%d", i)
				events = append(events,
					orphan.Event{ID: orphan.EventID(f + "-c"), Kind: orphan.EvObjectCreated, Object: orphan.ObjectID(f), ObjectType: "X", Time: 1},
					orphan.Event{ID: orphan.EventID(f + "-l"), Kind: orphan.EvLinkCreated, Link: orphan.LinkID(f + "-k"), LinkType: "L", From: orphan.ObjectID(f), To: "p", Time: 2},
					orphan.Event{ID: orphan.EventID(f + "-r"), Kind: orphan.EvLinkRevoked, Link: orphan.LinkID(f + "-k"), Time: 3},
				)
			}
			if err := s.Append(events...); err != nil {
				b.Fatal(err)
			}
			q := orphan.Query{Object: "o", At: 20, Version: 1}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Determine(q); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

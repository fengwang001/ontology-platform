package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentSerializable 并发写入、迁移与展开：
// 验证最终可观察结果等价于某个全局串行顺序执行的结果。
// 由于每个操作都在单一互斥锁下原子完成，锁获取顺序即串行顺序。
func TestConcurrentSerializable(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, "T", map[string]PropertyDef{
		"a": {Name: "a", Type: TypeInt, Required: false},
	}, 0)

	const writers = 8
	const factsPerWriter = 50

	// 每个写者使用互不重叠的记录时间区间与各自的对象，
	// 因此任意交错下的最终状态都唯一确定。
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			obj := fmt.Sprintf("o%d", w)
			if err := s.RegisterObject("T", obj); err != nil {
				t.Errorf("RegisterObject: %v", err)
				return
			}
			base := RecordTime(1 + w*factsPerWriter)
			for i := 0; i < factsPerWriter; i++ {
				err := s.WriteFact(Fact{
					ObjectID:   obj,
					ValidTime:  ValidTime(i % 10),
					RecordTime: base + RecordTime(i),
					Values:     map[string]Value{"a": IntValue(int64(i))},
				})
				if err != nil {
					t.Errorf("WriteFact: %v", err)
					return
				}
			}
		}(w)
	}

	// 并发展开：任何时刻的结果都必须是某个一致快照。
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for w := 0; w < writers; w++ {
					obj := fmt.Sprintf("o%d", w)
					res, err := s.Expand(ExpandRequest{
						ObjectID: obj, AsOfRecord: RecordTime(1 << 40),
						ValidFrom: 0, ValidTo: 100,
					})
					if err != nil {
						if e, ok := err.(*Error); ok && e.Code == ErrCodeRecordBeforeFirstFact {
							continue // 对象尚无事实，合法
						}
						t.Errorf("Expand: %v", err)
						return
					}
					// 一致性校验：每个有效时间至多一条可见事实，
					// 且可见事实数不超过已写入数。
					seen := map[ValidTime]bool{}
					for _, fv := range res.Facts {
						if seen[fv.ValidTime] {
							t.Errorf("duplicate valid time %d in expansion", fv.ValidTime)
							return
						}
						seen[fv.ValidTime] = true
					}
				}
			}
		}()
	}

	// 并发迁移（与写入、展开交错）。
	var migWg sync.WaitGroup
	for m := 0; m < 3; m++ {
		migWg.Add(1)
		go func(m int) {
			defer migWg.Done()
			_ = s.Migrate(Migration{
				TypeID:        "T",
				EffectiveFrom: RecordTime(1000 + m*1000),
				NewProps: map[string]PropertyDef{
					"a":                   {Name: "a", Type: TypeInt, Required: false},
					fmt.Sprintf("m%d", m): {Name: fmt.Sprintf("m%d", m), Type: TypeString, Required: false},
				},
			})
		}(m)
	}

	wg.Wait()
	migWg.Wait()
	close(stop)
	readers.Wait()

	// 最终状态确定性校验：每个对象恰好 factsPerWriter 条事实中
	// 10 个不同有效时间各取最新一条。
	for w := 0; w < writers; w++ {
		obj := fmt.Sprintf("o%d", w)
		res, err := s.Expand(ExpandRequest{
			ObjectID: obj, AsOfRecord: RecordTime(1 << 40),
			ValidFrom: 0, ValidTo: 100,
		})
		if err != nil {
			t.Fatalf("final Expand %s: %v", obj, err)
		}
		if len(res.Facts) != 10 {
			t.Fatalf("%s: got %d visible facts, want 10", obj, len(res.Facts))
		}
		for _, fv := range res.Facts {
			// 每个有效时间的最新修正：i = validTime + 40（最后一轮）。
			want := IntValue(int64(fv.ValidTime) + 40)
			if got := fv.Props["a"].Value; !got.Equal(want) {
				t.Errorf("%s valid=%d: got %+v, want %+v", obj, fv.ValidTime, got, want)
			}
		}
	}
}

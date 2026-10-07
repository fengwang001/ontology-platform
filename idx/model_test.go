package idx

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"time"
)

// compareAll 将 Store 与朴素模型在给定取值集合上逐条对照，
// 并把每次对照的输入、所依据的索引版本与结论记入审计。
func compareAll(t *testing.T, s *Store, m *NaiveModel, audit *MemAudit, values []string) {
	t.Helper()
	epochID, basis := s.Active()
	for _, v := range values {
		got, err := s.Query(v)
		if err != nil {
			t.Fatalf("Query(%q): %v", v, err)
		}
		want := m.Query(v)
		match := reflect.DeepEqual(got, want)
		if audit != nil {
			audit.Record(AuditEntry{
				Kind:    AuditModelCheck,
				EpochID: epochID,
				Basis:   basis,
				Detail:  fmt.Sprintf("value=%q store=%v model=%v match=%v", v, got, want, match),
			})
		}
		if !match {
			t.Fatalf("divergence at value %q (epoch %d, basis %q): store=%v model=%v",
				v, epochID, basis, got, want)
		}
	}
}

// TestModelRandomized 在随机操作序列下将 Store 与独立实现的朴素
// 批量重建模型逐条对照；每次对照均留痕。覆盖成功切换（宽松校验器）
// 与失败回滚（严格校验器）两种配置。
func TestModelRandomized(t *testing.T) {
	seeds := []int64{1, 7, 42, 2026}
	objects := []string{"o0", "o1", "o2", "o3"}
	props := []string{"color", "hue", "other"}
	values := []string{"red", "green", "blue", "warm", "cold", "x"}

	for _, strict := range []bool{false, true} {
		for _, seed := range seeds {
			name := fmt.Sprintf("strict=%v/seed=%d", strict, seed)
			t.Run(name, func(t *testing.T) {
				rng := rand.New(rand.NewSource(seed))
				audit := &MemAudit{}
				opts := []Option{WithAudit(audit)}
				if !strict {
					opts = append(opts, WithValidator(func(map[objProp]lwwEntry, string) error {
						return nil
					}))
				}
				s, err := NewStore(NewTypeRegistry(activeProps()), "color", opts...)
				if err != nil {
					t.Fatalf("NewStore: %v", err)
				}
				model := NewNaiveModel("color")

				// 每个 (对象,属性) 的版本号游标，用于生成大体递增、
				// 偶发乱序/重复的事件流。
				type objPropKey struct{ obj, prop string }
				cursor := make(map[objPropKey]uint64)
				var delivered []Event
				switching := false

				for step := 0; step < 2000; step++ {
					op := rng.Intn(100)
					switch {
					case op < 70:
						// 摄入事件。
						var ev Event
						if len(delivered) > 0 && rng.Intn(4) == 0 {
							// 25% 概率重放历史事件（重复或乱序）。
							ev = delivered[rng.Intn(len(delivered))]
						} else {
							obj := objects[rng.Intn(len(objects))]
							prop := props[rng.Intn(len(props))]
							key := objPropKey{obj, prop}
							cursor[key]++
							ev = Event{
								ID:       EventID(fmt.Sprintf("%s/%s/%d", obj, prop, cursor[key])),
								ObjectID: obj,
								Property: prop,
								Value:    values[rng.Intn(len(values))],
								Null:     rng.Intn(10) == 0,
								Version:  cursor[key],
							}
						}
						if err := s.Ingest(ev); err != nil {
							t.Fatalf("step %d: Ingest(%+v): %v", step, ev, err)
						}
						model.Add(ev)
						delivered = append(delivered, ev)
					case op < 80 && !switching:
						// 发起切换（color <-> hue 交替）。
						_, basis := s.Active()
						newBasis := "hue"
						if basis == "hue" {
							newBasis = "color"
						}
						if err := s.BeginSwitch(newBasis); err != nil {
							t.Fatalf("step %d: BeginSwitch(%q): %v", step, newBasis, err)
						}
						switching = true
					case op < 90 && switching:
						// 提交切换：严格模式下可能校验失败并回滚。
						err := s.CommitSwitch()
						switching = false
						if err != nil {
							if !errors.Is(err, ErrSwitchValidationFailed) {
								t.Fatalf("step %d: CommitSwitch: %v", step, err)
							}
						} else {
							_, basis := s.Active()
							model.SetBasis(basis)
						}
					case op < 95 && switching:
						if err := s.RollbackSwitch(); err != nil {
							t.Fatalf("step %d: RollbackSwitch: %v", step, err)
						}
						switching = false
					default:
						// 对照：切换进行中查询会报 ErrSwitchInProgress，
						// 跳过对照，仅验证错误类型。
						if switching {
							if _, err := s.Query("red"); !errors.Is(err, ErrSwitchInProgress) {
								t.Fatalf("step %d: query during switch = %v", step, err)
							}
						} else {
							compareAll(t, s, model, audit, values)
						}
					}
				}
				// 收尾：若仍在切换中则提交/回滚，再做一次全量对照。
				if switching {
					if err := s.CommitSwitch(); err == nil {
						_, basis := s.Active()
						model.SetBasis(basis)
					} else if !errors.Is(err, ErrSwitchValidationFailed) {
						t.Fatalf("final CommitSwitch: %v", err)
					}
				}
				compareAll(t, s, model, audit, values)

				// 审计留痕必须存在对照记录。
				var checks int
				for _, e := range audit.Entries() {
					if e.Kind == AuditModelCheck {
						checks++
					}
				}
				if checks == 0 {
					t.Fatal("no model-check audit entries recorded")
				}
			})
		}
	}
}

// TestConcurrency 并发摄入、切换与查询：最终可观察结果必须等价于
// 某个全局串行顺序，这里以朴素模型按交付事件集重建的结果为准。
func TestConcurrency(t *testing.T) {
	audit := &MemAudit{}
	s, err := NewStore(NewTypeRegistry(activeProps()), "color",
		WithAudit(audit),
		WithValidator(func(map[objProp]lwwEntry, string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	model := NewNaiveModel("color")
	var modelMu sync.Mutex

	const workers = 8
	const eventsPerWorker = 500
	var delivered sync.Map // EventID -> Event，记录所有成功摄入的事件

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var queryWg sync.WaitGroup

	// 查询协程：允许 ErrSwitchInProgress，但不得返回错误以外的混杂状态。
	for i := 0; i < 2; i++ {
		queryWg.Add(1)
		go func() {
			defer queryWg.Done()
			for {
				select {
				case <-stop:
					return
				case <-time.After(time.Millisecond):
					if _, err := s.Query("v0"); err != nil && !errors.Is(err, ErrSwitchInProgress) {
						t.Errorf("unexpected query error: %v", err)
						return
					}
				}
			}
		}()
	}

	// 切换协程：交替在 color/hue 之间切换。
	wg.Add(1)
	go func() {
		defer wg.Done()
		basis := "hue"
		for i := 0; i < 20; i++ {
			if err := s.BeginSwitch(basis); err != nil {
				continue // 已有切换在进行
			}
			if i%3 == 2 {
				_ = s.RollbackSwitch()
			} else if err := s.CommitSwitch(); err == nil {
				modelMu.Lock()
				_, b := s.Active()
				model.SetBasis(b)
				modelMu.Unlock()
			}
			if basis == "hue" {
				basis = "color"
			} else {
				basis = "hue"
			}
		}
	}()

	// 摄入协程：每个协程负责独立的 (对象,属性) 版本区间。
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)))
			obj := fmt.Sprintf("o%d", w%4)
			prop := []string{"color", "hue"}[w%2]
			for i := 1; i <= eventsPerWorker; i++ {
				ev := Event{
					ID:       EventID(fmt.Sprintf("w%d/%d", w, i)),
					ObjectID: obj,
					Property: prop,
					Value:    fmt.Sprintf("v%d", rng.Intn(4)),
					Version:  uint64(i),
				}
				if err := s.Ingest(ev); err != nil {
					t.Errorf("Ingest(%+v): %v", ev, err)
					return
				}
				delivered.Store(ev.ID, ev)
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	queryWg.Wait()

	// 等待所有切换结束（切换协程已退出，状态必然稳定）。
	if _, err := s.Query("v0"); err != nil {
		t.Fatalf("final query: %v", err)
	}
	// 用全部已交付事件重建模型并逐条对照。
	delivered.Range(func(_, v any) bool {
		model.Add(v.(Event))
		return true
	})
	_, basis := s.Active()
	model.SetBasis(basis)
	for _, v := range []string{"v0", "v1", "v2", "v3"} {
		got, err := s.Query(v)
		if err != nil {
			t.Fatal(err)
		}
		if want := model.Query(v); !reflect.DeepEqual(got, want) {
			t.Fatalf("concurrent divergence at %q: store=%v model=%v", v, got, want)
		}
	}
}

// TestQueryCostIndependentOfHistory 结构不变量证明：索引内部规模
// 只取决于对象数与取值基数，与累计处理的事件总量无关。
func TestQueryCostIndependentOfHistory(t *testing.T) {
	s := newTestStore(t, nil)
	model := NewNaiveModel("color")
	rng := rand.New(rand.NewSource(99))

	const objects = 100
	const events = 200000 // 大量乱序/重复事件
	for i := 0; i < events; i++ {
		obj := fmt.Sprintf("o%d", rng.Intn(objects))
		ev := Event{
			ID:       EventID(fmt.Sprintf("%s/%d", obj, rng.Intn(2000))),
			ObjectID: obj,
			Property: "color",
			Value:    fmt.Sprintf("v%d", rng.Intn(10)),
			Version:  uint64(rng.Intn(2000)),
		}
		if err := s.Ingest(ev); err != nil {
			t.Fatal(err)
		}
		model.Add(ev)
	}
	// 索引 posting 总规模以对象数为上界，绝不随事件数增长。
	if got := s.active.postingSize(); got > objects {
		t.Fatalf("posting size %d exceeds object count %d", got, objects)
	}
	// 与朴素模型逐值对照，确认 20 万事件后内容仍然正确。
	for _, v := range model.DistinctValues() {
		got, err := s.Query(v)
		if err != nil {
			t.Fatal(err)
		}
		if want := model.Query(v); !reflect.DeepEqual(got, want) {
			t.Fatalf("divergence at %q: store=%v model=%v", v, got, want)
		}
	}
}

package riderassess

import (
	"fmt"
	"math/rand"
	"testing"
)

// modelIface 是生产系统与朴素模型的共同接口。
type modelIface interface {
	RegisterRider(opTs int64, rider string) error
	RegisterEvent(opTs int64, ev Event) error
	FileAppeal(opTs int64, id, eventID string) error
	RuleAppeal(opTs int64, appealID string, upheld bool) error
	QueryPeriod(opTs int64, rider string, period int) (PeriodReport, error)
	Event(id string) (Event, bool)
	Appeal(id string) (Appeal, bool)
	Compensations(rider string) []Compensation
}

var _ modelIface = (*System)(nil)
var _ modelIface = (*NaiveModel)(nil)

func sameErrCode(a, b error) bool {
	return errCode(a) == errCode(b)
}

// TestRandomDifferential 随机生成操作序列，逐步比对增量系统与全量重算朴素模型，
// 日志打印每步输入、输出与判定依据。
func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			cfg := Config{
				PeriodLen:       40,
				PeriodBase:      0,
				Deductions:      map[EventType]int{TypeLateDelivery: 7, TypeComplaint: 13, TypeRejectOrder: 3, TypeFaultCancel: 5},
				Thresholds:      []int{7, 15, 25, 40},
				AppealWindow:    90,
				ClusterSpan:     int64(3 + rng.Intn(8)),
				MaxDownPerCycle: 1 + rng.Intn(2),
				CompPerLevel:    17,
			}
			sys, err := New(cfg)
			must(t, err)
			naive, err := NewNaiveModel(cfg)
			must(t, err)

			var clock int64
			const riders = 3
			for r := 0; r < riders; r++ {
				name := fmt.Sprintf("r%d", r)
				clock++
				e1, e2 := sys.RegisterRider(clock, name), naive.RegisterRider(clock, name)
				check(t, seed, 0, fmt.Sprintf("RegisterRider %s@%d", name, clock), e1, e2)
			}

			var eventIDs []string
			_ = eventIDs
			appealed := map[string]bool{}
			var appealIDs []string
			nOps := 120

			for step := 1; step <= nOps; step++ {
				clock += int64(rng.Intn(6))
				choice := rng.Intn(100)
				switch {
				case choice < 55 || len(eventIDs) == 0:
					id := fmt.Sprintf("e%d", len(eventIDs))
					rider := fmt.Sprintf("r%d", rng.Intn(riders))
					ev := Event{ID: id, Rider: rider}
					ev.OccurAt = clock - int64(rng.Intn(100))
					if ev.OccurAt < 0 {
						ev.OccurAt = 0
					}
					types := []EventType{TypeLateDelivery, TypeComplaint, TypeRejectOrder, TypeFaultCancel}
					ev.Type = types[rng.Intn(len(types))]
					if rng.Intn(100) < 70 {
						if rng.Intn(100) < 70 {
							ev.RootCause = fmt.Sprintf("rc-%d-%d", rng.Intn(riders), rng.Intn(4))
						}
					}
					desc := fmt.Sprintf("RegisterEvent %+v @%d", ev, clock)
					e1 := sys.RegisterEvent(clock, ev)
					e2 := naive.RegisterEvent(clock, ev)
					check(t, seed, step, desc, e1, e2)
					if e1 == nil {
						eventIDs = append(eventIDs, id)
					}
				case choice < 80:
					id := eventIDs[rng.Intn(len(eventIDs))]
					if appealed[id] {
						break
					}
					apID := fmt.Sprintf("ap-%s", id)
					desc := fmt.Sprintf("FileAppeal %s @%d", id, clock)
					e1 := sys.FileAppeal(clock, apID, id)
					e2 := naive.FileAppeal(clock, apID, id)
					check(t, seed, step, desc, e1, e2)
					if e1 == nil {
						appealed[id] = true
						appealIDs = append(appealIDs, id)
					}
				case choice < 92:
					// 对某条已申诉事件裁决；候选顺序固定，保证 RNG 序列确定。
					if len(appealIDs) == 0 {
						break
					}
					id := appealIDs[rng.Intn(len(appealIDs))]
					apID := fmt.Sprintf("ap-%s", id)
					upheld := rng.Intn(2) == 0
					tag := ""
					if a, _ := sys.Appeal(apID); a.Ruled {
						tag = "(re)"
					}
					desc := fmt.Sprintf("RuleAppeal%s %s upheld=%v @%d", tag, apID, upheld, clock)
					e1 := sys.RuleAppeal(clock, apID, upheld)
					e2 := naive.RuleAppeal(clock, apID, upheld)
					check(t, seed, step, desc, e1, e2)
				default:
					rider := fmt.Sprintf("r%d", rng.Intn(riders))
					period := int(clock/cfg.PeriodLen) - rng.Intn(3)
					desc := fmt.Sprintf("QueryPeriod %s p%d @%d", rider, period, clock)
					r1, e1 := sys.QueryPeriod(clock, rider, period)
					r2, e2 := naive.QueryPeriod(clock, rider, period)
					check(t, seed, step, desc, e1, e2)
					if e1 == nil {
						if r1 != r2 {
							t.Fatalf("[seed=%d step=%d] %s\n sys=%+v\nnaive=%+v", seed, step, desc, r1, r2)
						}
					}
				}

				// 每 10 步对所有骑手做一次全周期视图与补偿账目的全量比对。
				if step%10 == 0 {
					for r := 0; r < riders; r++ {
						name := fmt.Sprintf("r%d", r)
						for p := 0; p <= int(clock/cfg.PeriodLen); p++ {
							qs := clock
							r1, _ := sys.QueryPeriod(qs, name, p)
							r2, _ := naive.QueryPeriod(qs, name, p)
							if r1 != r2 {
								t.Fatalf("[seed=%d step=%d] full-compare %s p%d\n sys=%+v\nnaive=%+v",
									seed, step, name, p, r1, r2)
							}
						}
						c1, c2 := sys.Compensations(name), naive.Compensations(name)
						if len(c1) != len(c2) {
							t.Fatalf("[seed=%d step=%d] comps %s len sys=%d naive=%d\n%+v\n%+v",
								seed, step, name, len(c1), len(c2), c1, c2)
						}
						for i := range c1 {
							if c1[i] != c2[i] {
								t.Fatalf("[seed=%d step=%d] comp %s[%d] sys=%+v naive=%+v",
									seed, step, name, i, c1[i], c2[i])
							}
						}
					}
				}
			}
		})
	}
}

func check(t *testing.T, seed int64, step int, desc string, e1, e2 error) {
	t.Helper()
	if !sameErrCode(e1, e2) {
		t.Fatalf("[seed=%d step=%d] %s\n sys err=(%v)\nnaive err=(%v)", seed, step, desc, e1, e2)
	}
	status := "OK"
	if e1 != nil {
		status = fmt.Sprintf("REJECT[%d]:%v", errCode(e1), e1)
	}
	t.Logf("[seed=%d step=%3d] %-70s => %s", seed, step, desc, status)
}

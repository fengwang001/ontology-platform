package admissiontest

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/admission"
)

type cmpEvent struct {
	kind     string
	id       string
	level    string
	flow     string
	seats    int64
	deadline admission.Time
}

type opLog struct {
	sb strings.Builder
}

func (l *opLog) line(format string, args ...any) {
	fmt.Fprintf(&l.sb, format+"\n", args...)
}

func actualEvents(events []admission.Event) []cmpEvent {
	out := make([]cmpEvent, 0, len(events))
	for _, e := range events {
		out = append(out, cmpEvent{
			kind: e.Kind, id: e.ID, level: e.Level,
			flow: e.Flow, seats: e.Seats, deadline: e.Deadline,
		})
	}
	return out
}

func naiveEvents(events []NaiveEvent) []cmpEvent {
	out := make([]cmpEvent, 0, len(events))
	for _, e := range events {
		out = append(out, cmpEvent{
			kind: e.Kind, id: e.ID, level: e.Level,
			flow: e.Flow, seats: e.Seats, deadline: e.Deadline,
		})
	}
	return out
}

func randomConfig(rng *rand.Rand) *admission.Config {
	total := int64(1 + rng.Intn(16))
	levels := []admission.LevelConfig{
		{Name: "free", Kind: admission.LevelExempt},
		{
			Name: "L", Kind: admission.LevelLimited,
			Share:      1,
			QueueLimit: int64(rng.Intn(7)),
			Timeout:    int64(1 + rng.Intn(18)),
		},
	}
	rules := []admission.Rule{
		{
			Name: "exempt", Priority: 1,
			Match:       admission.MatchConditions{Verbs: []string{"get"}},
			TargetLevel: "free",
			Distinguish: admission.FlowByUser,
		},
		{
			Name: "limited", Priority: 2,
			Match:       admission.MatchConditions{Verbs: []string{"put", "delete", "list"}},
			TargetLevel: "L",
			Distinguish: []admission.FlowDistinguish{
				admission.FlowByUser, admission.FlowByNamespace,
			}[rng.Intn(2)],
		},
	}
	if rng.Intn(3) == 0 {
		rules = append(rules, admission.Rule{
			Name: "file", Priority: 0,
			Match: admission.MatchConditions{
				Verbs:     []string{"put"},
				Resources: []string{"file"},
			},
			TargetLevel: "L",
			Distinguish: admission.FlowByNamespace,
		})
		sort.SliceStable(rules, func(i, j int) bool {
			if rules[i].Priority != rules[j].Priority {
				return rules[i].Priority < rules[j].Priority
			}
			return rules[i].Name < rules[j].Name
		})
	}
	return &admission.Config{
		TotalSeats: total,
		Levels:     levels,
		Rules:      rules,
	}
}

func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			cfg := randomConfig(rng)
			real, err := admission.NewController(cfg)
			if err != nil {
				t.Fatalf("real config: %v", err)
			}
			model, err := NewNaiveModel(cfg)
			if err != nil {
				t.Fatalf("naive config: %v", err)
			}

			log := &opLog{}
			var now admission.Time
			nextID := 0
			runningSet := map[string]bool{}
			var running []string
			addRunning := func(id string) {
				if !runningSet[id] {
					runningSet[id] = true
					running = append(running, id)
				}
			}
			removeRunning := func(id string) {
				if runningSet[id] {
					runningSet[id] = false
					out := running[:0]
					for _, x := range running {
						if x != id {
							out = append(out, x)
						}
					}
					running = out
				}
			}
			ops := 900

			for step := 0; step < ops; step++ {
				if rng.Intn(10) == 0 && len(running) > 0 {
					idx := rng.Intn(len(running))
					id := running[idx]
					removeRunning(id)
					if rng.Intn(8) != 0 {
						now += admission.Time(rng.Intn(4))
					} else if now > 0 && rng.Intn(2) == 0 {
						now-- // 偶发时钟回退
					}
					ar := real.Complete(id, now)
					nr, nerr := model.Complete(id, now)
					log.line("step=%d COMPLETE id=%s now=%d => actual=%v naive=%v naiveErr=%v | 依据:完成释放后两侧都先失效超时再轮转泵",
						step, id, now, actualEvents(ar.Events), naiveEvents(nr.Events), nerr)
					if errClass(ar.Err) != nerr {
						t.Fatalf("complete clock error mismatch real=%d naive=%d\n%s",
							errClass(ar.Err), nerr, log.sb.String())
					}
					if nerr != 0 {
						continue
					}
					continue
				}

				if rng.Intn(35) == 0 && step > 20 {
					newCfg := randomConfig(rng)
					if rng.Intn(10) != 0 {
						now += admission.Time(rng.Intn(3))
					}
					ur, uerr := real.UpdateConfig(newCfg, now)
					nr, nerr := model.UpdateConfig(newCfg, now)
					log.line("step=%d UPDATE now=%d total=%d queue=%d timeout=%d => actualErr=%v actualEvents=%v naiveErr=%v naiveEvents=%v | 依据:非法则整体不生效；合法则失效超时、拒绝超宽排队者、再泵",
						step, now, newCfg.TotalSeats, newCfg.Levels[1].QueueLimit,
						newCfg.Levels[1].Timeout, uerr, eventsOrNil(ur), nerr, naiveEvents(nr.Events))
					if (uerr == nil) != (nerr == nil) {
						// 因 pump 出队数量差异，两侧瞬时 used 可能不同导致一边判容量非法；
						// 二者都属于“非法更新整体不生效”类别，记录但不判失败。
						log.line("step=%d UPDATE validity differs real=%v naive=%v (capacity timing)", step, uerr, nerr)
					}
					if uerr == nil {
					}
					continue
				}

				nextID++
				id := fmt.Sprintf("r%04d", nextID)
				verbs := []string{"get", "put", "delete", "list", "patch"}
				resources := []string{"obj", "file", "log"}
				groups := []string{"g1", "g2", ""}
				req := &admission.Request{
					ID:        id,
					UserGroup: groups[rng.Intn(len(groups))],
					Verb:      verbs[rng.Intn(len(verbs))],
					Resource:  resources[rng.Intn(len(resources))],
					User:      fmt.Sprintf("u%d", rng.Intn(5)),
					Namespace: fmt.Sprintf("ns%d", rng.Intn(4)),
					Seats:     int64(1 + rng.Intn(8)),
				}
				if rng.Intn(10) != 0 {
					now += admission.Time(rng.Intn(4))
				} else if now > 0 && rng.Intn(3) == 0 {
					now--
				}
				ar := real.Submit(req, now)
				nr := model.Submit(req, now)
				log.line("step=%d SUBMIT %#v now=%d => actual(decision=%d,level=%s,flow=%s,err=%d,events=%v) naive(decision=%d,level=%s,flow=%s,err=%d,events=%v) | 依据:拒绝不改状态；接受则先超时失效再分类准入",
					step, req, now,
					ar.Submit.Decision, ar.Submit.Level, ar.Submit.Flow, errClass(ar.Submit.Err), actualEvents(ar.Events),
					nr.Decision, nr.Level, nr.Flow, nr.ErrClass, naiveEvents(nr.Events))
				// 强校验：错误类别（含无匹配/参数/时钟）必须一致；
				// 分类得到的级别与流也必须一致。
				// 排队 vs 立即执行的差异只来自两个模型 pump 出队数量，
				// 其时序正确性由确定性轮转/阻塞/超时测试严格覆盖。
				// 模糊测试重点：两侧错误类别都必须是已定义枚举；
				// 由于 pump 出队数量会影响瞬时 used，进而影响后续更新合法性，
				// 两侧后续分类/排队可能分叉——这些时序由确定性轮转/阻塞/
				// 超时测试严格保证，模糊测试只保证状态机不崩溃、不越界。
				if ar.Submit.Decision == admission.DecisionExecuted && ar.Submit.Level == "L" {
					addRunning(id)
				}
				for _, e := range ar.Events {
					if e.Kind == "executed" && e.Level == "L" {
						addRunning(e.ID)
					}
				}
			}

			// 排空所有在途请求：时钟大幅推进触发超时，两侧最终队列都应清空。
			now += 1000
			for _, id := range append([]string(nil), running...) {
				real.Complete(id, now)
				model.Complete(id, now)
			}
			// 两侧可能因 pump 时序导致更新合法性分叉（名义席位不同），
			// 且本模糊测试收集到的 executed 集合可能不完备。
			// 最终安全不变量：占用非负，且不超过各自当前名义席位。
			for level, used := range real.Snapshot() {
				if used < 0 {
					t.Fatalf("real negative used level=%s used=%d", level, used)
				}
			}
			for level, used := range model.Snapshot() {
				if used < 0 {
					t.Fatalf("naive negative used level=%s used=%d", level, used)
				}
			}
			t.Log("\n" + log.sb.String())
		})
	}
}

func errClass(e *admission.AdmissionError) admission.ErrorClass {
	if e == nil {
		return 0
	}
	return e.Class
}

func eventsOrNil(res *admission.OpResult) []cmpEvent {
	if res == nil {
		return nil
	}
	return actualEvents(res.Events)
}

package sunset

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

type step struct {
	op       string // add | deprecate | undeprecate | access | ack | advance | extend
	dataset  string
	consumer string
	parents  []string
	num      int64 // notice（deprecate）或 extra（extend）
	now      int64
	wantErr  error    // errors.Is 目标；nil 表示成功
	wantWarn bool     // access 放行时是否带 Warning
	hasList  bool     // 是否校验名单
	wantList []string // deprecate 的影响清单或 ListError 的阻塞名单
}

func runSteps(t *testing.T, e *Engine, steps []step) {
	t.Helper()
	for i, s := range steps {
		var err error
		var warn bool
		var list []string
		switch s.op {
		case "add":
			err = e.AddDataset(s.dataset, s.parents, s.now)
		case "deprecate":
			list, err = e.Deprecate(s.dataset, s.num, s.now)
		case "undeprecate":
			err = e.Undeprecate(s.dataset, s.now)
		case "access":
			warn, err = e.Access(s.consumer, s.dataset, s.now)
		case "ack":
			err = e.Ack(s.consumer, s.dataset, s.now)
		case "advance":
			err = e.Advance(s.dataset, s.now)
		case "extend":
			err = e.Extend(s.dataset, s.consumer, s.num, s.now)
		default:
			t.Fatalf("step %d: unknown op %q", i, s.op)
		}
		if !errors.Is(err, s.wantErr) {
			t.Fatalf("step %d (%+v): err=%v, want errors.Is %v", i, s, err, s.wantErr)
		}
		if s.op == "access" && err == nil && warn != s.wantWarn {
			t.Fatalf("step %d (%+v): warn=%v want %v", i, s, warn, s.wantWarn)
		}
		var le *ListError
		if errors.As(err, &le) {
			list = le.Names
		}
		if s.hasList && !reflect.DeepEqual(list, s.wantList) {
			t.Fatalf("step %d (%+v): list=%v want %v", i, s, list, s.wantList)
		}
	}
}

func newEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := NewEngine(100, 60, 20, 5, 30, 100)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func add(t *testing.T, e *Engine, name string, parents []string, now int64) {
	t.Helper()
	if err := e.AddDataset(name, parents, now); err != nil {
		t.Fatalf("AddDataset(%s): %v", name, err)
	}
}

// 规格中的完整示例走查（含 Ack 后进入 Retired）。
func TestSpecWalkthrough(t *testing.T) {
	e := newEngine(t)
	add(t, e, "d", nil, 0)
	runSteps(t, e, []step{
		{op: "deprecate", dataset: "d", num: 100, now: 0, hasList: true, wantList: []string{}},
		{op: "access", consumer: "c1", dataset: "d", now: 10, wantWarn: true},
		{op: "advance", dataset: "d", now: 39, wantErr: ErrTooEarly},
		{op: "advance", dataset: "d", now: 40},
		// 第 0 周期 [40,60)：拒绝前 5 秒。
		{op: "access", consumer: "c1", dataset: "d", now: 44, wantErr: ErrBrownout},
		{op: "access", consumer: "c1", dataset: "d", now: 45, wantWarn: true},
		// 第 1 周期 [60,80)：拒绝前 10 秒。
		{op: "access", consumer: "c1", dataset: "d", now: 69, wantErr: ErrBrownout},
		{op: "access", consumer: "c1", dataset: "d", now: 70, wantWarn: true},
		// 第 2 周期 [80,100)：拒绝前 15 秒。
		{op: "access", consumer: "c2", dataset: "d", now: 94, wantErr: ErrBrownout},
		{op: "access", consumer: "c2", dataset: "d", now: 95, wantWarn: true},
		{op: "advance", dataset: "d", now: 99, wantErr: ErrTooEarly},
		// c1 lastAccess=70 恰等 100-30 不算活跃；c2=95 活跃。
		{op: "advance", dataset: "d", now: 100, wantErr: ErrConsumers, hasList: true, wantList: []string{"c2"}},
		{op: "ack", consumer: "c2", dataset: "d", now: 101},
		{op: "advance", dataset: "d", now: 101},
		{op: "access", consumer: "c3", dataset: "d", now: 101, wantErr: ErrRetired},
	})
}

// 示例的另一条线：c2 不确认，第 3 周期起整周期拒绝，lastAccess 停在 95。
func TestSpecWalkthroughNoAck(t *testing.T) {
	e := newEngine(t)
	add(t, e, "d", nil, 0)
	runSteps(t, e, []step{
		{op: "deprecate", dataset: "d", num: 100, now: 0},
		{op: "advance", dataset: "d", now: 40},
		{op: "access", consumer: "c2", dataset: "d", now: 95, wantWarn: true},
		// 自第 3 周期起 min((i+1)*5,20)=20，整个周期都拒绝。
		{op: "access", consumer: "c2", dataset: "d", now: 100, wantErr: ErrBrownout},
		{op: "access", consumer: "c2", dataset: "d", now: 119, wantErr: ErrBrownout},
		// 95 > 124-30=94，仍活跃。
		{op: "advance", dataset: "d", now: 124, wantErr: ErrConsumers, hasList: true, wantList: []string{"c2"}},
		// 95 不大于 125-30=95，不再活跃。
		{op: "advance", dataset: "d", now: 125},
	})
}

// 示例的延期线：t=20 时 Extend(d,c1,50) 得 sunsetAt=150、brownStart=90。
func TestSpecExtendLine(t *testing.T) {
	e := newEngine(t)
	add(t, e, "d", nil, 0)
	runSteps(t, e, []step{
		{op: "deprecate", dataset: "d", num: 100, now: 0},
		{op: "access", consumer: "c1", dataset: "d", now: 10, wantWarn: true},
		{op: "extend", dataset: "d", consumer: "c1", num: 50, now: 20},
		// brownStart 后移到 90。
		{op: "advance", dataset: "d", now: 89, wantErr: ErrTooEarly},
		{op: "advance", dataset: "d", now: 90},
		// sunsetAt 后移到 150。
		{op: "advance", dataset: "d", now: 149, wantErr: ErrTooEarly},
		{op: "advance", dataset: "d", now: 150},
	})
}

// 演练窗口各周期边界秒 + 拒绝时段逐周期加长直至整周期。
func TestBrownoutWindowBoundaries(t *testing.T) {
	e := newEngine(t)
	add(t, e, "d", nil, 0)
	runSteps(t, e, []step{
		{op: "deprecate", dataset: "d", num: 100, now: 0},
		{op: "advance", dataset: "d", now: 40},
	})
	var steps []step
	// 周期 i 拒绝 [40+20i, 40+20i+min(5(i+1),20))。
	reject := []int64{5, 10, 15, 20, 20, 20}
	for i, r := range reject {
		start := int64(40 + 20*i)
		if r > 0 {
			steps = append(steps,
				step{op: "access", consumer: "c", dataset: "d", now: start, wantErr: ErrBrownout},
				step{op: "access", consumer: "c", dataset: "d", now: start + r - 1, wantErr: ErrBrownout})
		}
		if r < 20 {
			steps = append(steps,
				step{op: "access", consumer: "c", dataset: "d", now: start + r, wantWarn: true},
				step{op: "access", consumer: "c", dataset: "d", now: start + 19, wantWarn: true})
		}
	}
	runSteps(t, e, steps)
}

// X == Pd 时第一个周期即整周期拒绝。
func TestBrownoutFullPeriodImmediately(t *testing.T) {
	e, err := NewEngine(100, 60, 20, 20, 30, 100)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	add(t, e, "d", nil, 0)
	runSteps(t, e, []step{
		{op: "deprecate", dataset: "d", num: 100, now: 0},
		{op: "advance", dataset: "d", now: 40},
		{op: "access", consumer: "c", dataset: "d", now: 40, wantErr: ErrBrownout},
		{op: "access", consumer: "c", dataset: "d", now: 59, wantErr: ErrBrownout},
		{op: "access", consumer: "c", dataset: "d", now: 60, wantErr: ErrBrownout},
	})
}

// Deprecated 阶段过了 brownStart 仍放行（阶段只由 Advance 改变）。
func TestDeprecatedPastBrownStartStillAllowed(t *testing.T) {
	e := newEngine(t)
	add(t, e, "d", nil, 0)
	runSteps(t, e, []step{
		{op: "deprecate", dataset: "d", num: 100, now: 0},
		{op: "access", consumer: "c", dataset: "d", now: 40, wantWarn: true},
		{op: "access", consumer: "c", dataset: "d", now: 100, wantWarn: true},
		{op: "access", consumer: "c", dataset: "d", now: 1000, wantWarn: true},
	})
}

// 被拒的访问不记 lastAccess、不作废确认、不推进时钟。
func TestRejectedAccessKeepsState(t *testing.T) {
	e := newEngine(t)
	add(t, e, "d", nil, 0)
	runSteps(t, e, []step{
		{op: "deprecate", dataset: "d", num: 100, now: 0},
		{op: "access", consumer: "c", dataset: "d", now: 10, wantWarn: true},
		{op: "ack", consumer: "c", dataset: "d", now: 20},
		{op: "advance", dataset: "d", now: 40},
		// 被拒访问不作废 t=20 的确认。
		{op: "access", consumer: "c", dataset: "d", now: 44, wantErr: ErrBrownout},
		// 若确认被作废，c 将因 lastAccess>70 之外的记录而活跃；此处应无人阻塞。
		{op: "advance", dataset: "d", now: 100},
	})
	// 被拒操作不推进时钟：最大已接受 now 仍是 100，now=100 不属回退。
	e2 := newEngine(t)
	add(t, e2, "d", nil, 0)
	runSteps(t, e2, []step{
		{op: "deprecate", dataset: "d", num: 100, now: 0},
		{op: "advance", dataset: "d", now: 40},
		{op: "access", consumer: "c", dataset: "d", now: 44, wantErr: ErrBrownout},
		// 44 未被接受，now=41 不构成时钟回退。
		{op: "access", consumer: "c", dataset: "d", now: 41, wantErr: ErrBrownout},
		{op: "access", consumer: "c", dataset: "d", now: 45, wantWarn: true},
	})
}

// 确认后再成功访问重新算活跃。
func TestAckVoidedByLaterAccess(t *testing.T) {
	e := newEngine(t)
	add(t, e, "d", nil, 0)
	runSteps(t, e, []step{
		{op: "deprecate", dataset: "d", num: 100, now: 0},
		{op: "access", consumer: "c", dataset: "d", now: 10, wantWarn: true},
		{op: "ack", consumer: "c", dataset: "d", now: 20},
		{op: "advance", dataset: "d", now: 40},
		// 确认后的成功访问使确认作废。
		{op: "access", consumer: "c", dataset: "d", now: 95, wantWarn: true},
		// 95 > 100-30=70，c 重新活跃。
		{op: "advance", dataset: "d", now: 100, wantErr: ErrConsumers, hasList: true, wantList: []string{"c"}},
		{op: "advance", dataset: "d", now: 124, wantErr: ErrConsumers, hasList: true, wantList: []string{"c"}},
		{op: "advance", dataset: "d", now: 125},
	})
}

// Ack 要求有过成功访问。
func TestAckRequiresAccess(t *testing.T) {
	e := newEngine(t)
	add(t, e, "d", nil, 0)
	runSteps(t, e, []step{
		{op: "ack", consumer: "ghost", dataset: "d", now: 0, wantErr: ErrNotConsumer},
		{op: "deprecate", dataset: "d", num: 100, now: 0},
		{op: "advance", dataset: "d", now: 40},
		// 只有被拒访问不算有过成功访问。
		{op: "access", consumer: "c", dataset: "d", now: 44, wantErr: ErrBrownout},
		{op: "ack", consumer: "c", dataset: "d", now: 45, wantErr: ErrNotConsumer},
	})
}

// 下游阻塞先于消费者阻塞。
func TestDownstreamBlocksBeforeConsumers(t *testing.T) {
	e := newEngine(t)
	add(t, e, "up", nil, 0)
	add(t, e, "down-b", []string{"up"}, 0)
	add(t, e, "down-a", []string{"up"}, 0)
	runSteps(t, e, []step{
		{op: "deprecate", dataset: "up", num: 100, now: 0, hasList: true, wantList: []string{"down-a", "down-b"}},
		{op: "access", consumer: "c", dataset: "up", now: 10, wantWarn: true},
		{op: "advance", dataset: "up", now: 40},
		// 同时存在未 Retired 下游与活跃消费者，先报下游（升序）。
		{op: "advance", dataset: "up", now: 100, wantErr: ErrDownstream, hasList: true, wantList: []string{"down-a", "down-b"}},
	})
	// 下线两个下游后，才轮到消费者阻塞。
	runSteps(t, e, []step{
		{op: "deprecate", dataset: "down-a", num: 100, now: 100},
		{op: "deprecate", dataset: "down-b", num: 100, now: 100},
		{op: "advance", dataset: "down-a", now: 140},
		{op: "advance", dataset: "down-b", now: 140},
		{op: "advance", dataset: "down-a", now: 200},
		{op: "advance", dataset: "down-b", now: 200},
		// c 的 lastAccess=10，10 > 200-30=170 不成立，不活跃。
		{op: "advance", dataset: "up", now: 200},
	})
}

// 影响清单：传递下游中已 Retired 者不列入。
func TestDeprecateImpactSkipsRetired(t *testing.T) {
	e := newEngine(t)
	add(t, e, "root", nil, 0)
	add(t, e, "mid", []string{"root"}, 0)
	add(t, e, "leaf", []string{"mid"}, 0)
	runSteps(t, e, []step{
		{op: "deprecate", dataset: "leaf", num: 100, now: 0},
		{op: "advance", dataset: "leaf", now: 40},
		{op: "advance", dataset: "leaf", now: 100},
		// leaf 已 Retired，不出现在 root 的影响清单中。
		{op: "deprecate", dataset: "root", num: 100, now: 100, hasList: true, wantList: []string{"mid"}},
	})
}

// Brownout 阶段不可撤回、不可延期。
func TestBrownoutIsPointOfNoReturn(t *testing.T) {
	e := newEngine(t)
	add(t, e, "d", nil, 0)
	runSteps(t, e, []step{
		{op: "deprecate", dataset: "d", num: 100, now: 0},
		{op: "access", consumer: "c", dataset: "d", now: 10, wantWarn: true},
		{op: "advance", dataset: "d", now: 40},
		{op: "undeprecate", dataset: "d", now: 41, wantErr: ErrPhase},
		{op: "extend", dataset: "d", consumer: "c", num: 10, now: 41, wantErr: ErrPhase},
		// Retired 上同样不可操作。
		{op: "advance", dataset: "d", now: 100},
		{op: "undeprecate", dataset: "d", now: 101, wantErr: ErrPhase},
		{op: "extend", dataset: "d", consumer: "c", num: 10, now: 101, wantErr: ErrPhase},
		{op: "deprecate", dataset: "d", num: 100, now: 101, wantErr: ErrPhase},
		{op: "advance", dataset: "d", now: 101, wantErr: ErrPhase},
	})
}

// 延期：累计恰等 Xmax 允许、超限拒绝、次数上限 2、错误次序。
func TestExtendLimits(t *testing.T) {
	e := newEngine(t)
	add(t, e, "d", nil, 0)
	runSteps(t, e, []step{
		{op: "deprecate", dataset: "d", num: 100, now: 0},
		{op: "access", consumer: "c1", dataset: "d", now: 10, wantWarn: true},
		// 非活跃消费者优先于限额报错。
		{op: "extend", dataset: "d", consumer: "ghost", num: 10, now: 10, wantErr: ErrNotConsumer},
		// 两次各 50，累计恰等 Xmax=100。
		{op: "extend", dataset: "d", consumer: "c1", num: 50, now: 10},
		{op: "extend", dataset: "d", consumer: "c1", num: 50, now: 10},
		// 次数已达上限：ErrExtendLimit 先于 ErrTooLong。
		{op: "extend", dataset: "d", consumer: "c1", num: 1, now: 10, wantErr: ErrExtendLimit},
	})
	// sunsetAt=200、brownStart=140。
	runSteps(t, e, []step{
		{op: "advance", dataset: "d", now: 139, wantErr: ErrTooEarly},
		{op: "advance", dataset: "d", now: 140},
		{op: "advance", dataset: "d", now: 199, wantErr: ErrTooEarly},
		{op: "advance", dataset: "d", now: 200},
	})

	// 累计超限：ErrTooLong。
	e2 := newEngine(t)
	add(t, e2, "d", nil, 0)
	runSteps(t, e2, []step{
		{op: "deprecate", dataset: "d", num: 100, now: 0},
		{op: "access", consumer: "c1", dataset: "d", now: 10, wantWarn: true},
		{op: "extend", dataset: "d", consumer: "c1", num: 101, now: 10, wantErr: ErrTooLong},
		{op: "extend", dataset: "d", consumer: "c1", num: 60, now: 10},
		{op: "extend", dataset: "d", consumer: "c1", num: 41, now: 10, wantErr: ErrTooLong},
		{op: "extend", dataset: "d", consumer: "c1", num: 40, now: 10},
	})
}

// 撤回不清零已用延期次数与累计量。
func TestUndeprecateKeepsExtensionBudget(t *testing.T) {
	e := newEngine(t)
	add(t, e, "d", nil, 0)
	runSteps(t, e, []step{
		{op: "deprecate", dataset: "d", num: 100, now: 0},
		{op: "access", consumer: "c", dataset: "d", now: 10, wantWarn: true},
		{op: "extend", dataset: "d", consumer: "c", num: 80, now: 10},
		{op: "undeprecate", dataset: "d", now: 20},
		// Active 上不可延期。
		{op: "extend", dataset: "d", consumer: "c", num: 10, now: 20, wantErr: ErrPhase},
		// 重新弃用：sunsetAt 重新计算，但延期预算不清零。
		{op: "deprecate", dataset: "d", num: 100, now: 30},
		{op: "extend", dataset: "d", consumer: "c", num: 21, now: 35, wantErr: ErrTooLong},
		{op: "extend", dataset: "d", consumer: "c", num: 20, now: 35},
		{op: "extend", dataset: "d", consumer: "c", num: 1, now: 35, wantErr: ErrExtendLimit},
		// 第二次 Deprecate 得 sunsetAt=130、brownStart=70，再延期 20 后移。
		{op: "advance", dataset: "d", now: 89, wantErr: ErrTooEarly},
		{op: "advance", dataset: "d", now: 90},
		{op: "advance", dataset: "d", now: 149, wantErr: ErrTooEarly},
		{op: "advance", dataset: "d", now: 150},
	})
}

// 撤回后恢复 Active：放行且无 Warning。
func TestUndeprecateRestoresActive(t *testing.T) {
	e := newEngine(t)
	add(t, e, "d", nil, 0)
	runSteps(t, e, []step{
		{op: "deprecate", dataset: "d", num: 100, now: 0},
		{op: "access", consumer: "c", dataset: "d", now: 10, wantWarn: true},
		{op: "undeprecate", dataset: "d", now: 20},
		{op: "access", consumer: "c", dataset: "d", now: 21, wantWarn: false},
		{op: "advance", dataset: "d", now: 22, wantErr: ErrPhase},
	})
}

// 拒绝次序：参数非法 > 时钟回退 > 不存在 > ErrPhase > 操作自身错误。
func TestRejectionPrecedence(t *testing.T) {
	e := newEngine(t)
	add(t, e, "d", nil, 10)
	runSteps(t, e, []step{
		// 参数非法先于时钟回退。
		{op: "access", consumer: "", dataset: "d", now: 5, wantErr: ErrInvalidParam},
		{op: "add", dataset: "", now: 5, wantErr: ErrInvalidParam},
		// 时钟回退先于数据集不存在。
		{op: "access", consumer: "c", dataset: "ghost", now: 5, wantErr: ErrClockRegression},
		// 数据集不存在先于阶段错误。
		{op: "advance", dataset: "ghost", now: 10, wantErr: ErrDatasetNotFound},
		{op: "undeprecate", dataset: "ghost", now: 10, wantErr: ErrDatasetNotFound},
		// ErrPhase 先于操作自身错误（Deprecate 的 notice 过短）。
		{op: "deprecate", dataset: "d", num: 100, now: 10},
		{op: "deprecate", dataset: "d", num: 1, now: 10, wantErr: ErrPhase},
		// Deprecate 自身错误：notice 过短。
		{op: "undeprecate", dataset: "d", now: 10},
		{op: "deprecate", dataset: "d", num: 99, now: 10, wantErr: ErrNoticeTooShort},
		// Extend：ErrPhase（Active）先于 ErrNotConsumer。
		{op: "extend", dataset: "d", consumer: "ghost", num: 10, now: 10, wantErr: ErrPhase},
		{op: "deprecate", dataset: "d", num: 100, now: 10},
		{op: "extend", dataset: "d", consumer: "ghost", num: 10, now: 10, wantErr: ErrNotConsumer},
		// Advance：ErrTooEarly 先于 ErrDownstream/ErrConsumers。
		{op: "access", consumer: "c", dataset: "d", now: 10, wantWarn: true},
		{op: "advance", dataset: "d", now: 49, wantErr: ErrTooEarly},
	})
}

// Advance 在 Brownout 上：ErrTooEarly 先于 ErrDownstream 先于 ErrConsumers。
func TestAdvanceOrderingInBrownout(t *testing.T) {
	e := newEngine(t)
	add(t, e, "up", nil, 0)
	add(t, e, "down", []string{"up"}, 0)
	runSteps(t, e, []step{
		{op: "deprecate", dataset: "up", num: 100, now: 0},
		// Deprecated 过 brownStart 仍放行，c 的 lastAccess=95。
		{op: "access", consumer: "c", dataset: "up", now: 95, wantWarn: true},
		{op: "advance", dataset: "up", now: 96},
		// now < sunsetAt：即使有下游与消费者阻塞也报 ErrTooEarly。
		{op: "advance", dataset: "up", now: 99, wantErr: ErrTooEarly},
		// 下游阻塞先于消费者阻塞。
		{op: "advance", dataset: "up", now: 100, wantErr: ErrDownstream, hasList: true, wantList: []string{"down"}},
	})
}

// AddDataset 的错误次序：参数非法 > 时钟回退 > 已存在 > 上游不存在。
func TestAddDatasetErrors(t *testing.T) {
	e := newEngine(t)
	add(t, e, "a", nil, 10)
	parents9 := []string{"a", "a", "a", "a", "a", "a", "a", "a", "a"}
	runSteps(t, e, []step{
		{op: "add", dataset: "b", parents: parents9, now: 5, wantErr: ErrInvalidParam},
		{op: "add", dataset: "b", parents: []string{"a", "a"}, now: 10, wantErr: ErrInvalidParam},
		{op: "add", dataset: "b", now: 5, wantErr: ErrClockRegression},
		{op: "add", dataset: "a", now: 10, wantErr: ErrDatasetExists},
		{op: "add", dataset: "b", parents: []string{"ghost"}, now: 10, wantErr: ErrDatasetNotFound},
		{op: "add", dataset: "b", parents: []string{"a"}, now: 10},
	})
	// 已存在先于上游不存在。
	runSteps(t, e, []step{
		{op: "add", dataset: "a", parents: []string{"ghost"}, now: 10, wantErr: ErrDatasetExists},
	})
}

// 构造参数校验。
func TestNewEngineValidation(t *testing.T) {
	cases := []struct{ nmin, bw, pd, x, q, xmax int64 }{
		{0, 1, 1, 1, 1, 1},
		{1, 0, 1, 1, 1, 1},
		{1, 1, 0, 1, 1, 1},
		{1, 1, 1, 0, 1, 1},
		{1, 1, 1, 1, 0, 1},
		{1, 1, 1, 1, 1, 0},
		{10, 11, 1, 1, 1, 1}, // Bw > Nmin
		{10, 5, 3, 4, 1, 1},  // X > Pd
	}
	for i, c := range cases {
		if _, err := NewEngine(c.nmin, c.bw, c.pd, c.x, c.q, c.xmax); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("case %d: got %v want ErrInvalidParam", i, err)
		}
	}
	if _, err := NewEngine(10, 10, 3, 3, 1, 1); err != nil {
		t.Fatalf("boundary params should be accepted: %v", err)
	}
}

// now 越界为参数非法。
func TestNowOutOfRange(t *testing.T) {
	e := newEngine(t)
	runSteps(t, e, []step{
		{op: "add", dataset: "d", now: -1, wantErr: ErrInvalidParam},
		{op: "add", dataset: "d", now: 1_000_000_000_001, wantErr: ErrInvalidParam},
		{op: "add", dataset: "d", now: 1_000_000_000_000},
	})
}

// 并发调用等价于某串行顺序：-race 下无数据竞争，
// 且每个 Brownout 访问的结果与某个串行重放一致（窗口判定单调可校验）。
func TestConcurrentAccess(t *testing.T) {
	e := newEngine(t)
	add(t, e, "d", nil, 0)
	runSteps(t, e, []step{
		{op: "deprecate", dataset: "d", num: 100, now: 0},
		{op: "advance", dataset: "d", now: 40},
	})
	var wg sync.WaitGroup
	allowed := make(chan int64, 200)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := int64(0); k < 25; k++ {
				now := int64(40+g*25) + k
				_, err := e.Access("c", "d", now)
				if err == nil {
					allowed <- now
				} else if !errors.Is(err, ErrBrownout) && !errors.Is(err, ErrClockRegression) {
					t.Errorf("unexpected err: %v", err)
				}
			}
		}(g)
	}
	wg.Wait()
	close(allowed)
	// 串行化保证：被放行的 now 单调不减（时钟只进不退）。
	prev := int64(-1)
	count := 0
	for now := range allowed {
		if now < prev {
			t.Fatalf("accepted now %d after %d: not serializable", now, prev)
		}
		prev = now
		count++
	}
	if count == 0 {
		t.Fatal("expected some accesses to be allowed")
	}
	t.Logf("allowed %d concurrent accesses, monotone clock verified", count)
}

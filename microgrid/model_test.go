package microgrid

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// naive 是按规则逐时隙直写的独立朴素模型，用于与 Controller 差分对照。
// 它不追求性能：备用求和扫描全部预测，推演每次重新排序，
// 以此交叉验证 Controller 的优化路径行为一致。
type naive struct {
	cfg        Config
	soc        int
	current    int
	mode       Mode
	locked     bool
	throughput int
	forecast   map[int]int
	surplus    map[int]int
	plans      map[int]PlanAction
	revLog     []Revocation
}

func newNaive(cfg Config) *naive {
	return &naive{
		cfg:      cfg,
		soc:      cfg.InitialSoC,
		forecast: make(map[int]int),
		surplus:  make(map[int]int),
		plans:    make(map[int]PlanAction),
	}
}

// 朴素备用求和：扫描全部预测，开销随预测总长度增长。
func (n *naive) reserveNeed(slot int) int {
	need := 0
	for s, v := range n.forecast {
		if s > slot && s <= slot+n.cfg.ReserveHorizon {
			need += v
		}
	}
	return need
}

func (n *naive) check(slot int, a PlanAction, soc *int) (ErrKind, bool) {
	fc, hasFc := n.forecast[slot]
	if n.locked && a.Action == ActionDischarge && a.Amount > 0 &&
		!(n.mode == ModeIsland && a.Amount <= fc) {
		return ErrMaintenanceLock, true
	}
	if n.mode == ModeIsland {
		if a.Action == ActionDischarge && a.Amount > fc {
			return ErrModeNotAllowed, true
		}
		if a.Action == ActionCharge && a.Amount > n.surplus[slot] {
			return ErrModeNotAllowed, true
		}
	}
	next := applyAction(n.cfg, *soc, a)
	if next < n.cfg.MinSoC || next > n.cfg.MaxSoC {
		return ErrOutOfBounds, true
	}
	*soc = next
	if next-n.cfg.MinSoC < n.reserveNeed(slot) {
		return ErrReserveShortfall, true
	}
	if !hasFc {
		return ErrForecastMissing, true
	}
	return 0, false
}

func (n *naive) sortedSlots() []int {
	ss := make([]int, 0, len(n.plans))
	for s := range n.plans {
		ss = append(ss, s)
	}
	sort.Ints(ss)
	return ss
}

func (n *naive) revalidate(reason RevokeReason) []int {
	soc := n.soc
	cut := -1
	for _, s := range n.sortedSlots() {
		if _, bad := n.check(s, n.plans[s], &soc); bad {
			cut = s
			break
		}
	}
	if cut < 0 {
		return nil
	}
	var revoked []int
	for _, s := range n.sortedSlots() {
		if s >= cut {
			delete(n.plans, s)
			n.revLog = append(n.revLog, Revocation{Seq: len(n.revLog), Slot: s, Reason: reason})
			revoked = append(revoked, s)
		}
	}
	return revoked
}

func (n *naive) SubmitPlan(start int, acts []PlanAction) error {
	if len(acts) == 0 {
		return &RejectError{Kind: ErrInvalidParam, Slot: start, Msg: "计划为空"}
	}
	if start <= n.current {
		return &RejectError{Kind: ErrInvalidParam, Slot: start, Msg: "起始时隙不是未来时隙"}
	}
	for i, a := range acts {
		if msg := validateAction(n.cfg, a); msg != "" {
			return &RejectError{Kind: ErrInvalidParam, Slot: start + i, Msg: msg}
		}
	}
	end := start + len(acts) - 1
	merged := make(map[int]PlanAction, len(n.plans)+len(acts))
	for s, a := range n.plans {
		if s < start || s > end {
			merged[s] = a
		}
	}
	for i, a := range acts {
		merged[start+i] = a
	}
	soc := n.soc
	ss := make([]int, 0, len(merged))
	for s := range merged {
		ss = append(ss, s)
	}
	sort.Ints(ss)
	for _, s := range ss {
		if kind, bad := n.check(s, merged[s], &soc); bad {
			return &RejectError{Kind: kind, Slot: s, Msg: "推演不满足接受条件"}
		}
	}
	for s := range n.plans {
		if s >= start && s <= end {
			delete(n.plans, s)
		}
	}
	for i, a := range acts {
		n.plans[start+i] = a
	}
	return nil
}

func (n *naive) UpdateForecast(start int, values []int) ([]int, error) {
	if len(values) == 0 {
		return nil, &RejectError{Kind: ErrInvalidParam, Slot: start, Msg: "预测为空"}
	}
	if start <= n.current {
		return nil, &RejectError{Kind: ErrInvalidParam, Slot: start, Msg: "只能更新未来时隙"}
	}
	for i, v := range values {
		if v < 0 {
			return nil, &RejectError{Kind: ErrInvalidParam, Slot: start + i, Msg: "预测值为负"}
		}
	}
	for i, v := range values {
		n.forecast[start+i] = v
	}
	return n.revalidate(ReasonForecastUpdate), nil
}

func (n *naive) RecordActual(slot int, action Action, amount int) ([]int, error) {
	if slot < 0 {
		return nil, &RejectError{Kind: ErrInvalidParam, Slot: slot, Msg: "时隙编号非法"}
	}
	if !action.valid() || amount < 0 || (action == ActionIdle && amount != 0) {
		return nil, &RejectError{Kind: ErrInvalidParam, Slot: slot, Msg: "实际电量非法"}
	}
	if slot != n.current {
		return nil, &RejectError{Kind: ErrSlotMismatch, Slot: slot, Msg: "只能登记当前时隙"}
	}
	a := PlanAction{Action: action, Amount: amount}
	next := applyAction(n.cfg, n.soc, a)
	if next < n.cfg.MinSoC || next > n.cfg.MaxSoC {
		return nil, &RejectError{Kind: ErrOutOfBounds, Slot: slot, Msg: "实际值使荷电越界"}
	}
	planned := n.plans[slot] // 无计划时按闲置计
	delete(n.plans, slot)
	delete(n.forecast, slot)
	delete(n.surplus, slot)
	n.soc = next
	if action == ActionDischarge {
		n.throughput += amount
	}
	var revoked []int
	if !n.locked && n.throughput >= n.cfg.MaintenanceThreshold {
		n.locked = true
		revoked = append(revoked, n.revokeLockedDischarges()...)
	}
	if d := signedAmount(a) - signedAmount(planned); d > n.cfg.DeviationTolerance || -d > n.cfg.DeviationTolerance {
		revoked = append(revoked, n.revalidate(ReasonDeviation)...)
	}
	n.current++
	return revoked, nil
}

func (n *naive) revokeLockedDischarges() []int {
	var revoked []int
	for _, s := range n.sortedSlots() {
		a := n.plans[s]
		if a.Action == ActionDischarge && a.Amount > 0 &&
			!(n.mode == ModeIsland && a.Amount <= n.forecast[s]) {
			delete(n.plans, s)
			n.revLog = append(n.revLog, Revocation{Seq: len(n.revLog), Slot: s, Reason: ReasonMaintenance})
			revoked = append(revoked, s)
		}
	}
	return append(revoked, n.revalidate(ReasonMaintenance)...)
}

func (n *naive) SetMode(m Mode) ([]int, error) {
	if m != ModeGrid && m != ModeIsland {
		return nil, &RejectError{Kind: ErrInvalidParam, Slot: -1, Msg: "未知运行模式"}
	}
	if m == n.mode {
		return nil, nil
	}
	n.mode = m
	if m == ModeIsland {
		return n.revalidate(ReasonIslandSwitch), nil
	}
	return nil, nil
}

func (n *naive) RegisterSurplus(slot, amount int) ([]int, error) {
	if slot <= n.current {
		return nil, &RejectError{Kind: ErrInvalidParam, Slot: slot, Msg: "只能登记未来时隙"}
	}
	if amount < 0 {
		return nil, &RejectError{Kind: ErrInvalidParam, Slot: slot, Msg: "盈余为负"}
	}
	n.surplus[slot] = amount
	if n.mode == ModeIsland {
		return n.revalidate(ReasonSurplusUpdate), nil
	}
	return nil, nil
}

func (n *naive) CompleteMaintenance() {
	n.throughput = 0
	n.locked = false
}

// system 抽象被对照的两个实现。
type system interface {
	UpdateForecast(start int, values []int) ([]int, error)
	SubmitPlan(start int, acts []PlanAction) error
	RecordActual(slot int, action Action, amount int) ([]int, error)
	SetMode(m Mode) ([]int, error)
	RegisterSurplus(slot, amount int) ([]int, error)
	CompleteMaintenance()
}

func randomConfig(rng *rand.Rand) Config {
	cfg := Config{
		Capacity:             20 + rng.Intn(40),
		MinSoC:               rng.Intn(5),
		MaxChargePerSlot:     1 + rng.Intn(8),
		MaxDischargePerSlot:  1 + rng.Intn(8),
		ChargeLossPermille:   rng.Intn(6) * 100,
		MaintenanceThreshold: 5 + rng.Intn(60),
		ReserveHorizon:       rng.Intn(4),
		DeviationTolerance:   rng.Intn(4),
	}
	cfg.MaxSoC = cfg.Capacity - rng.Intn(3)
	if cfg.MaxSoC < cfg.MinSoC {
		cfg.MaxSoC = cfg.MinSoC
	}
	cfg.InitialSoC = cfg.MinSoC + rng.Intn(cfg.MaxSoC-cfg.MinSoC+1)
	return cfg
}

// genOp 依据朴素模型的当前状态生成一条随机操作及其描述。
func genOp(rng *rand.Rand, nv *naive) (string, func(system) ([]int, error)) {
	cur := nv.current
	switch roll := rng.Intn(100); {
	case roll < 30: // 预测更新
		start := cur + 1 + rng.Intn(6)
		vals := make([]int, 1+rng.Intn(5))
		for i := range vals {
			vals[i] = rng.Intn(9)
		}
		return fmt.Sprintf("forecast start=%d vals=%v", start, vals),
			func(s system) ([]int, error) { return s.UpdateForecast(start, vals) }
	case roll < 55: // 提交计划
		start := cur + 1 + rng.Intn(6)
		acts := make([]PlanAction, 1+rng.Intn(5))
		for i := range acts {
			switch a := Action(rng.Intn(3)); a {
			case ActionCharge:
				acts[i] = PlanAction{a, rng.Intn(nv.cfg.MaxChargePerSlot + 3)}
			case ActionDischarge:
				acts[i] = PlanAction{a, rng.Intn(nv.cfg.MaxDischargePerSlot + 3)}
			default:
				acts[i] = PlanAction{a, 0}
			}
		}
		return fmt.Sprintf("plan start=%d acts=%v", start, acts),
			func(s system) ([]int, error) { return nil, s.SubmitPlan(start, acts) }
	case roll < 75: // 登记实际值
		slot := cur
		if rng.Intn(10) == 0 {
			slot = cur + rng.Intn(3) - 1 // 偶尔错误时隙
		}
		var a PlanAction
		switch r := rng.Intn(10); {
		case r < 2:
			a = PlanAction{ActionIdle, 0}
		case r < 8:
			if p, ok := nv.plans[cur]; ok {
				a = p
				if rng.Intn(3) == 0 {
					a.Amount += rng.Intn(5) // 制造偏差
				}
			} else {
				a = PlanAction{ActionIdle, 0}
			}
		default:
			act := Action(rng.Intn(3))
			amt := rng.Intn(4)
			if act == ActionIdle {
				amt = 0
			}
			a = PlanAction{act, amt}
		}
		return fmt.Sprintf("actual slot=%d act=%v", slot, a),
			func(s system) ([]int, error) { return s.RecordActual(slot, a.Action, a.Amount) }
	case roll < 85: // 模式切换
		m := Mode(rng.Intn(2))
		return fmt.Sprintf("mode=%v", m),
			func(s system) ([]int, error) { return s.SetMode(m) }
	case roll < 95: // 登记盈余
		slot := cur + 1 + rng.Intn(6)
		amount := rng.Intn(9)
		return fmt.Sprintf("surplus slot=%d amount=%d", slot, amount),
			func(s system) ([]int, error) { return s.RegisterSurplus(slot, amount) }
	default: // 维护完成
		return "maintenance",
			func(s system) ([]int, error) { s.CompleteMaintenance(); return nil, nil }
	}
}

func sameError(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	var ra, rb *RejectError
	if !errors.As(a, &ra) || !errors.As(b, &rb) {
		return false
	}
	return ra.Kind == rb.Kind && ra.Slot == rb.Slot
}

func assertSameState(t *testing.T, c *Controller, nv *naive) {
	t.Helper()
	snap := c.Snapshot()
	if snap.SoC != nv.soc || snap.Current != nv.current || snap.Mode != nv.mode ||
		snap.Locked != nv.locked || snap.Throughput != nv.throughput {
		t.Fatalf("状态分叉: snap=%+v naive=%+v", snap, nv)
	}
	if !reflect.DeepEqual(snap.Accepted, nv.plans) {
		t.Fatalf("计划分叉: controller=%v naive=%v", snap.Accepted, nv.plans)
	}
	if !reflect.DeepEqual(c.fc.forecast, nv.forecast) {
		t.Fatalf("预测分叉: controller=%v naive=%v", c.fc.forecast, nv.forecast)
	}
	if !reflect.DeepEqual(c.fc.surplus, nv.surplus) {
		t.Fatalf("盈余分叉: controller=%v naive=%v", c.fc.surplus, nv.surplus)
	}
	if !reflect.DeepEqual(c.Revocations(), nv.revLog) {
		t.Fatalf("撤销记录分叉: controller=%v naive=%v", c.Revocations(), nv.revLog)
	}
}

// 与朴素模型对照大量随机操作序列，逐步比对输出、撤销与全部状态。
// 日志打印每条输入、输出与判定依据（go test -v 可见）。
func TestRandomizedDifferential(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			cfg := randomConfig(rng)
			real, err := NewController(cfg)
			if err != nil {
				t.Fatalf("配置非法: %v", err)
			}
			nv := newNaive(cfg)
			t.Logf("cfg=%+v", cfg)
			for step := 0; step < 300; step++ {
				desc, apply := genOp(rng, nv)
				gotReal, errReal := apply(real)
				gotNaive, errNaive := apply(nv)
				if !sameError(errReal, errNaive) || !reflect.DeepEqual(gotReal, gotNaive) {
					t.Fatalf("步骤 %d 操作 %s 结果分叉: controller=(%v,%v) naive=(%v,%v)",
						step, desc, gotReal, errReal, gotNaive, errNaive)
				}
				t.Logf("step=%d op=%s -> revoked=%v err=%v soc=%d current=%d locked=%v throughput=%d",
					step, desc, gotReal, errReal, nv.soc, nv.current, nv.locked, nv.throughput)
				assertSameState(t, real, nv)
			}
		})
	}
}

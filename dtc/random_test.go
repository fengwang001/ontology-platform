package dtc_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/dtc"
	"ontology/dtctest"
)

// randomConfig 生成合法但随机化的配置。
func randomConfig(r *rand.Rand) dtc.Config {
	rise := 1 + r.Intn(4)
	fall := 1 + r.Intn(4)
	heal := 1 + r.Intn(3)
	return dtc.Config{
		DebounceRiseStep:  rise,
		DebounceFailLimit: rise * (1 + r.Intn(3)),
		DebounceFallStep:  fall,
		DebouncePassLimit: -fall * (1 + r.Intn(3)),
		ConfirmCycles:     1 + r.Intn(3),
		HealWarmUpCycles:  heal,
		AutoClearWarmUps:  heal + r.Intn(4),
		WarmUpTempRise:    r.Intn(31),
		WarmUpFinalTemp:   40 + r.Intn(51),
	}
}

func errKind(err error) string {
	if err == nil {
		return "ok"
	}
	var de *dtc.Error
	if errors.As(err, &de) {
		return de.Kind.String()
	}
	return fmt.Sprintf("non-dtc error: %v", err)
}

// TestRandomSequencesVsNaiveModel 用大量随机事件序列对照生产实现与独立朴素模型，
// 逐步比对错误类别与全部故障码快照，日志打印输入、输出与判定依据。
func TestRandomSequencesVsNaiveModel(t *testing.T) {
	const seeds = 100
	const steps = 500

	for seed := int64(1); seed <= seeds; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			cfg := randomConfig(r)

			mgr, err := dtc.NewManager(cfg)
			if err != nil {
				t.Fatalf("NewManager: %v", err)
			}
			model, err := dtctest.NewModel(cfg)
			if err != nil {
				t.Fatalf("NewModel: %v", err)
			}

			nCodes := 2 + r.Intn(3)
			codes := make([]int, 0, nCodes)
			passProb := map[int]int{}
			for i := 1; i <= nCodes; i++ {
				sev := 1 + r.Intn(3)
				if err := mgr.RegisterDTC(i, sev); err != nil {
					t.Fatal(err)
				}
				if err := model.RegisterDTC(i, sev); err != nil {
					t.Fatal(err)
				}
				codes = append(codes, i)
				passProb[i] = r.Intn(101) // 每个码随机通过率，覆盖常坏/常好/抖动
			}
			t.Logf("config=%+v codes=%v passProb=%v", cfg, codes, passProb)

			ignitionOn := false
			var time, odo int64
			temp := int64(20)

			for step := 0; step < steps; step++ {
				ev, accepted := genRandomEvent(r, cfg, codes, passProb, ignitionOn, time, odo, &temp)
				errMgr := mgr.Handle(ev)
				errModel := model.Handle(ev)
				if errKind(errMgr) != errKind(errModel) {
					t.Fatalf("step %d: error mismatch on %+v: manager=%s model=%s",
						step, ev, errKind(errMgr), errKind(errModel))
				}
				if errMgr == nil {
					time, odo = ev.Time, ev.Odometer
					switch ev.Kind {
					case dtc.EventIgnitionOn:
						ignitionOn = true
					case dtc.EventIgnitionOff:
						ignitionOn = false
					}
					accepted = true
				}
				t.Logf("step=%d ev=%+v -> %s (accepted=%v)", step, ev, errKind(errMgr), accepted)

				// 逐步比对全部故障码快照。
				want := model.Snapshots()
				for _, code := range codes {
					got, err := mgr.Snapshot(code)
					if err != nil {
						t.Fatalf("step %d: Snapshot(%d): %v", step, code, err)
					}
					if got != want[code] {
						t.Fatalf("step %d: snapshot mismatch code=%d\nevent=%+v\ngot =%+v\nwant=%+v",
							step, code, ev, got, want[code])
					}
				}
				if step%20 == 0 {
					t.Logf("step=%d snapshots=%v", step, want)
				}
			}
		})
	}
}

// genRandomEvent 按当前模拟状态生成一个事件（多数合法，少数故意非法）。
func genRandomEvent(r *rand.Rand, cfg dtc.Config, codes []int, passProb map[int]int,
	ignitionOn bool, time, odo int64, temp *int64) (dtc.Event, bool) {

	advance := func() (int64, int64) {
		return time + int64(r.Intn(3)), odo + int64(r.Intn(8))
	}
	roll := r.Intn(100)

	// 5% 概率制造时刻回退（若可能）。
	if roll < 5 && time > 0 {
		kind := dtc.EventIgnitionOn
		if ignitionOn {
			kind = dtc.EventMonitorResult
		}
		return dtc.Event{Kind: kind, Time: time - 1, Odometer: odo, DTCCode: codes[0]}, false
	}
	// 3% 概率制造非法参数。
	if roll >= 5 && roll < 8 {
		return dtc.Event{Kind: dtc.EventIgnitionOn, Time: -1, Odometer: odo}, false
	}

	tm, od := advance()
	if !ignitionOn {
		switch {
		case roll < 60:
			return dtc.Event{Kind: dtc.EventIgnitionOn, Time: tm, Odometer: od}, true
		case roll < 70:
			return dtc.Event{Kind: dtc.EventScanToolClear, Time: tm, Odometer: od}, true
		case roll < 80:
			return dtc.Event{Kind: dtc.EventIgnitionOff, Time: tm, Odometer: od}, false
		case roll < 90:
			return dtc.Event{Kind: dtc.EventMonitorResult, DTCCode: codes[r.Intn(len(codes))], Time: tm, Odometer: od}, false
		default:
			return dtc.Event{Kind: dtc.EventEnvironmentSample, Speed: int64(r.Intn(200)), CoolantTemp: *temp, Time: tm, Odometer: od}, false
		}
	}

	switch {
	case roll < 45:
		code := codes[r.Intn(len(codes))]
		return dtc.Event{
			Kind:     dtc.EventMonitorResult,
			DTCCode:  code,
			Passed:   r.Intn(100) < passProb[code],
			Time:     tm,
			Odometer: od,
		}, true
	case roll < 50:
		return dtc.Event{Kind: dtc.EventMonitorResult, DTCCode: 9000 + r.Intn(1000), Time: tm, Odometer: od}, false
	case roll < 68:
		*temp += int64(r.Intn(21)) - 8 // 温度随机游走，偏升温
		return dtc.Event{Kind: dtc.EventEnvironmentSample, Speed: int64(r.Intn(200)), CoolantTemp: *temp, Time: tm, Odometer: od}, true
	case roll < 85:
		return dtc.Event{Kind: dtc.EventIgnitionOff, Time: tm, Odometer: od}, true
	case roll < 92:
		return dtc.Event{Kind: dtc.EventIgnitionOn, Time: tm, Odometer: od}, false
	default:
		return dtc.Event{Kind: dtc.EventScanToolClear, Time: tm, Odometer: od}, false
	}
}

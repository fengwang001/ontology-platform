package naive_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/calendar"
	"ontology/domain"
	"ontology/naive"
	"ontology/system"
)

func diffConfigs() map[domain.Category]domain.Config {
	return map[domain.Category]domain.Config{
		domain.CatBoiler:         {PeriodMonths: 12, EarlyWindowDays: 30, MinUnsealDays: 10, WarnAheadDays: 60},
		domain.CatPressureVessel: {PeriodMonths: 24, EarlyWindowDays: 45, MinUnsealDays: 15, WarnAheadDays: 90},
		domain.CatSafetyValve:    {PeriodMonths: 6, EarlyWindowDays: 15, MinUnsealDays: 5, WarnAheadDays: 30},
		domain.CatPressureGauge:  {PeriodMonths: 3, EarlyWindowDays: 10, MinUnsealDays: 3, WarnAheadDays: 20},
	}
}

var (
	deviceIDs = []string{"D0", "D1", "D2", "D3", "D4", "D5"}
	accIDs    = []string{"A0", "A1", "A2", "A3", "A4", "A5", "A6", "A7", "A8", "A9", "A10", "A11"}
	ghostIDs  = []string{"X0", "X1"} // 永不登记的编号
)

func devCat(id string) domain.Category {
	if id[len(id)-1]%2 == 0 {
		return domain.CatBoiler
	}
	return domain.CatPressureVessel
}

func accCat(id string) domain.Category {
	if id[len(id)-1]%3 == 0 {
		return domain.CatPressureGauge
	}
	return domain.CatSafetyValve
}

func kindOf(err error) string {
	if err == nil {
		return "<nil>"
	}
	if de, ok := err.(*domain.Error); ok {
		return de.Kind.String()
	}
	return fmt.Sprintf("%T", err)
}

// denialKey 提取判定依据的关键字段用于对照（Detail 文案允许两实现不同）。
func denialKey(d *system.UseDenial) string {
	if d == nil {
		return "<nil>"
	}
	return d.Reason + "/" + d.AccID
}

func warnKey(es []system.WarnEntry) string {
	s := ""
	for _, e := range es {
		s += fmt.Sprintf("%s|%s|%d|%v;", e.ID, e.Cat, e.Expiry, e.Via)
	}
	return s
}

// TestDifferential 在大量随机操作序列下对照正式实现与朴素模型，
// 日志打印每步的输入、输出与判定依据。
func TestDifferential(t *testing.T) {
	base := calendar.FromCivil(2024, 1, 1)
	for _, seed := range []int64{1, 2, 3, 42, 777, 20241006} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			sys, err := system.New(diffConfigs())
			if err != nil {
				t.Fatalf("system.New: %v", err)
			}
			nav := naive.New(diffConfigs())
			cur := base
			nextDate := func() int {
				if rng.Intn(100) < 6 { // 偶发日期回退
					cur -= rng.Intn(15)
				} else {
					cur += rng.Intn(4)
				}
				return cur
			}
			pickDev := func() string { return deviceIDs[rng.Intn(len(deviceIDs))] }
			pickAcc := func() string { return accIDs[rng.Intn(len(accIDs))] }
			pickAny := func() string {
				switch rng.Intn(4) {
				case 0:
					return pickDev()
				case 1:
					return pickAcc()
				default:
					if rng.Intn(10) == 0 {
						return ghostIDs[rng.Intn(len(ghostIDs))]
					}
					if rng.Intn(2) == 0 {
						return pickDev()
					}
					return pickAcc()
				}
			}

			const steps = 5000
			for i := 0; i < steps; i++ {
				op := rng.Intn(100)
				date := nextDate()
				var desc string
				var sysErr, navErr error
				switch {
				case op < 14: // 登记
					var id string
					var cat domain.Category
					if rng.Intn(2) == 0 {
						id = pickDev()
						cat = devCat(id)
					} else {
						id = pickAcc()
						cat = accCat(id)
					}
					desc = fmt.Sprintf("Register(%s,%s,%d)", id, cat, date)
					sysErr = sys.Register(id, cat, date)
					navErr = nav.Register(id, cat, date)
				case op < 38: // 检验
					id := pickAny()
					var res domain.InspectResult
					rect := 0
					switch rng.Intn(6) {
					case 0, 1, 2:
						res = domain.ResultPass
					case 3:
						res = domain.ResultConditional
						rect = 1 + rng.Intn(60)
					case 4:
						res = domain.ResultFail
					default: // 偶发非法参数
						res = domain.ResultConditional
						rect = 0
					}
					desc = fmt.Sprintf("Inspect(%s,%d,%s,%d)", id, date, res, rect)
					sysErr = sys.Inspect(id, date, res, rect)
					navErr = nav.Inspect(id, date, res, rect)
				case op < 46: // 封存
					id := pickAny()
					desc = fmt.Sprintf("Seal(%s,%d)", id, date)
					sysErr = sys.Seal(id, date)
					navErr = nav.Seal(id, date)
				case op < 54: // 启封
					id := pickAny()
					desc = fmt.Sprintf("Unseal(%s,%d)", id, date)
					sysErr = sys.Unseal(id, date)
					navErr = nav.Unseal(id, date)
				case op < 66: // 挂接/转移
					devID, accID := pickDev(), pickAcc()
					desc = fmt.Sprintf("Attach(%s,%s,%d)", devID, accID, date)
					sysErr = sys.Attach(devID, accID, date)
					navErr = nav.Attach(devID, accID, date)
				case op < 72: // 摘除
					id := pickAcc()
					desc = fmt.Sprintf("Detach(%s,%d)", id, date)
					sysErr = sys.Detach(id, date)
					navErr = nav.Detach(id, date)
				case op < 82: // 使用登记
					id := pickDev()
					desc = fmt.Sprintf("RegisterUse(%s,%d)", id, date)
					sOK, sDeny, sErr := sys.RegisterUse(id, date)
					nOK, nDeny, nErr := nav.RegisterUse(id, date)
					sysErr, navErr = sErr, nErr
					if sOK != nOK || denialKey(sDeny) != denialKey(nDeny) {
						t.Fatalf("步骤 %d %s 判定不一致: system=(%v,%s) naive=(%v,%s)",
							i, desc, sOK, denialKey(sDeny), nOK, denialKey(nDeny))
					}
					t.Logf("步骤 %d 输入=%s 输出=ok:%v 判定依据=%s 错误=%s", i, desc, sOK, denialKey(sDeny), kindOf(sErr))
				case op < 88: // 可使用性查询
					id := pickDev()
					desc = fmt.Sprintf("Usable(%s,%d)", id, date)
					sOK, sDeny, sErr := sys.Usable(id, date)
					nOK, nDeny, nErr := nav.Usable(id, date)
					sysErr, navErr = sErr, nErr
					if sOK != nOK || denialKey(sDeny) != denialKey(nDeny) {
						t.Fatalf("步骤 %d %s 判定不一致: system=(%v,%s) naive=(%v,%s)",
							i, desc, sOK, denialKey(sDeny), nOK, denialKey(nDeny))
					}
					t.Logf("步骤 %d 输入=%s 输出=ok:%v 判定依据=%s 错误=%s", i, desc, sOK, denialKey(sDeny), kindOf(sErr))
				case op < 94: // 预警查询
					desc = fmt.Sprintf("Warn(%d)", date)
					sW, sErr := sys.Warn(date)
					nW, nErr := nav.Warn(date)
					sysErr, navErr = sErr, nErr
					if !reflect.DeepEqual(sW, nW) {
						t.Fatalf("步骤 %d %s 预警不一致:\nsystem=%s\nnaive =%s", i, desc, warnKey(sW), warnKey(nW))
					}
					t.Logf("步骤 %d 输入=%s 输出=%d 条: %s", i, desc, len(sW), warnKey(sW))
				default: // 报废
					id := pickAny()
					desc = fmt.Sprintf("Scrap(%s,%d)", id, date)
					sysErr = sys.Scrap(id, date)
					navErr = nav.Scrap(id, date)
				}
				if kindOf(sysErr) != kindOf(navErr) {
					t.Fatalf("步骤 %d %s 错误类别不一致: system=%s naive=%s",
						i, desc, kindOf(sysErr), kindOf(navErr))
				}
				if op < 66 || op >= 94 { // 纯错误类操作在此统一记日志
					t.Logf("步骤 %d 输入=%s 输出=错误:%s", i, desc, kindOf(sysErr))
				}
			}
		})
	}
}

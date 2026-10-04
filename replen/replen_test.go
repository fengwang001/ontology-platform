package replen_test

import (
	"errors"
	"fmt"
	"testing"

	"ontology/replen"
	"ontology/task"
)

type taskRow struct {
	id   int64
	qty  int64
	kind task.Kind
}

func rows(ts []*replen.TaskView) []taskRow {
	out := make([]taskRow, len(ts))
	for i, t := range ts {
		out[i] = taskRow{t.ID, t.Qty, t.Kind}
	}
	return out
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func expectErr(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want %v, got %v", target, err)
	}
}

func expectRows(t *testing.T, e *replen.Engine, want []taskRow) {
	t.Helper()
	got := rows(e.Tasks())
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("tasks want %v, got %v", want, got)
	}
}

// TestManualExample 覆盖题目给出的完整走查。
func TestManualExample(t *testing.T) {
	e := replen.New()
	must(t, e.AddSlot("K1", "S1", 10, 40, 60, 12, 15))
	must(t, e.AddReserve("S1", 100))
	if len(e.Tasks()) != 0 {
		t.Fatalf("AddSlot eff=15>10 不触发")
	}
	must(t, e.Pick("K1", 5))
	expectRows(t, e, []taskRow{{1, 24, task.Regular}})
	must(t, e.Pick("K1", 8))
	if len(e.Tasks()) != 1 {
		t.Fatalf("在途阻止重复触发, got %v", rows(e.Tasks()))
	}
	res, err := e.Demand("K1", 30)
	must(t, err)
	if fmt.Sprint(res.Upgraded) != "[1]" || res.Created == nil ||
		res.Created.ID != 2 || res.Created.Qty != 12 {
		t.Fatalf("Demand(30) 不符: %+v", res)
	}
	expectRows(t, e, []taskRow{{1, 24, task.Urgent}, {2, 12, task.Urgent}})
	must(t, e.Confirm(1, 20))
	sl := e.Slot("K1")
	if sl.OnHand != 22 || sl.InTransit != 12 {
		t.Fatalf("Confirm 后 onHand=%d inTransit=%d", sl.OnHand, sl.InTransit)
	}
	if e.Reserve("S1") != 76 {
		t.Fatalf("储备按任务量全额扣减, want 76 got %d", e.Reserve("S1"))
	}
	must(t, e.Confirm(2, 12))
	if e.Slot("K1").OnHand != 34 {
		t.Fatalf("最终 onHand=34")
	}
}

func TestTriggers(t *testing.T) {
	type tc struct {
		name string
		run  func(t *testing.T, e *replen.Engine)
	}
	cases := []tc{
		{"eff恰等min触发", func(t *testing.T, e *replen.Engine) {
			must(t, e.AddSlot("L", "S", 10, 40, 60, 12, 11))
			must(t, e.AddReserve("S", 100))
			must(t, e.Pick("L", 1))
			expectRows(t, e, []taskRow{{1, 24, task.Regular}})
		}},
		{"eff大1不触发", func(t *testing.T, e *replen.Engine) {
			must(t, e.AddSlot("L", "S", 10, 40, 60, 12, 11))
			must(t, e.AddReserve("S", 100))
			if len(e.Tasks()) != 0 {
				t.Fatalf("onHand=11>min=10 不触发")
			}
		}},
		{"在途阻止重复触发", func(t *testing.T, e *replen.Engine) {
			must(t, e.AddSlot("L", "S", 10, 40, 60, 12, 10))
			must(t, e.AddReserve("S", 100))
			must(t, e.Pick("L", 1))
			must(t, e.Pick("L", 1))
			if len(e.Tasks()) != 1 {
				t.Fatalf("重复触发: %v", rows(e.Tasks()))
			}
		}},
		{"紧急向上取整且受cap封顶", func(t *testing.T, e *replen.Engine) {
			must(t, e.AddSlot("L", "S", 10, 40, 60, 12, 10))
			must(t, e.AddReserve("S", 100))
			must(t, e.Pick("L", 8)) // onHand=2, T1(36), eff=38
			expectRows(t, e, []taskRow{{1, 36, task.Regular}})
			// need=60: 升级 T1 后 onHand2+36=38 <60；新建 want=ceil(22/12)=24，
			// cap 限 floor((60-38)/12)=12，储备限 floor(64/12)=60 -> 取 12
			res, err := e.Demand("L", 60)
			must(t, err)
			if fmt.Sprint(res.Upgraded) != "[1]" {
				t.Fatalf("应升级 T1, got %v", res.Upgraded)
			}
			if res.Created == nil || res.Created.Qty != 12 {
				t.Fatalf("cap 封顶应为 12, got %+v", res.Created)
			}
			// eff=50 <= 60 cap 不变式成立
			if sl := e.Slot("L"); sl.OnHand+sl.InTransit > sl.Cap {
				t.Fatalf("eff 超 cap: %d", sl.OnHand+sl.InTransit)
			}
		}},
		{"紧急受储备封顶", func(t *testing.T, e *replen.Engine) {
			must(t, e.AddSlot("L", "S", 10, 40, 60, 12, 10))
			must(t, e.AddReserve("S", 24))
			must(t, e.Pick("L", 8)) // onHand=2, want=36, avail=floor(24/12)=24
			expectRows(t, e, []taskRow{{1, 24, task.Regular}})
			// T1 占满 24，avail=0；升级后 need=50 仍欠 24，但无储备 -> 不新建
			res, err := e.Demand("L", 50)
			must(t, err)
			if fmt.Sprint(res.Upgraded) != "[1]" || res.Created != nil {
				t.Fatalf("储备占满时不应新建, got %+v", res)
			}
		}},
		{"升级后恰好够用不新建", func(t *testing.T, e *replen.Engine) {
			must(t, e.AddSlot("K1", "S", 10, 40, 60, 12, 15))
			must(t, e.AddReserve("S", 100))
			must(t, e.Pick("K1", 5))
			must(t, e.Pick("K1", 8)) // onHand=2
			res, err := e.Demand("K1", 20)
			must(t, err)
			if fmt.Sprint(res.Upgraded) != "[1]" || res.Created != nil {
				t.Fatalf("升级 T1 后 2+24>=20 即停, got %+v", res)
			}
		}},
		{"want为0不计Starved", func(t *testing.T, e *replen.Engine) {
			must(t, e.AddSlot("L", "S", 10, 20, 60, 12, 10))
			must(t, e.AddReserve("S", 100))
			if e.Slot("L").Starved != 0 || len(e.Tasks()) != 0 {
				t.Fatalf("want=0 应什么都不做")
			}
		}},
		{"avail不足计Starved且AddReserve不触发", func(t *testing.T, e *replen.Engine) {
			must(t, e.AddSlot("L", "S", 10, 40, 60, 12, 11))
			must(t, e.Pick("L", 1)) // onHand=10 恰等 min，无储备
			if e.Slot("L").Starved != 1 || len(e.Tasks()) != 0 {
				t.Fatalf("Starved 应为 1")
			}
			must(t, e.AddReserve("S", 24))
			if e.Slot("L").Starved != 1 {
				t.Fatalf("AddReserve 不触发, Starved 不变")
			}
			must(t, e.AddSlot("L2", "S", 10, 40, 60, 12, 11))
			must(t, e.Pick("L2", 1)) // 吃掉 24 建 T1(24)，avail 归 0
			must(t, e.Pick("L", 1))  // onHand=9，avail=0 -> Starved 再+1
			if e.Slot("L").Starved != 2 {
				t.Fatalf("Starved 应累加到 2, got %d", e.Slot("L").Starved)
			}
		}},
		{"Cancel后连锁新建更大号", func(t *testing.T, e *replen.Engine) {
			must(t, e.AddSlot("L", "S", 10, 40, 60, 12, 10))
			must(t, e.AddReserve("S", 100))
			must(t, e.Pick("L", 1))
			must(t, e.Cancel(1))
			expectRows(t, e, []taskRow{{2, 24, task.Regular}})
		}},
		{"Confirm短补后储备按全额扣且可连锁", func(t *testing.T, e *replen.Engine) {
			must(t, e.AddSlot("L", "S", 10, 40, 60, 12, 10))
			must(t, e.AddReserve("S", 100))
			must(t, e.Pick("L", 1))
			must(t, e.Confirm(1, 12))
			if e.Slot("L").OnHand != 21 || e.Reserve("S") != 76 {
				t.Fatalf("短补: onHand=%d reserve=%d", e.Slot("L").OnHand, e.Reserve("S"))
			}
			must(t, e.Pick("L", 15))
			expectRows(t, e, []taskRow{{2, 24, task.Regular}})
		}},
		{"Confirm到货充足不连锁", func(t *testing.T, e *replen.Engine) {
			must(t, e.AddSlot("L", "S", 10, 40, 60, 12, 10))
			must(t, e.AddReserve("S", 100))
			must(t, e.Pick("L", 9))
			must(t, e.Confirm(1, 36))
			if len(e.Tasks()) != 0 || e.Slot("L").OnHand != 37 {
				t.Fatalf("全额到货不应连锁")
			}
		}},
		{"Demand在onHand足够时无动作", func(t *testing.T, e *replen.Engine) {
			must(t, e.AddSlot("L", "S", 10, 40, 60, 12, 30))
			must(t, e.AddReserve("S", 100))
			res, err := e.Demand("L", 30)
			must(t, err)
			if len(res.Upgraded) != 0 || res.Created != nil || len(e.Tasks()) != 0 {
				t.Fatalf("onHand>=need 应无动作")
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.run(t, replen.New())
		})
	}
}

// TestUpgradeOrderMulti 构造同一库位两条常规任务，验证按号升序升级、恰好即停。
func TestUpgradeOrderMulti(t *testing.T) {
	setup := func(t *testing.T) *replen.Engine {
		e := replen.New()
		// min=100, max=200, cap=1000, c=10；储备每次仅够 1 箱，
		// 使触发后 eff 仍 ≤ min，从而在同库位累计两条常规任务。
		must(t, e.AddSlot("L", "S", 100, 200, 1000, 10, 2))
		must(t, e.AddReserve("S", 10))
		must(t, e.Pick("L", 1)) // onHand=1, T1(10), eff=11
		must(t, e.AddReserve("S", 10))
		must(t, e.Pick("L", 1)) // onHand=0, eff=10<=100 -> T2(10)
		expectRows(t, e, []taskRow{{1, 10, task.Regular}, {2, 10, task.Regular}})
		return e
	}

	t.Run("升级第一条恰好够用即停", func(t *testing.T) {
		e := setup(t)
		res, err := e.Demand("L", 10)
		must(t, err)
		if fmt.Sprint(res.Upgraded) != "[1]" || res.Created != nil {
			t.Fatalf("应只升级 T1, got %+v", res)
		}
		expectRows(t, e, []taskRow{{1, 10, task.Urgent}, {2, 10, task.Regular}})
	})

	t.Run("需要两条时按号升序连续升级", func(t *testing.T) {
		e := setup(t)
		res, err := e.Demand("L", 15)
		must(t, err)
		if fmt.Sprint(res.Upgraded) != "[1 2]" || res.Created != nil {
			t.Fatalf("应按序升级 T1,T2, got %+v", res)
		}
	})
}

// TestRejectOrder 校验拒绝次序：非法 > 不存在 > 状态 > 冲突 > 库存不足 > 超量。
func TestRejectOrder(t *testing.T) {
	type tc struct {
		name string
		call func(e *replen.Engine) error
		want error
	}
	cases := []tc{
		{"AddSlot重复库位为冲突", func(e *replen.Engine) error {
			return e.AddSlot("L", "S", 1, 2, 60, 12, 0)
		}, replen.ErrConflict},
		{"AddReserve未知SKU为不存在", func(e *replen.Engine) error {
			return e.AddReserve("NOPE", 1)
		}, replen.ErrNotFound},
		{"AddReserve超累计上限为超量", func(e *replen.Engine) error {
			return e.AddReserve("S", 49) // 储备 1e12-48，+49 即超 1e12
		}, replen.ErrOverQty},
		{"Pick不存在库位", func(e *replen.Engine) error {
			return e.Pick("NOPE", 1)
		}, replen.ErrNotFound},
		{"Pick库存不足", func(e *replen.Engine) error {
			return e.Pick("M", 2) // M 最终 onHand=1
		}, replen.ErrShortPick},
		{"Demand不存在", func(e *replen.Engine) error {
			_, err := e.Demand("NOPE", 1)
			return err
		}, replen.ErrNotFound},
		{"Demand超过cap为非法", func(e *replen.Engine) error {
			_, err := e.Demand("M", 61)
			return err
		}, replen.ErrInvalid},
		{"Confirm未知任务为不存在", func(e *replen.Engine) error {
			return e.Confirm(999, 0)
		}, replen.ErrNotFound},
		{"Confirm已完成任务为状态不符", func(e *replen.Engine) error {
			return e.Confirm(1, 0)
		}, replen.ErrState},
		{"Cancel未知任务为不存在", func(e *replen.Engine) error {
			return e.Cancel(999)
		}, replen.ErrNotFound},
		{"Confirm短补超任务量为超量", func(e *replen.Engine) error {
			return e.Confirm(2, 49) // T2 量为 48
		}, replen.ErrOverQty},
		{"Cancel已取消任务为状态不符", func(e *replen.Engine) error {
			if err := e.Cancel(2); err != nil { // 第一次取消成功（可能连锁）
				t.Errorf("首次 Cancel 失败: %v", err)
			}
			return e.Cancel(2)
		}, replen.ErrState},
		{"非法参数优先于不存在", func(e *replen.Engine) error {
			return e.Pick("NOPE", 0)
		}, replen.ErrInvalid},
		{"不存在优先于状态/超量", func(e *replen.Engine) error {
			return e.Confirm(999, 100)
		}, replen.ErrNotFound},
	}
	e := replen.New()
	must(t, e.AddSlot("L", "S", 1, 50, 60, 12, 5))
	cases = append([]tc{
		{"AddSlot非法参数", func(e *replen.Engine) error {
			return e.AddSlot("B", "S", 10, 10, 60, 12, 5)
		}, replen.ErrInvalid},
		{"AddSlot箱规越界", func(e *replen.Engine) error {
			return e.AddSlot("B", "S", 1, 2, 60, 0, 0)
		}, replen.ErrInvalid},
	}, cases...)
	// 储备恰好 1e12（单次上限 1e9，分次加入），再 AddReserve(37) 超累计上限。
	for i := 0; i < 1000; i++ {
		must(t, e.AddReserve("S", 1_000_000_000))
	}
	// 构造 T1（已完成）：先补到不触发水位以外——min=1，onHand=5 本就不触发，
	// 直接 Pick 到 0 会 Starved（储备可用被全额占用，avail 计算含未完成任务）。
	// 为得到 T1，先把储备中 1e12 视为可用：Pick(L,5) 后 want=floor(45/12)=36，建 T1。
	must(t, e.Pick("L", 5))
	if ts := e.Tasks(); len(ts) != 1 || ts[0].ID != 1 {
		t.Fatalf("setup T1 失败: %v", rows(ts))
	}
	must(t, e.Confirm(1, 36))
	// 构造 T2（未完成）于另一库位，避免 L 的连锁干扰。
	must(t, e.AddSlot("M", "S", 10, 50, 60, 12, 11))
	must(t, e.Pick("M", 10)) // onHand=1 触发 T2: floor(49/12)*12=48
	if ts := e.Tasks(); len(ts) != 1 || ts[0].ID != 2 || ts[0].Qty != 48 {
		t.Fatalf("setup T2 失败: %v", rows(ts))
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.call(e)
			expectErr(t, err, c.want)
		})
	}
}

// TestRejectedConsumesNoID 被拒绝的操作不占任务号，且状态不改变。
func TestRejectedConsumesNoID(t *testing.T) {
	e := replen.New()
	must(t, e.AddSlot("L", "S", 1, 50, 60, 12, 5))
	must(t, e.AddReserve("S", 100))
	before := e.Slot("L")
	expectErr(t, e.Pick("L", 99), replen.ErrShortPick)
	expectErr(t, e.Pick("", 1), replen.ErrInvalid)
	_, err := e.Demand("NOPE", 1)
	expectErr(t, err, replen.ErrNotFound)
	must(t, e.Pick("L", 5))
	if ts := e.Tasks(); len(ts) != 1 || ts[0].ID != 1 {
		t.Fatalf("拒绝不应占号, got %v", rows(ts))
	}
	if e.Slot("L").Starved != before.Starved {
		t.Fatalf("被拒绝操作不得改变 Starved")
	}
}

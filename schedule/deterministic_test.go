package schedule_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/schedule"
)

func code(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, schedule.ErrInvalid):
		return "invalid"
	case errors.Is(err, schedule.ErrClockBack):
		return "clock"
	case errors.Is(err, schedule.ErrDuplicate):
		return "dup"
	case errors.Is(err, schedule.ErrNotFound):
		return "notfound"
	case errors.Is(err, schedule.ErrNoRoom):
		return "noroom"
	case errors.Is(err, schedule.ErrRoomBusy):
		return "room"
	case errors.Is(err, schedule.ErrSurgeon):
		return "surgeon"
	case errors.Is(err, schedule.ErrEquip):
		var es *schedule.EquipShortError
		if errors.As(err, &es) {
			return "equip:" + es.Type
		}
		return "equip"
	case errors.Is(err, schedule.ErrState):
		return "state"
	default:
		return err.Error()
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func eq[T comparable](t *testing.T, name string, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func setup(t *testing.T) *schedule.Scheduler {
	t.Helper()
	sc := schedule.New()
	must(t, sc.AddRoom(0, "R1", 30))
	must(t, sc.AddRoom(0, "R2", 20))
	must(t, sc.AddEquip(0, "C", 1, 15))
	return sc
}

// TestSpecExample 覆盖题目给出的两组示例。
func TestSpecExample(t *testing.T) {
	sc := setup(t)
	must(t, sc.Book(0, "A", "R1", 480, 120, "u", map[string]int{"C": 1}))

	eq(t, "629 room", code(sc.Book(0, "B", "R1", 629, 10, "x", nil)), "room")
	must(t, sc.Book(0, "B", "R1", 630, 10, "x", nil))

	eq(t, "610 equip", code(sc.Book(0, "C1", "R2", 610, 90, "y", map[string]int{"C": 1})), "equip:C")
	must(t, sc.Book(0, "C2", "R2", 615, 85, "y", map[string]int{"C": 1}))

	// 医生 u：A 为 [480,600)，[600,660) 首尾相接不冲突（另起排程避免与 C2 同间清洁冲突）。
	scD := setup(t)
	must(t, scD.Book(0, "A", "R1", 480, 120, "u", nil))
	must(t, scD.Book(0, "U1", "R2", 600, 60, "u", nil))

	// 急诊示例 1：顶替 R2 的 D。
	sc2 := setup(t)
	must(t, sc2.Book(0, "A", "R1", 480, 120, "u", map[string]int{"C": 1}))
	must(t, sc2.Book(0, "D", "R2", 520, 60, "v", nil))
	res, err := sc2.Emergency(500, "E", 60, "w", nil)
	must(t, err)
	eq(t, "E room", res.Room, "R2")
	eq(t, "E start", res.Start, int64(500))
	eq(t, "E displaced", fmt.Sprint(res.Displaced), "[D]")

	// 急诊示例 2：E 需 C 臂，A 不可顶替 -> 设备不足，D 保持原样。
	sc3 := setup(t)
	must(t, sc3.Book(0, "A", "R1", 480, 120, "u", map[string]int{"C": 1}))
	must(t, sc3.Book(0, "D", "R2", 520, 60, "v", nil))
	_, err = sc3.Emergency(500, "E", 60, "w", map[string]int{"C": 1})
	eq(t, "E equip reject", code(err), "equip:C")
	eq(t, "D untouched", code(sc3.Cancel(500, "D")), "ok")

	// 急诊示例 3：医生 u 与已开始的 A 相交，医生冲突先于设备不足。
	sc4 := setup(t)
	must(t, sc4.Book(0, "A", "R1", 480, 120, "u", map[string]int{"C": 1}))
	must(t, sc4.Book(0, "D", "R2", 520, 60, "v", nil))
	_, err = sc4.Emergency(500, "E", 60, "u", map[string]int{"C": 1})
	eq(t, "E surgeon first", code(err), "surgeon")
	eq(t, "D untouched 2", code(sc4.Cancel(500, "D")), "ok")
}

// TestTableBook 表驱动覆盖各类 Book 判定。
func TestTableBook(t *testing.T) {
	type tc struct {
		name string
		fn   func(sc *schedule.Scheduler) string
		want string
	}
	cases := []tc{
		{"end+turn 恰等相容", func(sc *schedule.Scheduler) string {
			return code(sc.Book(0, "B", "R1", 630, 10, "x", nil))
		}, "ok"},
		{"end+turn 早一分钟冲突", func(sc *schedule.Scheduler) string {
			return code(sc.Book(0, "B", "R1", 629, 10, "x", nil))
		}, "room"},
		{"消毒占用恰到时相容", func(sc *schedule.Scheduler) string {
			return code(sc.Book(0, "B", "R2", 615, 10, "x", map[string]int{"C": 1}))
		}, "ok"},
		{"消毒早一分钟不足", func(sc *schedule.Scheduler) string {
			return code(sc.Book(0, "B", "R2", 614, 10, "x", map[string]int{"C": 1}))
		}, "equip:C"},
		{"医生首尾相接不冲突", func(sc *schedule.Scheduler) string {
			return code(sc.Book(0, "B", "R2", 600, 10, "u", nil))
		}, "ok"},
		{"医生相交冲突", func(sc *schedule.Scheduler) string {
			return code(sc.Book(0, "B", "R2", 599, 10, "u", nil))
		}, "surgeon"},
		{"start<now 非法", func(sc *schedule.Scheduler) string {
			return code(sc.Book(100, "B", "R2", 99, 10, "x", nil))
		}, "invalid"},
		{"dur 超界非法", func(sc *schedule.Scheduler) string {
			return code(sc.Book(0, "B", "R2", 10, 1441, "x", nil))
		}, "invalid"},
		{"end 超 1e9 非法", func(sc *schedule.Scheduler) string {
			return code(sc.Book(0, "B", "R2", 1_000_000_000, 1, "x", nil))
		}, "invalid"},
		{"空编号非法", func(sc *schedule.Scheduler) string {
			return code(sc.Book(0, "", "R2", 700, 10, "x", nil))
		}, "invalid"},
		{"房间不存在", func(sc *schedule.Scheduler) string {
			return code(sc.Book(0, "B", "RX", 700, 10, "x", nil))
		}, "notfound"},
		{"设备类型不存在", func(sc *schedule.Scheduler) string {
			return code(sc.Book(0, "B", "R2", 700, 10, "x", map[string]int{"Z": 1}))
		}, "notfound"},
		{"需求件数超 n 非法", func(sc *schedule.Scheduler) string {
			return code(sc.Book(0, "B", "R2", 700, 10, "x", map[string]int{"C": 2}))
		}, "invalid"},
		{"编号已存在", func(sc *schedule.Scheduler) string {
			return code(sc.Book(0, "A", "R2", 700, 10, "x", nil))
		}, "dup"},
		{"同间冲突先于医生冲突", func(sc *schedule.Scheduler) string {
			return code(sc.Book(0, "B", "R1", 500, 10, "u", nil))
		}, "room"},
		{"医生冲突先于设备不足", func(sc *schedule.Scheduler) string {
			return code(sc.Book(0, "B", "R2", 500, 10, "u", map[string]int{"C": 1}))
		}, "surgeon"},
		{"设备不足", func(sc *schedule.Scheduler) string {
			return code(sc.Book(0, "B", "R2", 500, 10, "z", map[string]int{"C": 1}))
		}, "equip:C"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sc := setup(t)
			must(t, sc.Book(0, "A", "R1", 480, 120, "u", map[string]int{"C": 1}))
			eq(t, c.name, c.fn(sc), c.want)
		})
	}
}

// TestMultiEquipByteOrder 多类型同时不足报字节序最小者。
func TestMultiEquipByteOrder(t *testing.T) {
	sc := schedule.New()
	must(t, sc.AddRoom(0, "R1", 0))
	must(t, sc.AddRoom(0, "R2", 0))
	must(t, sc.AddEquip(0, "b", 1, 0))
	must(t, sc.AddEquip(0, "a", 1, 0))
	must(t, sc.Book(0, "A", "R1", 100, 100, "u", map[string]int{"a": 1, "b": 1}))
	got := code(sc.Book(0, "B", "R2", 100, 100, "v", map[string]int{"b": 1, "a": 1}))
	eq(t, "byte order", got, "equip:a")
}

// TestClockOrder 时钟回退与拒绝次序。
func TestClockOrder(t *testing.T) {
	sc := setup(t)
	must(t, sc.AddRoom(100, "R9", 0))
	eq(t, "clock back", code(sc.AddRoom(50, "R8", 0)), "clock")
	eq(t, "invalid beats clock", code(sc.AddRoom(50, "", 0)), "invalid")
	// 编号重复检测在时钟检查之后（统一次序：非法 > 回退 > 已存在）。
	eq(t, "dup after clock check", code(sc.AddRoom(100, "R9", 0)), "dup")
}

// TestCancel 取消规则与编号复用。
func TestCancel(t *testing.T) {
	sc := setup(t)
	must(t, sc.Book(0, "A", "R1", 480, 120, "u", map[string]int{"C": 1}))
	eq(t, "cancel unknown", code(sc.Cancel(0, "ZZ")), "state")
	eq(t, "cancel started", code(sc.Cancel(480, "A")), "state")
	must(t, sc.Book(100, "B", "R2", 700, 10, "v", nil))
	must(t, sc.Cancel(200, "B"))
	eq(t, "cancel twice", code(sc.Cancel(200, "B")), "state")
	must(t, sc.Book(200, "B", "R2", 700, 10, "v", nil))
	res, err := sc.Emergency(400, "EM", 30, "w", nil)
	must(t, err)
	eq(t, "emergency landed", res.Room != "", true)
	eq(t, "cancel emergency", code(sc.Cancel(400, "EM")), "state")
}

// TestEmergencyTie 选间并列规则。
func TestEmergencyTie(t *testing.T) {
	sc := schedule.New()
	must(t, sc.AddRoom(0, "B", 0))
	must(t, sc.AddRoom(0, "A", 0))
	res, err := sc.Emergency(100, "E", 10, "u", nil)
	must(t, err)
	eq(t, "byte order tie", res.Room, "A")

	sc2 := schedule.New()
	must(t, sc2.AddRoom(0, "A", 0))
	must(t, sc2.AddRoom(0, "B", 0))
	must(t, sc2.Book(0, "a1", "A", 100, 5, "x", nil))
	must(t, sc2.Book(0, "a2", "A", 106, 6, "z", nil))
	must(t, sc2.Book(0, "b1", "B", 100, 5, "y", nil))
	res2, err := sc2.Emergency(50, "E", 60, "u", nil)
	must(t, err)
	eq(t, "fewer displacements", res2.Room, "B")
	eq(t, "displaced b1", fmt.Sprint(res2.Displaced), "[b1]")

	// s_r 只统计不可顶替者：未开始择期不抬高 s_r。
	sc3 := schedule.New()
	must(t, sc3.AddRoom(0, "A", 0))
	must(t, sc3.AddRoom(0, "B", 0))
	must(t, sc3.Book(0, "e1", "A", 200, 10, "x", nil))
	res3, err := sc3.Emergency(100, "E", 10, "u", nil)
	must(t, err)
	eq(t, "s ignores elective", res3.Room, "A")
	eq(t, "s=100", res3.Start, int64(100))
	eq(t, "no displace", fmt.Sprint(res3.Displaced), "[]")

	// 已开始手术抬高 s_r。
	sc4 := schedule.New()
	must(t, sc4.AddRoom(0, "A", 20))
	must(t, sc4.AddRoom(0, "B", 0))
	must(t, sc4.Book(0, "e1", "A", 80, 30, "x", nil)) // [80,110)
	res4, err := sc4.Emergency(100, "E", 10, "u", nil)
	must(t, err)
	eq(t, "B earlier", res4.Room, "B")
}

// TestEmergencySurgeonStep 第二步医生顶替与不可顶替拒绝。
func TestEmergencySurgeonStep(t *testing.T) {
	sc := schedule.New()
	must(t, sc.AddRoom(0, "R1", 0))
	must(t, sc.AddRoom(0, "R2", 0))
	// R1 上 q1；R2 上同医生 q2 可顶替。
	must(t, sc.Book(0, "q1", "R1", 100, 60, "u", nil))
	must(t, sc.Book(0, "q2", "R2", 120, 60, "v", nil))
	res, err := sc.Emergency(50, "E", 100, "v", nil)
	must(t, err)
	eq(t, "land R1", res.Room, "R1")
	eq(t, "step1 then step2", fmt.Sprint(res.Displaced), "[q1 q2]")

	// 与已开始的同医生手术相交 -> 拒绝不留痕。
	sc2 := schedule.New()
	must(t, sc2.AddRoom(0, "R1", 0))
	must(t, sc2.AddRoom(0, "R2", 0))
	must(t, sc2.Book(0, "q1", "R1", 100, 60, "u", nil))
	// R1 的 s 被 q1 顶到 160，故落 R2 的 120；仍与 q1 同医生相交。
	_, err = sc2.Emergency(120, "E", 10, "u", nil)
	eq(t, "immovable surgeon", code(err), "surgeon")
	eq(t, "q1 stays", code(sc2.Cancel(120, "q1")), "state") // 已开始，不能取消，说明仍在
}

// TestEmergencyEquipStep 第三步设备顶替：start 降序、编号降序、类型名升序。
func TestEmergencyEquipStep(t *testing.T) {
	sc := schedule.New()
	must(t, sc.AddRoom(0, "R1", 0))
	must(t, sc.AddEquip(0, "C", 1, 0))
	// 急诊占 R1 [100,110)；R1 内无可顶替，无需顶替间内者。
	// 另有两台可顶替择期在足够远的房间占用 C。
	must(t, sc.AddRoom(0, "R2", 0))
	must(t, sc.Book(0, "c2", "R2", 100, 5, "v", map[string]int{"C": 1}))
	must(t, sc.Book(0, "c1", "R2", 105, 50, "u", map[string]int{"C": 1}))
	res, err := sc.Emergency(90, "E", 20, "w", map[string]int{"C": 1})
	must(t, err)
	eq(t, "equip start desc", fmt.Sprint(res.Displaced), "[c1 c2]")

	// start 并列时编号降序。
	sc2 := schedule.New()
	must(t, sc2.AddRoom(0, "R1", 0))
	must(t, sc2.AddRoom(0, "R2", 0))
	must(t, sc2.AddRoom(0, "R3", 0))
	must(t, sc2.AddEquip(0, "C", 2, 0))
	must(t, sc2.Book(0, "aa", "R2", 100, 50, "u", map[string]int{"C": 1}))
	must(t, sc2.Book(0, "bb", "R3", 100, 50, "v", map[string]int{"C": 1}))
	res2, err := sc2.Emergency(99, "E", 20, "w", map[string]int{"C": 2})
	must(t, err)
	eq(t, "equip id desc", fmt.Sprint(res2.Displaced), "[bb aa]")

	// 全部可顶替者移走后仍不足（不可顶替者占着）-> 拒绝不留痕。
	sc3 := schedule.New()
	must(t, sc3.AddRoom(0, "R1", 0))
	must(t, sc3.AddRoom(0, "R2", 0))
	must(t, sc3.AddEquip(0, "C", 1, 0))
	must(t, sc3.Book(0, "fixed", "R2", 100, 50, "u", map[string]int{"C": 1}))
	_, err = sc3.Emergency(105, "E", 20, "w", map[string]int{"C": 1})
	eq(t, "equip final reject", code(err), "equip:C")
}

// TestEmergencyRejectOrder 急诊拒绝次序。
func TestEmergencyRejectOrder(t *testing.T) {
	sc := setup(t)
	eq(t, "invalid", code(func() error {
		_, e := sc.Emergency(-1, "E", 10, "u", nil)
		return e
	}()), "invalid")
	eq(t, "clock", code(func() error {
		_, e := sc.Emergency(0, "X", 0, "u", nil) // dur=0 非法优先
		return e
	}()), "invalid")
	must(t, sc.Book(100, "A", "R1", 480, 10, "u", nil))
	eq(t, "dup", code(func() error {
		_, e := sc.Emergency(200, "A", 10, "u", nil)
		return e
	}()), "dup")
	eq(t, "type missing", code(func() error {
		_, e := sc.Emergency(200, "E", 10, "u", map[string]int{"ZZ": 1})
		return e
	}()), "notfound")
}

// buildExamined 构造 base 上的 n 台远离窗口的手术，再 Book 一次目标，
// 返回该次 Book 考察到的既有手术数与返回码。
func buildExamined(t *testing.T, n int) (int, string) {
	t.Helper()
	sc := schedule.New()
	must(t, sc.AddRoom(0, "R1", 240))
	must(t, sc.AddRoom(0, "ROUT", 0))
	must(t, sc.AddEquip(0, "C", 1, 240))

	// 目标：R1 [10000,10010)。窗口 [8320,11790)。
	// 窗口内放 3 台与目标可能相关的手术。
	must(t, sc.Book(0, "near1", "R1", 9000, 10, "s1", nil))
	must(t, sc.Book(0, "near2", "ROUT", 9500, 10, "s2", map[string]int{"C": 1}))
	must(t, sc.Book(0, "near3", "R1", 11000, 10, "s3", nil))

	// 窗口外：早于 start-1680-1440 与晚于 start+dur+1680 的手术，
	// 无论多少台都不影响考察数。
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("old%05d", i)
		if i < n/2 {
			must(t, sc.Book(0, id, "ROUT", int64(i), 1, "os"+id, nil))
		} else {
			must(t, sc.Book(0, id, "ROUT", 13000+int64((i-n/2)), 1, "os"+id, nil))
		}
	}

	err := sc.Book(5000, "TARGET", "R1", 10000, 10, "zz", map[string]int{"C": 1})
	return sc.Examined(), code(err)
}

// TestExaminedWindowProof examined 读数不随窗口外手术数增长。
func TestExaminedWindowProof(t *testing.T) {
	n100, c100 := buildExamined(t, 100)
	n10000, c10000 := buildExamined(t, 10000)
	if n100 != n10000 {
		t.Fatalf("examined diverges: 100 -> %d, 10000 -> %d", n100, n10000)
	}
	if c100 != c10000 {
		t.Fatalf("result code diverges: %s vs %s", c100, c10000)
	}
	t.Logf("examined=%d for both 100 and 10000 out-of-window surgeries; code=%s", n100, c100)
}

// TestConcurrentLinearizable 并发结果等价于某个串行顺序：
// 用全局单调 now 且每台手术编号唯一、参数合法，比较并发与串行的
// TestConcurrentLinearizable 并发结果等价于某个串行顺序。
// 按批次屏障并发：第 k 批全部 goroutine 用同一 now，批间 now 严格递增，
// 且每个 goroutine 独占房间与医生，故批内任意串行序都应全部成功。
func TestConcurrentLinearizable(t *testing.T) {
	const goroutines = 16
	const perG = 40

	type op struct {
		now     int64
		id      string
		room    string
		start   int64
		surgeon string
	}
	mkOps := func() []op {
		ops := make([]op, 0, goroutines*perG)
		for k := 0; k < perG; k++ {
			now := int64(1 + k)
			for g := 0; g < goroutines; g++ {
				ops = append(ops, op{
					now:     now,
					id:      fmt.Sprintf("g%02d-%03d", g, k),
					room:    fmt.Sprintf("R%02d", g),
					start:   1000 + int64(k*10),
					surgeon: fmt.Sprintf("surgeon-%02d-%02d", g, k),
				})
			}
		}
		return ops
	}

	run := func(ops []op, parallel bool) map[string]bool {
		sc := schedule.New()
		for g := 0; g < goroutines; g++ {
			must(t, sc.AddRoom(0, fmt.Sprintf("R%02d", g), 0))
		}
		must(t, sc.AddEquip(0, "C", 2, 0))

		ok := map[string]bool{}
		var okMu sync.Mutex
		var wg sync.WaitGroup
		do := func(o op) {
			err := sc.Book(o.now, o.id, o.room, o.start, 4, o.surgeon, nil)
			if err == nil {
				okMu.Lock()
				ok[o.id] = true
				okMu.Unlock()
			}
		}
		if parallel {
			for k := 0; k < perG; k++ {
				for g := 0; g < goroutines; g++ {
					wg.Add(1)
					go func(o op) {
						defer wg.Done()
						do(o)
					}(ops[k*goroutines+g])
				}
				wg.Wait()
			}
		} else {
			for _, o := range ops {
				do(o)
			}
		}
		return ok
	}

	serialOps := mkOps()
	serial := run(serialOps, false)
	for attempt := 0; attempt < 5; attempt++ {
		par := run(serialOps, true)
		if len(par) != len(serial) {
			t.Fatalf("attempt %d: ok count %d vs serial %d", attempt, len(par), len(serial))
		}
		for id := range serial {
			if !par[id] {
				t.Fatalf("attempt %d: %s accepted serially but not in parallel", attempt, id)
			}
		}
	}
	t.Logf("concurrent/serial both accepted %d surgeries", len(serial))
}

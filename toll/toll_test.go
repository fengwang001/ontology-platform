package toll

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

var base = time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func defaultConfig() Config {
	return Config{
		DuplicateWindow:  10 * time.Second,
		BackchargeWindow: time.Hour,
		MonthlyCap:       0,
		Location:         time.UTC,
	}
}

// 多条等费路径按门架序列字典序取最小。
func TestLexicographicTieBreak(t *testing.T) {
	net := NewNetwork()
	must(t, net.AddSegment("S", "B", 10, map[string]int64{"car": 1}))
	must(t, net.AddSegment("B", "E", 10, map[string]int64{"car": 1}))
	must(t, net.AddSegment("S", "A", 10, map[string]int64{"car": 1}))
	must(t, net.AddSegment("A", "Z", 10, map[string]int64{"car": 1}))
	must(t, net.AddSegment("Z", "E", 10, map[string]int64{"car": 1}))
	// S->B->E 费用 20;S->A->Z->E 费用 30;另加一条等费 20 的 S->A->E。
	must(t, net.AddSegment("A", "E", 10, map[string]int64{"car": 1}))
	svc := NewService(net, defaultConfig())
	must(t, svc.RegisterVehicle(at(0), "V1", "car"))
	jid, err := svc.Entry(at(1), "V1", "S", at(1))
	must(t, err)
	res, err := svc.Exit(at(2), "V1", "E", at(2))
	must(t, err)
	// S->A->E 与 S->B->E 等费 20,字典序 [S A E] < [S B E]。
	want := []string{"S", "A", "E"}
	if !reflect.DeepEqual(res.Path, want) {
		t.Fatalf("path = %v, want %v", res.Path, want)
	}
	if res.Fee != 20 || res.Charged != 20 {
		t.Fatalf("fee=%d charged=%d, want 20/20", res.Fee, res.Charged)
	}
	view, err := svc.QueryJourney(jid)
	must(t, err)
	if !reflect.DeepEqual(view.Path, want) || view.Received != 20 {
		t.Fatalf("view = %+v", view)
	}
	if len(view.Adjustments) != 1 || view.Adjustments[0].Kind != AdjustCharge {
		t.Fatalf("adjustments = %+v", view.Adjustments)
	}
}

// 线性路网 G1-G2-G3-G4,另有高价支路 G1-X-G2 用于迟到记录改变路径。
func lateNet() *Network {
	net := NewNetwork()
	_ = net.AddSegment("G1", "G2", 10, map[string]int64{"car": 2, "truck": 5, "free": 0})
	_ = net.AddSegment("G2", "G3", 10, map[string]int64{"car": 2, "truck": 5, "free": 0})
	_ = net.AddSegment("G3", "G4", 10, map[string]int64{"car": 2, "truck": 5, "free": 0})
	_ = net.AddSegment("G1", "X", 10, map[string]int64{"car": 2, "truck": 5, "free": 0})
	_ = net.AddSegment("X", "G2", 10, map[string]int64{"car": 2, "truck": 5, "free": 0})
	return net
}

// 迟到记录时刻恰等于入口或出口时刻,仍属于本行程并触发重算。
func TestLateRecordExactlyAtEntryOrExitTime(t *testing.T) {
	// 恰等于入口时刻
	svc := NewService(lateNet(), defaultConfig())
	must(t, svc.RegisterVehicle(at(0), "V1", "car"))
	jid, err := svc.Entry(at(100), "V1", "G1", at(100))
	must(t, err)
	exit, err := svc.Exit(at(200), "V1", "G3", at(200))
	must(t, err)
	if exit.Fee != 40 { // G1-G2-G3: 10*2*2
		t.Fatalf("initial fee = %d, want 40", exit.Fee)
	}
	// 迟到记录 X,记录时刻恰等于入口时刻 at(100)。
	rr, err := svc.Record(at(300), "V1", "X", at(100))
	must(t, err)
	if rr.Status != StatusLate {
		t.Fatalf("status = %s, want LATE", rr.Status)
	}
	// G1-X-G2-G3 = 20+20+20 = 60,补扣 20。
	if rr.Adjustment == nil || rr.Adjustment.Kind != AdjustBackcharge || rr.Adjustment.Amount != 20 {
		t.Fatalf("adjustment = %+v, want backcharge 20", rr.Adjustment)
	}
	view, _ := svc.QueryJourney(jid)
	if !reflect.DeepEqual(view.Path, []string{"G1", "X", "G2", "G3"}) || view.Received != 60 {
		t.Fatalf("view = %+v", view)
	}

	// 恰等于出口时刻
	svc2 := NewService(lateNet(), defaultConfig())
	must(t, svc2.RegisterVehicle(at(0), "V2", "car"))
	jid2, err := svc2.Entry(at(100), "V2", "G1", at(100))
	must(t, err)
	_, err = svc2.Exit(at(200), "V2", "G3", at(200))
	must(t, err)
	rr2, err := svc2.Record(at(300), "V2", "X", at(200)) // 恰等于出口时刻
	must(t, err)
	if rr2.Status != StatusLate || rr2.Adjustment == nil || rr2.Adjustment.Amount != 20 {
		t.Fatalf("rr2 = %+v", rr2)
	}
	view2, _ := svc2.QueryJourney(jid2)
	if view2.Received != 60 {
		t.Fatalf("received = %d, want 60", view2.Received)
	}

	// 时刻不在入口与出口之间:孤立记录,不触发重算。
	rr3, err := svc2.Record(at(400), "V2", "X", at(50))
	must(t, err)
	if rr3.Status != StatusOrphan || rr3.Adjustment != nil {
		t.Fatalf("rr3 = %+v, want ORPHAN", rr3)
	}
}

// 迟到记录恰在补扣期限最后一刻仍触发重算;超过一刻只登记。
func TestLateRecordAtDeadlineBoundary(t *testing.T) {
	cfg := defaultConfig()
	cfg.BackchargeWindow = time.Hour

	svc := NewService(lateNet(), cfg)
	must(t, svc.RegisterVehicle(at(0), "V1", "car"))
	jid, _ := svc.Entry(at(100), "V1", "G1", at(100))
	_, err := svc.Exit(at(200), "V1", "G3", at(200))
	must(t, err)
	// 到达时刻 = 出口时刻 + 期限,恰在最后一刻:触发重算。
	rr, err := svc.Record(at(200+3600), "V1", "X", at(150))
	must(t, err)
	if rr.Status != StatusLate || rr.Adjustment == nil {
		t.Fatalf("rr = %+v, want LATE with adjustment", rr)
	}
	view, _ := svc.QueryJourney(jid)
	if view.Received != 60 {
		t.Fatalf("received = %d, want 60", view.Received)
	}

	// 超过期限 1 纳秒:登记为 EXPIRED,不重算。
	svc2 := NewService(lateNet(), cfg)
	must(t, svc2.RegisterVehicle(at(0), "V2", "car"))
	jid2, _ := svc2.Entry(at(100), "V2", "G1", at(100))
	_, err = svc2.Exit(at(200), "V2", "G3", at(200))
	must(t, err)
	late := at(200 + 3600).Add(time.Nanosecond)
	rr2, err := svc2.Record(late, "V2", "X", at(150))
	must(t, err)
	if rr2.Status != StatusExpired || rr2.Adjustment != nil {
		t.Fatalf("rr2 = %+v, want EXPIRED", rr2)
	}
	view2, _ := svc2.QueryJourney(jid2)
	if view2.Received != 40 || !reflect.DeepEqual(view2.Path, []string{"G1", "G2", "G3"}) {
		t.Fatalf("view2 = %+v, want unchanged", view2)
	}
}

// 重复窗口恰等于间隔视为两条独立记录;严格小于窗口为重复。
func TestDuplicateWindowBoundary(t *testing.T) {
	cfg := defaultConfig()
	cfg.DuplicateWindow = 10 * time.Second
	svc := NewService(lateNet(), cfg)
	must(t, svc.RegisterVehicle(at(0), "V1", "car"))
	jid, _ := svc.Entry(at(100), "V1", "G1", at(100))

	rr1, err := svc.Record(at(110), "V1", "G2", at(110))
	must(t, err)
	if rr1.Status != StatusAccepted {
		t.Fatalf("rr1 = %s", rr1.Status)
	}
	// 间隔 5s < 窗口 10s:重复,不触发任何重算。
	rr2, err := svc.Record(at(115), "V1", "G2", at(115))
	must(t, err)
	if rr2.Status != StatusDuplicate {
		t.Fatalf("rr2 = %s, want DUPLICATE", rr2.Status)
	}
	// 间隔恰等于窗口 10s:独立记录。
	rr3, err := svc.Record(at(120), "V1", "G2", at(120))
	must(t, err)
	if rr3.Status != StatusAccepted {
		t.Fatalf("rr3 = %s, want ACCEPTED", rr3.Status)
	}
	exit, err := svc.Exit(at(200), "V1", "G3", at(200))
	must(t, err)
	// 两条独立的 G2 记录都是途径点,费用不变。
	if exit.Fee != 40 {
		t.Fatalf("fee = %d, want 40", exit.Fee)
	}
	view, _ := svc.QueryJourney(jid)
	if !reflect.DeepEqual(view.Path, []string{"G1", "G2", "G3"}) {
		t.Fatalf("path = %v", view.Path)
	}
}

// 车型变更恰在经过某门架时刻,该门架出发的路段按新车型计费。
func TestTypeChangeExactlyAtGantryTime(t *testing.T) {
	svc := NewService(lateNet(), defaultConfig())
	must(t, svc.RegisterVehicle(at(0), "V1", "car"))
	// 生效时刻恰等于经过 G2 的时刻 at(150)。
	must(t, svc.ChangeVehicleType(at(1), "V1", "truck", at(150)))
	jid, _ := svc.Entry(at(100), "V1", "G1", at(100))
	_, err := svc.Record(at(150), "V1", "G2", at(150))
	must(t, err)
	exit, err := svc.Exit(at(200), "V1", "G3", at(200))
	must(t, err)
	// G1->G2 按 car(10*2=20),G2->G3 按 truck(10*5=50)。
	if exit.Fee != 70 {
		t.Fatalf("fee = %d, want 70", exit.Fee)
	}
	view, _ := svc.QueryJourney(jid)
	if view.Received != 70 {
		t.Fatalf("received = %d, want 70", view.Received)
	}

	// 重算时同样适用:迟到记录触发重算,车型分段规则不变。
	svc2 := NewService(lateNet(), defaultConfig())
	must(t, svc2.RegisterVehicle(at(0), "V2", "car"))
	must(t, svc2.ChangeVehicleType(at(1), "V2", "truck", at(150)))
	jid2, _ := svc2.Entry(at(100), "V2", "G1", at(100))
	exit2, err := svc2.Exit(at(200), "V2", "G3", at(200))
	must(t, err)
	if exit2.Fee != 40 { // 无 G2 记录,整段按入口时刻车型 car
		t.Fatalf("fee2 = %d, want 40", exit2.Fee)
	}
	rr, err := svc2.Record(at(300), "V2", "G2", at(150))
	must(t, err)
	if rr.Adjustment == nil || rr.Adjustment.Amount != 30 {
		t.Fatalf("adjustment = %+v, want backcharge 30", rr.Adjustment)
	}
	view2, _ := svc2.QueryJourney(jid2)
	if view2.Fee != 70 || view2.Received != 70 {
		t.Fatalf("view2 = %+v", view2)
	}
}

// 补扣恰好触及月封顶;超出部分单独可查。
func TestBackchargeExactlyAtMonthlyCap(t *testing.T) {
	cfg := defaultConfig()
	cfg.MonthlyCap = 100
	net := NewNetwork()
	must(t, net.AddSegment("G1", "G2", 10, map[string]int64{"car": 2}))
	must(t, net.AddSegment("G2", "G3", 10, map[string]int64{"car": 2}))
	must(t, net.AddSegment("G3", "G4", 10, map[string]int64{"car": 2}))
	must(t, net.AddSegment("G1", "X", 10, map[string]int64{"car": 5}))
	must(t, net.AddSegment("X", "G2", 10, map[string]int64{"car": 3}))
	svc := NewService(net, cfg)
	must(t, svc.RegisterVehicle(at(0), "V1", "car"))
	jid, _ := svc.Entry(at(100), "V1", "G1", at(100))
	exit, err := svc.Exit(at(200), "V1", "G3", at(200))
	must(t, err)
	if exit.Charged != 40 {
		t.Fatalf("charged = %d, want 40", exit.Charged)
	}
	// 迟到记录使费用 40 -> 100(G1-X-G2-G3 = 50+30+20),补扣 60,月实收恰为 100。
	rr, err := svc.Record(at(300), "V1", "X", at(150))
	must(t, err)
	if rr.Adjustment == nil || rr.Adjustment.Amount != 60 || rr.Adjustment.CappedUncollected != 0 {
		t.Fatalf("adjustment = %+v, want backcharge 60 capped 0", rr.Adjustment)
	}
	led, err := svc.QueryMonth("V1", at(200))
	must(t, err)
	if led.Received != 100 || led.CappedUncollected != 0 {
		t.Fatalf("ledger = %+v, want received 100", led)
	}
	// 第二个行程费用 60,封顶后只能再收 0,60 单独可查。
	jid2, err := svc.Entry(at(400), "V1", "G1", at(400))
	must(t, err)
	exit2, err := svc.Exit(at(500), "V1", "G4", at(500))
	must(t, err)
	if exit2.Fee != 60 || exit2.Charged != 0 || exit2.CappedUncollected != 60 {
		t.Fatalf("exit2 = %+v, want fee 60 charged 0 capped 60", exit2)
	}
	led, _ = svc.QueryMonth("V1", at(200))
	if led.Received != 100 || led.CappedUncollected != 60 {
		t.Fatalf("ledger = %+v, want received 100 capped 60", led)
	}
	view2, _ := svc.QueryJourney(jid2)
	if view2.Received != 0 {
		t.Fatalf("view2 received = %d, want 0", view2.Received)
	}
	_ = jid
}

// 退款使当月实收恰为零。
func TestRefundToExactlyZero(t *testing.T) {
	cfg := defaultConfig()
	cfg.MonthlyCap = 1000
	svc := NewService(lateNet(), cfg)
	must(t, svc.RegisterVehicle(at(0), "V1", "truck"))
	jid, _ := svc.Entry(at(100), "V1", "G1", at(100))
	exit, err := svc.Exit(at(200), "V1", "G3", at(200))
	must(t, err)
	if exit.Charged != 100 { // truck: 10*5*2
		t.Fatalf("charged = %d, want 100", exit.Charged)
	}
	// 车型变更为 free,生效时刻在入口之前;随后迟到记录触发重算。
	must(t, svc.ChangeVehicleType(at(250), "V1", "free", at(50)))
	rr, err := svc.Record(at(300), "V1", "G2", at(150))
	must(t, err)
	if rr.Adjustment == nil || rr.Adjustment.Kind != AdjustRefund || rr.Adjustment.Amount != 100 {
		t.Fatalf("adjustment = %+v, want refund 100", rr.Adjustment)
	}
	view, _ := svc.QueryJourney(jid)
	if view.Fee != 0 || view.Received != 0 {
		t.Fatalf("view = %+v, want fee 0 received 0", view)
	}
	led, _ := svc.QueryMonth("V1", at(200))
	if led.Received != 0 || led.RefundOverflow != 0 {
		t.Fatalf("ledger = %+v, want zero", led)
	}
}

// 乱序到达的中间记录,在结算前与结算后分别到达,终态相同。
func TestOutOfOrderRecordsSameFinalState(t *testing.T) {
	build := func(late bool) *Service {
		svc := NewService(lateNet(), defaultConfig())
		must(t, svc.RegisterVehicle(at(0), "V1", "car"))
		must(t, svc.ChangeVehicleType(at(1), "V1", "truck", at(150)))
		_, err := svc.Entry(at(100), "V1", "G1", at(100))
		must(t, err)
		if !late {
			// 结算前乱序到达:先 X 后 G2。
			if _, err := svc.Record(at(120), "V1", "X", at(120)); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Record(at(150), "V1", "G2", at(150)); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Exit(at(200), "V1", "G3", at(200)); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := svc.Exit(at(200), "V1", "G3", at(200)); err != nil {
				t.Fatal(err)
			}
			// 结算后作为迟到记录到达,顺序同样乱。
			if _, err := svc.Record(at(300), "V1", "X", at(120)); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Record(at(400), "V1", "G2", at(150)); err != nil {
				t.Fatal(err)
			}
		}
		return svc
	}
	a := build(false)
	b := build(true)
	va, err := a.QueryJourney("V1#1")
	must(t, err)
	vb, err := b.QueryJourney("V1#1")
	must(t, err)
	if !reflect.DeepEqual(va.Path, vb.Path) || va.Fee != vb.Fee || va.Received != vb.Received {
		t.Fatalf("final state differs:\n before-exit: %+v\n after-exit: %+v", va, vb)
	}
	la, _ := a.QueryMonth("V1", at(200))
	lb, _ := b.QueryMonth("V1", at(200))
	if la != lb {
		t.Fatalf("ledgers differ: %+v vs %+v", la, lb)
	}
	t.Logf("final path=%v fee=%d received=%d", va.Path, va.Fee, va.Received)
}

// 错误可区分且只报次序最靠前的一类;被拒绝的操作不改变状态与时钟。
func TestErrorPrecedence(t *testing.T) {
	svc := NewService(lateNet(), defaultConfig())
	must(t, svc.RegisterVehicle(at(10), "V1", "car"))

	// 参数非法优先于时钟回退。
	if _, err := svc.Record(at(5), "", "G2", at(6)); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("want ErrInvalidParams, got %v", err)
	}
	// 时钟回退优先于门架不存在。
	if _, err := svc.Record(at(5), "V1", "NOPE", at(6)); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("want ErrClockRollback, got %v", err)
	}
	// 门架不存在优先于车辆不存在。
	if _, err := svc.Record(at(20), "GHOST", "NOPE", at(21)); !errors.Is(err, ErrGantryNotFound) {
		t.Fatalf("want ErrGantryNotFound, got %v", err)
	}
	// 车辆不存在。
	if _, err := svc.Record(at(30), "GHOST", "G2", at(31)); !errors.Is(err, ErrVehicleNotFound) {
		t.Fatalf("want ErrVehicleNotFound, got %v", err)
	}
	// 无入口记录的出口。
	if _, err := svc.Exit(at(40), "V1", "G3", at(40)); !errors.Is(err, ErrExitWithoutEntry) {
		t.Fatalf("want ErrExitWithoutEntry, got %v", err)
	}
	// 被拒绝的操作不推进时钟:时钟仍停在 at(10)。
	if _, err := svc.Record(at(9), "V1", "G2", at(11)); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("want ErrClockRollback, got %v", err)
	}
	// 成功操作推进时钟到 at(40)(孤立记录)。
	if _, err := svc.Record(at(40), "V1", "G2", at(41)); err != nil {
		t.Fatalf("orphan record: %v", err)
	}
	if _, err := svc.Record(at(39), "V1", "G2", at(42)); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("want ErrClockRollback, got %v", err)
	}

	// 路径不可达:孤岛门架。
	net := lateNet()
	must(t, net.AddGantry("ISLAND"))
	svc2 := NewService(net, defaultConfig())
	must(t, svc2.RegisterVehicle(at(0), "V2", "car"))
	if _, err := svc2.Entry(at(1), "V2", "G1", at(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc2.Exit(at(2), "V2", "ISLAND", at(2)); !errors.Is(err, ErrPathUnreachable) {
		t.Fatalf("want ErrPathUnreachable, got %v", err)
	}
	// 行程保持未结算,可人工关闭。
	if _, err := svc2.QueryAdjustments("V2#1"); !errors.Is(err, ErrJourneyNotSettled) {
		t.Fatalf("want ErrJourneyNotSettled, got %v", err)
	}
	must(t, svc2.CloseJourney(at(3), "V2"))

	// 再次出口:行程已结算。
	svc3 := NewService(lateNet(), defaultConfig())
	must(t, svc3.RegisterVehicle(at(0), "V3", "car"))
	jid3, _ := svc3.Entry(at(1), "V3", "G1", at(1))
	if _, err := svc3.Exit(at(2), "V3", "G3", at(2)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc3.Exit(at(3), "V3", "G3", at(3)); !errors.Is(err, ErrJourneySettled) {
		t.Fatalf("want ErrJourneySettled, got %v", err)
	}
	// 行程不存在。
	if _, err := svc3.QueryJourney("V3#99"); !errors.Is(err, ErrJourneyNotFound) {
		t.Fatalf("want ErrJourneyNotFound, got %v", err)
	}
	if _, err := svc3.QueryAdjustments(jid3); err != nil {
		t.Fatalf("settled journey adjustments: %v", err)
	}
}

// 相同操作序列重放得到完全相同的路径、金额与调整明细。
func TestReplayDeterminism(t *testing.T) {
	run := func() string {
		svc := NewService(lateNet(), defaultConfig())
		must(t, svc.RegisterVehicle(at(0), "V1", "car"))
		must(t, svc.ChangeVehicleType(at(1), "V1", "truck", at(150)))
		jid, _ := svc.Entry(at(100), "V1", "G1", at(100))
		_, _ = svc.Record(at(150), "V1", "G2", at(150))
		_, _ = svc.Exit(at(200), "V1", "G3", at(200))
		_, _ = svc.Record(at(300), "V1", "X", at(120))
		view, _ := svc.QueryJourney(jid)
		return fmt.Sprintf("%v|%d|%d|%v", view.Path, view.Fee, view.Received, view.Adjustments)
	}
	if run() != run() {
		t.Fatal("replay produced different results")
	}
}

// 并发调用等价于某个串行顺序:并发与串行终态一致。
func TestConcurrentOps(t *testing.T) {
	svc := NewService(lateNet(), defaultConfig())
	must(t, svc.RegisterVehicle(at(0), "V1", "car"))
	jid, _ := svc.Entry(at(100), "V1", "G1", at(100))
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ts := at(101 + i)
			now := at(1000 + i)
			_, _ = svc.Record(now, "V1", "G2", ts)
			_, _ = svc.QueryJourney(jid)
			_, _ = svc.QueryMonth("V1", at(200))
		}(i)
	}
	wg.Wait()
	if _, err := svc.Exit(at(2000), "V1", "G3", at(200)); err != nil {
		t.Fatal(err)
	}
	view, err := svc.QueryJourney(jid)
	must(t, err)
	if !view.Settled || view.Received < 0 {
		t.Fatalf("bad final state: %+v", view)
	}
}

// 查询开销不随历史行程总数增长:行程按 ID 直查。
func BenchmarkQueryJourney(b *testing.B) {
	for _, n := range []int{100, 10000} {
		b.Run(fmt.Sprintf("journeys=%d", n), func(b *testing.B) {
			svc := NewService(lateNet(), defaultConfig())
			now := at(0)
			_ = svc.RegisterVehicle(now, "V1", "car")
			var lastID string
			for i := 0; i < n; i++ {
				now = now.Add(time.Second)
				jid, err := svc.Entry(now, "V1", "G1", now)
				if err != nil {
					b.Fatal(err)
				}
				now = now.Add(time.Second)
				if _, err := svc.Exit(now, "V1", "G3", now); err != nil {
					b.Fatal(err)
				}
				lastID = jid
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := svc.QueryJourney(lastID); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

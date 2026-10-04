package clinic

import (
	"strings"
	"testing"

	"ontology/vaxrule"
)

func errCode(err error) string {
	if err == nil {
		return ""
	}
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return err.Error()
}

// addM 增加题例系列 M：活疫苗、n=2、minAge=[365,393]、minInt[2]=28、R=28。
func addM(c *Clinic) {
	mustOK(c.AddSeries("M", true, 2, []int{365, 393}, []int{0, 28}, 28))
}

// addV 增加题例系列 V：活疫苗、n=1、minAge=[365]、R=28。
func addV(c *Clinic) {
	mustOK(c.AddSeries("V", true, 1, []int{365}, []int{0}, 28))
}

func mustOK(err error) {
	if err != nil {
		panic("unexpected error: " + err.Error())
	}
}

func recAt(ev *Evaluation, series string, date int) RecordJudgment {
	for _, j := range ev.Records {
		if j.Series == series && j.Date == date {
			return j
		}
	}
	return RecordJudgment{}
}

func nextAt(ev *Evaluation, series string) (NextDose, bool) {
	for _, n := range ev.Next {
		if n.Series == series {
			return n, true
		}
	}
	return NextDose{}, false
}

func TestSpecExamples(t *testing.T) {
	// 甲：M 于 361 有效（恰等 365-4）。
	c := New()
	addM(c)
	mustOK(c.AddPatient(0, "甲", 0))
	mustOK(c.Record(400, "甲", "M", 361))
	ev, err := c.Evaluate(400, "甲")
	if err != nil {
		t.Fatal(err)
	}
	j := recAt(ev, "M", 361)
	if j.Status != StatusValid {
		t.Fatalf("甲: want valid, got status=%d reason=%d", j.Status, j.Reason)
	}

	// 乙：360 无效（年龄）；387 无效（重打 27<R=28，无宽限）；415 有效（=28）。
	c = New()
	addM(c)
	mustOK(c.AddPatient(0, "乙", 0))
	mustOK(c.Record(500, "乙", "M", 360))
	mustOK(c.Record(500, "乙", "M", 387))
	mustOK(c.Record(500, "乙", "M", 415))
	ev, _ = c.Evaluate(500, "乙")
	if j := recAt(ev, "M", 360); j.Status != StatusInvalid || j.Reason != vaxrule.ReasonAge {
		t.Fatalf("乙360: %+v", j)
	}
	if j := recAt(ev, "M", 387); j.Status != StatusInvalid || j.Reason != vaxrule.ReasonRedose {
		t.Fatalf("乙387: %+v", j)
	}
	if j := recAt(ev, "M", 415); j.Status != StatusValid || j.Dose != 1 {
		t.Fatalf("乙415: %+v", j)
	}

	// 丙：400 M 有效；410 V 无效（活疫苗）；428 M 无效（活疫苗，无效 V 仍冲突）。
	c = New()
	addM(c)
	addV(c)
	mustOK(c.AddPatient(0, "丙", 0))
	mustOK(c.Record(400, "丙", "M", 400))
	mustOK(c.Record(410, "丙", "V", 410))
	mustOK(c.Record(428, "丙", "M", 428))
	ev, _ = c.Evaluate(430, "丙")
	if j := recAt(ev, "M", 400); j.Status != StatusValid {
		t.Fatalf("丙400: %+v", j)
	}
	if j := recAt(ev, "V", 410); j.Status != StatusInvalid || j.Reason != vaxrule.ReasonLive {
		t.Fatalf("丙410: %+v", j)
	}
	if j := recAt(ev, "M", 428); j.Status != StatusInvalid || j.Reason != vaxrule.ReasonLive {
		t.Fatalf("丙428: %+v", j)
	}
	// now=430：M 第2剂与 V 第1剂最早日均为 456。
	if n, ok := nextAt(ev, "M"); !ok || n.Dose != 2 || n.Earliest != 456 {
		t.Fatalf("丙 next M: %+v ok=%v", n, ok)
	}
	if n, ok := nextAt(ev, "V"); !ok || n.Dose != 1 || n.Earliest != 456 {
		t.Fatalf("丙 next V: %+v ok=%v", n, ok)
	}
	// 两者同在 456 接种都有效（同日不冲突）。
	mustOK(c.Record(456, "丙", "M", 456))
	mustOK(c.Record(456, "丙", "V", 456))
	ev, _ = c.Evaluate(456, "丙")
	if j := recAt(ev, "M", 456); j.Status != StatusValid || j.Dose != 2 {
		t.Fatalf("丙 M456: %+v", j)
	}
	if j := recAt(ev, "V", 456); j.Status != StatusValid {
		t.Fatalf("丙 V456: %+v", j)
	}

	// 丁：M 与 V 同日 400 都有效。
	c = New()
	addM(c)
	addV(c)
	mustOK(c.AddPatient(0, "丁", 0))
	mustOK(c.Record(400, "丁", "M", 400))
	mustOK(c.Record(400, "丁", "V", 400))
	ev, _ = c.Evaluate(400, "丁")
	if j := recAt(ev, "M", 400); j.Status != StatusValid {
		t.Fatalf("丁 M: %+v", j)
	}
	if j := recAt(ev, "V", 400); j.Status != StatusValid {
		t.Fatalf("丁 V: %+v", j)
	}
}

func TestOutOfOrderFlipsJudgment(t *testing.T) {
	// 戊：M400、V428 均有效；补录 M420（间隔不足，无效）后 V428 翻转为活疫苗无效。
	c := New()
	addM(c)
	addV(c)
	mustOK(c.AddPatient(0, "戊", 0))
	mustOK(c.Record(500, "戊", "M", 400))
	mustOK(c.Record(500, "戊", "V", 428))
	ev, _ := c.Evaluate(500, "戊")
	if j := recAt(ev, "V", 428); j.Status != StatusValid {
		t.Fatalf("翻转前 V428: %+v", j)
	}
	if err := c.Record(500, "戊", "M", 420); err != nil {
		t.Fatal(err)
	}
	ev, _ = c.Evaluate(500, "戊")
	if j := recAt(ev, "M", 420); j.Status != StatusInvalid || j.Reason != vaxrule.ReasonInterval {
		t.Fatalf("补录 M420: %+v", j)
	}
	if j := recAt(ev, "V", 428); j.Status != StatusInvalid || j.Reason != vaxrule.ReasonLive {
		t.Fatalf("翻转后 V428: %+v", j)
	}
}

func TestAgeGraceBoundary(t *testing.T) {
	// 非活疫苗 A：n=1、minAge=[10]、R=5。361 式检查：恰等 6 有效，5 无效（年龄）。
	c := New()
	mustOK(c.AddSeries("A", false, 1, []int{10}, []int{0}, 5))
	mustOK(c.AddPatient(0, "p", 0))
	mustOK(c.Record(100, "p", "A", 6))
	ev, _ := c.Evaluate(100, "p")
	if j := recAt(ev, "A", 6); j.Status != StatusValid {
		t.Fatalf("age == min-grace: %+v", j)
	}
	if err := c.Record(100, "p", "A", 5); err == nil {
		ev, _ = c.Evaluate(100, "p")
		// 5 作为第二条记录：k=2>n=1 => 多余（第一条已有效）。改单独患者验证少 1 日。
		_ = ev
	}
	c2 := New()
	mustOK(c2.AddSeries("A", false, 1, []int{10}, []int{0}, 5))
	mustOK(c2.AddPatient(0, "p", 0))
	mustOK(c2.Record(100, "p", "A", 5))
	ev2, _ := c2.Evaluate(100, "p")
	if j := recAt(ev2, "A", 5); j.Status != StatusInvalid || j.Reason != vaxrule.ReasonAge {
		t.Fatalf("age == min-grace-1: %+v", j)
	}
}

func TestIntervalAndRedoseGrace(t *testing.T) {
	// B：非活、n=3、minAge 全 0、minInt=[0,30,30]、R=10。
	newC := func() *Clinic {
		c := New()
		mustOK(c.AddSeries("B", false, 3, []int{0, 0, 0}, []int{0, 30, 30}, 10))
		mustOK(c.AddPatient(0, "p", 0))
		return c
	}
	// 间隔恰等 30-4=26 有效；25 无效（间隔）。
	c := newC()
	mustOK(c.Record(100, "p", "B", 0))
	mustOK(c.Record(100, "p", "B", 26))
	ev, _ := c.Evaluate(100, "p")
	if j := recAt(ev, "B", 26); j.Status != StatusValid || j.Dose != 2 {
		t.Fatalf("interval == min-grace: %+v", j)
	}
	c = newC()
	mustOK(c.Record(100, "p", "B", 0))
	mustOK(c.Record(100, "p", "B", 25))
	ev, _ = c.Evaluate(100, "p")
	if j := recAt(ev, "B", 25); j.Status != StatusInvalid || j.Reason != vaxrule.ReasonInterval {
		t.Fatalf("interval min-grace-1: %+v", j)
	}
	// 重打不享受宽限：第2剂于 10 间隔无效（10<26），最近记录变为无效；
	// 于 19（距 10 为 9<10=R）间隔已过（19>=26 不成立，故先用长间隔系列）。
	// 改用 minInt=20 的系列：10 间隔无效（10<16），19 距有效剂 0 为 19>=16 间隔通过，
	// 但距最近无效记录 10 为 9<10 -> 重打无效（无宽限）。
	c = New()
	mustOK(c.AddSeries("B2", false, 2, []int{0, 0}, []int{0, 20}, 10))
	mustOK(c.AddPatient(0, "p", 0))
	mustOK(c.Record(100, "p", "B2", 0))
	mustOK(c.Record(100, "p", "B2", 10)) // 间隔 10<16 无效，最近记录变为无效
	ev, _ = c.Evaluate(100, "p")
	if j := recAt(ev, "B2", 10); j.Status != StatusInvalid || j.Reason != vaxrule.ReasonInterval {
		t.Fatalf("B2 10 interval: %+v", j)
	}
	mustOK(c.Record(100, "p", "B2", 19)) // 距最近无效 10 为 9<10 -> 重打（间隔 19>=16 已通过）
	ev, _ = c.Evaluate(100, "p")
	if j := recAt(ev, "B2", 19); j.Status != StatusInvalid || j.Reason != vaxrule.ReasonRedose {
		t.Fatalf("redose R-1: %+v", j)
	}
	// 最近无效记录现在是 19；20 距 19 仅 1 日仍重打无效（证明不享受宽限且自最近无效剂起算）。
	mustOK(c.Record(100, "p", "B2", 20))
	ev, _ = c.Evaluate(100, "p")
	if j := recAt(ev, "B2", 20); j.Status != StatusInvalid || j.Reason != vaxrule.ReasonRedose {
		t.Fatalf("redose from latest 19: %+v", j)
	}
	mustOK(c.Record(100, "p", "B2", 30)) // 距最近无效 20 恰为 10=R、间隔满足 -> 有效第2剂
	ev, _ = c.Evaluate(100, "p")
	if j := recAt(ev, "B2", 30); j.Status != StatusValid || j.Dose != 2 {
		t.Fatalf("redose == R valid: %+v", j)
	}
}

func TestConsecutiveInvalidUsesLatest(t *testing.T) {
	// 连续无效取最近一条：minInt=10（含宽限下限 6）、R=5。
	c := New()
	mustOK(c.AddSeries("C", false, 2, []int{0, 0}, []int{0, 10}, 5))
	mustOK(c.AddPatient(0, "p", 0))
	mustOK(c.Record(1000, "p", "C", 0))
	mustOK(c.Record(1000, "p", "C", 5)) // 间隔 5<6 无效
	ev, _ := c.Evaluate(1000, "p")
	if j := recAt(ev, "C", 5); j.Status != StatusInvalid || j.Reason != vaxrule.ReasonInterval {
		t.Fatalf("C5 interval: %+v", j)
	}
	mustOK(c.Record(1000, "p", "C", 9)) // 间隔 9>=6 通过；距最近无效 5 为 4<5 -> 重打
	ev, _ = c.Evaluate(1000, "p")
	if j := recAt(ev, "C", 9); j.Status != StatusInvalid || j.Reason != vaxrule.ReasonRedose {
		t.Fatalf("C9 redose from 5: %+v", j)
	}
	// 取最近一条：13 时间隔早已满足；若错误自 5 起算（8>=5）会判有效；
	// 正确自最近无效记录 9 起算，4<5 -> 重打。
	mustOK(c.Record(1000, "p", "C", 13))
	ev, _ = c.Evaluate(1000, "p")
	if j := recAt(ev, "C", 13); j.Status != StatusInvalid || j.Reason != vaxrule.ReasonRedose {
		t.Fatalf("C13 must redose from latest 9: %+v", j)
	}
	// 不再添加 13 之后的记录；直接验证：去掉 13 这条、保留 9 时，14 距 9 恰为 5=R。
	// （13 已证明“取最近一条”，此处另起干净序列验证恰等 R 通过。）
	c2 := New()
	mustOK(c2.AddSeries("C", false, 2, []int{0, 0}, []int{0, 10}, 5))
	mustOK(c2.AddPatient(0, "q", 0))
	mustOK(c2.Record(1000, "q", "C", 0))
	mustOK(c2.Record(1000, "q", "C", 5))
	mustOK(c2.Record(1000, "q", "C", 9))
	mustOK(c2.Record(1000, "q", "C", 14)) // 距最近无效 9 恰为 5=R -> 有效
	ev2, _ := c2.Evaluate(1000, "q")
	if j := recAt(ev2, "C", 14); j.Status != StatusValid || j.Dose != 2 {
		t.Fatalf("C14 valid when latest invalid is 9: %+v", j)
	}
}

func TestLiveSameDay27And28(t *testing.T) {
	c := New()
	mustOK(c.AddSeries("L1", true, 1, []int{0}, []int{0}, 0))
	mustOK(c.AddSeries("L2", true, 1, []int{0}, []int{0}, 0))
	mustOK(c.AddPatient(0, "p", 0))
	mustOK(c.Record(100, "p", "L1", 50))
	mustOK(c.Record(100, "p", "L2", 77)) // 差 27 <28 无效
	ev, _ := c.Evaluate(100, "p")
	if j := recAt(ev, "L2", 77); j.Status != StatusInvalid || j.Reason != vaxrule.ReasonLive {
		t.Fatalf("live delta 27: %+v", j)
	}
	c = New()
	mustOK(c.AddSeries("L1", true, 1, []int{0}, []int{0}, 0))
	mustOK(c.AddSeries("L2", true, 1, []int{0}, []int{0}, 0))
	mustOK(c.AddPatient(0, "p", 0))
	mustOK(c.Record(100, "p", "L1", 50))
	mustOK(c.Record(100, "p", "L2", 78)) // 差 28，有效
	ev, _ = c.Evaluate(100, "p")
	if j := recAt(ev, "L2", 78); j.Status != StatusValid {
		t.Fatalf("live delta 28: %+v", j)
	}
}

func TestExtraDose(t *testing.T) {
	c := New()
	mustOK(c.AddSeries("D", false, 1, []int{0}, []int{0}, 0))
	mustOK(c.AddPatient(0, "p", 0))
	mustOK(c.Record(100, "p", "D", 10))
	mustOK(c.Record(100, "p", "D", 20))
	ev, _ := c.Evaluate(100, "p")
	js := []RecordJudgment{}
	for _, j := range ev.Records {
		if j.Series == "D" {
			js = append(js, j)
		}
	}
	if len(js) != 2 || js[0].Status != StatusValid || js[1].Status != StatusExtra || js[1].Dose != 2 {
		t.Fatalf("extra: %+v", js)
	}
	if _, ok := nextAt(ev, "D"); ok {
		t.Fatal("completed series must not appear in Next")
	}
}

func TestRecordValidationOrder(t *testing.T) {
	c := New()
	addM(c)
	mustOK(c.AddPatient(10, "p", 5))
	// 参数非法先于一切。
	if code := errCode(c.Record(0, "", "M", 0)); code != CodeInvalid {
		t.Fatalf("empty patient: %s", code)
	}
	// 时钟回退先于不存在：now=9 < 已接受最大 now=10，患者与系列都给存在值。
	if code := errCode(c.Record(9, "p", "M", 5)); code != CodeClockRewind {
		t.Fatalf("rewind vs missing: %s", code)
	}
	// 不存在先于重复/越界。
	if code := errCode(c.Record(10, "p", "ZZ", 5)); code != CodeMissing {
		t.Fatalf("missing series: %s", code)
	}
	if code := errCode(c.Record(10, "ghost", "M", 5)); code != CodeMissing {
		t.Fatalf("missing patient: %s", code)
	}
	// d < birth 为参数非法（存在性之后）。
	if code := errCode(c.Record(10, "p", "M", 4)); code != CodeInvalid {
		t.Fatalf("d<birth: %s", code)
	}
	// d > now 为参数非法。
	if code := errCode(c.Record(10, "p", "M", 11)); code != CodeInvalid {
		t.Fatalf("d>now: %s", code)
	}
	mustOK(c.Record(10, "p", "M", 10))
	if code := errCode(c.Record(10, "p", "M", 10)); code != CodeDuplicate {
		t.Fatalf("duplicate: %s", code)
	}
}

func TestAddSeriesValidation(t *testing.T) {
	c := New()
	if err := c.AddSeries("", false, 1, []int{0}, []int{0}, 0); err == nil {
		t.Fatal("empty name accepted")
	}
	if err := c.AddSeries("X", false, 0, []int{}, []int{}, 0); err == nil {
		t.Fatal("n=0 accepted")
	}
	if err := c.AddSeries("X", false, 7, make([]int, 7), make([]int, 7), 0); err == nil {
		t.Fatal("n=7 accepted")
	}
	if err := c.AddSeries("X", false, 2, []int{0, -1}, []int{0, 0}, 0); err == nil {
		t.Fatal("negative minAge accepted")
	}
	if err := c.AddSeries("X", false, 1, []int{0}, []int{0}, 10001); err == nil {
		t.Fatal("R>10000 accepted")
	}
	mustOK(c.AddSeries("X", false, 1, []int{0}, []int{0}, 0))
	if err := c.AddSeries("X", false, 1, []int{0}, []int{0}, 0); err == nil {
		t.Fatal("duplicate series accepted")
	}
}

func TestAdministerOrderAndStock(t *testing.T) {
	c := New()
	addM(c)
	mustOK(c.AddPatient(400, "p", 0))
	// 参数非法 > 时钟回退 > 不存在 > 资格 > 重复 > 完成 > 过早 > 无库存。
	if code := adminCode(c, 0, "", "p", "M"); code != CodeInvalid {
		t.Fatalf("invalid: %s", code)
	}
	if code := adminCode(c, 399, "n", "p", "M"); code != CodeClockRewind {
		t.Fatalf("rewind: %s", code)
	}
	if code := adminCode(c, 400, "n", "ghost", "M"); code != CodeMissing {
		t.Fatalf("missing patient: %s", code)
	}
	if code := adminCode(c, 400, "n", "p", "ZZ"); code != CodeMissing {
		t.Fatalf("missing series: %s", code)
	}
	if code := adminCode(c, 400, "n", "p", "M"); code != CodeNotGranted {
		t.Fatalf("not granted: %s", code)
	}
	mustOK(c.Grant(400, "n"))
	// 无资格通过后：过早（400 < 361 不，birth=0 时 400>=361 年龄通过）——400 实际可接种；无库存先报。
	if code := adminCode(c, 400, "n", "p", "M"); code != CodeNoStock {
		t.Fatalf("no stock: %s", code)
	}
	// 加两批次：lot2 exp 更早优先；exp=400 在 now=400 已过期不可用。
	mustOK(c.AddLot(400, "lot1", "M", 500, 1))
	mustOK(c.AddLot(400, "lot2", "M", 450, 1))
	mustOK(c.AddLot(400, "lot3", "M", 400, 1)) // 恰等 exp，过期
	res, err := c.Administer(400, "n", "p", "M")
	if err != nil || res.Lot != "lot2" {
		t.Fatalf("pick earliest exp: res=%+v err=%v", res, err)
	}
	// 同日重复优先于库存/过早。
	if code := adminCode(c, 400, "n", "p", "M"); code != CodeDuplicate {
		t.Fatalf("duplicate same day: %s", code)
	}
	// lot1 于 now=450 被隔离则无库存；解除后可用。
	mustOK(c.Quarantine(450, "lot1", true))
	if code := adminCode(c, 450, "n", "p", "M"); code != CodeNoStock {
		t.Fatalf("quarantined: %s", code)
	}
	mustOK(c.Quarantine(450, "lot1", false))
	// now=450：第2剂需 400+24=424 起；450 通过，活疫苗无其它系列，扣 lot1。
	res, err = c.Administer(450, "n", "p", "M")
	if err != nil || res.Lot != "lot1" {
		t.Fatalf("dose2: res=%+v err=%v", res, err)
	}
	// 系列已完成：用完成的患者在更晚日期（避免同日重复）验证先于无库存。
	if code := adminCode(c, 500, "n", "p", "M"); code != CodeComplete {
		t.Fatalf("complete: %s", code)
	}
	if q, _ := c.Store().Qty("lot1"); q != 0 {
		t.Fatalf("qty lot1=%d", q)
	}
}

func TestAdministerTooEarlyReason(t *testing.T) {
	c := New()
	addM(c)
	mustOK(c.AddPatient(360, "p", 0))
	mustOK(c.Grant(360, "n"))
	mustOK(c.AddLot(360, "l", "M", 900, 5))
	res, err := c.Administer(360, "n", "p", "M")
	if errCode(err) != CodeTooEarly {
		t.Fatalf("want too early, got %v %+v", err, res)
	}
	if res.Reason != vaxrule.ReasonAge || res.Earliest != 361 {
		t.Fatalf("too early payload: %+v", res)
	}
	// 在最早日接种成功。
	res, err = c.Administer(361, "n", "p", "M")
	if err != nil || res.Lot != "l" {
		t.Fatalf("earliest day administer: %+v %v", res, err)
	}
}

func TestTouchedDoesNotGrow(t *testing.T) {
	build := func(extra int) (*Clinic, string) {
		c := New()
		addM(c)
		addV(c)
		mustOK(c.AddPatient(2000, "p", 0))
		mustOK(c.Grant(2000, "n"))
		mustOK(c.AddLot(2000, "l", "M", 9999, 99))
		mustOK(c.AddLot(2000, "lv", "V", 9999, 99))
		// 制造 extra 条与目标判定完全无关的旧记录（远早于 27 日窗）。
		for i := 0; i < extra; i++ {
			mustOK(c.Record(2000, "p", "M", 10+i)) // 全部远早于 1973，不进 27 日窗
		}
		return c, "p"
	}
	c10, p := build(10)
	_, n10, err := c10.administerTouched(2000, "n", p, "V")
	if err != nil {
		t.Fatal(err)
	}
	c1000, p := build(1000)
	_, n1000, err := c1000.administerTouched(2000, "n", p, "V")
	if err != nil {
		t.Fatal(err)
	}
	if n10 != n1000 {
		t.Fatalf("touched grows: %d vs %d", n10, n1000)
	}
}

func TestReplayDeterministic(t *testing.T) {
	run := func() []string {
		c := New()
		addM(c)
		mustOK(c.AddPatient(500, "p", 0))
		mustOK(c.Grant(500, "n"))
		mustOK(c.AddLot(500, "l", "M", 900, 2))
		out := []string{}
		_, e1 := c.Administer(361, "n", "p", "M")
		out = append(out, errCode(e1))
		_, e2 := c.Administer(400, "n", "p", "M")
		out = append(out, errCode(e2))
		ev, _ := c.Evaluate(500, "p")
		for _, j := range ev.Records {
			out = append(out, j.Series, itoa(j.Date), itoa(int(j.Status)), itoa(int(j.Reason)))
		}
		return out
	}
	a := run()
	b := run()
	if strings.Join(a, "|") != strings.Join(b, "|") {
		t.Fatalf("non deterministic: %v vs %v", a, b)
	}
}

func adminCode(c *Clinic, now int, nurse, patient, series string) string {
	_, err := c.Administer(now, nurse, patient, series)
	return errCode(err)
}

func itoa(x int) string {
	if x == 0 {
		return "0"
	}
	neg := x < 0
	if neg {
		x = -x
	}
	var b [24]byte
	i := len(b)
	for x > 0 {
		i--
		b[i] = byte('0' + x%10)
		x /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

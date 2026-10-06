package registry

import (
	"strings"
	"testing"
)

func newTest(t *testing.T, maxAge int64) *Registry {
	t.Helper()
	var b strings.Builder
	t.Cleanup(func() {
		if t.Failed() {
			t.Log("\n" + b.String())
		}
	})
	return New(Config{UnitQty: 1, MaxAgePeriods: maxAge, MinPeriod: 1, MaxPeriod: 100000}, &b)
}

// 余量恰好凑满一单位：0.6 与 0.4 合并核发 1 张。
func TestRemainderAccumulatesToExactUnit(t *testing.T) {
	r := newTest(t, 5)
	if e := r.RegisterFacility("F", "H", 1); e != nil {
		t.Fatal(e)
	}
	if e := r.RegisterMeter("F", 1, 0); e != nil {
		t.Fatal(e)
	}
	if rem, _ := r.Remainder("F"); rem != 0 {
		t.Fatalf("余量=%d 期望0", rem)
	}
	must(t, r.RegisterMeter("F", 2, 1)) // 合并后恰好1单位
	if rem, _ := r.Remainder("F"); rem != 0 || r.CertCount() != 1 {
		t.Fatalf("期望凑满1张且余量0, got count=%d rem=%d", r.CertCount(), rem)
	}
	c := r.Cert(1)
	if c == nil || c.Period != 2 || c.Holder != "H" {
		t.Fatalf("证书1状态异常: %+v", c)
	}
}

// 发电期恰等于用电期、恰在最大年限边界（取等允许）。
func TestDeclarePeriodBoundaries(t *testing.T) {
	r := newTest(t, 3)
	must(t, r.RegisterFacility("F", "H", 1))
	must(t, r.RegisterMeter("F", 1, 1)) // 序号1 -> 期1
	must(t, r.RegisterMeter("F", 4, 1)) // 序号2 -> 期4
	// 用电期 4：证书期 4 取等允许；证书期 1 恰在 4-3=1 边界允许。
	must(t, r.RegisterConsumption("H", 4, 5))
	must(t, r.Declare("H", 4, []int64{1, 2}))
	if r.CancelledQty("H", 4) != 2 {
		t.Fatalf("有效注销量=%d 期望2", r.CancelledQty("H", 4))
	}
	// 期 0 超下界 -> 期限不符。
	must(t, r.RegisterMeter("F", 5, 1)) // 序号3 期5
	must(t, r.RegisterConsumption("H", 6, 5))
	must(t, r.RegisterMeter("F", 7, 1)) // 序号4 期7
	e := r.Declare("H", 6, []int64{4})  // 期7 > 用电期6，不符
	if e == nil || e.Code != CodePeriodMismatch {
		t.Fatalf("期望期限不符, got %v", e)
	}
}

// 注销总量恰等于用电量，取等允许；再多一张则拒绝。
func TestDeclareExactCapacity(t *testing.T) {
	r := newTest(t, 10)
	must(t, r.RegisterFacility("F", "H", 1))
	must(t, r.RegisterMeter("F", 1, 3))
	must(t, r.RegisterConsumption("H", 1, 3))
	must(t, r.Declare("H", 1, []int64{1, 2, 3}))
	must(t, r.RegisterMeter("F", 2, 1)) // 序号4
	must(t, r.RegisterConsumption("H", 2, 1))
	e := r.Declare("H", 2, []int64{4})
	if e != nil {
		t.Fatalf("恰等于用电量应接受, got %v", e)
	}
}

// 撤销先持有（序号降序）后已注销（注销时刻降序），并产生声明失效事件。
func TestRevocationOrderAndInvalidation(t *testing.T) {
	r := newTest(t, 10)
	must(t, r.RegisterFacility("F", "H", 1))
	must(t, r.RegisterMeter("F", 1, 4)) // 1..4 持有
	must(t, r.RegisterConsumption("H", 2, 10))
	// 注销时刻升序：先 1，再 3，再 2（打乱序号制造非自然顺序）。
	must(t, r.Declare("H", 2, []int64{1}))
	must(t, r.Declare("H", 2, []int64{3}))
	must(t, r.Declare("H", 2, []int64{2}))
	if r.CancelledQty("H", 2) != 3 {
		t.Fatalf("注销量=%d 期望3", r.CancelledQty("H", 2))
	}
	// 电量降到 1：应核发 1 张。先撤销持有 4，再撤销最晚注销的 2，再 3。
	must(t, r.RegisterMeter("F", 1, 1))
	wantRevoked := []int64{4, 2, 3}
	var got []int64
	var invalidated int
	for _, ev := range r.Events() {
		if ev.Kind == EventRevokedHeld {
			got = append(got, ev.Serial)
		}
		if ev.Kind == EventDeclarationInvalidated {
			got = append(got, ev.Serial)
			invalidated++
			if ev.Consumer != "H" || ev.UsePeriod != 2 {
				t.Fatalf("声明失效事件归属错误: %+v", ev)
			}
		}
	}
	if len(got) != 3 || got[0] != wantRevoked[0] || got[1] != wantRevoked[1] || got[2] != wantRevoked[2] {
		t.Fatalf("撤销顺序=%v 期望%v", got, wantRevoked)
	}
	if invalidated != 2 {
		t.Fatalf("声明失效事件数=%d 期望2", invalidated)
	}
	if r.CancelledQty("H", 2) != 1 {
		t.Fatalf("回溯后有效注销量=%d 期望1", r.CancelledQty("H", 2))
	}
	if r.Cert(1).Status != StatusCancelled {
		t.Fatalf("证书1应保持已注销")
	}
}

// 向上修正不恢复已撤销证书，只发新序号。
func TestUpwardCorrectionIssuesNewSerials(t *testing.T) {
	r := newTest(t, 10)
	must(t, r.RegisterFacility("F", "H", 1))
	must(t, r.RegisterMeter("F", 1, 3))
	must(t, r.RegisterMeter("F", 1, 1)) // 撤销 3,2
	if r.LastSerial() != 3 {
		t.Fatalf("序号=%d 期望3", r.LastSerial())
	}
	must(t, r.RegisterMeter("F", 1, 4)) // 回到4张：发4,5,6，不恢复2,3
	for _, s := range []int64{2, 3} {
		if r.Cert(s).Status != StatusRevoked {
			t.Fatalf("证书%d应仍为已撤销", s)
		}
	}
	if r.LastSerial() != 6 || r.CertCount() != 6 {
		t.Fatalf("序号=%d count=%d 期望6/6", r.LastSerial(), r.CertCount())
	}
}

// 用电量修正恰等于已注销量允许；再低报低于已注销量。
func TestConsumptionCorrectionAtBoundary(t *testing.T) {
	r := newTest(t, 10)
	must(t, r.RegisterFacility("F", "H", 1))
	must(t, r.RegisterMeter("F", 1, 2))
	must(t, r.RegisterConsumption("H", 1, 5))
	must(t, r.Declare("H", 1, []int64{1, 2}))
	must(t, r.RegisterConsumption("H", 1, 2))
	e := r.RegisterConsumption("H", 1, 1)
	if e == nil || e.Code != CodeBelowCancelled {
		t.Fatalf("期望低于已注销量, got %v", e)
	}
}

// 资格终止期与已核发冲突；终止只能推后或撤销；撤销后重登仍受约束。
func TestQualificationEndConflicts(t *testing.T) {
	r := newTest(t, 10)
	must(t, r.RegisterFacility("F", "H", 1))
	must(t, r.RegisterMeter("F", 5, 1)) // 已核发最大期5
	if e := r.SetQualificationEnd("F", 5); e == nil || e.Code != CodeConflictIssued {
		t.Fatalf("期望与已核发冲突, got %v", e)
	}
	must(t, r.SetQualificationEnd("F", 6))
	if e := r.SetQualificationEnd("F", 5); e == nil || e.Code != CodeInvalidParam {
		t.Fatalf("提前终止应报参数非法, got %v", e)
	}
	must(t, r.RevokeQualificationEnd("F"))
	if e := r.SetQualificationEnd("F", 5); e == nil || e.Code != CodeConflictIssued {
		t.Fatalf("撤销后重登仍应冲突, got %v", e)
	}
	must(t, r.SetQualificationEnd("F", 7))
	// 终止后发电期 7 资格外。
	if e := r.RegisterMeter("F", 7, 1); e == nil || e.Code != CodeOutsideQualification {
		t.Fatalf("期望资格外, got %v", e)
	}
}

// 批内混合失败报首个（序号最小）失败项及类别。
func TestBatchReportsFirstFailure(t *testing.T) {
	r := newTest(t, 10)
	must(t, r.RegisterFacility("F", "H", 1))
	must(t, r.RegisterMeter("F", 1, 3))
	must(t, r.RegisterConsumption("X", 1, 10))
	// 先把 2 转给 X；批 H->Y [1,2] 中 2 非 H 持有，首个失败项即序号2。
	must(t, r.Transfer("H", "X", []int64{2}))
	if e := r.Transfer("H", "Y", []int64{1, 2}); e == nil || e.Serial != 2 || e.Code != CodeNotHolder {
		t.Fatalf("期望序号2非持有人, got %v", e)
	}
	if r.Cert(1).Holder != "H" || r.Cert(2).Holder != "X" {
		t.Fatal("失败批不应改变状态")
	}
	// X 持 2：批 [2,999] 逐张升序，2 通过，999 序号不存在。
	e := r.Declare("X", 1, []int64{2, 999})
	if e == nil || e.Serial != 999 || e.Code != CodeInvalidParam {
		t.Fatalf("期望999序号不存在, got %v", e)
	}
	if r.Cert(2).Status != StatusHeld {
		t.Fatal("失败批不应改变证书状态")
	}
}

// 拒绝次序：参数非法 > 资格外 > 与已核发冲突 > 状态 > 非持有人 > 期限 > 超量 > 低于已注销。
func TestRejectionOrdering(t *testing.T) {
	r := newTest(t, 20)
	// 参数非法优先于资格外（设施存在但期越界 vs 期合法但设施不存在都属参数类）。
	if e := r.RegisterMeter("NOPE", 1, 1); e == nil || e.Code != CodeInvalidParam {
		t.Fatalf("got %v", e)
	}
	must(t, r.RegisterFacility("F", "H", 10))
	if e := r.RegisterMeter("F", 5, 1); e == nil || e.Code != CodeOutsideQualification {
		t.Fatalf("资格外优先, got %v", e)
	}
	must(t, r.RegisterMeter("F", 10, 1))
	// 与已核发冲突优先于状态问题（无法在同一操作制造，资格终止处单列）。
	if e := r.SetQualificationEnd("F", 10); e == nil || e.Code != CodeConflictIssued {
		t.Fatalf("冲突优先, got %v", e)
	}
	must(t, r.RegisterMeter("F", 11, 1)) // 序号2
	must(t, r.RegisterConsumption("H", 20, 10))
	must(t, r.Declare("H", 20, []int64{2}))
	// 状态不允许优先于非持有人。
	if e := r.Transfer("X", "Y", []int64{2}); e == nil || e.Code != CodeStateNotAllowed {
		t.Fatalf("状态优先于持有人, got %v", e)
	}
	// 非持有人优先于期限不符：把期10的证书(1)拿到手的 H 用远期申报。
	must(t, r.RegisterConsumption("Z", 50, 10))
	if e := r.Declare("Z", 50, []int64{1}); e == nil || e.Code != CodeNotHolder {
		t.Fatalf("非持有人优先于期限, got %v", e)
	}
	// 期限不符优先于超出用电量：小容量 + 超期证书。
	must(t, r.RegisterConsumption("H", 50, 0))
	if e := r.Declare("H", 50, []int64{1}); e == nil || e.Code != CodePeriodMismatch {
		t.Fatalf("期限优先于超量, got %v", e)
	}
}

// 自转视为参数非法。
func TestSelfTransferInvalid(t *testing.T) {
	r := newTest(t, 10)
	must(t, r.RegisterFacility("F", "H", 1))
	must(t, r.RegisterMeter("F", 1, 1))
	if e := r.Transfer("H", "H", []int64{1}); e == nil || e.Code != CodeInvalidParam {
		t.Fatalf("自转应参数非法, got %v", e)
	}
}

func must(t *testing.T, e *Error) {
	t.Helper()
	if e != nil {
		t.Fatalf("意外拒绝: %v", e)
	}
}

// TestConcurrentSerializability 并发混合操作下不应崩溃，序号连续、状态自洽。
// 用 -race 运行可验证数据竞争；结果等价于某种串行交错（本测试只校验不变量）。
func TestConcurrentSerializability(t *testing.T) {
	r := New(Config{UnitQty: 1, MaxAgePeriods: 5, MinPeriod: 1, MaxPeriod: 100000}, nil)
	if e := r.RegisterFacility("F", "H", 1); e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for p := int64(1); p <= 200; p++ {
			_ = r.RegisterMeter("F", p, 2)
		}
	}()
	for p := int64(1); p <= 200; p++ {
		_ = r.RegisterConsumption("H", p+200, 10)
	}
	<-done

	const workers = 8
	ch := make(chan int64, 400)
	finish := make(chan struct{}, workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer func() { finish <- struct{}{} }()
			for s := range ch {
				_ = r.Transfer("H", "H2", []int64{s})
				_ = r.Transfer("H2", "H", []int64{s})
				_ = r.Declare("H", s+200, []int64{s})
			}
		}()
	}
	for s := int64(1); s <= 400; s++ {
		ch <- s
	}
	close(ch)
	for w := 0; w < workers; w++ {
		<-finish
	}

	// 400 张中部分被注销/被下游撤销，但 1..LastSerial 必须连续无洞。
	if r.LastSerial() != int64(r.CertCount()) {
		t.Fatalf("序号有洞: last=%d count=%d", r.LastSerial(), r.CertCount())
	}
	for s := int64(1); s <= r.LastSerial(); s++ {
		if r.Cert(s) == nil {
			t.Fatalf("序号%d缺失", s)
		}
	}
}

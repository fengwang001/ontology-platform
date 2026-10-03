package bucket_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	bucket "ontology"
	"ontology/authz"
)

func op(perms ...authz.Permission) authz.Operator { return authz.New(perms...) }

const (
	pDel = authz.DeleteVersion
	pByp = authz.BypassGovernance
	pPut = authz.PutRetention
)

func k(s string) []byte { return []byte(s) }

func must(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: 意外错误 %v", what, err)
	}
}

func errCode(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, bucket.ErrInvalidArg):
		return "invalid"
	case errors.Is(err, bucket.ErrClockRegression):
		return "regress"
	case errors.Is(err, bucket.ErrPermission):
		return "perm"
	case errors.Is(err, bucket.ErrNotExist):
		return "notexist"
	case errors.Is(err, bucket.ErrNoSuchKey):
		return "absent"
	case errors.Is(err, bucket.ErrDeleted):
		return "deleted"
	case errors.Is(err, bucket.ErrLegalHold):
		return "hold"
	case errors.Is(err, bucket.ErrCompliance):
		return "compliance"
	case errors.Is(err, bucket.ErrGovernance):
		return "governance"
	}
	return "unknown:" + err.Error()
}

func batchCode(err error) string {
	if err == nil {
		return "ok"
	}
	var be *bucket.BatchError
	if errors.As(err, &be) {
		return fmt.Sprintf("batch:%d:%s", be.Index, errCode(be.Err))
	}
	return errCode(err)
}

// TestSpecExamples 规格中的走查例、锁例与默认保留例。
func TestSpecExamples(t *testing.T) {
	b := bucket.New(bucket.GOVERNANCE, 0)
	v1, err := b.Put(nil, k("a"), 1, 1)
	must(t, err, "put a@1")
	v2, err := b.Delete(nil, k("a"), 2)
	must(t, err, "delete a@2")
	v3, err := b.Put(nil, k("a"), 3, 3)
	must(t, err, "put a@3")
	if v1 != 1 || v2 != 2 || v3 != 3 {
		t.Fatalf("版本号非连续: %d %d %d", v1, v2, v3)
	}
	o, err := b.Get(k("a"))
	must(t, err, "get a=v3")
	if o.Ver != 3 {
		t.Fatalf("期望 v3 得 v%d", o.Ver)
	}
	must(t, b.DeleteVersion(op(pDel), k("a"), 3, false, 4), "删无保留 v3")
	_, err = b.Get(k("a"))
	if !errors.Is(err, bucket.ErrDeleted) {
		t.Fatalf("删 v3 后应被标记删除, 得 %v", err)
	}
	must(t, b.DeleteVersion(op(pDel), k("a"), 2, false, 5), "删标记 v2")
	o, err = b.Get(k("a"))
	must(t, err, "删 v2 后 Get=v1")
	if o.Ver != 1 {
		t.Fatalf("期望 v1 得 v%d", o.Ver)
	}

	v4, err := b.Put(nil, k("b"), 1, 10)
	must(t, err, "put b@10")
	must(t, b.SetRetention(op(pPut), k("b"), v4, bucket.GOVERNANCE, 200, false, 11), "设保留 200")
	if code := errCode(b.DeleteVersion(op(pDel), k("b"), v4, false, 150)); code != "governance" {
		t.Fatalf("t=150 无绕过: %s", code)
	}
	must(t, b.DeleteVersion(op(pDel, pByp), k("b"), v4, true, 150), "t=150 绕过删")
	au := b.Audit()
	if len(au) != 1 || au[0].Ver != v4 || au[0].Now != 150 || au[0].OldUntil != 200 {
		t.Fatalf("审计不符: %+v", au)
	}

	b2 := bucket.New(bucket.COMPLIANCE, 0)
	w, _ := b2.Put(nil, k("c"), 1, 1)
	must(t, b2.SetRetention(op(pPut), k("c"), w, bucket.COMPLIANCE, 200, false, 2), "设 COMPLIANCE 200")
	if code := errCode(b2.DeleteVersion(op(pDel, pByp), k("c"), w, true, 199)); code != "compliance" {
		t.Fatalf("t=199 绕过也不可越 COMPLIANCE: %s", code)
	}
	must(t, b2.DeleteVersion(op(pDel), k("c"), w, false, 200), "t=200 到期可删")

	b3 := bucket.New(bucket.COMPLIANCE, 100)
	d, _ := b3.Put(nil, k("d"), 1, 5)
	if code := errCode(b3.DeleteVersion(op(pDel), k("d"), d, false, 104)); code != "compliance" {
		t.Fatalf("t=104 应合规保留: %s", code)
	}
	must(t, b3.DeleteVersion(op(pDel), k("d"), d, false, 105), "t=105 到期")
}

// TestBoundaryEquality now 恰等于 retainUntil 视为到期，小 1 仍生效。
func TestBoundaryEquality(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode bucket.Mode
		at   int64
		want string
	}{
		{"COMPLIANCE now=until-1", bucket.COMPLIANCE, 199, "compliance"},
		{"COMPLIANCE now=until", bucket.COMPLIANCE, 200, "ok"},
		{"GOVERNANCE now=until-1", bucket.GOVERNANCE, 199, "governance"},
		{"GOVERNANCE now=until", bucket.GOVERNANCE, 200, "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := bucket.New(tc.mode, 0)
			v, _ := b.Put(nil, k("x"), 1, 1)
			must(t, b.SetRetention(op(pPut), k("x"), v, tc.mode, 200, false, 2), "设保留")
			if code := errCode(b.DeleteVersion(op(pDel), k("x"), v, false, tc.at)); code != tc.want {
				t.Fatalf("now=%d 得 %s 想 %s", tc.at, code, tc.want)
			}
		})
	}
}

// TestRetentionMigration 规格迁移例：升级/缩短差异、到期视同无锁。
func TestRetentionMigration(t *testing.T) {
	b := bucket.New(bucket.GOVERNANCE, 0)
	v, _ := b.Put(nil, k("g"), 1, 1)
	must(t, b.SetRetention(op(pPut), k("g"), v, bucket.GOVERNANCE, 100, false, 20), "GOV100")
	must(t, b.SetRetention(op(pPut), k("g"), v, bucket.GOVERNANCE, 100, false, 20), "GOV100 等长再设")
	if code := errCode(b.SetRetention(op(pPut), k("g"), v, bucket.COMPLIANCE, 99, false, 20)); code != "governance" {
		t.Fatalf("升级+缩短无绕过: %s", code)
	}
	// bypass 形参为真但操作者没有 BypassGovernance 权限：仍按治理保留拒绝。
	if code := errCode(b.SetRetention(op(pPut), k("g"), v, bucket.GOVERNANCE, 90, true, 20)); code != "governance" {
		t.Fatalf("无 BypassGovernance 权限的 bypass 不生效: %s", code)
	}
	must(t, b.SetRetention(op(pPut, pByp), k("g"), v, bucket.COMPLIANCE, 99, true, 20), "升级+缩短绕过")

	b2 := bucket.New(bucket.GOVERNANCE, 0)
	v2, _ := b2.Put(nil, k("g2"), 1, 1)
	must(t, b2.SetRetention(op(pPut), k("g2"), v2, bucket.GOVERNANCE, 100, false, 20), "GOV100")
	must(t, b2.SetRetention(op(pPut), k("g2"), v2, bucket.COMPLIANCE, 150, false, 20), "升级 COMP150 允许")
	if code := errCode(b2.SetRetention(op(pPut, pByp), k("g2"), v2, bucket.GOVERNANCE, 200, true, 20)); code != "compliance" {
		t.Fatalf("生效 COMPLIANCE 不可降级（绕过也不行）: %s", code)
	}
	must(t, b2.SetRetention(op(pPut), k("g2"), v2, bucket.GOVERNANCE, 160, false, 150), "t=150 到期后设 GOV160 允许")
}

// TestExpiredComplianceCanShrink 到期的 COMPLIANCE 可被重设为更短/清除。
func TestExpiredComplianceCanShrink(t *testing.T) {
	b := bucket.New(bucket.COMPLIANCE, 0)
	v, _ := b.Put(nil, k("e"), 1, 1)
	must(t, b.SetRetention(op(pPut), k("e"), v, bucket.COMPLIANCE, 100, false, 2), "COMP100")
	// 生效中缩短被 COMPLIANCE 拒绝；到期后同一变更视为无锁，立即允许。
	if code := errCode(b.SetRetention(op(pPut), k("e"), v, bucket.GOVERNANCE, 50, false, 50)); code != "invalid" {
		t.Fatalf("until<=now 恒为参数非法（参数先于保留类）: %s", code)
	}
	if code := errCode(b.SetRetention(op(pPut), k("e"), v, bucket.GOVERNANCE, 90, false, 50)); code != "compliance" {
		t.Fatalf("生效中缩短/降级: %s", code)
	}
	must(t, b.SetRetention(op(pPut), k("e"), v, bucket.GOVERNANCE, 105, false, 100), "到期后降级（until>now）允许")
	must(t, b.SetRetention(op(pPut), k("e"), v, bucket.NONE, 0, false, 105), "新保留也到期后清除允许")
}

// TestMarkerNoLock 标记不可加保留/法律保留（按版本不存在），但永远可删。
func TestMarkerNoLock(t *testing.T) {
	b := bucket.New(bucket.GOVERNANCE, 0)
	mk, _ := b.Delete(nil, k("z"), 1)
	if code := errCode(b.SetRetention(op(pPut), k("z"), mk, bucket.GOVERNANCE, 100, false, 2)); code != "notexist" {
		t.Fatalf("标记加保留: %s", code)
	}
	if code := errCode(b.SetHold(op(pPut), k("z"), mk, true, 2)); code != "notexist" {
		t.Fatalf("标记加法律保留: %s", code)
	}
	must(t, b.DeleteVersion(op(pDel), k("z"), mk, false, 3), "标记永远可删")
}

// TestLegalHoldBeatsExpired 法律保留压过已到期保留。
func TestLegalHoldBeatsExpired(t *testing.T) {
	b := bucket.New(bucket.COMPLIANCE, 0)
	v, _ := b.Put(nil, k("h"), 1, 1)
	must(t, b.SetRetention(op(pPut), k("h"), v, bucket.COMPLIANCE, 50, false, 2), "设保留")
	must(t, b.SetHold(op(pPut), k("h"), v, true, 3), "开法律保留")
	if code := errCode(b.DeleteVersion(op(pDel, pByp), k("h"), v, true, 100)); code != "hold" {
		t.Fatalf("已到期但 hold: %s", code)
	}
	must(t, b.SetHold(op(pPut), k("h"), v, false, 101), "关法律保留")
	must(t, b.DeleteVersion(op(pDel), k("h"), v, false, 102), "到期且无 hold 可删")
}

// TestBatchAtomic 批量任一失败整批不留痕；成功审计按下标升序。
func TestBatchAtomic(t *testing.T) {
	b := bucket.New(bucket.COMPLIANCE, 0)
	x, _ := b.Put(nil, k("x"), 1, 1)
	y, _ := b.Put(nil, k("y"), 1, 1)
	must(t, b.SetRetention(op(pPut), k("y"), y, bucket.COMPLIANCE, 100, false, 2), "y 保留")
	items := []bucket.Item{{Key: k("x"), Ver: x}, {Key: k("y"), Ver: y}}
	if code := batchCode(b.DeleteVersions(op(pDel), items, false, 10)); code != "batch:1:compliance" {
		t.Fatalf("批量失败定位: %s", code)
	}
	if o, err := b.Get(k("x")); err != nil || o.Ver != x {
		t.Fatalf("失败批量删除了 x: %v %v", o, err)
	}
	if len(b.Audit()) != 0 {
		t.Fatal("失败批量写入了审计")
	}
	// 时钟仍停在被接受的 t=2：t=2 时 y 仍在保留内。
	if code := errCode(b.DeleteVersion(op(pDel), k("y"), y, false, 2)); code != "compliance" {
		t.Fatalf("时钟疑似被失败批量推进: %s", code)
	}

	if code := batchCode(b.DeleteVersions(op(pDel), nil, false, 10)); code != "invalid" {
		t.Fatalf("空批: %s", code)
	}
	dup := []bucket.Item{{Key: k("x"), Ver: x}, {Key: k("x"), Ver: x}}
	if code := batchCode(b.DeleteVersions(op(pDel), dup, false, 10)); code != "invalid" {
		t.Fatalf("重复项: %s", code)
	}
	badVer := []bucket.Item{{Key: k("x"), Ver: 0}}
	if code := batchCode(b.DeleteVersions(op(pDel), badVer, false, 10)); code != "invalid" {
		t.Fatalf("非法 ver: %s", code)
	}
	if code := batchCode(b.DeleteVersions(op(pDel), items[:1], false, 1)); code != "regress" {
		t.Fatalf("批量时钟回退: %s", code)
	}
	if code := batchCode(b.DeleteVersions(nil, items[:1], false, 10)); code != "perm" {
		t.Fatalf("批量权限: %s", code)
	}

	// 全成功：两个生效 GOVERNANCE 经绕过删除，审计按下标升序。
	g1, _ := b.Put(nil, k("g1"), 1, 20)
	g2, _ := b.Put(nil, k("g2"), 1, 21)
	must(t, b.SetRetention(op(pPut), k("g1"), g1, bucket.GOVERNANCE, 100, false, 22), "g1 保留")
	must(t, b.SetRetention(op(pPut), k("g2"), g2, bucket.GOVERNANCE, 100, false, 23), "g2 保留")
	okItems := []bucket.Item{{Key: k("g2"), Ver: g2}, {Key: k("g1"), Ver: g1}}
	must(t, b.DeleteVersions(op(pDel, pByp), okItems, true, 50), "批量绕过删除")
	au := b.Audit()
	if len(au) != 2 || au[0].Ver != g2 || au[1].Ver != g1 {
		t.Fatalf("审计次序/内容: %+v", au)
	}
	if _, err := b.Get(k("g1")); !errors.Is(err, bucket.ErrNoSuchKey) {
		t.Fatalf("g1 应已删: %v", err)
	}
}

// TestRejectOrder 校验每个操作的拒绝次序只报第一个原因。
func TestRejectOrder(t *testing.T) {
	b := bucket.New(bucket.GOVERNANCE, 0)
	// Put：参数非法先于时钟回退（t=-1 且 key 空）。
	if _, err := b.Put(nil, nil, 0, -1); !errors.Is(err, bucket.ErrInvalidArg) {
		t.Fatalf("Put 次序: %v", err)
	}
	v, _ := b.Put(nil, k("r"), 1, 10)
	// DeleteVersion：非法参数 > 时钟回退 > 权限 > 不存在 > hold > compliance > governance。
	if code := errCode(b.DeleteVersion(nil, k("r"), 0, false, 1)); code != "invalid" {
		t.Fatalf("ver 非正: %s", code)
	}
	if code := errCode(b.DeleteVersion(nil, k("r"), v, false, 1)); code != "regress" {
		t.Fatalf("时钟回退先于权限: %s", code)
	}
	if code := errCode(b.DeleteVersion(nil, k("r"), v, false, 10)); code != "perm" {
		t.Fatalf("权限先于存在性: %s", code)
	}
	if code := errCode(b.DeleteVersion(op(pDel), k("r"), 999, false, 10)); code != "notexist" {
		t.Fatalf("不存在: %s", code)
	}
	must(t, b.SetHold(op(pPut), k("r"), v, true, 11), "开 hold")
	must(t, b.SetRetention(op(pPut), k("r"), v, bucket.COMPLIANCE, 100, false, 12), "加 COMPLIANCE")
	if code := errCode(b.DeleteVersion(op(pDel, pByp), k("r"), v, true, 13)); code != "hold" {
		t.Fatalf("法律保留先于合规: %s", code)
	}
	must(t, b.SetHold(op(pPut), k("r"), v, false, 14), "关 hold")
	if code := errCode(b.DeleteVersion(op(pDel, pByp), k("r"), v, true, 15)); code != "compliance" {
		t.Fatalf("合规先于治理: %s", code)
	}
	// SetRetention 非法参数：until 不严格大于 now。
	if code := errCode(b.SetRetention(op(pPut), k("r"), v, bucket.GOVERNANCE, 15, false, 15)); code != "invalid" {
		t.Fatalf("until==now 非法: %s", code)
	}
	// SetRetention 无权限先于版本不存在。
	if code := errCode(b.SetRetention(nil, k("nope"), 1, bucket.GOVERNANCE, 100, false, 16)); code != "perm" {
		t.Fatalf("SetRetention 权限次序: %s", code)
	}
}

// TestVersionChainAndReplay 版本号连续无洞；同序列重放结果与审计一致。
func TestVersionChainAndReplay(t *testing.T) {
	run := func() (seq []int64, au []bucket.AuditEntry) {
		b := bucket.New(bucket.COMPLIANCE, 10)
		a, _ := b.Put(nil, k("a"), 1, 1)
		m, _ := b.Delete(nil, k("a"), 2)
		c, _ := b.Put(nil, k("c"), 1, 3)
		// 被拒绝的操作不占号、不推进时钟。
		_, _ = b.Put(nil, nil, 1, 4)
		_, _ = b.Put(nil, k("a"), -1, 4)
		d, _ := b.Put(nil, k("d"), 1, 4)
		must(t, b.DeleteVersion(op(pDel), k("a"), a, false, 20), "到期删 v1")
		must(t, b.DeleteVersion(op(pDel), k("a"), m, false, 21), "删标记")
		return []int64{a, m, c, d}, b.Audit()
	}
	s1, a1 := run()
	s2, a2 := run()
	if fmt.Sprint(s1) != "[1 2 3 4]" || fmt.Sprint(s1) != fmt.Sprint(s2) {
		t.Fatalf("版本链: %v %v", s1, s2)
	}
	if fmt.Sprint(a1) != fmt.Sprint(a2) {
		t.Fatalf("重放审计不一致")
	}
	if len(a1) != 0 {
		t.Fatalf("到期/无绕过删除不应入审计: %+v", a1)
	}
}

// TestConcurrentEquivalentToSerial 高并发调用结果必须等价于某个串行顺序：
// 版本号集合恰为 1..N 无洞无重。
func TestConcurrentEquivalentToSerial(t *testing.T) {
	b := bucket.New(bucket.COMPLIANCE, 0)
	const writers, each = 16, 200
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	var clock int64
	var accepted int64
	accMu := sync.Mutex{}
	gotVers := map[int64]bool{}
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := k(fmt.Sprintf("p%d", id))
			for i := 0; i < each; i++ {
				now := atomic.AddInt64(&clock, 1)
				v, err := b.Put(nil, key, 1, now)
				if err != nil {
					// 取号与加锁分离时，时钟回退被拒绝是合法的串行化结果。
					if errors.Is(err, bucket.ErrClockRegression) {
						continue
					}
					errs <- err
					return
				}
				accMu.Lock()
				accepted++
				gotVers[v] = true
				accMu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if accepted == 0 {
		t.Fatal("没有任何被接受的 Put")
	}
	// 被接受的版本号必须恰好为 1..accepted（全桶单调、连续、无重号）。
	if int64(len(gotVers)) != accepted {
		t.Fatalf("接受数=%d 去重版本数=%d（有重号）", accepted, len(gotVers))
	}
	for v := int64(1); v <= accepted; v++ {
		if !gotVers[v] {
			t.Fatalf("被接受版本号缺 %d（有洞），共接受 %d", v, accepted)
		}
	}
	// 对一个键做“删当前→重指”，验证并发写入后版本链仍正确。
	o, err := b.Get(k("p0"))
	if err != nil {
		t.Fatal(err)
	}
	top := o.Ver
	if err := b.DeleteVersion(op(pDel), k("p0"), top, false, atomic.LoadInt64(&clock)+1); err != nil {
		t.Fatal(err)
	}
	o2, err := b.Get(k("p0"))
	if err != nil || o2.Ver >= top {
		t.Fatalf("并发后删当前未正确重指: v=%d err=%v", o2.Ver, err)
	}
}

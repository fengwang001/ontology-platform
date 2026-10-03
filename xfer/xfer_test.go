package xfer

import (
	"testing"
)

// step 是表驱动场景中的一步操作。
type step struct {
	name string
	run  func(s *Service) error
	want error
}

// runSteps 逐步执行并比对错误，日志打印输入、输出与判定依据。
func runSteps(t *testing.T, s *Service, steps []step) {
	t.Helper()
	for i, st := range steps {
		got := st.run(s)
		t.Logf("step=%d 输入=%s 输出=%v 期望=%v", i, st.name, got, st.want)
		if got != st.want {
			t.Fatalf("step=%d %s: got %v, want %v", i, st.name, got, st.want)
		}
	}
}

// checkTenant 校验租户的用量、额度与桶数。
func checkTenant(t *testing.T, s *Service, tenant string, usage, limit int64, buckets int) {
	t.Helper()
	if got := s.Usage(tenant); got != usage {
		t.Errorf("Usage(%s)=%d, want %d", tenant, got, usage)
	}
	if got := s.Limit(tenant); got != limit {
		t.Errorf("Limit(%s)=%d, want %d", tenant, got, limit)
	}
	if got := s.BucketCount(tenant); got != buckets {
		t.Errorf("BucketCount(%s)=%d, want %d", tenant, got, buckets)
	}
}

// checkBucket 校验桶的属主、字节数与开启上传数。
func checkBucket(t *testing.T, s *Service, bucket, owner string, bytes, open int64) {
	t.Helper()
	if got, ok := s.Owner(bucket); !ok || got != owner {
		t.Errorf("Owner(%s)=(%q,%v), want (%q,true)", bucket, got, ok, owner)
	}
	if got, _ := s.BucketBytes(bucket); got != bytes {
		t.Errorf("BucketBytes(%s)=%d, want %d", bucket, got, bytes)
	}
	if got, _ := s.OpenUploads(bucket); got != open {
		t.Errorf("OpenUploads(%s)=%d, want %d", bucket, got, open)
	}
}

// checkInvariants 校验全局不变式：租户用量等于其拥有桶的字节数之和，
// 租户桶数等于其拥有的桶数，每个桶恰有一个属主。
func checkInvariants(t *testing.T, s *Service, tenants, buckets []string) {
	t.Helper()
	sum := make(map[string]int64)
	cnt := make(map[string]int)
	for _, b := range buckets {
		owner, ok := s.Owner(b)
		if !ok {
			continue
		}
		if owner == "" {
			t.Errorf("桶 %s 属主为空", b)
		}
		bytes, _ := s.BucketBytes(b)
		sum[owner] += bytes
		cnt[owner]++
	}
	for _, tn := range tenants {
		if got := s.Usage(tn); got != sum[tn] {
			t.Errorf("不变式违反: Usage(%s)=%d, 拥有桶字节和=%d", tn, got, sum[tn])
		}
		if got := s.BucketCount(tn); got != cnt[tn] {
			t.Errorf("不变式违反: BucketCount(%s)=%d, 实际拥有=%d", tn, got, cnt[tn])
		}
	}
}

// newExample 构造题目示例场景：MaxB=2，额度 A=100、B=50，A 建桶 b1 并 Put 40。
func newExample(t *testing.T) *Service {
	t.Helper()
	s := New(2)
	runSteps(t, s, []step{
		{"SetLimit(A,100)", func(s *Service) error { return s.SetLimit("A", 100) }, nil},
		{"SetLimit(B,50)", func(s *Service) error { return s.SetLimit("B", 50) }, nil},
		{"CreateBucket(A,b1,0)", func(s *Service) error { return s.CreateBucket("A", "b1", 0) }, nil},
		{"Put(A,b1,40,1)", func(s *Service) error { return s.Put("A", "b1", 40, 1) }, nil},
	})
	return s
}

// TestExampleScenario 复现题目主示例。
func TestExampleScenario(t *testing.T) {
	s := newExample(t)
	runSteps(t, s, []step{
		{"t=5 Offer(b1,A->B,ttl=10) 截止15", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 5) }, nil},
		{"t=6 A Put 冻结", func(s *Service) error { return s.Put("A", "b1", 1, 6) }, ErrFrozen},
		{"t=15 B Accept 已到期", func(s *Service) error { return s.Accept("b1", "B", 15) }, ErrExpired},
		{"t=14 B Accept 成功(0+40<=50)", func(s *Service) error { return s.Accept("b1", "B", 14) }, nil},
		{"A 再 Put 非属主", func(s *Service) error { return s.Put("A", "b1", 1, 16) }, ErrNotOwner},
	})
	checkTenant(t, s, "A", 0, 100, 0)
	checkTenant(t, s, "B", 40, 50, 1)
	checkBucket(t, s, "b1", "B", 40, 0)
	checkInvariants(t, s, []string{"A", "B"}, []string{"b1"})
}

// TestExampleQuotaExceeded 复现示例变体：B 已有 11 字节用量时 Accept 报额度不足。
func TestExampleQuotaExceeded(t *testing.T) {
	s := New(2)
	runSteps(t, s, []step{
		{"SetLimit(A,100)", func(s *Service) error { return s.SetLimit("A", 100) }, nil},
		{"SetLimit(B,50)", func(s *Service) error { return s.SetLimit("B", 50) }, nil},
		{"CreateBucket(A,b1,0)", func(s *Service) error { return s.CreateBucket("A", "b1", 0) }, nil},
		{"CreateBucket(B,b2,1)", func(s *Service) error { return s.CreateBucket("B", "b2", 1) }, nil},
		{"Put(A,b1,40,2)", func(s *Service) error { return s.Put("A", "b1", 40, 2) }, nil},
		{"Put(B,b2,11,3)", func(s *Service) error { return s.Put("B", "b2", 11, 3) }, nil},
		{"Offer(b1,A->B,ttl=10,4)", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 4) }, nil},
		{"Accept: 11+40=51>50 额度不足", func(s *Service) error { return s.Accept("b1", "B", 5) }, ErrQuota},
	})
	checkInvariants(t, s, []string{"A", "B"}, []string{"b1", "b2"})
}

// TestExampleInFlight 复现示例变体：有在途上传时报有在途上传，EndUpload 后成功。
func TestExampleInFlight(t *testing.T) {
	s := newExample(t)
	runSteps(t, s, []step{
		{"BeginUpload(A,b1,2)", func(s *Service) error { return s.BeginUpload("A", "b1", 2) }, nil},
		{"Offer(b1,A->B,ttl=10,5)", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 5) }, nil},
		{"Accept 有在途上传", func(s *Service) error { return s.Accept("b1", "B", 6) }, ErrInFlight},
		{"EndUpload 冻结期放行", func(s *Service) error { return s.EndUpload("A", "b1", 7) }, nil},
		{"Accept 成功", func(s *Service) error { return s.Accept("b1", "B", 8) }, nil},
	})
	checkTenant(t, s, "A", 0, 100, 0)
	checkTenant(t, s, "B", 40, 50, 1)
	checkInvariants(t, s, []string{"A", "B"}, []string{"b1"})
}

// TestDeadlineBoundary 截止时刻恰等到期、小 1 未到期。
func TestDeadlineBoundary(t *testing.T) {
	// now == 截止时刻：已到期。
	s1 := newExample(t)
	runSteps(t, s1, []step{
		{"Offer ttl=10 @5 截止15", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 5) }, nil},
		{"Accept @15 恰等截止 已到期", func(s *Service) error { return s.Accept("b1", "B", 15) }, ErrExpired},
	})
	// now == 截止时刻-1：成功。
	s2 := newExample(t)
	runSteps(t, s2, []step{
		{"Offer ttl=10 @5 截止15", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 5) }, nil},
		{"Accept @14 小1 成功", func(s *Service) error { return s.Accept("b1", "B", 14) }, nil},
	})
	checkTenant(t, s2, "B", 40, 50, 1)
	// 冻结边界：@14 仍冻结，@15 起不再冻结。
	s3 := newExample(t)
	runSteps(t, s3, []step{
		{"Offer ttl=10 @5", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 5) }, nil},
		{"Put @14 冻结", func(s *Service) error { return s.Put("A", "b1", 1, 14) }, ErrFrozen},
		{"Put @15 不再冻结且清掉到期要约", func(s *Service) error { return s.Put("A", "b1", 1, 15) }, nil},
		{"Accept @16 无要约", func(s *Service) error { return s.Accept("b1", "B", 16) }, ErrNoOffer},
	})
	checkTenant(t, s3, "A", 41, 100, 1)
	checkInvariants(t, s3, []string{"A", "B"}, []string{"b1"})
}

// TestQuotaBoundary 额度恰等通过、大 1 拒绝（Put 与 Accept 两侧）。
func TestQuotaBoundary(t *testing.T) {
	// Put：用量+size 恰等额度通过，大 1 拒绝。
	s1 := New(2)
	runSteps(t, s1, []step{
		{"SetLimit(A,100)", func(s *Service) error { return s.SetLimit("A", 100) }, nil},
		{"CreateBucket(A,b1,0)", func(s *Service) error { return s.CreateBucket("A", "b1", 0) }, nil},
		{"Put 100 恰等额度", func(s *Service) error { return s.Put("A", "b1", 100, 1) }, nil},
		{"Put 1 超出 额度不足", func(s *Service) error { return s.Put("A", "b1", 1, 2) }, ErrQuota},
	})
	// Accept：to 用量+桶字节 恰等额度通过，大 1 拒绝。
	s2 := New(3)
	runSteps(t, s2, []step{
		{"SetLimit(A,100)", func(s *Service) error { return s.SetLimit("A", 100) }, nil},
		{"SetLimit(B,50)", func(s *Service) error { return s.SetLimit("B", 50) }, nil},
		{"CreateBucket(A,b1,0)", func(s *Service) error { return s.CreateBucket("A", "b1", 0) }, nil},
		{"CreateBucket(B,b2,1)", func(s *Service) error { return s.CreateBucket("B", "b2", 1) }, nil},
		{"Put(A,b1,40,2)", func(s *Service) error { return s.Put("A", "b1", 40, 2) }, nil},
		{"Put(B,b2,11,3)", func(s *Service) error { return s.Put("B", "b2", 11, 3) }, nil},
		{"Offer(b1,A->B,4)", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 4) }, nil},
		{"Accept 51>50 额度不足", func(s *Service) error { return s.Accept("b1", "B", 5) }, ErrQuota},
		{"Delete(B,b2,1,6) 降到10", func(s *Service) error { return s.Delete("B", "b2", 1, 6) }, nil},
		{"Accept 10+40=50 恰等通过", func(s *Service) error { return s.Accept("b1", "B", 7) }, nil},
	})
	checkTenant(t, s2, "B", 50, 50, 2)
	checkInvariants(t, s2, []string{"A", "B"}, []string{"b1", "b2"})
}

// TestFreeze 冻结期内 Put/Delete/BeginUpload 报桶被冻结，EndUpload 与只读查询照常。
func TestFreeze(t *testing.T) {
	s := newExample(t)
	runSteps(t, s, []step{
		{"BeginUpload(A,b1,2)", func(s *Service) error { return s.BeginUpload("A", "b1", 2) }, nil},
		{"Offer(b1,A->B,ttl=10,5)", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 5) }, nil},
		{"Put 冻结", func(s *Service) error { return s.Put("A", "b1", 1, 6) }, ErrFrozen},
		{"Delete 冻结", func(s *Service) error { return s.Delete("A", "b1", 1, 6) }, ErrFrozen},
		{"BeginUpload 冻结", func(s *Service) error { return s.BeginUpload("A", "b1", 6) }, ErrFrozen},
		{"EndUpload 放行", func(s *Service) error { return s.EndUpload("A", "b1", 6) }, nil},
	})
	checkBucket(t, s, "b1", "A", 40, 0)
	if _, ok := s.OfferOf("b1"); !ok {
		t.Error("冻结期 EndUpload 不应清除未到期要约")
	}
	checkInvariants(t, s, []string{"A", "B"}, []string{"b1"})
}

// TestExpiredOfferLanding 到期要约只落地于被接受的操作。
func TestExpiredOfferLanding(t *testing.T) {
	// 被拒绝的操作不清除到期要约；被接受的 Put 清除后 Accept 报无要约。
	s1 := newExample(t)
	runSteps(t, s1, []step{
		{"Offer ttl=10 @5 截止15", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 5) }, nil},
		{"Accept @15 已到期(拒绝不清除)", func(s *Service) error { return s.Accept("b1", "B", 15) }, ErrExpired},
		{"Cancel @16 已到期(拒绝不清除)", func(s *Service) error { return s.Cancel("b1", "A", 16) }, ErrExpired},
		{"Put @17 被接受 清掉到期要约", func(s *Service) error { return s.Put("A", "b1", 1, 17) }, nil},
		{"Accept @18 无要约", func(s *Service) error { return s.Accept("b1", "B", 18) }, ErrNoOffer},
	})
	// 被接受的 EndUpload 同样清掉到期要约。
	s2 := newExample(t)
	runSteps(t, s2, []step{
		{"BeginUpload(A,b1,2)", func(s *Service) error { return s.BeginUpload("A", "b1", 2) }, nil},
		{"Offer ttl=10 @5", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 5) }, nil},
		{"EndUpload @15 被接受 清掉到期要约", func(s *Service) error { return s.EndUpload("A", "b1", 15) }, nil},
		{"Accept @16 无要约", func(s *Service) error { return s.Accept("b1", "B", 16) }, ErrNoOffer},
	})
	// 已到期的要约被新要约取代。
	s3 := newExample(t)
	runSteps(t, s3, []step{
		{"Offer(A->B,ttl=10,@5)", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 5) }, nil},
		{"Offer(A->B) @14 已有要约", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 14) }, ErrHasOffer},
		{"Offer(A->B) @15 取代到期要约", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 15) }, nil},
		{"Accept @24 新要约未到期 成功", func(s *Service) error { return s.Accept("b1", "B", 24) }, nil},
	})
	checkTenant(t, s3, "B", 40, 50, 1)
	checkInvariants(t, s3, []string{"A", "B"}, []string{"b1"})
}

// TestAcceptRejectionOrder 拒绝次序：有在途上传 > 桶数超限 > 额度不足。
func TestAcceptRejectionOrder(t *testing.T) {
	s := New(2)
	runSteps(t, s, []step{
		{"SetLimit(A,1000)", func(s *Service) error { return s.SetLimit("A", 1000) }, nil},
		{"SetLimit(B,50)", func(s *Service) error { return s.SetLimit("B", 50) }, nil},
		{"CreateBucket(A,b1,0)", func(s *Service) error { return s.CreateBucket("A", "b1", 0) }, nil},
		{"CreateBucket(B,b2,1)", func(s *Service) error { return s.CreateBucket("B", "b2", 1) }, nil},
		{"CreateBucket(B,b3,2)", func(s *Service) error { return s.CreateBucket("B", "b3", 2) }, nil},
		{"Put(A,b1,40,3)", func(s *Service) error { return s.Put("A", "b1", 40, 3) }, nil},
		{"Put(B,b2,11,4)", func(s *Service) error { return s.Put("B", "b2", 11, 4) }, nil},
		{"BeginUpload(A,b1,5)", func(s *Service) error { return s.BeginUpload("A", "b1", 5) }, nil},
		{"Offer(b1,A->B,ttl=100,6)", func(s *Service) error { return s.Offer("b1", "A", "B", 100, 6) }, nil},
		{"三种拒绝同时成立 报有在途上传", func(s *Service) error { return s.Accept("b1", "B", 7) }, ErrInFlight},
		{"EndUpload(A,b1,8)", func(s *Service) error { return s.EndUpload("A", "b1", 8) }, nil},
		{"桶数超限先于额度不足", func(s *Service) error { return s.Accept("b1", "B", 9) }, ErrTooManyBuckets},
	})
	// 释放 B 的一个桶（通过把 b3 转给 A）后，报额度不足。
	runSteps(t, s, []step{
		{"Offer(b3,B->A,ttl=100,10)", func(s *Service) error { return s.Offer("b3", "B", "A", 100, 10) }, nil},
		{"Accept(b3,A,11) A 桶数 1<2 额度 1000 足够", func(s *Service) error { return s.Accept("b3", "A", 11) }, nil},
		{"Accept(b1,B,12) 11+40=51>50 额度不足", func(s *Service) error { return s.Accept("b1", "B", 12) }, ErrQuota},
	})
	checkInvariants(t, s, []string{"A", "B"}, []string{"b1", "b2", "b3"})
}

// TestCancel 撤销要约的全部路径。
func TestCancel(t *testing.T) {
	s := newExample(t)
	runSteps(t, s, []step{
		{"Cancel 无要约", func(s *Service) error { return s.Cancel("b1", "A", 2) }, ErrNoOffer},
		{"Cancel 桶不存在", func(s *Service) error { return s.Cancel("nope", "A", 2) }, ErrNoBucket},
		{"Offer ttl=10 @5", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 5) }, nil},
		{"Cancel 非当事人", func(s *Service) error { return s.Cancel("b1", "C", 6) }, ErrNotParty},
		{"Cancel 已到期", func(s *Service) error { return s.Cancel("b1", "A", 15) }, ErrExpired},
		{"Cancel by=to 成功", func(s *Service) error { return s.Cancel("b1", "B", 7) }, nil},
		{"Cancel 已无要约", func(s *Service) error { return s.Cancel("b1", "A", 8) }, ErrNoOffer},
		{"Put 解冻成功", func(s *Service) error { return s.Put("A", "b1", 1, 9) }, nil},
	})
	// by=from 也可撤销。
	s2 := newExample(t)
	runSteps(t, s2, []step{
		{"Offer ttl=10 @5", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 5) }, nil},
		{"Cancel by=from 成功", func(s *Service) error { return s.Cancel("b1", "A", 6) }, nil},
	})
	checkTenant(t, s2, "A", 40, 100, 1)
}

// TestOfferErrors 要约的参数与次序。
func TestOfferErrors(t *testing.T) {
	s := newExample(t)
	runSteps(t, s, []step{
		{"to==from 参数非法", func(s *Service) error { return s.Offer("b1", "A", "A", 10, 2) }, ErrBadParam},
		{"ttl=0 参数非法", func(s *Service) error { return s.Offer("b1", "A", "B", 0, 2) }, ErrBadParam},
		{"ttl>1e9 参数非法", func(s *Service) error { return s.Offer("b1", "A", "B", 1_000_000_001, 2) }, ErrBadParam},
		{"from 非属主 报非属主", func(s *Service) error { return s.Offer("b1", "B", "A", 10, 2) }, ErrNotOwner},
		{"桶不存在", func(s *Service) error { return s.Offer("nope", "A", "B", 10, 2) }, ErrNoBucket},
		{"时钟回退", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 0) }, ErrClock},
		{"正常要约", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 5) }, nil},
		{"已有要约", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 6) }, ErrHasOffer},
	})
}

// TestPutDeleteOrder Put/Delete/BeginUpload/EndUpload 的拒绝次序。
func TestPutDeleteOrder(t *testing.T) {
	s := newExample(t)
	runSteps(t, s, []step{
		{"Put size=0 参数非法", func(s *Service) error { return s.Put("A", "b1", 0, 2) }, ErrBadParam},
		{"Put size>1e12 参数非法", func(s *Service) error { return s.Put("A", "b1", 1_000_000_000_001, 2) }, ErrBadParam},
		{"Put 空属主 参数非法", func(s *Service) error { return s.Put("", "b1", 1, 2) }, ErrBadParam},
		{"Put 时钟回退", func(s *Service) error { return s.Put("A", "b1", 1, 0) }, ErrClock},
		{"Put 桶不存在", func(s *Service) error { return s.Put("A", "nope", 1, 2) }, ErrNoBucket},
		{"Put 非属主", func(s *Service) error { return s.Put("B", "b1", 1, 2) }, ErrNotOwner},
		{"Delete 超过桶字节数 参数非法", func(s *Service) error { return s.Delete("A", "b1", 41, 2) }, ErrBadParam},
		{"Delete 恰等桶字节数", func(s *Service) error { return s.Delete("A", "b1", 40, 2) }, nil},
		{"Put 回 40", func(s *Service) error { return s.Put("A", "b1", 40, 3) }, nil},
		{"EndUpload 计数为0 参数非法", func(s *Service) error { return s.EndUpload("A", "b1", 4) }, ErrBadParam},
		{"BeginUpload 非属主", func(s *Service) error { return s.BeginUpload("B", "b1", 4) }, ErrNotOwner},
		{"BeginUpload ok", func(s *Service) error { return s.BeginUpload("A", "b1", 4) }, nil},
		{"EndUpload 不查属主 非属主也可收尾", func(s *Service) error { return s.EndUpload("B", "b1", 5) }, nil},
	})
	checkBucket(t, s, "b1", "A", 40, 0)
	checkInvariants(t, s, []string{"A", "B"}, []string{"b1"})
}

// TestCreateBucket 建桶的重名、桶数超限与时钟。
func TestCreateBucket(t *testing.T) {
	s := New(2)
	runSteps(t, s, []step{
		{"空桶名 参数非法", func(s *Service) error { return s.CreateBucket("A", "", 0) }, ErrBadParam},
		{"now<0 参数非法", func(s *Service) error { return s.CreateBucket("A", "b1", -1) }, ErrBadParam},
		{"CreateBucket(A,b1,0)", func(s *Service) error { return s.CreateBucket("A", "b1", 0) }, nil},
		{"重名 已存在", func(s *Service) error { return s.CreateBucket("B", "b1", 1) }, ErrBucketExists},
		{"CreateBucket(A,b2,1)", func(s *Service) error { return s.CreateBucket("A", "b2", 1) }, nil},
		{"A 已有2桶 桶数超限", func(s *Service) error { return s.CreateBucket("A", "b3", 2) }, ErrTooManyBuckets},
		{"时钟回退", func(s *Service) error { return s.CreateBucket("B", "b3", 0) }, ErrClock},
		{"B 建 b3 ok", func(s *Service) error { return s.CreateBucket("B", "b3", 2) }, nil},
	})
	checkTenant(t, s, "A", 0, 0, 2)
	checkTenant(t, s, "B", 0, 0, 1)
}

// TestSetLimit 额度设置：总被接受、可低于现有用量、未设额度为 0。
func TestSetLimit(t *testing.T) {
	s := New(1)
	runSteps(t, s, []step{
		{"q<0 参数非法", func(s *Service) error { return s.SetLimit("A", -1) }, ErrBadParam},
		{"q>1e15 参数非法", func(s *Service) error { return s.SetLimit("A", 1_000_000_000_000_001) }, ErrBadParam},
		{"SetLimit(A,10)", func(s *Service) error { return s.SetLimit("A", 10) }, nil},
		{"CreateBucket(A,b1,0)", func(s *Service) error { return s.CreateBucket("A", "b1", 0) }, nil},
		{"Put 10 恰等", func(s *Service) error { return s.Put("A", "b1", 10, 1) }, nil},
		{"SetLimit(A,5) 低于现有用量 仍被接受", func(s *Service) error { return s.SetLimit("A", 5) }, nil},
		{"Put 超额 额度不足", func(s *Service) error { return s.Put("A", "b1", 1, 2) }, ErrQuota},
		{"Delete 5 不查额度", func(s *Service) error { return s.Delete("A", "b1", 5, 3) }, nil},
	})
	if got := s.Limit("B"); got != 0 {
		t.Errorf("未设额度的租户 Limit=%d, want 0", got)
	}
	checkTenant(t, s, "A", 5, 5, 1)
}

// cleanAcceptLog 是一次完整 Accept 应产生的日志。
func cleanAcceptLog() []LogEntry {
	return []LogEntry{
		{Kind: LogBegin, Bucket: "b1", From: "A", To: "B", Bytes: 40, Step: 1},
		{Kind: LogStep, Bucket: "b1", Step: 2},
		{Kind: LogStep, Bucket: "b1", Step: 3},
		{Kind: LogStep, Bucket: "b1", Step: 4},
		{Kind: LogEnd, Bucket: "b1", From: "A", To: "B", Bytes: 40, Step: 5},
	}
}

// equalLog 比较两条日志是否逐字段相同。
func equalLog(a, b []LogEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestCrashRecovery 对 k 取 0 到 5 的六个崩溃点分别验证恢复结果。
func TestCrashRecovery(t *testing.T) {
	// 对照组：一次完整 Accept 的最终状态与日志。
	clean := newExample(t)
	runSteps(t, clean, []step{
		{"Offer ttl=10 @5", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 5) }, nil},
		{"Accept @10", func(s *Service) error { return s.Accept("b1", "B", 10) }, nil},
	})
	wantLog := cleanAcceptLog()
	if got := clean.Log(); !equalLog(got, wantLog) {
		t.Fatalf("完整 Accept 日志=%+v, want %+v", got, wantLog)
	}

	for k := 0; k <= 5; k++ {
		t.Run("k="+string(rune('0'+k)), func(t *testing.T) {
			s := newExample(t)
			runSteps(t, s, []step{
				{"Offer ttl=10 @5", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 5) }, nil},
			})
			s.InjectCrashAfterStep(k)
			if got := s.Accept("b1", "B", 10); got != ErrNeedRecovery {
				t.Fatalf("崩溃的 Accept 应报需要恢复, got %v", got)
			}
			// 崩溃后除 Recover 外一切操作报需要恢复。
			if got := s.Put("A", "b1", 1, 11); got != ErrNeedRecovery {
				t.Fatalf("崩溃后 Put 应报需要恢复, got %v", got)
			}
			s.Recover()
			s.Recover() // 重复 Recover 幂等
			if k == 0 {
				// 未做任何事：要约仍在，A 用量仍为 40。
				checkTenant(t, s, "A", 40, 100, 1)
				checkTenant(t, s, "B", 0, 50, 0)
				checkBucket(t, s, "b1", "A", 40, 0)
				if _, ok := s.OfferOf("b1"); !ok {
					t.Error("k=0 恢复后要约应仍在")
				}
				if got := len(s.Log()); got != 0 {
					t.Errorf("k=0 不应有日志, got %d 条", got)
				}
			} else {
				// 结果与一次完整的 Accept 逐字段相同。
				checkTenant(t, s, "A", 0, 100, 0)
				checkTenant(t, s, "B", 40, 50, 1)
				checkBucket(t, s, "b1", "B", 40, 0)
				if _, ok := s.OfferOf("b1"); ok {
					t.Error("k>0 恢复后要约应已清除")
				}
				if got := s.Log(); !equalLog(got, wantLog) {
					t.Errorf("k=%d 恢复后日志=%+v, want %+v", k, got, wantLog)
				}
			}
			// Accept 通过校验即推进时钟（含 k=0）：恢复后小于 10 的 now 报时钟回退。
			if got := s.Put("A", "b1", 1, 9); got != ErrClock {
				t.Errorf("k=%d 恢复后时钟应已推进到 10, got %v", k, got)
			}
			checkInvariants(t, s, []string{"A", "B"}, []string{"b1"})
		})
	}
}

// TestCrashGate 崩溃后除 Recover 外的一切操作报需要恢复且不改状态。
func TestCrashGate(t *testing.T) {
	s := newExample(t)
	runSteps(t, s, []step{
		{"Offer ttl=10 @5", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 5) }, nil},
	})
	s.InjectCrashAfterStep(2)
	if got := s.Accept("b1", "B", 10); got != ErrNeedRecovery {
		t.Fatalf("got %v", got)
	}
	ops := []step{
		{"CreateBucket", func(s *Service) error { return s.CreateBucket("A", "b9", 11) }, ErrNeedRecovery},
		{"SetLimit", func(s *Service) error { return s.SetLimit("A", 1) }, ErrNeedRecovery},
		{"Put", func(s *Service) error { return s.Put("A", "b1", 1, 11) }, ErrNeedRecovery},
		{"Delete", func(s *Service) error { return s.Delete("A", "b1", 1, 11) }, ErrNeedRecovery},
		{"BeginUpload", func(s *Service) error { return s.BeginUpload("A", "b1", 11) }, ErrNeedRecovery},
		{"EndUpload", func(s *Service) error { return s.EndUpload("A", "b1", 11) }, ErrNeedRecovery},
		{"Offer", func(s *Service) error { return s.Offer("b1", "A", "B", 1, 11) }, ErrNeedRecovery},
		{"Cancel", func(s *Service) error { return s.Cancel("b1", "A", 11) }, ErrNeedRecovery},
		{"Accept", func(s *Service) error { return s.Accept("b1", "B", 11) }, ErrNeedRecovery},
	}
	runSteps(t, s, ops)
	// 崩溃于第 2 步后：from 已减、to 未加、属主未改、要约仍在。
	checkTenant(t, s, "A", 0, 100, 1)
	checkTenant(t, s, "B", 0, 50, 0)
	checkBucket(t, s, "b1", "A", 40, 0)
	s.Recover()
	checkTenant(t, s, "A", 0, 100, 0)
	checkTenant(t, s, "B", 40, 50, 1)
	checkBucket(t, s, "b1", "B", 40, 0)
	// 恢复后操作恢复正常。
	if got := s.Put("B", "b1", 1, 11); got != nil {
		t.Fatalf("恢复后 Put 应成功, got %v", got)
	}
	checkInvariants(t, s, []string{"A", "B"}, []string{"b1"})
}

// TestRecoverWithoutCrash 无崩溃时 Recover 为幂等空操作。
func TestRecoverWithoutCrash(t *testing.T) {
	s := newExample(t)
	runSteps(t, s, []step{
		{"Offer ttl=10 @5", func(s *Service) error { return s.Offer("b1", "A", "B", 10, 5) }, nil},
	})
	s.Recover()
	s.Recover()
	checkTenant(t, s, "A", 40, 100, 1)
	if _, ok := s.OfferOf("b1"); !ok {
		t.Error("无 BEGIN 时 Recover 不应改变状态，要约应保留")
	}
}

// TestTouchedZero Accept 触碰的对象记录数为 0，与桶内对象数无关。
func TestTouchedZero(t *testing.T) {
	for _, n := range []int{100, 10000} {
		s := New(2)
		if err := s.SetLimit("A", maxQuota); err != nil {
			t.Fatal(err)
		}
		if err := s.SetLimit("B", maxQuota); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateBucket("A", "b1", 0); err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= n; i++ {
			if err := s.Put("A", "b1", 1, int64(i)); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Offer("b1", "A", "B", 10, int64(n)+1); err != nil {
			t.Fatal(err)
		}
		if err := s.Accept("b1", "B", int64(n)+2); err != nil {
			t.Fatal(err)
		}
		if s.touched != 0 {
			t.Errorf("对象数=%d: Accept 触碰对象记录数=%d, want 0", n, s.touched)
		}
		if got, _ := s.BucketObjects("b1"); got != int64(n) {
			t.Errorf("对象数=%d: 转移后 BucketObjects=%d, want %d", n, got, n)
		}
		checkTenant(t, s, "B", int64(n), maxQuota, 1)
	}
}

// TestConcurrency 并发调用等价于某个串行顺序，结束后不变式成立。
func TestConcurrency(t *testing.T) {
	s := New(4)
	tenants := []string{"A", "B", "C"}
	buckets := []string{"b1", "b2", "b3", "b4"}
	for _, tn := range tenants {
		if err := s.SetLimit(tn, maxQuota); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateBucket("A", "b1", 0); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	for g := 0; g < 8; g++ {
		go func(g int) {
			defer func() { done <- struct{}{} }()
			for i := 1; i <= 200; i++ {
				now := int64(i)
				tn := tenants[(g+i)%len(tenants)]
				bk := buckets[(g+i)%len(buckets)]
				switch (g + i) % 6 {
				case 0:
					_ = s.CreateBucket(tn, bk, now)
				case 1:
					_ = s.Put(tn, bk, 1, now)
				case 2:
					_ = s.Delete(tn, bk, 1, now)
				case 3:
					_ = s.BeginUpload(tn, bk, now)
				case 4:
					_ = s.EndUpload(tn, bk, now)
				case 5:
					_ = s.Offer(bk, tn, tenants[(g+i+1)%len(tenants)], 100, now)
					_ = s.Accept(bk, tenants[(g+i+1)%len(tenants)], now)
					_ = s.Cancel(bk, tn, now)
				}
			}
		}(g)
	}
	for g := 0; g < 8; g++ {
		<-done
	}
	s.Recover()
	checkInvariants(t, s, tenants, buckets)
}

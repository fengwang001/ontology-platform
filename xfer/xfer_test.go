package xfer

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", ctx, err)
	}
}

func wantErr(t *testing.T, got, want error, ctx string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: got %v, want %v", ctx, got, want)
	}
}

// assertInvariants 校验：用量=所属桶字节之和；桶数=拥有桶数；每桶唯一属主。
func assertInvariants(t *testing.T, s *Service, ctx string) {
	t.Helper()
	snap := s.Snapshot()
	used := map[string]int64{}
	cnt := map[string]int64{}
	for name, owner := range snap.Owners {
		used[owner] += snap.Bytes[name]
		cnt[owner]++
	}
	for tenant := range snap.Used {
		used[tenant] += 0
	}
	for tenant, u := range used {
		if snap.Used[tenant] != u {
			t.Fatalf("%s: used[%s]=%d, sum of owned bytes=%d", ctx, tenant, snap.Used[tenant], u)
		}
		if snap.BucketN[tenant] != cnt[tenant] {
			t.Fatalf("%s: count[%s]=%d, owned=%d", ctx, tenant, snap.BucketN[tenant], cnt[tenant])
		}
	}
}

func TestSpecExample(t *testing.T) {
	s := New(2)
	s.SetLimit("A", 100)
	s.SetLimit("B", 50)
	mustOK(t, s.CreateBucket("A", "b1", 0), "create")
	mustOK(t, s.Put("A", "b1", 40, 1), "put 40")
	if s.Used("A") != 40 {
		t.Fatalf("A used = %d", s.Used("A"))
	}
	mustOK(t, s.Offer("b1", "A", "B", 10, 5), "offer")
	wantErr(t, s.Put("A", "b1", 1, 6), ErrFrozen, "frozen put")
	wantErr(t, s.Accept("b1", "B", 15), ErrExpired, "accept at expiry")
	mustOK(t, s.Accept("b1", "B", 14), "accept one before expiry")
	if s.Used("A") != 0 || s.Used("B") != 40 {
		t.Fatalf("used A=%d B=%d", s.Used("A"), s.Used("B"))
	}
	owner, _ := s.Owner("b1")
	if owner != "B" {
		t.Fatalf("owner = %s", owner)
	}
	wantErr(t, s.Put("A", "b1", 1, 14), ErrNotOwner, "A put after transfer")
	assertInvariants(t, s, "after example")
}

func TestQuotaExactAndPlusOne(t *testing.T) {
	for _, tc := range []struct {
		name string
		q    int64
		want error
	}{
		{"exact", 40, nil},
		{"plus-one-fails", 39, ErrQuota},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(2)
			s.SetLimit("A", 100)
			s.SetLimit("B", tc.q)
			mustOK(t, s.CreateBucket("A", "b1", 0), "create")
			mustOK(t, s.Put("A", "b1", 40, 1), "put")
			mustOK(t, s.Offer("b1", "A", "B", 100, 5), "offer")
			err := s.Accept("b1", "B", 6)
			if tc.want != nil {
				wantErr(t, err, tc.want, tc.name)
			} else {
				mustOK(t, err, tc.name)
			}
			assertInvariants(t, s, tc.name)
		})
	}
}

func TestFreezeAllowsEndUpload(t *testing.T) {
	s := New(2)
	s.SetLimit("A", 100)
	s.SetLimit("B", 100)
	mustOK(t, s.CreateBucket("A", "b1", 0), "create")
	mustOK(t, s.Put("A", "b1", 10, 1), "seed bytes")
	mustOK(t, s.BeginUpload("A", "b1", 1), "begin")
	mustOK(t, s.Offer("b1", "A", "B", 100, 5), "offer")
	wantErr(t, s.BeginUpload("A", "b1", 6), ErrFrozen, "begin during freeze")
	wantErr(t, s.Delete("A", "b1", 1, 6), ErrFrozen, "delete during freeze")
	mustOK(t, s.EndUpload("A", "b1", 7), "end during freeze")
	up, _ := s.OpenUploads("b1")
	if up != 0 {
		t.Fatalf("uploads = %d", up)
	}
	mustOK(t, s.Accept("b1", "B", 8), "accept after upload ends")
	assertInvariants(t, s, "freeze/end")
}

func TestExpiredOfferLanding(t *testing.T) {
	s := New(2)
	s.SetLimit("A", 100)
	s.SetLimit("B", 100)
	mustOK(t, s.CreateBucket("A", "b1", 0), "create")
	mustOK(t, s.Put("A", "b1", 10, 1), "put")
	mustOK(t, s.Offer("b1", "A", "B", 10, 5), "offer expiry=15")

	// 被拒绝的操作（非属主）不清到期要约。
	wantErr(t, s.Put("C", "b1", 1, 15), ErrNotOwner, "stranger put rejected")
	wantErr(t, s.Accept("b1", "B", 15), ErrExpired, "still expired after rejection")

	// 被接受的属主 Put 落地到期要约，随后 Accept 报无要约。
	mustOK(t, s.Put("A", "b1", 1, 15), "owner put lands expiry")
	wantErr(t, s.Accept("b1", "B", 15), ErrNoOffer, "offer landed by accepted put")

	// 新要约可取代已到期要约。
	mustOK(t, s.Offer("b1", "A", "B", 10, 16), "renew after expiry")
	wantErr(t, s.Offer("b1", "A", "B", 10, 17), ErrOfferExists, "active offer blocks")
	assertInvariants(t, s, "landing")
}

func TestAcceptRejectOrder(t *testing.T) {
	// 同时触发：在途上传 + to 桶数超限 + 额度不足，应先报在途上传。
	s := New(2)
	s.SetLimit("A", 100)
	s.SetLimit("B", 10)
	mustOK(t, s.CreateBucket("A", "b1", 0), "create b1")
	mustOK(t, s.Put("A", "b1", 40, 1), "put 40")
	mustOK(t, s.CreateBucket("B", "b2", 2), "B bucket 1")
	mustOK(t, s.CreateBucket("B", "b3", 3), "B bucket 2 (MaxB reached)")
	mustOK(t, s.BeginUpload("A", "b1", 4), "open upload")
	mustOK(t, s.Offer("b1", "A", "B", 100, 5), "offer")
	wantErr(t, s.Accept("b1", "B", 6), ErrUploadsOpen, "uploads first")

	mustOK(t, s.EndUpload("A", "b1", 7), "end upload")
	wantErr(t, s.Accept("b1", "B", 8), ErrTooMany, "bucket count before quota")

	// 桶数不再超限后暴露额度不足。
	s2 := New(2)
	s2.SetLimit("A", 100)
	s2.SetLimit("B", 10)
	mustOK(t, s2.CreateBucket("A", "b1", 0), "create")
	mustOK(t, s2.Put("A", "b1", 40, 1), "put")
	mustOK(t, s2.CreateBucket("B", "b2", 2), "B has free slot")
	mustOK(t, s2.Offer("b1", "A", "B", 100, 5), "offer")
	wantErr(t, s2.Accept("b1", "B", 6), ErrQuota, "quota exposed")

	// 非当事人先于在途上传；已到期先于非当事人。
	wantErr(t, s.Accept("b1", "C", 8), ErrNotParty, "stranger accept")
	wantErr(t, s.Accept("b1", "C", 200), ErrExpired, "expired before party")
}

func TestCancelOrder(t *testing.T) {
	s := New(2)
	s.SetLimit("A", 100)
	s.SetLimit("B", 100)
	mustOK(t, s.CreateBucket("A", "b1", 0), "create")
	mustOK(t, s.Offer("b1", "A", "B", 10, 5), "offer expiry=15")
	wantErr(t, s.Cancel("b1", "C", 6), ErrNotParty, "non-party")
	mustOK(t, s.Cancel("b1", "B", 7), "to cancels")
	wantErr(t, s.Cancel("b1", "A", 8), ErrNoOffer, "no offer")
	mustOK(t, s.Offer("b1", "A", "B", 10, 9), "offer2 expiry=19")
	wantErr(t, s.Cancel("b1", "A", 19), ErrExpired, "cancel at expiry")
	wantErr(t, s.Cancel("missing", "A", 19), ErrNotFound, "missing bucket")
}

func TestClockRollback(t *testing.T) {
	s := New(2)
	s.SetLimit("A", 100)
	mustOK(t, s.CreateBucket("A", "b1", 10), "create at 10")
	wantErr(t, s.Put("A", "b1", 1, 9), ErrClockGoBack, "rollback")
	mustOK(t, s.Put("A", "b1", 1, 10), "equal clock accepted")
	wantErr(t, s.Put("A", "b1", 0, 1), ErrInvalid, "invalid before rollback")
	wantErr(t, s.EndUpload("A", "b1", 1), ErrClockGoBack, "end rollback")
}

func TestBasicValidationAndLimits(t *testing.T) {
	s := New(2)
	wantErr(t, s.CreateBucket("", "b1", 0), ErrInvalid, "empty tenant")
	wantErr(t, s.CreateBucket("A", "", 0), ErrInvalid, "empty bucket")
	wantErr(t, s.CreateBucket("A", "b1", -1), ErrInvalid, "bad now")
	wantErr(t, s.CreateBucket("A", "b1", maxNow+1), ErrInvalid, "now too big")
	mustOK(t, s.CreateBucket("A", "b1", 0), "create b1")
	wantErr(t, s.CreateBucket("A", "b1", 0), ErrExists, "dup")
	s.SetLimit("A", 100)
	wantErr(t, s.Put("A", "b1", 0, 1), ErrInvalid, "size 0")
	wantErr(t, s.Put("A", "b1", maxSize+1, 1), ErrInvalid, "size too big")
	wantErr(t, s.Put("X", "b1", 1, 1), ErrNotOwner, "not owner")
	wantErr(t, s.Put("A", "nope", 1, 1), ErrNotFound, "no bucket")
	wantErr(t, s.EndUpload("A", "b1", 1), ErrInvalid, "end with zero uploads")
	mustOK(t, s.CreateBucket("A", "b2", 2), "b2")
	wantErr(t, s.CreateBucket("A", "b3", 3), ErrTooMany, "MaxB")
	s.SetLimit("A", 0)
	wantErr(t, s.Put("A", "b1", 1, 4), ErrQuota, "limit below usage enforced")
}

// -- 崩溃恢复用例 --

func setupTransfer(t *testing.T) *Service {
	t.Helper()
	s := New(2)
	s.SetLimit("A", 100)
	s.SetLimit("B", 100)
	mustOK(t, s.CreateBucket("A", "b1", 0), "create")
	mustOK(t, s.Put("A", "b1", 40, 1), "put")
	mustOK(t, s.Offer("b1", "A", "B", 100, 5), "offer")
	return s
}

func TestCrashPoints(t *testing.T) {
	type partial struct {
		owner string
		usedA int64
		usedB int64
		offer bool
	}
	wantPartial := map[int]partial{
		0: {"A", 40, 0, true},
		1: {"A", 40, 0, true},
		2: {"A", 0, 0, true},
		3: {"A", 0, 40, true},
		4: {"B", 0, 40, false},
		5: {"B", 0, 40, false},
	}
	for k := 0; k <= 5; k++ {
		t.Run(fmt.Sprintf("crash-%d", k), func(t *testing.T) {
			s := setupTransfer(t)
			s.SetCrashAfter(k)
			wantErr(t, s.Accept("b1", "B", 6), ErrNeedRecovery, fmt.Sprintf("k=%d", k))

			snap := s.Snapshot()
			before := snap
			wantErr(t, s.Put("B", "b1", 1, 7), ErrNeedRecovery, "op while down")
			wantErr(t, s.Accept("b1", "B", 7), ErrNeedRecovery, "accept while down")
			if !reflect.DeepEqual(before, s.Snapshot()) {
				t.Fatalf("k=%d: state changed while awaiting recovery", k)
			}

			exp := wantPartial[k]
			owner := snap.Owners["b1"]
			if owner != exp.owner || snap.Used["A"] != exp.usedA || snap.Used["B"] != exp.usedB {
				t.Fatalf("k=%d partial: owner=%s A=%d B=%d want %+v",
					k, owner, snap.Used["A"], snap.Used["B"], exp)
			}
			if _, ok := snap.Offers["b1"]; ok != exp.offer {
				t.Fatalf("k=%d offer presence=%v want %v", k, ok, exp.offer)
			}

			s.Recover()
			// 重复 Recover 幂等。
			s.Recover()
			s.Recover()

			after := s.Snapshot()
			if k == 0 {
				// BEGIN 未写出：向前恢复无事可补，要约保持、A 仍持有 40 字节。
				if after.Owners["b1"] != "A" || after.Used["A"] != 40 || after.Used["B"] != 0 {
					t.Fatalf("k=0 after recover changed: %+v", after)
				}
				if _, ok := after.Offers["b1"]; !ok {
					t.Fatalf("k=0 offer lost after recover")
				}
				if after.BucketN["A"] != 1 || after.BucketN["B"] != 0 {
					t.Fatalf("k=0 counts A=%d B=%d", after.BucketN["A"], after.BucketN["B"])
				}
			} else {
				if after.Owners["b1"] != "B" {
					t.Fatalf("k=%d owner after recover=%s", k, after.Owners["b1"])
				}
				if after.Used["A"] != 0 || after.Used["B"] != 40 {
					t.Fatalf("k=%d used after recover A=%d B=%d", k, after.Used["A"], after.Used["B"])
				}
				if after.BucketN["A"] != 0 || after.BucketN["B"] != 1 {
					t.Fatalf("k=%d counts A=%d B=%d", k, after.BucketN["A"], after.BucketN["B"])
				}
				if _, ok := after.Offers["b1"]; ok {
					t.Fatalf("k=%d offer still present after recover", k)
				}
			}
			assertInvariants(t, s, fmt.Sprintf("k=%d", k))

			// 恢复后服务继续可用，且时钟已推进到被接受时刻 6。
			if k == 0 {
				// k=0 时转移未落地，属主仍是 A，要约仍在，A 的写仍被冻结。
				wantErr(t, s.Put("A", "b1", 1, 6), ErrFrozen, "still frozen after k=0 recover")
				wantErr(t, s.Put("A", "b1", 1, 5), ErrClockGoBack, "clock committed at crash")
			} else {
				mustOK(t, s.Put("B", "b1", 5, 6), "put after recover")
				wantErr(t, s.Put("B", "b1", 1, 5), ErrClockGoBack, "clock committed at crash")
			}
		})
	}
}

func TestCrashMidpointsPreserveInvariant(t *testing.T) {
	for k := 0; k <= 5; k++ {
		s := setupTransfer(t)
		s.SetCrashAfter(k)
		_ = s.Accept("b1", "B", 6)
		s.Recover()
		got := s.Snapshot()
		if k == 0 {
			// k=0：无事可补，状态与未发生 Accept 完全一致。
			full := setupTransfer(t)
			want := full.Snapshot()
			got.LastNow, want.LastNow = 0, 0
			if !reflect.DeepEqual(got.Owners, want.Owners) ||
				!reflect.DeepEqual(got.Bytes, want.Bytes) ||
				!reflect.DeepEqual(got.Uploads, want.Uploads) ||
				!reflect.DeepEqual(got.Used, want.Used) ||
				!reflect.DeepEqual(got.BucketN, want.BucketN) ||
				!reflect.DeepEqual(got.Offers, want.Offers) {
				t.Fatalf("k=0 recovered %+v != untouched %+v", got, want)
			}
		} else {
			// 恢复后必须逐字段等价于一次完整 Accept。
			full := setupTransfer(t)
			mustOK(t, full.Accept("b1", "B", 6), "full accept")
			want := full.Snapshot()
			got.LastNow, want.LastNow = 0, 0
			if !reflect.DeepEqual(got.Owners, want.Owners) ||
				!reflect.DeepEqual(got.Bytes, want.Bytes) ||
				!reflect.DeepEqual(got.Uploads, want.Uploads) ||
				!reflect.DeepEqual(got.Used, want.Used) ||
				!reflect.DeepEqual(got.BucketN, want.BucketN) ||
				!reflect.DeepEqual(got.Offers, want.Offers) {
				t.Fatalf("k=%d recovered %+v != full %+v", k, got, want)
			}
		}
	}
}

func TestRecoverWithoutCrash(t *testing.T) {
	s := setupTransfer(t)
	s.Recover() // 无 BEGIN：什么也不做，要约保持。
	if _, ok := s.ActiveOffer("b1", 6); !ok {
		t.Fatal("offer lost by no-op recover")
	}
	if s.Used("A") != 40 {
		t.Fatalf("A used=%d", s.Used("A"))
	}
}

func TestTouchedIndependentOfObjects(t *testing.T) {
	for _, n := range []int{100, 10000} {
		s := New(2)
		s.SetLimit("A", 1_000_000_000_000)
		s.SetLimit("B", 1_000_000_000_000)
		mustOK(t, s.CreateBucket("A", "b1", 0), "create")
		for i := 0; i < n; i++ {
			mustOK(t, s.Put("A", "b1", 1, int64(i+1)), fmt.Sprintf("put %d", i))
		}
		mustOK(t, s.Offer("b1", "A", "B", 100, int64(n+1)), "offer")
		mustOK(t, s.Accept("b1", "B", int64(n+2)), "accept")
		if s.TouchCount() != 0 {
			t.Fatalf("n=%d: Accept touched %d object records, want 0", n, s.TouchCount())
		}
		if s.Used("B") != int64(n) {
			t.Fatalf("n=%d: B used=%d", n, s.Used("B"))
		}
	}
}

func TestConcurrentSerializability(t *testing.T) {
	s := New(1000)
	s.SetLimit("T", 1_000_000)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			name := fmt.Sprintf("b-%d", g)
			_ = s.CreateBucket("T", name, 0)
			for i := 0; i < 50; i++ {
				_ = s.Put("T", name, 1, 0)
			}
		}(g)
	}
	wg.Wait()
	assertInvariants(t, s, "concurrent")
	if s.Used("T") != 8*50 {
		t.Fatalf("used=%d", s.Used("T"))
	}
}

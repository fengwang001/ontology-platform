package archive_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/archive"
	"ontology/archive/naive"
)

func diffCfg() archive.Config {
	return archive.Config{
		LoanDays:         [4]int{3, 4, 5, 6},
		PickupDays:       2,
		RenewWindowDays:  3,
		MaxRenews:        2,
		OverdueThreshold: 6,
		CooldownDays:     3,
	}
}

func fmtResult(r archive.Result) string {
	if r.Err != nil {
		return fmt.Sprintf("拒绝(%s: %s)", r.Err.Code, r.Err.Msg)
	}
	return fmt.Sprintf("接受(%+v)", r)
}

// 与独立朴素模型对照大量随机操作序列，
// 日志打印每步输入、输出与判定依据。
func TestDifferentialRandom(t *testing.T) {
	cfg := diffCfg()
	for seed := int64(0); seed < 30; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runDifferential(t, cfg, seed)
		})
	}
}

func runDifferential(t *testing.T, cfg archive.Config, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	real, err := archive.New(cfg)
	if err != nil {
		t.Fatalf("archive.New: %v", err)
	}
	model, err := naive.New(cfg)
	if err != nil {
		t.Fatalf("naive.New: %v", err)
	}
	volIDs := []string{"v0", "v1", "v2", "v3", "v4", "v5"}
	brwIDs := []string{"u0", "u1", "u2", "u3", "u4", "u5"}
	levels := []archive.SecretLevel{archive.Public, archive.Internal, archive.Confidential, archive.TopSecret}
	for _, id := range volIDs {
		op := archive.Op{Kind: archive.OpAddVolume, Now: 0, VolumeID: id, Level: levels[rng.Intn(4)]}
		real.Apply(op)
		model.Apply(op)
	}
	for _, id := range brwIDs {
		op := archive.Op{Kind: archive.OpAddBorrower, Now: 0, BorrowerID: id, Level: levels[rng.Intn(4)]}
		real.Apply(op)
		model.Apply(op)
	}
	now := 0
	allVolIDs := append(append([]string{}, volIDs...), "ghost")
	for step := 0; step < 1200; step++ {
		op := genOp(rng, &now, allVolIDs, brwIDs)
		r1 := real.Apply(op)
		r2 := model.Apply(op)
		t.Logf("seed=%d step=%d 输入=%+v 真实输出=%s 模型输出=%s",
			seed, step, op, fmtResult(r1), fmtResult(r2))
		if !sameResult(r1, r2) {
			t.Fatalf("seed=%d step=%d 分歧\n输入: %+v\n真实: %s\n模型: %s",
				seed, step, op, fmtResult(r1), fmtResult(r2))
		}
		if step%20 == 0 {
			s1 := real.Snapshot(now)
			s2 := model.Snapshot(now)
			if !reflect.DeepEqual(s1, s2) {
				t.Fatalf("seed=%d step=%d 状态分歧\n真实: %+v\n模型: %+v",
					seed, step, s1, s2)
			}
		}
	}
}

func sameResult(a, b archive.Result) bool {
	if (a.Err == nil) != (b.Err == nil) {
		return false
	}
	if a.Err != nil {
		return a.Err.Code == b.Err.Code && a.Err.Index == b.Err.Index
	}
	return reflect.DeepEqual(a.BorrowResults, b.BorrowResults) &&
		a.OverdueDays == b.OverdueDays &&
		a.DueDay == b.DueDay &&
		a.NewDueDay == b.NewDueDay
}

func genOp(rng *rand.Rand, now *int, volIDs, brwIDs []string) archive.Op {
	// 时钟通常前进 0~2 日，小概率回退一日以覆盖时钟回退分支。
	if rng.Intn(20) == 0 {
		*now--
	} else {
		*now += rng.Intn(3)
	}
	if *now < 0 {
		*now = 0
	}
	vol := func() string { return volIDs[rng.Intn(len(volIDs))] }
	brw := func() string { return brwIDs[rng.Intn(len(brwIDs))] }
	lvl := func() archive.SecretLevel { return archive.SecretLevel(rng.Intn(4)) }
	switch rng.Intn(24) {
	case 0, 1, 2, 3, 4: // 借阅（含多卷同借）
		n := 1 + rng.Intn(3)
		ids := make([]string, 0, n)
		for i := 0; i < n; i++ {
			ids = append(ids, vol())
		}
		if rng.Intn(10) == 0 && n > 1 { // 偶发批内重复
			ids[1] = ids[0]
		}
		return archive.Op{Kind: archive.OpBorrow, Now: *now, BorrowerID: brw(), VolumeIDs: ids}
	case 5, 6, 7, 8: // 预约
		return archive.Op{Kind: archive.OpReserve, Now: *now, BorrowerID: brw(), VolumeID: vol()}
	case 9, 10, 11, 12: // 归还
		return archive.Op{Kind: archive.OpReturn, Now: *now, VolumeID: vol()}
	case 13, 14, 15: // 续借
		var approver string
		switch rng.Intn(4) {
		case 0, 3:
			approver = brw()
		case 1:
			approver = "ghost"
		default:
			approver = ""
		}
		op := archive.Op{Kind: archive.OpRenew, Now: *now, BorrowerID: brw(), VolumeID: vol(), ApproverID: approver}
		if rng.Intn(6) == 0 { // 偶发自审
			op.ApproverID = op.BorrowerID
		}
		return op
	case 16, 17, 18: // 取卷
		return archive.Op{Kind: archive.OpPickup, Now: *now, BorrowerID: brw(), VolumeID: vol()}
	case 19: // 封存
		return archive.Op{Kind: archive.OpSeal, Now: *now, VolumeID: vol()}
	case 20, 21: // 调整密级
		return archive.Op{Kind: archive.OpSetBorrowerLevel, Now: *now, BorrowerID: brw(), Level: lvl()}
	case 22: // 新卷
		return archive.Op{Kind: archive.OpAddVolume, Now: *now,
			VolumeID: fmt.Sprintf("x%d", rng.Intn(4)), Level: lvl()}
	default: // 参数非法
		return archive.Op{Kind: archive.OpBorrow, Now: *now, BorrowerID: "", VolumeIDs: nil}
	}
}

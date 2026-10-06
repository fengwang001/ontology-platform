package slotting

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// 随机生成货位与操作，逐步比较生产实现与朴素模型的：
// 成功/失败、拒绝原因、分配货位与全量占用账。
func TestDifferentialAgainstNaive(t *testing.T) {
	const iterations = 60
	for seed := int64(1); seed <= iterations; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			runDifferential(t, rng, 200)
		})
	}
}

func TestDifferentialAgainstNaiveLogged(t *testing.T) {
	// 小规模运行并把操作与结果通过日志装饰器打印，作为日志判定依据的演示。
	var logBuf bytesBuffer
	svc := newSvcWith(
		locCfg(1, 1, 1, 100, 50, true, cats(CategoryNormal, CategoryFood, CategoryFlammable), true),
		locCfg(1, 1, 2, 20, 50, false, cats(CategoryNormal), true),
	)
	logged := NewLoggedService(svc, &logBuf)

	if _, err := logged.AutoPutaway(pallet("L1", "G", "B", CategoryNormal, 30, 40)); err != nil {
		t.Fatal(err)
	}
	if _, err := logged.AutoPutaway(pallet("L2", "G", "B", CategoryNormal, 30, 40)); err != nil {
		t.Fatal(err)
	}
	if err := logged.PutawayTo(pallet("L3", "H", "B", CategoryNormal, 500, 1), Coord{1, 1, 1}); !errors.Is(err, ErrWeight) {
		t.Fatalf("logged reject: %v", err)
	}
	logged.Location(Coord{1, 1, 1})
	t.Logf("operation log:\n%s", logBuf.String())
}

func runDifferential(t *testing.T, rng *rand.Rand, ops int) {
	t.Helper()
	var cfgs []LocationConfig
	allCats := cats(CategoryNormal, CategoryFood, CategoryFlammable)

	// 固定一小块网格，保证相邻关系与冻结场景都可能出现。
	nAisle := 2 + rng.Intn(2)
	nLevel := 2 + rng.Intn(2)
	nPos := 3 + rng.Intn(3)
	for a := 1; a <= nAisle; a++ {
		for l := 1; l <= nLevel; l++ {
			for p := 1; p <= nPos; p++ {
				var allowed map[Category]bool
				switch rng.Intn(3) {
				case 0:
					allowed = cats(CategoryNormal)
				case 1:
					allowed = cats(CategoryNormal, CategoryFood)
				default:
					allowed = allCats
				}
				cfg := locCfg(a, l, p,
					50+rng.Intn(300), // 承重上限
					20+rng.Intn(80),  // 净高
					rng.Intn(2) == 0, // 容量 1 或 2
					allowed,
					rng.Intn(2) == 0) // 是否允许混批
				cfgs = append(cfgs, cfg)
			}
		}
	}
	svc := newSvcWith(cfgs...)
	naive := newNaive(cfgs)

	products := []string{"G1", "G2", "G3", "FoodX", "OilY"}
	batches := []string{"B1", "B2"}

	randPallet := func(id int) Pallet {
		cat := Category(rng.Intn(3))
		return Pallet{
			ID:       fmt.Sprintf("T%d", id),
			Product:  products[rng.Intn(len(products))],
			Batch:    batches[rng.Intn(2)],
			Category: cat,
			Weight:   1 + rng.Intn(120),
			Height:   1 + rng.Intn(60),
		}
	}

	for i := 0; i < ops; i++ {
		// 偶尔引用已存在托盘或已存在坐标。
		var existing []string
		for id := range naive.where {
			existing = append(existing, id)
		}
		switch rng.Intn(8) {
		case 0, 1: // 自动上架
			p := randPallet(1000 + i)
			gotC, gotErr := svc.AutoPutaway(p)
			wantC, wantR := naive.doAuto(p)
			if AsReason(gotErr) != wantR || (wantR == "" && gotC != wantC) {
				t.Fatalf("op%d auto %v: got (%v,%v) want (%v,%v)",
					i, p, gotC, AsReason(gotErr), wantC, wantR)
			}
		case 2: // 指定货位上架
			p := randPallet(2000 + i)
			target := cfgs[rng.Intn(len(cfgs))].Coord
			gotErr := svc.PutawayTo(p, target)
			wantR := naive.doPut(p, target)
			if AsReason(gotErr) != wantR {
				t.Fatalf("op%d put %v -> %v: got %v want %v",
					i, p, target, AsReason(gotErr), wantR)
			}
		case 3: // 移库
			if len(existing) == 0 {
				i--
				continue
			}
			id := existing[rng.Intn(len(existing))]
			target := cfgs[rng.Intn(len(cfgs))].Coord
			gotErr := svc.Move(id, target)
			wantR := naive.doMove(id, target)
			if AsReason(gotErr) != wantR {
				t.Fatalf("op%d move %s -> %v: got %v want %v",
					i, id, target, AsReason(gotErr), wantR)
			}
		case 4: // 取出
			id := fmt.Sprintf("T%d", 1000+rng.Intn(ops+1))
			if len(existing) > 0 && rng.Intn(2) == 0 {
				id = existing[rng.Intn(len(existing))]
			}
			gotErr := svc.Retrieve(id)
			wantR := naive.doRetrieve(id)
			if AsReason(gotErr) != wantR {
				t.Fatalf("op%d retrieve %s: got %v want %v",
					i, id, AsReason(gotErr), wantR)
			}
		case 5: // 冻结/解冻
			c := cfgs[rng.Intn(len(cfgs))].Coord
			frozen := rng.Intn(2) == 0
			var gotErr error
			if frozen {
				gotErr = svc.Freeze(c)
			} else {
				gotErr = svc.Unfreeze(c)
			}
			wantR := naive.doFreeze(c, frozen)
			if AsReason(gotErr) != wantR {
				t.Fatalf("op%d freeze(%v,%v): got %v want %v",
					i, c, frozen, AsReason(gotErr), wantR)
			}
		default: // 自动上架（占比更高，提高占用密度）
			p := randPallet(3000 + i)
			gotC, gotErr := svc.AutoPutaway(p)
			wantC, wantR := naive.doAuto(p)
			if AsReason(gotErr) != wantR || (wantR == "" && gotC != wantC) {
				t.Fatalf("op%d auto %v: got (%v,%v) want (%v,%v)",
					i, p, gotC, AsReason(gotErr), wantC, wantR)
			}
		}
		if !naive.dumpEqual(t, svc) {
			t.Fatalf("op%d state divergence", i)
		}
	}
}

// bytesBuffer 是测试内最小的字节缓冲，避免每个测试文件各自 import。
type bytesBuffer struct{ b []byte }

func (x *bytesBuffer) Write(p []byte) (int, error) { x.b = append(x.b, p...); return len(p), nil }
func (x *bytesBuffer) String() string              { return string(x.b) }
func (x *bytesBuffer) Len() int                    { return len(x.b) }

package sorting

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// 随机对照：对同一随机操作序列，逐操作比较优化实现 Hub 与朴素模型的
// 返回值（集袋编号 / 错误类别 / 差异清单），结束后比较全量快照与时钟。
// 日志打印每一步的输入、输出与判定依据（go test -v 可见）。
func TestRandomAgainstNaiveModel(t *testing.T) {
	seeds := []int64{1, 7, 42, 2024, 987654}
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runRandomSequence(t, rand.New(rand.NewSource(seed)), 2000)
		})
	}
}

type opKind int

const (
	opAdd opKind = iota
	opSeal
	opDepart
	opUnpack
)

func runRandomSequence(t *testing.T, r *rand.Rand, steps int) {
	t.Helper()
	cfg := Config{
		MaxBagCount:  1 + r.Intn(4),
		MaxBagWeight: int64(1 + r.Intn(500)),
		DwellLimit:   int64(r.Intn(30)),
	}
	t.Logf("配置: %+v", cfg)

	h, err := NewHub(cfg)
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	n := newNaiveHub(cfg)

	sites := []string{"S1", "S2", "S3"}
	categories := []Category{CategoryNormal, CategoryFragile, CategoryLiquid}
	// 运单号池：小规模以制造重复与拆袋差异。
	var waybillPool []string
	nextWaybill := 0
	freshWaybill := func() string {
		nextWaybill++
		return fmt.Sprintf("WB%04d", nextWaybill)
	}

	now := int64(0)
	var bagIDs []int64 // 已出现过的集袋编号（含非法值，用于触发对象不存在）

	pickWaybill := func() string {
		if len(waybillPool) == 0 || r.Intn(3) == 0 {
			w := freshWaybill()
			waybillPool = append(waybillPool, w)
			return w
		}
		return waybillPool[r.Intn(len(waybillPool))]
	}
	pickBagID := func() int64 {
		if len(bagIDs) == 0 || r.Intn(10) == 0 {
			return int64(r.Intn(3)) // 可能不存在
		}
		return bagIDs[r.Intn(len(bagIDs))]
	}

	for step := 0; step < steps; step++ {
		// 时刻：多数单调前进，少数回退以触发时钟回退。
		switch r.Intn(20) {
		case 0:
			now -= int64(r.Intn(5) + 1) // 回退
		case 1, 2, 3:
			// 原地不动（允许等于上次）
		default:
			now += int64(r.Intn(8))
		}
		if now < 0 {
			now = 0
		}

		kind := opKind(r.Intn(10))
		if kind > opUnpack {
			kind = opAdd // 加入占比最高
		}

		switch kind {
		case opAdd:
			p := Parcel{
				Waybill:  pickWaybill(),
				Site:     sites[r.Intn(len(sites))],
				Weight:   pickWeight(r, cfg),
				Category: categories[r.Intn(len(categories))],
			}
			if r.Intn(40) == 0 {
				p.Category = Category(99) // 非法品类
			}
			if r.Intn(40) == 0 {
				p.Waybill = "" // 非法参数
			}
			idGot, errGot := h.AddParcel(p, now)
			idWant, errWant := n.add(p, now)
			if !sameErrKind(errGot, errWant) || idGot != idWant {
				t.Fatalf("step %d AddParcel(%+v, %d) 不一致: 优化实现=(%d, %v) 朴素模型=(%d, %v)",
					step, p, now, idGot, errGot, idWant, errWant)
			}
			t.Logf("step %d AddParcel(%s %s %dg cat=%d, t=%d) -> bag=%d err=%v",
				step, p.Waybill, p.Site, p.Weight, p.Category, now, idGot, errGot)
			if errGot == nil {
				bagIDs = appendUnique(bagIDs, idGot)
			}

		case opSeal:
			site := sites[r.Intn(len(sites))]
			idGot, errGot := h.SealBag(site, now)
			idWant, errWant := n.seal(site, now)
			if !sameErrKind(errGot, errWant) || idGot != idWant {
				t.Fatalf("step %d SealBag(%s, %d) 不一致: 优化实现=(%d, %v) 朴素模型=(%d, %v)",
					step, site, now, idGot, errGot, idWant, errWant)
			}
			t.Logf("step %d SealBag(%s, t=%d) -> bag=%d err=%v", step, site, now, idGot, errGot)

		case opDepart:
			bagID := pickBagID()
			train := fmt.Sprintf("T%d", r.Intn(5))
			if r.Intn(30) == 0 {
				train = "" // 非法参数
			}
			errGot := h.DepartBag(bagID, train, now)
			errWant := n.depart(bagID, train, now)
			if !sameErrKind(errGot, errWant) {
				t.Fatalf("step %d DepartBag(%d, %q, %d) 不一致: 优化实现=%v 朴素模型=%v",
					step, bagID, train, now, errGot, errWant)
			}
			t.Logf("step %d DepartBag(bag=%d train=%q, t=%d) -> err=%v", step, bagID, train, now, errGot)

		case opUnpack:
			bagID := pickBagID()
			site := sites[r.Intn(len(sites))]
			if r.Intn(4) != 0 {
				// 多数情况下用真实目的网点，保证拆袋成功率。
				if info, ok := h.BagInfo(bagID); ok {
					site = info.Site
				}
			}
			scanned := pickScanned(r, h, bagID, waybillPool)
			resGot, errGot := h.UnpackBag(bagID, site, scanned, now)
			resWant, errWant := n.unpack(bagID, site, scanned, now)
			if !sameErrKind(errGot, errWant) || !reflect.DeepEqual(resGot, resWant) {
				t.Fatalf("step %d UnpackBag(%d, %s, %v, %d) 不一致: 优化实现=(%+v, %v) 朴素模型=(%+v, %v)",
					step, bagID, site, scanned, now, resGot, errGot, resWant, errWant)
			}
			t.Logf("step %d UnpackBag(bag=%d site=%s scanned=%v, t=%d) -> missing=%v extra=%v err=%v",
				step, bagID, site, scanned, now, resGot.Missing, resGot.Extra, errGot)
		}
	}

	// 终态全量比对：每个集袋的内容、状态、各时刻、差异清单，以及时钟。
	if got, want := h.LastAcceptedTime(), n.last; got != want {
		t.Fatalf("最终时钟不一致: 优化实现=%d 朴素模型=%d", got, want)
	}
	wantSnap := n.snapshot()
	if len(wantSnap) == 0 {
		return
	}
	var maxID int64
	for _, info := range wantSnap {
		if info.ID > maxID {
			maxID = info.ID
		}
	}
	for id := int64(1); id <= maxID; id++ {
		got, okGot := h.BagInfo(id)
		if !okGot {
			t.Fatalf("集袋 %d 在优化实现中缺失", id)
		}
		want := wantSnap[id-1]
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("集袋 %d 快照不一致:\n优化实现=%+v\n朴素模型=%+v", id, got, want)
		}
	}
	t.Logf("终态一致：共 %d 个集袋，最终时刻 %d", maxID, h.LastAcceptedTime())
}

// pickWeight 混合生成：常规小重量、逼近上限、恰等上限、超过上限、非法值。
func pickWeight(r *rand.Rand, cfg Config) int64 {
	switch r.Intn(10) {
	case 0:
		return cfg.MaxBagWeight // 恰等上限
	case 1:
		return cfg.MaxBagWeight + 1 // 单件超重
	case 2:
		return 0 // 非法
	case 3:
		return MaxParcelWeight + 1 // 非法
	default:
		return int64(1 + r.Intn(int(cfg.MaxBagWeight)))
	}
}

// pickScanned 构造扫描集合：袋内运单号的随机子集 + 随机多出件，
// 小概率注入重复运单号以触发参数非法。
func pickScanned(r *rand.Rand, h *Hub, bagID int64, pool []string) []string {
	var scanned []string
	if info, ok := h.BagInfo(bagID); ok {
		for _, w := range info.Waybills {
			if r.Intn(3) != 0 {
				scanned = append(scanned, w)
			}
		}
	}
	for i := 0; i < r.Intn(3); i++ {
		scanned = append(scanned, fmt.Sprintf("X%d", r.Intn(1000)))
	}
	if len(scanned) > 0 && r.Intn(30) == 0 {
		scanned = append(scanned, scanned[0]) // 重复 -> 参数非法
	}
	if len(pool) > 0 && r.Intn(20) == 0 {
		scanned = append(scanned, "") // 空运单号 -> 参数非法
	}
	return scanned
}

func sameErrKind(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ka, oka := KindOf(a)
	kb, okb := KindOf(b)
	return oka && okb && ka == kb
}

func appendUnique(s []int64, v int64) []int64 {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

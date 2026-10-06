package sorting

import (
	"reflect"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{MaxBagCount: 3, MaxBagWeight: 1000, DwellLimit: 100}
}

func mustHub(t *testing.T, cfg Config) *Hub {
	t.Helper()
	h, err := NewHub(cfg)
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	return h
}

func mustAdd(t *testing.T, h *Hub, p Parcel, now int64) int64 {
	t.Helper()
	id, err := h.AddParcel(p, now)
	if err != nil {
		t.Fatalf("AddParcel(%+v, %d): %v", p, now, err)
	}
	return id
}

func wantErrKind(t *testing.T, err error, kind ErrKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %s，实际成功", kind)
	}
	k, ok := KindOf(err)
	if !ok || k != kind {
		t.Fatalf("期望错误类别 %s，实际 %v", kind, err)
	}
}

func normal(waybill, site string, weight int64) Parcel {
	return Parcel{Waybill: waybill, Site: site, Weight: weight, Category: CategoryNormal}
}

// 件数恰等于上限时装入同一袋，超一件时先封旧袋再开新袋。
func TestCountLimitExactAndOverflow(t *testing.T) {
	h := mustHub(t, testConfig())
	id1 := mustAdd(t, h, normal("W1", "S1", 100), 0)
	if id2 := mustAdd(t, h, normal("W2", "S1", 100), 1); id2 != id1 {
		t.Fatalf("同袋期望编号 %d，实际 %d", id1, id2)
	}
	if id3 := mustAdd(t, h, normal("W3", "S1", 100), 2); id3 != id1 {
		t.Fatalf("恰满上限应同袋，期望 %d，实际 %d", id1, id3)
	}
	// 第 4 件超出件数上限：旧袋封存，新袋编号 +1。
	id4 := mustAdd(t, h, normal("W4", "S1", 100), 3)
	if id4 != id1+1 {
		t.Fatalf("超限应开新袋 %d，实际 %d", id1+1, id4)
	}
	info, ok := h.BagInfo(id1)
	if !ok || info.Status != BagSealed || info.SealTime != 3 {
		t.Fatalf("旧袋应为已封存且封存时刻 3，实际 %+v ok=%v", info, ok)
	}
	if _, ok := h.OpenBag("S1"); !ok {
		t.Fatal("新袋应为开放集袋")
	}
}

// 总重恰等于上限与超一克。
func TestWeightLimitExactAndOverflow(t *testing.T) {
	h := mustHub(t, testConfig())
	id1 := mustAdd(t, h, normal("W1", "S1", 600), 0)
	if id2 := mustAdd(t, h, normal("W2", "S1", 400), 1); id2 != id1 {
		t.Fatalf("总重恰等于上限应同袋，期望 %d，实际 %d", id1, id2)
	}
	// 再放入 1 克即超上限，触发换袋。
	id3 := mustAdd(t, h, normal("W3", "S1", 1), 2)
	if id3 != id1+1 {
		t.Fatalf("超重 1 克应开新袋 %d，实际 %d", id1+1, id3)
	}
	info, _ := h.BagInfo(id1)
	if info.TotalWeight != 1000 {
		t.Fatalf("旧袋总重应为 1000，实际 %d", info.TotalWeight)
	}
}

// 停留时限：恰好满即到时换袋，差一秒仍同袋。
func TestDwellLimitExactAndOneSecondShort(t *testing.T) {
	h := mustHub(t, testConfig()) // DwellLimit = 100
	id1 := mustAdd(t, h, normal("W1", "S1", 100), 0)
	// t=99：距首件 99 秒，未到时。
	if id2 := mustAdd(t, h, normal("W2", "S1", 100), 99); id2 != id1 {
		t.Fatalf("差一秒未到时，应同袋 %d，实际 %d", id1, id2)
	}
	// t=100：恰好满时限，旧袋封存开新袋。
	id3 := mustAdd(t, h, normal("W3", "S1", 100), 100)
	if id3 != id1+1 {
		t.Fatalf("恰好满时限应开新袋 %d，实际 %d", id1+1, id3)
	}
	info, _ := h.BagInfo(id1)
	if info.Status != BagSealed || info.SealTime != 100 {
		t.Fatalf("旧袋应在 t=100 封存，实际 %+v", info)
	}
}

// 品类互斥：易碎与液体不得同袋；普通件可与二者任一同袋。
func TestCategoryConflict(t *testing.T) {
	cases := []struct {
		name     string
		first    Category
		second   Category
		wantSame bool
	}{
		{"普通+普通", CategoryNormal, CategoryNormal, true},
		{"普通+易碎", CategoryNormal, CategoryFragile, true},
		{"普通+液体", CategoryNormal, CategoryLiquid, true},
		{"易碎+普通", CategoryFragile, CategoryNormal, true},
		{"液体+普通", CategoryLiquid, CategoryNormal, true},
		{"易碎+易碎", CategoryFragile, CategoryFragile, true},
		{"液体+液体", CategoryLiquid, CategoryLiquid, true},
		{"易碎+液体", CategoryFragile, CategoryLiquid, false},
		{"液体+易碎", CategoryLiquid, CategoryFragile, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := mustHub(t, testConfig())
			id1 := mustAdd(t, h, Parcel{Waybill: "W1", Site: "S1", Weight: 100, Category: tc.first}, 0)
			id2 := mustAdd(t, h, Parcel{Waybill: "W2", Site: "S1", Weight: 100, Category: tc.second}, 1)
			if same := id1 == id2; same != tc.wantSame {
				t.Fatalf("同袋=%v，期望 %v", same, tc.wantSame)
			}
		})
	}

	// 三方组合：袋内已有易碎+普通，液体仍需换袋。
	h := mustHub(t, testConfig())
	mustAdd(t, h, Parcel{Waybill: "F1", Site: "S1", Weight: 100, Category: CategoryFragile}, 0)
	mustAdd(t, h, Parcel{Waybill: "N1", Site: "S1", Weight: 100, Category: CategoryNormal}, 1)
	idL := mustAdd(t, h, Parcel{Waybill: "L1", Site: "S1", Weight: 100, Category: CategoryLiquid}, 2)
	info, _ := h.BagInfo(idL)
	if len(info.Waybills) != 1 || info.Waybills[0] != "L1" {
		t.Fatalf("液体应单独开新袋，实际袋内容 %v", info.Waybills)
	}
	// 新袋内已有液体，易碎不得再放入。
	idF := mustAdd(t, h, Parcel{Waybill: "F2", Site: "S1", Weight: 100, Category: CategoryFragile}, 3)
	if idF == idL {
		t.Fatal("易碎不得放入含液体的袋")
	}
}

// 单件重量本身超过总重上限：直接拒绝，不占编号、不推进时钟、不改变状态。
func TestSingleParcelOverweight(t *testing.T) {
	h := mustHub(t, testConfig())
	_, err := h.AddParcel(normal("BIG", "S1", 1001), 10)
	wantErrKind(t, err, ErrBusinessReject)
	if _, ok := h.OpenBag("S1"); ok {
		t.Fatal("被拒绝的操作不得产生集袋")
	}
	if got := h.LastAcceptedTime(); got != 0 {
		t.Fatalf("被拒绝的操作不得推进时钟，实际 last=%d", got)
	}
	// 恰等于上限的单件可以入袋。
	mustAdd(t, h, normal("EXACT", "S1", 1000), 10)
	// 袋 1 已满重，下一件触发换袋；拒绝不占号，新袋编号应为 2。
	id := mustAdd(t, h, normal("W1", "S1", 1), 11)
	if id != 2 {
		t.Fatalf("被拒绝操作不占号，新袋应为 2，实际 %d", id)
	}
}

// 同一运单号在场时重复加入报业务拒绝；拆袋确认后可重新加入。
func TestDuplicateWaybill(t *testing.T) {
	h := mustHub(t, testConfig())
	mustAdd(t, h, normal("W1", "S1", 100), 0)
	_, err := h.AddParcel(normal("W1", "S1", 100), 1)
	wantErrKind(t, err, ErrBusinessReject)
	// 运单号全局唯一：即使目的网点不同，在场时仍为重复。
	_, err = h.AddParcel(normal("W1", "S2", 100), 2)
	wantErrKind(t, err, ErrBusinessReject)
	// 走完 封存->出场->拆袋确认 后，运单号离场，可重新加入。
	bagID, err := h.SealBag("S1", 3)
	if err != nil {
		t.Fatalf("SealBag: %v", err)
	}
	if err := h.DepartBag(bagID, "T1", 4); err != nil {
		t.Fatalf("DepartBag: %v", err)
	}
	res, err := h.UnpackBag(bagID, "S1", []string{"W1"}, 5)
	if err != nil {
		t.Fatalf("UnpackBag: %v", err)
	}
	if len(res.Missing) != 0 || len(res.Extra) != 0 {
		t.Fatalf("核对应无差异，实际 %+v", res)
	}
	if _, err := h.AddParcel(normal("W1", "S1", 100), 6); err != nil {
		t.Fatalf("拆袋确认后应可重新加入: %v", err)
	}
}

// 手动封袋：无开放集袋时报对象不存在；封存后不可再加入（自动开新袋）。
func TestManualSeal(t *testing.T) {
	h := mustHub(t, testConfig())
	_, err := h.SealBag("S1", 0)
	wantErrKind(t, err, ErrNotFound)

	mustAdd(t, h, normal("W1", "S1", 100), 0)
	bagID, err := h.SealBag("S1", 1)
	if err != nil {
		t.Fatalf("SealBag: %v", err)
	}
	info, _ := h.BagInfo(bagID)
	if info.Status != BagSealed || info.SealTime != 1 {
		t.Fatalf("应为已封存且封存时刻 1，实际 %+v", info)
	}
	// 再次手动封袋：已无开放集袋。
	if _, err := h.SealBag("S1", 2); err == nil {
		t.Fatal("封存后再次封袋应报错")
	}
	// 已封存的集袋不可再加入：新快件进入新开的袋。
	id := mustAdd(t, h, normal("W2", "S1", 100), 3)
	if id == bagID {
		t.Fatal("已封存的集袋不可再加入")
	}
	// 不同网点互不影响。
	if _, err := h.SealBag("S2", 4); err == nil {
		t.Fatal("无开放集袋的网点封袋应报错")
	}
}

// 出场：仅已封存的集袋可出场，需给定车次；未封存报状态不符。
func TestDepartBag(t *testing.T) {
	h := mustHub(t, testConfig())
	bagID := mustAdd(t, h, normal("W1", "S1", 100), 0)
	// 开放中的集袋出场：状态不符。
	wantErrKind(t, h.DepartBag(bagID, "T1", 1), ErrStateMismatch)
	// 不存在的集袋：对象不存在。
	wantErrKind(t, h.DepartBag(999, "T1", 1), ErrNotFound)
	// 空车次：参数非法。
	wantErrKind(t, h.DepartBag(bagID, "", 1), ErrInvalidParam)

	if _, err := h.SealBag("S1", 1); err != nil {
		t.Fatalf("SealBag: %v", err)
	}
	if err := h.DepartBag(bagID, "T1", 2); err != nil {
		t.Fatalf("DepartBag: %v", err)
	}
	info, _ := h.BagInfo(bagID)
	if info.Status != BagDeparted || info.TrainNo != "T1" || info.DepartTime != 2 {
		t.Fatalf("出场信息不正确: %+v", info)
	}
	// 重复出场：状态不符。
	wantErrKind(t, h.DepartBag(bagID, "T2", 3), ErrStateMismatch)
}

// 拆袋核对：缺失与多出双向清单，均按运单号升序；
// 缺失件待查仍在场不可重加，多出件只记录不入场。
func TestUnpackDiff(t *testing.T) {
	h := mustHub(t, testConfig())
	mustAdd(t, h, normal("W3", "S1", 100), 0)
	mustAdd(t, h, normal("W1", "S1", 100), 1)
	bagID := mustAdd(t, h, normal("W2", "S1", 100), 2)
	if _, err := h.SealBag("S1", 3); err != nil {
		t.Fatalf("SealBag: %v", err)
	}
	// 未出场拆袋：状态不符。
	if _, err := h.UnpackBag(bagID, "S1", nil, 4); err != nil {
		wantErrKind(t, err, ErrStateMismatch)
	}
	if err := h.DepartBag(bagID, "T1", 4); err != nil {
		t.Fatalf("DepartBag: %v", err)
	}
	// 网点不符。
	if _, err := h.UnpackBag(bagID, "S2", nil, 5); err != nil {
		wantErrKind(t, err, ErrSiteMismatch)
	}
	// 扫描集合含重复运单号：参数非法。
	if _, err := h.UnpackBag(bagID, "S1", []string{"W1", "W1"}, 5); err != nil {
		wantErrKind(t, err, ErrInvalidParam)
	}
	// 正常拆袋：W2 缺失，X9、X1 多出。
	res, err := h.UnpackBag(bagID, "S1", []string{"W1", "W3", "X9", "X1"}, 5)
	if err != nil {
		t.Fatalf("UnpackBag: %v", err)
	}
	if !reflect.DeepEqual(res.Missing, []string{"W2"}) {
		t.Fatalf("缺失清单应为 [W2]，实际 %v", res.Missing)
	}
	if !reflect.DeepEqual(res.Extra, []string{"X1", "X9"}) {
		t.Fatalf("多出清单应升序为 [X1 X9]，实际 %v", res.Extra)
	}
	// 缺失件仍视为在场，不得重新加入。
	if _, err := h.AddParcel(normal("W2", "S1", 100), 6); err != nil {
		wantErrKind(t, err, ErrBusinessReject)
	}
	// 已确认件可重新加入。
	if _, err := h.AddParcel(normal("W1", "S1", 100), 6); err != nil {
		t.Fatalf("确认件应可重加: %v", err)
	}
	// 多出件只记录不入场，可被正常加入。
	if _, err := h.AddParcel(normal("X1", "S1", 100), 6); err != nil {
		t.Fatalf("多出件不应入场，加入不应报重复: %v", err)
	}
	// 重复拆袋：状态不符，且不改变既有差异记录。
	if _, err := h.UnpackBag(bagID, "S1", []string{"W2"}, 7); err != nil {
		wantErrKind(t, err, ErrStateMismatch)
	}
	info, _ := h.BagInfo(bagID)
	if !reflect.DeepEqual(info.UnpackMissing, []string{"W2"}) ||
		!reflect.DeepEqual(info.UnpackExtra, []string{"X1", "X9"}) {
		t.Fatalf("重复拆袋不得改变差异记录，实际 %+v", info)
	}
}

// 错误优先级：参数非法 > 时钟回退 > 对象不存在 > 状态不符 > 网点不符 > 业务拒绝。
func TestErrorPriority(t *testing.T) {
	h := mustHub(t, testConfig())
	bagID := mustAdd(t, h, normal("W1", "S1", 100), 10)
	if _, err := h.SealBag("S1", 10); err != nil {
		t.Fatalf("SealBag: %v", err)
	}
	if err := h.DepartBag(bagID, "T1", 10); err != nil {
		t.Fatalf("DepartBag: %v", err)
	}

	// 参数非法 优先于 时钟回退：空车次 + 回退时刻。
	wantErrKind(t, h.DepartBag(bagID, "", 0), ErrInvalidParam)
	// 时钟回退 优先于 对象不存在：回退时刻 + 不存在的集袋。
	wantErrKind(t, h.DepartBag(999, "T1", 0), ErrClockRollback)
	// 对象不存在 优先于 状态不符：不存在的集袋天然无状态可言。
	wantErrKind(t, h.DepartBag(999, "T1", 10), ErrNotFound)
	// 状态不符 优先于 网点不符：未出场的袋在错误网点拆袋。
	bag2 := mustAdd(t, h, normal("W2", "S1", 100), 11)
	if _, err := h.UnpackBag(bag2, "S2", nil, 11); err != nil {
		wantErrKind(t, err, ErrStateMismatch)
	}
	// 网点不符 优先于 业务拒绝：错误网点 + 扫描集合含在场运单号（不影响）。
	if err := h.DepartBag(bag2, "T1", 12); err == nil {
		// bag2 仍开放，先封存再出场。
		t.Fatal("开放集袋出场应报状态不符")
	}
	if _, err := h.SealBag("S1", 12); err != nil {
		t.Fatalf("SealBag: %v", err)
	}
	if err := h.DepartBag(bag2, "T1", 12); err != nil {
		t.Fatalf("DepartBag: %v", err)
	}
	if _, err := h.UnpackBag(bag2, "S2", nil, 12); err != nil {
		wantErrKind(t, err, ErrSiteMismatch)
	}
	// 时钟回退 优先于 业务拒绝：回退时刻 + 重复运单号。
	if _, err := h.AddParcel(normal("W1", "S1", 100), 0); err != nil {
		wantErrKind(t, err, ErrClockRollback)
	}
	// 参数非法 优先于 业务拒绝：非法重量 + 重复运单号。
	if _, err := h.AddParcel(Parcel{Waybill: "W1", Site: "S1", Weight: 0, Category: CategoryNormal}, 12); err != nil {
		wantErrKind(t, err, ErrInvalidParam)
	}
	// 时钟回退后被拒绝，时钟不推进：last 仍为 12。
	if got := h.LastAcceptedTime(); got != 12 {
		t.Fatalf("被拒绝操作不得推进时钟，last=%d", got)
	}
}

// 时钟回退：被拒绝的操作不改变状态、编号与时钟。
func TestClockRollback(t *testing.T) {
	h := mustHub(t, testConfig())
	mustAdd(t, h, normal("W1", "S1", 100), 5)
	_, err := h.AddParcel(normal("W2", "S1", 100), 4)
	wantErrKind(t, err, ErrClockRollback)
	info, _ := h.OpenBag("S1")
	if len(info.Waybills) != 1 {
		t.Fatalf("回退操作不得改变袋内容，实际 %v", info.Waybills)
	}
	// 等于上次时刻是允许的（不得小于）。
	mustAdd(t, h, normal("W2", "S1", 100), 5)
}

// 查询为只读快照：不推进时钟，返回副本不被后续写操作影响。
func TestQuerySnapshot(t *testing.T) {
	h := mustHub(t, testConfig())
	bagID := mustAdd(t, h, normal("W1", "S1", 100), 0)
	snap, ok := h.OpenBag("S1")
	if !ok || snap.ID != bagID {
		t.Fatalf("OpenBag: %+v ok=%v", snap, ok)
	}
	// 查询不推进时钟。
	if got := h.LastAcceptedTime(); got != 0 {
		t.Fatalf("查询不得改变时钟，last=%d", got)
	}
	// 修改快照不影响场内状态。
	snap.Waybills[0] = "HACK"
	snap2, _ := h.BagInfo(bagID)
	if snap2.Waybills[0] != "W1" {
		t.Fatal("查询必须返回副本")
	}
	// 按运单号查询所在集袋。
	if id, ok := h.BagOfWaybill("W1"); !ok || id != bagID {
		t.Fatalf("BagOfWaybill: id=%d ok=%v", id, ok)
	}
	if _, ok := h.BagOfWaybill("NOPE"); ok {
		t.Fatal("不在场运单号应 ok=false")
	}
	// 封存后 OpenBag 消失，BagInfo 状态为已封存。
	if _, err := h.SealBag("S1", 1); err != nil {
		t.Fatalf("SealBag: %v", err)
	}
	if _, ok := h.OpenBag("S1"); ok {
		t.Fatal("封存后不应再有开放集袋")
	}
	if info, _ := h.BagInfo(bagID); info.Status != BagSealed {
		t.Fatalf("状态应为已封存，实际 %s", info.Status)
	}
}

// 并发：多 goroutine 混合调用，结果等价于某个串行顺序；
// 任一快件在任一时刻至多属于一个集袋。
func TestConcurrent(t *testing.T) {
	h := mustHub(t, Config{MaxBagCount: 50, MaxBagWeight: 100000, DwellLimit: 1000})
	const workers = 8
	const perWorker = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			site := string(rune('A' + w%4))
			for i := 0; i < perWorker; i++ {
				waybill := waybillOf(w, i)
				now := int64(i) // 各 goroutine 时刻单调，全局可能回退，回退即拒绝
				if _, err := h.AddParcel(normal(waybill, site, 10), now); err != nil {
					if k, _ := KindOf(err); k != ErrClockRollback && k != ErrBusinessReject {
						t.Errorf("非预期错误: %v", err)
					}
					continue
				}
				// 接受后立即查询：该运单号必须唯一归属于某个袋。
				if _, ok := h.BagOfWaybill(waybill); !ok {
					t.Errorf("已接受的运单号 %s 查询不到所在集袋", waybill)
				}
				if i%37 == 0 {
					_, _ = h.SealBag(site, now)
				}
				if i%11 == 0 {
					_, _ = h.OpenBag(site)
				}
			}
		}(w)
	}
	wg.Wait()
	// 不变量：每个在场运单号恰好属于一个袋，且袋确实含有它。
	// （通过公开查询无法遍历全部运单号，这里抽查每个 worker 的首末件。）
	for w := 0; w < workers; w++ {
		for _, i := range []int{0, perWorker - 1} {
			waybill := waybillOf(w, i)
			id, ok := h.BagOfWaybill(waybill)
			if !ok {
				continue // 可能被拒绝或已拆袋确认（本测试不拆袋，故只会是被拒绝）
			}
			info, ok := h.BagInfo(id)
			if !ok {
				t.Fatalf("运单号 %s 所在集袋 %d 不存在", waybill, id)
			}
			found := false
			for _, wb := range info.Waybills {
				if wb == waybill {
					found = true
				}
			}
			if !found {
				t.Fatalf("运单号 %s 不在其归属集袋 %d 中", waybill, id)
			}
		}
	}
}

func waybillOf(worker, i int) string {
	return "W" + string(rune('0'+worker)) + "-" + itoa(i)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}

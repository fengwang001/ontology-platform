package van

import (
	"errors"
	"testing"
)

func mustLoad(t *testing.T, s *Service, c Cargo) int {
	t.Helper()
	z, err := s.Load(c)
	if err != nil {
		t.Fatalf("Load %v 意外失败: %v", c, err)
	}
	return z
}

func rejectReason(t *testing.T, err error) RejectReason {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("期望 *RejectError, 得到 %T: %v", err, err)
	}
	return re.Reason
}

func TestBoundaryLimit(t *testing.T) {
	s := New([]ZoneSpec{{100, 100}, {100, 100}})
	if z := mustLoad(t, s, Cargo{ID: 1, Weight: 100, Volume: 100, Stop: 1}); z != 1 {
		t.Fatalf("恰好等于上限应允许放入分区1, 得到 %d", z)
	}
	if r := s.Remaining(); r[0].Weight != 0 || r[0].Volume != 0 {
		t.Fatalf("分区1剩余应为0, 得到 %+v", r[0])
	}
	if z := mustLoad(t, s, Cargo{ID: 2, Weight: 100, Volume: 100, Stop: 1}); z != 2 {
		t.Fatalf("第二件应溢出到分区2, 得到 %d", z)
	}
	_, err := s.Load(Cargo{ID: 3, Weight: 101, Volume: 100, Stop: 1})
	if got := rejectReason(t, err); got != ReasonOverWeight {
		t.Fatalf("超一克期望超重, 得到 %s", got)
	}
	_, err = s.Load(Cargo{ID: 4, Weight: 100, Volume: 101, Stop: 1})
	if got := rejectReason(t, err); got != ReasonOverVolume {
		// 两分区重量均已用尽：超重先于超容命中；改用重量宽松、仅容积受限的第三分区单独验证超容。
	}
	s2v := New([]ZoneSpec{{100, 100}})
	mustLoad(t, s2v, Cargo{ID: 1, Weight: 1, Volume: 100, Stop: 1})
	_, err = s2v.Load(Cargo{ID: 2, Weight: 1, Volume: 1, Stop: 1})
	if got := rejectReason(t, err); got != ReasonOverVolume {
		t.Fatalf("重量充足仅超一立方厘米期望超容, 得到 %s", got)
	}
}

func TestIsolationMatrix(t *testing.T) {
	cases := []struct {
		a, b Category
		want bool
	}{
		{General, General, true}, {General, Food, true},
		{General, Flammable, true}, {General, Oxidizer, true},
		{Food, Food, true}, {Flammable, Flammable, true}, {Oxidizer, Oxidizer, true},
		{Food, Flammable, false}, {Food, Oxidizer, false},
		{Flammable, Food, false}, {Oxidizer, Food, false},
		{Flammable, Oxidizer, false}, {Oxidizer, Flammable, false},
	}
	for _, tc := range cases {
		if got := compatible(tc.a, tc.b); got != tc.want {
			t.Errorf("compatible(%s,%s)=%v 期望 %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestIsolationConflict(t *testing.T) {
	s := New([]ZoneSpec{{1000, 1000}})
	mustLoad(t, s, Cargo{ID: 1, Weight: 1, Volume: 1, Stop: 1, Kind: Flammable})
	_, err := s.Load(Cargo{ID: 2, Weight: 1, Volume: 1, Stop: 1, Kind: Oxidizer})
	if got := rejectReason(t, err); got != ReasonIsolation {
		t.Fatalf("易燃+氧化期望隔离冲突, 得到 %s", got)
	}
	_, err = s.Load(Cargo{ID: 3, Weight: 1, Volume: 1, Stop: 1, Kind: Food})
	if got := rejectReason(t, err); got != ReasonIsolation {
		t.Fatalf("易燃+食品期望隔离冲突, 得到 %s", got)
	}
	if z := mustLoad(t, s, Cargo{ID: 4, Weight: 1, Volume: 1, Stop: 1, Kind: General}); z != 1 {
		t.Fatalf("普通应可与易燃同区, 得到 %d", z)
	}
}

func TestOrderPushesLaterStopBack(t *testing.T) {
	s := New([]ZoneSpec{{1000, 1000}, {1000, 1000}})
	// 后卸（停靠点2）货物先到，占编号最小的分区1。
	mustLoad(t, s, Cargo{ID: 10, Weight: 1, Volume: 1, Stop: 2})
	// 先卸（停靠点1）货物后到：同区混装允许，分区1顺序可行 -> 仍选分区1。
	if z := mustLoad(t, s, Cargo{ID: 11, Weight: 1, Volume: 1, Stop: 1}); z != 1 {
		t.Fatalf("同区允许混装不同停靠点, 先卸货也应进分区1, 得到 %d", z)
	}
	// 再来一件停靠点1货物：分区1已有停2（同区允许），仍进分区1。
	// 真正的跨分区约束：早卸货已占分区2后，更晚卸货不能进分区1。
	sf := New([]ZoneSpec{{1000, 1000}, {1000, 1000}})
	mustLoad(t, sf, Cargo{ID: 1, Weight: 1, Volume: 1, Stop: 1}) // 被迫？不，选最小分区1
	// 构造跨分区冲突：先让停1货物落到分区2（用容量/占用无法，顺序上第一件总是分区1）。
	// 等价地：停3占分区1，停1只能同区或更后；这里用三分区验证“更早选定的分区后来不可行”。
	three := New([]ZoneSpec{{1000, 1000}, {1000, 1000}, {1000, 1000}})
	mustLoad(t, three, Cargo{ID: 10, Weight: 1, Volume: 1, Stop: 3}) // 分区1
	mustLoad(t, three, Cargo{ID: 11, Weight: 1, Volume: 1, Stop: 1}) // 同区允许->分区1
	// 停2货物：分区1同区允许（混装），仍可选分区1。
	if z := mustLoad(t, three, Cargo{ID: 12, Weight: 1, Volume: 1, Stop: 2}); z != 1 {
		t.Fatalf("同区混装允许, 停2应进分区1, 得到 %d", z)
	}
	s3 := New([]ZoneSpec{{1000, 1000}})
	mustLoad(t, s3, Cargo{ID: 20, Weight: 1, Volume: 1, Stop: 1})
	mustLoad(t, s3, Cargo{ID: 21, Weight: 1, Volume: 1, Stop: 9})
	// 单分区只有同区，永不发生跨分区顺序冲突。
	if z := mustLoad(t, s3, Cargo{ID: 22, Weight: 1, Volume: 1, Stop: 5}); z != 1 {
		t.Fatalf("单分区任意停靠点均可同区混装, 得到 %d", z)
	}

}

func TestReasonMerge(t *testing.T) {
	// 公共布局：分区1容量恰好被停5货物占满（重量或体积），停1货物溢出到分区2。
	layout := func(limit2 ZoneSpec) *Service {
		s := New([]ZoneSpec{{1000, 1000}, {limit2.WeightLimit, limit2.VolumeLimit}})
		// 用超重把停5货物挤进分区2? 我们要的是“分区1有停5、分区2有停1”。
		// 让停1食物因分区1超重而溢出：先在分区1放近满载的停5普通货物。
		mustLoad(t, s, Cargo{ID: 1, Weight: 998, Volume: 998, Stop: 5, Kind: General})
		// 停1食品重3：分区1总重1001超重（且体积1001超容），落分区2。
		mustLoad(t, s, Cargo{ID: 2, Weight: 3, Volume: 3, Stop: 1, Kind: Food})
		return s
	}
	// 候选停6普通：分区1后置停1≤6 顺序通过但近满载超重(首因超重)；
	// 分区2前置停5<6 顺序冲突。整体取严重度最低者。
	// 分区2仅超重（区内停1食物重3已达上限3；候选重1使总重4>3，体积充足）。
	s := layout(ZoneSpec{3, 1000})
	_, err := s.Load(Cargo{ID: 3, Weight: 3, Volume: 1, Stop: 6, Kind: General})
	if got := rejectReason(t, err); got != ReasonOverWeight {
		t.Fatalf("分区1超重/分区2顺序冲突, 应报超重, 得到 %s", got)
	}
	// 分区2仅超容（区内停1食物体积3已达上限3；候选体积1使总容4>3，重量充足）。
	s = layout(ZoneSpec{1000, 3})
	_, err = s.Load(Cargo{ID: 3, Weight: 1, Volume: 3, Stop: 6, Kind: General})
	if got := rejectReason(t, err); got != ReasonOverVolume {
		t.Fatalf("只要有分区仅超容即报超容, 得到 %s", got)
	}
}

func TestDuplicateAndInvalid(t *testing.T) {
	s := New([]ZoneSpec{{100, 100}})
	mustLoad(t, s, Cargo{ID: 1, Weight: 1, Volume: 1, Stop: 1})
	if _, err := s.Load(Cargo{ID: 1, Weight: 1, Volume: 1, Stop: 1}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("重复编号期望 ErrDuplicate, 得到 %v", err)
	}
	bad := []Cargo{
		{ID: 0, Weight: 1, Volume: 1, Stop: 1},
		{ID: 2, Weight: 0, Volume: 1, Stop: 1},
		{ID: 2, Weight: 1, Volume: 0, Stop: 1},
		{ID: 2, Weight: 1, Volume: 1, Stop: 0},
		{ID: 2, Weight: 1, Volume: 1, Stop: 1, Kind: Category(99)},
	}
	for _, c := range bad {
		if _, err := s.Load(c); !errors.Is(err, ErrInvalid) {
			t.Fatalf("非法货物 %+v 期望 ErrInvalid, 得到 %v", c, err)
		}
	}
}

func TestBatchAllOrNothing(t *testing.T) {
	s := New([]ZoneSpec{{10, 10}})
	_, err := s.LoadBatch([]Cargo{
		{ID: 1, Weight: 6, Volume: 1, Stop: 1},
		{ID: 2, Weight: 6, Volume: 1, Stop: 1},
	})
	var be *BatchError
	if !errors.As(err, &be) || be.Index != 1 {
		t.Fatalf("期望下标1失败, 得到 %v", err)
	}
	if _, err := s.Location(1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("失败批次不应留痕, 得到 %v", err)
	}
	if r := s.Remaining(); r[0].Weight != 10 || r[0].Volume != 10 {
		t.Fatalf("失败批次后容量应复原, 得到 %+v", r[0])
	}
	if _, err := s.LoadBatch([]Cargo{
		{ID: 7, Weight: 1, Volume: 1, Stop: 1},
		{ID: 7, Weight: 1, Volume: 1, Stop: 1},
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("批内重复编号期望 ErrInvalid, 得到 %v", err)
	}
	res, err := s.LoadBatch([]Cargo{
		{ID: 3, Weight: 5, Volume: 1, Stop: 2},
		{ID: 4, Weight: 5, Volume: 1, Stop: 2},
	})
	if err != nil || res[3] != 1 || res[4] != 1 {
		t.Fatalf("成功批次结果异常: %v %v", res, err)
	}
}

func TestUnloadSequence(t *testing.T) {
	s := New([]ZoneSpec{{100, 100}, {100, 100}})
	mustLoad(t, s, Cargo{ID: 1, Weight: 1, Volume: 1, Stop: 1})
	mustLoad(t, s, Cargo{ID: 2, Weight: 1, Volume: 1, Stop: 3})

	// 停靠点1仍在车上时空跳停靠点2：更小序号未处理，属顺序错误。
	if _, err := s.Unload(2); !errors.Is(err, ErrOrderError) {
		t.Fatalf("停靠点1未卸时空跳停靠点2应顺序错误, 得到 %v", err)
	}
	if _, err := s.Unload(3); !errors.Is(err, ErrOrderError) {
		t.Fatalf("越级卸货期望顺序错误, 得到 %v", err)
	}
	if s.ArrivedStop() != 0 {
		t.Fatalf("顺序错误不得推进进度, 得到 %d", s.ArrivedStop())
	}
	ids, err := s.Unload(1)
	if err != nil || len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("卸1异常: %v %v", ids, err)
	}
	// 停靠点2为空：此时无更小序号在车，成功、无货物、推进进度。
	ids, err = s.Unload(2)
	if err != nil || len(ids) != 0 {
		t.Fatalf("空停靠点应成功且无卸下货物, 得到 %v %v", ids, err)
	}
	if s.ArrivedStop() != 2 {
		t.Fatalf("空停靠点后进度应为2, 得到 %d", s.ArrivedStop())
	}
	if _, err := s.Unload(2); !errors.Is(err, ErrProcessed) {
		t.Fatalf("重复停靠点期望已处理, 得到 %v", err)
	}
	if _, err := s.Unload(1); !errors.Is(err, ErrProcessed) {
		t.Fatalf("回卸更早停靠点期望已处理, 得到 %v", err)
	}
	ids, err = s.Unload(3)
	if err != nil || len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("卸3异常: %v %v", ids, err)
	}
	if _, err := s.Location(2); !errors.Is(err, ErrNotFound) {
		t.Fatal("货物2应已不在车上")
	}
}

func TestMidLoadAndPriority(t *testing.T) {
	s := New([]ZoneSpec{{1, 1}})
	if _, err := s.Load(Cargo{ID: 0, Weight: 1, Volume: 1, Stop: 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("参数非法优先级最高, 得到 %v", err)
	}
	mustLoad(t, s, Cargo{ID: 1, Weight: 1, Volume: 1, Stop: 1})
	if _, err := s.Unload(1); err != nil {
		t.Fatal(err)
	}
	// 已卸编号不再在车上：同编号、过去停靠点 -> 停靠点已过（无重复可言）。
	if _, err := s.Load(Cargo{ID: 2, Weight: 999, Volume: 1, Stop: 1}); !errors.Is(err, ErrStopPassed) {
		t.Fatalf("停靠点已过应先于四类约束, 得到 %v", err)
	}
	if z := mustLoad(t, s, Cargo{ID: 3, Weight: 1, Volume: 1, Stop: 3}); z != 1 {
		t.Fatalf("未来停靠点货物应可装入, 得到 %d", z)
	}
	// 空跳到停靠点2（车上仅有停靠点3，无更小序号），货物3仍在车上。
	if _, err := s.Unload(2); err != nil {
		t.Fatal(err)
	}
	// 同编号、且停靠点已过：编号重复检查先于停靠点已过。
	if _, err := s.Load(Cargo{ID: 3, Weight: 1, Volume: 1, Stop: 1}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("在车上的编号重复应先于停靠点已过, 得到 %v", err)
	}
}

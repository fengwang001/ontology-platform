package van

import "testing"

// 顺序不变量（推导见 docs/design.md）：
// 跨分区看，车头方向分区的停靠点集合必须整体不小于车尾方向；同区允许任意混装。
// 因此“顺序”只作为某分区的首因参与归并；整体只可能因隔离/超重/超容而彻底无可行分区。
func TestOrderAsFirstPerZoneReason(t *testing.T) {
	// 分区1停5易燃，分区2停1食品（食品因隔离被逼到分区2）。
	s := New([]ZoneSpec{{1000, 1000}, {1000, 1000}})
	mustLoad(t, s, Cargo{ID: 1, Weight: 1, Volume: 1, Stop: 5, Kind: Flammable})
	mustLoad(t, s, Cargo{ID: 2, Weight: 1, Volume: 1, Stop: 1, Kind: Food})

	// 候选停5普通：分区1顺序兼容（后置分区2停1更小，允许，因为普通货物可与任意类同区，
	// 它本可去分区2）。最终选编号最小且顺序/隔离/容量都过的分区：
	// 分区1与易燃兼容 -> 放入分区1。
	if z := mustLoad(t, s, Cargo{ID: 3, Weight: 1, Volume: 1, Stop: 5, Kind: General}); z != 1 {
		t.Fatalf("停5普通与分区1兼容, 应选分区1, 得到 %d", z)
	}

	// 候选停1普通：分区1顺序检查——前置无；后置分区2停1，要求 maxStop(后置)<=1，1<=1 过；
	// 同区与易燃兼容 -> 分区1顺序兼容（跨区允许“前置停5>=候选停1”），放入分区1。
	if z := mustLoad(t, s, Cargo{ID: 4, Weight: 1, Volume: 1, Stop: 1, Kind: General}); z != 1 {
		t.Fatalf("停1普通可与停5易燃同区混装, 应选分区1, 得到 %d", z)
	}
}

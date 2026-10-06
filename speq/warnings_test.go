package speq

import "testing"

func TestWarningOrderingAndTriggers(t *testing.T) {
	s := testSystem(t)
	d0 := ord(2020, 1, 1)

	// 设备 D1（BOILER，预警 15 天）到期 2021-01-01。
	_, _ = s.RegisterDevice(d0, "D1", "BOILER", d0)
	// 设备 D2（PV，预警 30 天）到期 2022-01-01。
	_, _ = s.RegisterDevice(d0, "D2", "PV", d0)
	// 安全阀 SV1 挂 D1，到期 2020-12-20；压力表 PG1 挂 D1，到期 2021-06-01。
	svFirst := ord(2019, 12, 20)
	_, _ = s.RegisterAttachment(d0, "SV1", "SV", KindSafetyValve, svFirst)
	_, _ = s.RegisterAttachment(d0, "SV2", "SV", KindSafetyValve, ord(2019, 12, 25))
	must(t, s.MountAttachment(d0, "SV1", "D1"))
	_, _ = s.RegisterAttachment(ord(2020, 6, 1), "PG1", "PG", KindPressureGauge, ord(2020, 6, 1))
	must(t, s.MountAttachment(ord(2020, 6, 1), "PG1", "D1"))

	q := ord(2020, 12, 18)
	got, err := s.QueryWarnings(q)
	must(t, err)
	// 命中：
	//   SV1 2020-12-20（窗口 10 天）；D1 由 SV1 触发（自身 2021-01-01 差 14 天也命中，窗口 15）
	//   其他均不在窗口：SV2 差 7 天命中（12-25）！
	// SV2: 2020-12-25 - 2020-12-18 = 7 <= 10 -> 命中。
	// 排序键：SV1=12-20, SV2=12-25, D1=min(01-01, 12-20)=12-20 与 SV1 并列 -> 编号升序 D1 先。
	wantIDs := []string{"D1", "SV1", "SV2"}
	if len(got) != len(wantIDs) {
		t.Fatalf("warning count = %d want %d: %+v", len(got), len(wantIDs), got)
	}
	for i, id := range wantIDs {
		if got[i].ID != id {
			t.Fatalf("warning[%d] = %s want %s (all=%+v)", i, got[i].ID, id, got)
		}
	}
	if len(got[0].Triggers) != 1 || got[0].Triggers[0] != "SV1" {
		t.Fatalf("D1 triggers should be [SV1], got %v", got[0].Triggers)
	}

	// 边界：查询日 2020-12-09：SV1 差 11 天不命中；D1 自身差 23 天也不命中。
	// 查询日 2020-12-17：D1 差 15 天恰好命中（含边界），SV1 差 3 天命中并触发 D1。
	got2, err := s.QueryWarnings(ord(2020, 12, 9))
	must(t, err)
	found := map[string]bool{}
	for _, e := range got2 {
		found[e.ID] = true
	}
	if found["SV1"] {
		t.Fatal("SV1 should be out of window at 11 days")
	}
	if found["D1"] {
		t.Fatal("D1 at 23 days should not hit")
	}
	got2, err = s.QueryWarnings(ord(2020, 12, 17))
	must(t, err)
	found = map[string]bool{}
	for _, e := range got2 {
		found[e.ID] = true
	}
	if !found["D1"] {
		t.Fatal("D1 self window 15 days boundary should hit")
	}

	// 封存对象不预警；停用对象不预警。
	must(t, s.Seal(ord(2020, 12, 18), "SV2"))
	got3, _ := s.QueryWarnings(ord(2020, 12, 18))
	for _, e := range got3 {
		if e.ID == "SV2" {
			t.Fatal("sealed object must not warn")
		}
	}
	_, _ = s.Inspect(ord(2020, 12, 19), "PG1", ResultFail, 0)
	got4, _ := s.QueryWarnings(ord(2020, 12, 19))
	for _, e := range got4 {
		if e.ID == "PG1" {
			t.Fatal("disabled object must not warn")
		}
	}
}

func TestTreapBalance(t *testing.T) {
	s := New()
	must(t, s.AddCategory(CategoryConfig{
		Code: "C", Kind: KindDevice, PeriodMonths: 12,
		EarlyWindowDays: 1, MinUnsealDays: 1, WarningLeadDays: 100000,
	}))
	const n = 5000
	for i := 0; i < n; i++ {
		date := i * 30
		_, err := s.RegisterDevice(date, idFor(i), "C", date)
		must(t, err)
	}
	if s.IndexSize() != n {
		t.Fatalf("index size = %d want %d", s.IndexSize(), n)
	}
	d := s.IndexDepth()
	// 5000 个键的 treap 高度应在 ~3*log2(n) ≈ 37 以内（留足余量取 60）。
	if d > 60 {
		t.Fatalf("treap not balanced, depth = %d for %d keys", d, n)
	}
}

func idFor(i int) string {
	// 定长编号，便于排序稳定。
	const digits = "0123456789"
	if i == 0 {
		return "OBJ-00000"
	}
	buf := []byte("00000")
	for p := 4; i > 0; p-- {
		buf[p] = digits[i%10]
		i /= 10
	}
	return "OBJ-" + string(buf)
}

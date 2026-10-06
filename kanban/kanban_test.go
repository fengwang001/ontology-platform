package kanban

import (
	"errors"
	"testing"
)

func mustBoard(t *testing.T, cfg Config) *Board {
	t.Helper()
	b, err := NewBoard(cfg)
	if err != nil {
		t.Fatalf("NewBoard: %v", err)
	}
	return b
}

func baseCfg(cols, g int, limits []int) Config {
	names := make([]string, cols)
	for i := range names {
		names[i] = "C" + string(rune('0'+i))
	}
	return Config{Columns: names, Limits: limits, OwnerLimit: g}
}

func errCode(err error) ErrCode {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func assertCode(t *testing.T, want ErrCode, err error) {
	t.Helper()
	if got := errCode(err); got != want {
		t.Fatalf("want %s, got %v", want, err)
	}
}

func TestNewBoardValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"two columns", baseCfg(2, 2, []int{0, 0})},
		{"nine columns", func() Config {
			c := baseCfg(9, 2, nil)
			c.Limits = []int{0, 1, 1, 1, 1, 1, 1, 1, 0}
			return c
		}()},
		{"limits length mismatch", baseCfg(3, 2, []int{0, 1})},
		{"todo limited", baseCfg(3, 2, []int{3, 1, 0})},
		{"done limited", baseCfg(3, 2, []int{0, 1, 3})},
		{"negative limit", baseCfg(3, 2, []int{0, -1, 0})},
		{"G zero", baseCfg(3, 0, []int{0, 1, 0})},
		{"G 51", baseCfg(3, 51, []int{0, 1, 0})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewBoard(tc.cfg); errCode(err) != ErrInvalidArgument {
				t.Fatalf("want invalid_argument, got %v", err)
			}
		})
	}
	if _, err := NewBoard(baseCfg(3, 5, []int{0, LimitUnlimited, 0})); err != nil {
		t.Fatalf("unlimited wip column should be legal: %v", err)
	}
}

func TestAddCardAndClock(t *testing.T) {
	b := mustBoard(t, baseCfg(3, 2, []int{0, 1, 0}))
	c, err := b.AddCard("a", "alice", 5)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 1 || c.Column != 0 {
		t.Fatalf("new card wrong: %+v", c)
	}
	assertCode(t, ErrInvalidArgument, mustErr(b.AddCard("", "alice", 6)))
	assertCode(t, ErrInvalidArgument, mustErr(b.AddCard("b", "", 6)))
	assertCode(t, ErrInvalidArgument, mustErr(b.AddCard("b2", "alice", -1)))

	// duplicate id
	assertCode(t, ErrInvalidArgument, func() error { _, e := b.AddCard("a", "bob", 7); return e }())
	// clock rollback
	_, err = b.AddCard("c", "bob", 4)
	assertCode(t, ErrClockRollback, err)
}

func mustErr(_ *Card, err error) error { return err }

func TestFlowLegality(t *testing.T) {
	b := mustBoard(t, baseCfg(4, 5, []int{0, 10, 10, 0}))
	add := func(id string) {
		t.Helper()
		if _, err := b.AddCard(id, "alice", 1); err != nil {
			t.Fatal(err)
		}
	}
	add("a")
	// 越列向右
	_, err := b.Move("u", "a", 2, 1, false, 2)
	assertCode(t, ErrIllegalFlow, err)
	// 版本冲突先于流转不合法：错误版本 + 越列
	_, err = b.Move("u", "a", 2, 99, false, 2)
	assertCode(t, ErrVersionConflict, err)
	// 已在原列
	_, err = b.Move("u", "a", 0, 1, false, 2)
	assertCode(t, ErrIllegalFlow, err)
	// 卡片不存在先于版本冲突
	_, err = b.Move("u", "ghost", 1, 99, false, 2)
	assertCode(t, ErrCardNotFound, err)
	// 时钟回退先于卡片不存在
	_, err = b.Move("u", "ghost", 1, 1, false, 0)
	assertCode(t, ErrClockRollback, err)

	// 合法右移
	c, err := b.Move("u", "a", 1, 1, false, 3)
	if err != nil || c.Version != 2 || c.Column != 1 {
		t.Fatalf("move: %+v %v", c, err)
	}
	// 向左任意列
	c, err = b.Move("u", "a", 0, 2, false, 4)
	if err != nil || c.Column != 0 || c.Version != 3 {
		t.Fatalf("move back: %+v %v", c, err)
	}
	// 走到完成
	mustOK(t, ignoreErr(b.Move("u", "a", 1, 3, false, 5)))
	mustOK(t, ignoreErr(b.Move("u", "a", 2, 4, false, 6)))
	mustOK(t, ignoreErr(b.Move("u", "a", 3, 5, false, 7)))
	_, err = b.Move("u", "a", 2, 6, false, 8)
	assertCode(t, ErrIllegalFlow, err)
	// 非法目标列
	_, err = b.Move("u", "a", 9, 6, false, 8)
	assertCode(t, ErrInvalidArgument, err)
}

func must(t *testing.T, c *Card, err error) *Card {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func okv(t *testing.T, _ *Card, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func ignoreErr(_ *Card, err error) error { return err }

func cardOf(c *Card, err error) *Card {
	if err != nil {
		panic(err)
	}
	return c
}

func TestColumnLimitAtAndBelow(t *testing.T) {
	b := mustBoard(t, baseCfg(3, 50, []int{0, 2, 0}))
	for _, id := range []string{"a", "b", "c"} {
		if _, err := b.AddCard(id, "alice", 1); err != nil {
			t.Fatal(err)
		}
	}
	for i, id := range []string{"a", "b"} {
		mustOK(t, ignoreErr(b.Move("u", id, 1, 1, false, int64(2+i))))
	}
	if b.ColumnCount(1) != 2 {
		t.Fatalf("occupancy want 2 got %d", b.ColumnCount(1))
	}
	// 恰好等于上限后，第三个被拒
	_, err := b.Move("u", "c", 1, 1, false, 10)
	assertCode(t, ErrColumnFull, err)
	// 离开一列后立刻可进入（先释放再判定）
	mustOK(t, ignoreErr(b.Move("u", "a", 0, 2, false, 11)))
	c := cardOf(b.Move("u", "c", 1, 1, false, 12))
	if c.Version != 2 {
		t.Fatalf("c version %d", c.Version)
	}
	// 被拒操作不改版本与时钟
	c2, _ := b.GetCard("c")
	if c2.Version != 2 || b.LastNow() != 12 {
		t.Fatalf("state changed after reject: %d lastNow=%d", c2.Version, b.LastNow())
	}
}

func TestOwnerLimitAndReleaseFirst(t *testing.T) {
	// G=1，列上限足够大：同一负责人相邻列前移不应因负责人上限被拒。
	b := mustBoard(t, baseCfg(4, 1, []int{0, 10, 10, 0}))
	mustOK(t, ignoreErr(b.AddCard("a", "alice", 1)))
	mustOK(t, ignoreErr(b.Move("u", "a", 1, 1, false, 2)))
	// 从 col1 前移到 col2：离开列先释放，负责人计数 1->0 再 +1，通过
	c := cardOf(b.Move("u", "a", 2, 2, false, 3))
	if c.Column != 2 {
		t.Fatalf("forward between in-progress columns rejected wrongly")
	}
	// 另一张同负责人卡片进入进行中应被负责人上限拒绝
	mustOK(t, ignoreErr(b.AddCard("b", "alice", 4)))
	_, err := b.Move("u", "b", 1, 1, false, 5)
	assertCode(t, ErrOwnerFull, err)
	// 列错误信息可区分：把列1上限调成0再试另一人
	mustOK(t, ignoreErr(b.AddCard("c", "bob", 6)))
	if err := b.SetColumnLimit(1, 1, 7); err != nil {
		t.Fatal(err)
	}
	// 列1 已被 a 占满（G=1 的 a 在 col2 时列1为空）；先让 a 回到 col1 占满
	ca := cardOf(b.Move("u", "a", 1, 3, false, 7))
	_ = ca
	_, err = b.Move("u", "c", 1, 1, false, 8)
	assertCode(t, ErrColumnFull, err)
}

func TestExpediteUniqueAndClear(t *testing.T) {
	// 4 列板：待办 / 进行中 col1 / 进行中 col2 / 完成；列不限，G=1。
	b := mustBoard(t, baseCfg(4, 1, []int{0, 0, 0, 0}))
	mustOK(t, ignoreErr(b.AddCard("a", "alice", 1)))
	mustOK(t, ignoreErr(b.AddCard("b", "alice", 2)))
	ca := cardOf(b.Move("u", "a", 1, 1, true, 3))
	if !ca.Expedited || b.ExpeditedCard() != "a" {
		t.Fatal("expedite not granted")
	}
	// 加急豁免 G：第二人普通移入受 G=1 约束 -> owner full
	_, err := b.Move("u", "b", 1, 1, false, 4)
	assertCode(t, ErrOwnerFull, err)
	// 加急同样豁免列上限：把 col1 调成 0 后，加急卡仍可在进行中列间前移
	if err := b.SetColumnLimit(1, 0, 5); err != nil {
		t.Fatal(err)
	}
	if err := b.SetColumnLimit(2, 0, 6); err != nil {
		t.Fatal(err)
	}
	mustOK(t, ignoreErr(b.Move("u", "a", 2, 2, true, 7)))
	if b.ExpeditedCard() != "a" {
		t.Fatal("expedite lost on move between in-progress columns")
	}
	// 第二人再申请加急报已占用
	_, err = b.Move("u", "b", 1, 1, true, 8)
	assertCode(t, ErrExpediteBusy, err)
	// 目标列非进行中（待办/完成）时 expedite=true 参数非法
	_, err = b.Move("u", "a", 0, 3, true, 9)
	assertCode(t, ErrInvalidArgument, err)
	_, err = b.Move("u", "a", 3, 3, true, 9)
	assertCode(t, ErrInvalidArgument, err)
	// 进入完成清除标记（加急卡的完成移动用 expedite=false，正常无上限判定）
	mustOK(t, ignoreErr(b.Move("u", "a", 3, 3, false, 10)))
	if b.ExpeditedCard() != "" {
		t.Fatalf("expedite not cleared on completion: %q", b.ExpeditedCard())
	}
	cx, _ := b.GetCard("a")
	if cx.Expedited {
		t.Fatal("card still flagged expedited after done")
	}
	// 加急槽释放后，其他卡片可申请
	mustOK(t, ignoreErr(b.Move("u", "b", 1, 1, true, 11)))
	if b.ExpeditedCard() != "b" {
		t.Fatal("expedite slot not reusable after clear")
	}
	// 回待办也清除
	mustOK(t, ignoreErr(b.Move("u", "b", 0, 2, false, 12)))
	if b.ExpeditedCard() != "" {
		t.Fatal("expedite not cleared on return to todo")
	}
}

func TestLowerLimitDoesNotEvict(t *testing.T) {
	b := mustBoard(t, baseCfg(3, 50, []int{0, 5, 0}))
	for _, id := range []string{"a", "b", "c"} {
		mustOK(t, ignoreErr(b.AddCard(id, "alice", 1)))
		mustOK(t, ignoreErr(b.Move("u", id, 1, 1, false, 1)))
	}
	if err := b.SetColumnLimit(1, 1, 10); err != nil {
		t.Fatal(err)
	}
	if b.ColumnCount(1) != 3 {
		t.Fatal("existing cards evicted after lowering limit")
	}
	mustOK(t, ignoreErr(b.AddCard("d", "alice", 11)))
	_, err := b.Move("u", "d", 1, 1, false, 12)
	assertCode(t, ErrColumnFull, err)
	// 非法下调参数
	assertCode(t, ErrInvalidArgument, b.SetColumnLimit(0, 1, 13))
	assertCode(t, ErrInvalidArgument, b.SetColumnLimit(1, -2, 13))
}

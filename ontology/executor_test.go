package ontology

import (
	"errors"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// ---------- 测试辅助 ----------

func sortedKeys(rows map[int64]map[string]int64) []int64 {
	pks := make([]int64, 0, len(rows))
	for pk := range rows {
		pks = append(pks, pk)
	}
	sort.Slice(pks, func(i, j int) bool { return pks[i] < pks[j] })
	return pks
}

func copyRows(rows map[int64]map[string]int64) map[int64]map[string]int64 {
	out := make(map[int64]map[string]int64, len(rows))
	for pk, row := range rows {
		cp := make(map[string]int64, len(row))
		for c, v := range row {
			cp[c] = v
		}
		out[pk] = cp
	}
	return out
}

// mustTable 建表（索引列 v，普通列 w）并按主键序插入全部行。
func mustTable(t *testing.T, unique bool, rows map[int64]map[string]int64) *Table {
	t.Helper()
	tbl, err := NewTable("v", unique, "v", "w")
	if err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	for _, pk := range sortedKeys(rows) {
		if err := tbl.Insert(pk, rows[pk]); err != nil {
			t.Fatalf("插入 pk=%d 失败: %v", pk, err)
		}
	}
	if err := tbl.CheckInvariants(); err != nil {
		t.Fatalf("建表后索引不变量被破坏: %v", err)
	}
	return tbl
}

// naiveApply 是朴素参考语义：先按更新前快照选出全部行，再逐行更新。
// 作为各用例的「判定依据」与执行器结果对照。
func naiveApply(t *testing.T, before map[int64]map[string]int64, stmt UpdateStmt) map[int64]map[string]int64 {
	t.Helper()
	out := copyRows(before)
	for _, pk := range sortedKeys(before) {
		row := before[pk] // 更新前快照中的行
		if row["v"] < stmt.Lo || row["v"] >= stmt.Hi {
			continue
		}
		if stmt.Filter != nil && !stmt.Filter.match(row) {
			continue
		}
		nv, overflow := applyAssign(row[stmt.Assign.Column], stmt.Assign)
		if overflow {
			t.Fatalf("朴素参考实现自身溢出 pk=%d", pk)
		}
		out[pk][stmt.Assign.Column] = nv
	}
	return out
}

// runAndCheck 执行语句并校验：结果与朴素语义一致、计数正确、索引不变量成立。
func runAndCheck(t *testing.T, tbl *Table, before map[int64]map[string]int64, stmt UpdateStmt, wantUpdated, wantExamined int) {
	t.Helper()
	res, err := tbl.Update(stmt)
	if err != nil {
		t.Fatalf("Update 返回意外错误: %v", err)
	}
	after := tbl.Snapshot()
	want := naiveApply(t, before, stmt)
	t.Logf("输入: 区间=[%d,%d) 过滤=%+v 赋值=%+v", stmt.Lo, stmt.Hi, stmt.Filter, stmt.Assign)
	t.Logf("输出: 更新行数=%d 考察条目数=%d", res.Updated, res.Examined)
	t.Logf("判定依据: 与「先按更新前快照选出全部行再逐行更新」的朴素结果逐行对比")
	if res.Updated != wantUpdated || res.Examined != wantExamined {
		t.Fatalf("计数不符: got (updated=%d, examined=%d), want (%d, %d)",
			res.Updated, res.Examined, wantUpdated, wantExamined)
	}
	if !reflect.DeepEqual(after, want) {
		t.Fatalf("结果与朴素语义不符:\n got=%v\nwant=%v", after, want)
	}
	if err := tbl.CheckInvariants(); err != nil {
		t.Fatalf("语句结束后索引不变量被破坏: %v", err)
	}
}

// ---------- 用例 ----------

// 乘以 2 使行跳到扫描前方：v=1->2 落在区间内且在扫描位置之后，
// 朴素语义下不得被再次更新，最终应为 {2,4,6} 而非 {4,8,12}。
func TestMultiplyTwoSkipsAhead(t *testing.T) {
	rows := map[int64]map[string]int64{
		1: {"v": 1, "w": 10},
		2: {"v": 2, "w": 20},
		3: {"v": 3, "w": 30},
	}
	tbl := mustTable(t, false, rows)
	stmt := UpdateStmt{Lo: 1, Hi: 4, Assign: Assignment{Column: "v", Op: AssignMul, Operand: 2}}
	t.Logf("输入: 行=%v 语句=v*2 区间[1,4)", rows)
	t.Logf("判定依据: 行 pk=1 更新后 v=2 移到扫描前方，若被重复更新会变成 4；恰好一次时三行应为 2,4,6")
	runAndCheck(t, tbl, rows, stmt, 3, 3)
	got := tbl.Snapshot()
	for pk, want := range map[int64]int64{1: 2, 2: 4, 3: 6} {
		if got[pk]["v"] != want {
			t.Fatalf("pk=%d 被重复更新或遗漏: v=%d, want %d", pk, got[pk]["v"], want)
		}
	}
	t.Logf("输出: %v", got)
}

// 唯一列 1..5 整体加 1：中途会产生暂时重复（1->2 与原有的 2 冲突），
// 但语句结束时键为 2..6，无冲突，必须成功。
func TestUniqueShiftUpByOne(t *testing.T) {
	rows := map[int64]map[string]int64{}
	for i := int64(1); i <= 5; i++ {
		rows[i] = map[string]int64{"v": i, "w": i * 100}
	}
	tbl := mustTable(t, true, rows)
	stmt := UpdateStmt{Lo: 1, Hi: 6, Assign: Assignment{Column: "v", Op: AssignAdd, Operand: 1}}
	t.Logf("输入: 唯一列 v=1..5，语句 v=v+1，区间[1,6)")
	t.Logf("判定依据: 唯一性按语句结束时判定，中途暂时重复不算冲突；结束后应为 2..6")
	runAndCheck(t, tbl, rows, stmt, 5, 5)
	for pk := int64(1); pk <= 5; pk++ {
		if got := tbl.Snapshot()[pk]["v"]; got != pk+1 {
			t.Fatalf("pk=%d: v=%d, want %d", pk, got, pk+1)
		}
	}
	t.Logf("输出: %v", tbl.Snapshot())
}

// 唯一列 1..5 整体减 1：结束后键为 0..4，无冲突。
func TestUniqueShiftDownByOne(t *testing.T) {
	rows := map[int64]map[string]int64{}
	for i := int64(1); i <= 5; i++ {
		rows[i] = map[string]int64{"v": i, "w": i * 100}
	}
	tbl := mustTable(t, true, rows)
	stmt := UpdateStmt{Lo: 1, Hi: 6, Assign: Assignment{Column: "v", Op: AssignAdd, Operand: -1}}
	t.Logf("输入: 唯一列 v=1..5，语句 v=v-1，区间[1,6)")
	t.Logf("判定依据: 结束后键 0..4 互不重复，语句应成功")
	runAndCheck(t, tbl, rows, stmt, 5, 5)
	for pk := int64(1); pk <= 5; pk++ {
		if got := tbl.Snapshot()[pk]["v"]; got != pk-1 {
			t.Fatalf("pk=%d: v=%d, want %d", pk, got, pk-1)
		}
	}
	t.Logf("输出: %v", tbl.Snapshot())
}

// 减法把行移出区间下界、同时有行移回区间：
// v=5,6,7,8 区间 [5,9)，v=v-3 -> 2,3,4,5。
// pk=4 的 8->5 重新落入区间，但不得被再次更新（否则 5->2）。
func TestSubtractMovesAcrossLowerBound(t *testing.T) {
	rows := map[int64]map[string]int64{
		1: {"v": 5, "w": 1},
		2: {"v": 6, "w": 2},
		3: {"v": 7, "w": 3},
		4: {"v": 8, "w": 4},
	}
	tbl := mustTable(t, false, rows)
	stmt := UpdateStmt{Lo: 5, Hi: 9, Assign: Assignment{Column: "v", Op: AssignAdd, Operand: -3}}
	t.Logf("输入: v=5,6,7,8，语句 v=v-3，区间[5,9)")
	t.Logf("判定依据: 考察条目数必须等于更新前区间内的 4 条；pk=4 的 8->5 移回区间不得再更新")
	runAndCheck(t, tbl, rows, stmt, 4, 4)
	for pk, want := range map[int64]int64{1: 2, 2: 3, 3: 4, 4: 5} {
		if got := tbl.Snapshot()[pk]["v"]; got != want {
			t.Fatalf("pk=%d: v=%d, want %d", pk, got, want)
		}
	}
	t.Logf("输出: %v", tbl.Snapshot())
}

// 第 7 行溢出时前 6 行必须回滚，表与索引逐项恢复原状。
func TestOverflowRollsBackAll(t *testing.T) {
	rows := map[int64]map[string]int64{}
	for i := int64(1); i <= 6; i++ {
		rows[i] = map[string]int64{"v": i, "w": i}
	}
	rows[7] = map[string]int64{"v": 1 << 62, "w": 7}
	before := copyRows(rows)
	tbl := mustTable(t, false, rows)
	stmt := UpdateStmt{Lo: 1, Hi: 1<<62 + 1, Assign: Assignment{Column: "v", Op: AssignMul, Operand: 2}}
	t.Logf("输入: 前 6 行 v=1..6，第 7 行 v=2^62，语句 v=v*2")
	t.Logf("判定依据: 第 7 行 2^62*2 溢出 int64，整条语句拒绝，前 6 行回滚")
	res, err := tbl.Update(stmt)
	if !errors.Is(err, ErrOverflow) {
		t.Fatalf("错误原因不符: got %v, want ErrOverflow", err)
	}
	if res != (UpdateResult{}) {
		t.Fatalf("失败语句不应返回计数: %+v", res)
	}
	after := tbl.Snapshot()
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("回滚后表未恢复原状:\n got=%v\nwant=%v", after, before)
	}
	if err := tbl.CheckInvariants(); err != nil {
		t.Fatalf("回滚后索引不变量被破坏: %v", err)
	}
	t.Logf("输出: err=%v，表已恢复为 %v", err, after)
}

// 空区间 lo == hi：更新零行、考察零条、不算错误。
func TestEmptyRange(t *testing.T) {
	rows := map[int64]map[string]int64{1: {"v": 1, "w": 1}, 2: {"v": 2, "w": 2}}
	tbl := mustTable(t, false, rows)
	stmt := UpdateStmt{Lo: 5, Hi: 5, Assign: Assignment{Column: "v", Op: AssignAdd, Operand: 1}}
	t.Logf("输入: 区间 [5,5) 为空")
	t.Logf("判定依据: 空区间更新零行且不报错")
	res, err := tbl.Update(stmt)
	if err != nil {
		t.Fatalf("空区间不应报错: %v", err)
	}
	if res.Updated != 0 || res.Examined != 0 {
		t.Fatalf("空区间计数应为零: %+v", res)
	}
	if !reflect.DeepEqual(tbl.Snapshot(), rows) {
		t.Fatalf("空区间不应改变表")
	}
	t.Logf("输出: %+v", res)
}

// lo > hi 必须整体拒绝并给出可区分原因。
func TestInvalidRange(t *testing.T) {
	tbl := mustTable(t, false, map[int64]map[string]int64{1: {"v": 1, "w": 1}})
	stmt := UpdateStmt{Lo: 9, Hi: 5, Assign: Assignment{Column: "v", Op: AssignAdd, Operand: 1}}
	t.Logf("输入: 区间 [9,5)，lo > hi")
	t.Logf("判定依据: 必须返回 ErrInvalidRange 且表不变")
	_, err := tbl.Update(stmt)
	if !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("错误原因不符: got %v, want ErrInvalidRange", err)
	}
	t.Logf("输出: err=%v", err)
}

// 引用未知列必须整体拒绝并给出可区分原因。
func TestUnknownColumn(t *testing.T) {
	tbl := mustTable(t, false, map[int64]map[string]int64{1: {"v": 1, "w": 1}})
	cases := []UpdateStmt{
		{Lo: 0, Hi: 10, Assign: Assignment{Column: "nope", Op: AssignSet, Operand: 1}},
		{Lo: 0, Hi: 10, Filter: &Filter{Column: "nope", Op: OpEq, Value: 1},
			Assign: Assignment{Column: "w", Op: AssignSet, Operand: 1}},
	}
	for i, stmt := range cases {
		_, err := tbl.Update(stmt)
		if !errors.Is(err, ErrUnknownColumn) {
			t.Fatalf("用例 %d 错误原因不符: got %v, want ErrUnknownColumn", i, err)
		}
		t.Logf("用例 %d 输出: err=%v", i, err)
	}
	t.Logf("判定依据: 赋值列或过滤列不在表结构中时必须返回 ErrUnknownColumn")
}

// 唯一索引在语句结束时仍有冲突：整体回滚并给出可区分原因。
func TestUniqueViolationRollsBack(t *testing.T) {
	rows := map[int64]map[string]int64{
		1: {"v": 1, "w": 1},
		2: {"v": 2, "w": 2},
		3: {"v": 9, "w": 9}, // 在区间外，保持不动
	}
	before := copyRows(rows)
	tbl := mustTable(t, true, rows)
	// 区间 [1,2) 只更新 pk=1：1+1=2，与区间外 pk=2 的 v=2 在语句结束时冲突。
	stmt := UpdateStmt{Lo: 1, Hi: 2, Assign: Assignment{Column: "v", Op: AssignAdd, Operand: 1}}
	t.Logf("输入: 唯一列 v=1,2,9，只对 [1,2) 执行 v=v+1")
	t.Logf("判定依据: 语句结束时 pk=1 的 v=2 与 pk=2 的 v=2 冲突，整体回滚")
	res, err := tbl.Update(stmt)
	if !errors.Is(err, ErrUniqueViolation) {
		t.Fatalf("错误原因不符: got %v, want ErrUniqueViolation", err)
	}
	if res != (UpdateResult{}) {
		t.Fatalf("失败语句不应返回计数: %+v", res)
	}
	if after := tbl.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatalf("回滚后表未恢复原状: got=%v want=%v", after, before)
	}
	if err := tbl.CheckInvariants(); err != nil {
		t.Fatalf("回滚后索引不变量被破坏: %v", err)
	}
	t.Logf("输出: err=%v，表已恢复", err)
}

// 其他列过滤 + 设置其他列：只更新过滤命中的行，索引列不动。
func TestFilterAndSetOtherColumn(t *testing.T) {
	rows := map[int64]map[string]int64{
		1: {"v": 1, "w": 7},
		2: {"v": 2, "w": 8},
		3: {"v": 3, "w": 7},
		4: {"v": 4, "w": 8},
	}
	tbl := mustTable(t, false, rows)
	stmt := UpdateStmt{
		Lo: 1, Hi: 5,
		Filter: &Filter{Column: "w", Op: OpEq, Value: 7},
		Assign: Assignment{Column: "w", Op: AssignSet, Operand: 70},
	}
	t.Logf("输入: 行=%v，过滤 w=7，赋值 w=70", rows)
	t.Logf("判定依据: 考察 4 条索引条目，只有 pk=1,3 命中过滤被更新")
	runAndCheck(t, tbl, rows, stmt, 2, 4)
	t.Logf("输出: %v", tbl.Snapshot())
}

// 并发读者只能看到某条语句之前或之后的完整表。
func TestConcurrentReadersSeeWholeStates(t *testing.T) {
	const n = 200
	rows := map[int64]map[string]int64{}
	for i := int64(1); i <= n; i++ {
		rows[i] = map[string]int64{"v": i, "w": i}
	}
	before := copyRows(rows)
	tbl := mustTable(t, false, rows)
	stmt := UpdateStmt{Lo: 1, Hi: n + 1, Assign: Assignment{Column: "v", Op: AssignAdd, Operand: 1000}}
	undo := UpdateStmt{Lo: 1001, Hi: n + 1001, Assign: Assignment{Column: "v", Op: AssignAdd, Operand: -1000}}
	after := naiveApply(t, before, stmt)
	t.Logf("输入: %d 行，语句 v=v+1000 与其逆语句交替；8 个并发读者循环快照", n)
	t.Logf("判定依据: 每次快照必须逐行等于前态或后态之一，不得出现中间态")

	done := make(chan struct{})
	var wg sync.WaitGroup
	errCh := make(chan string, 64)
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				snap := tbl.Snapshot()
				if !reflect.DeepEqual(snap, before) && !reflect.DeepEqual(snap, after) {
					errCh <- "读者观察到中间态"
					return
				}
			}
		}()
	}
	const rounds = 50
	for i := 0; i < rounds; i++ {
		if _, err := tbl.Update(stmt); err != nil {
			t.Fatalf("第 %d 轮 Update 失败: %v", i, err)
		}
		if _, err := tbl.Update(undo); err != nil {
			t.Fatalf("第 %d 轮复位失败: %v", i, err)
		}
	}
	close(done)
	wg.Wait()
	select {
	case msg := <-errCh:
		t.Fatal(msg)
	default:
	}
	if err := tbl.CheckInvariants(); err != nil {
		t.Fatalf("并发结束后索引不变量被破坏: %v", err)
	}
	t.Logf("输出: %d 轮更新未观察到中间态", rounds)
}

// 同一语句序列反复执行，结果完全相同。
func TestDeterministicReplay(t *testing.T) {
	rows := map[int64]map[string]int64{}
	for i := int64(1); i <= 50; i++ {
		rows[i] = map[string]int64{"v": i * 3, "w": i}
	}
	stmts := []UpdateStmt{
		{Lo: 3, Hi: 90, Assign: Assignment{Column: "v", Op: AssignAdd, Operand: -2}},
		{Lo: 0, Hi: 200, Filter: &Filter{Column: "w", Op: OpGt, Value: 10},
			Assign: Assignment{Column: "w", Op: AssignSet, Operand: 0}},
		{Lo: 1, Hi: 60, Assign: Assignment{Column: "v", Op: AssignMul, Operand: 2}},
	}
	run := func() ([]UpdateResult, map[int64]map[string]int64) {
		tbl := mustTable(t, false, rows)
		results := make([]UpdateResult, 0, len(stmts))
		for _, s := range stmts {
			res, err := tbl.Update(s)
			if err != nil {
				t.Fatalf("Update 失败: %v", err)
			}
			results = append(results, res)
		}
		return results, tbl.Snapshot()
	}
	res1, snap1 := run()
	res2, snap2 := run()
	t.Logf("输入: 50 行 + %d 条语句的固定序列，执行两遍", len(stmts))
	t.Logf("判定依据: 两遍的返回计数与最终表必须完全一致")
	if !reflect.DeepEqual(res1, res2) || !reflect.DeepEqual(snap1, snap2) {
		t.Fatalf("重放结果不一致: %v/%v", res1, res2)
	}
	t.Logf("输出: 两遍结果一致，计数=%v", res1)
}

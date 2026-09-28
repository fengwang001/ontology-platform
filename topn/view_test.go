package topn

// 测试覆盖：
//   TestNewInvalidArgs         构造参数非法
//   TestTieOrdering            分数并列时按键字典序排序
//   TestRetractAndFill         撤回榜内/榜外行后的补位
//   TestLeaveBeforeEnter       新增挤入榜单时先离开后进入
//   TestInvalidChanges         各类非法变更（空键、未知操作、重复键、撤回缺失、分数不符、超限）
//   TestRejectHasNoSideEffect  被拒绝输入不改变状态与日志
//   TestDeterministic          同一输入序列反复计算输出完全一致
//   TestConcurrentRead         并发读取逐字段一致

import (
	"fmt"
	"sync"
	"testing"
)

// describeChange 与 describeEntry 用于在日志中打印输入、输出条目与判定依据。
func describeChange(c Change) string {
	op := "?"
	switch c.Op {
	case OpAdd:
		op = "ADD"
	case OpRetract:
		op = "RETRACT"
	}
	return fmt.Sprintf("%s(key=%q, score=%d)", op, c.Key, c.Score)
}

func describeEntry(e Entry) string {
	kind := "?"
	switch e.Kind {
	case KindLeave:
		kind = "LEAVE"
	case KindEnter:
		kind = "ENTER"
	}
	return fmt.Sprintf("%s(key=%q, score=%d, rank=%d)", kind, e.Key, e.Score, e.Rank)
}

func describeRows(rows []Row) string {
	s := "["
	for i, r := range rows {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("#%d=%q:%d", i+1, r.Key, r.Score)
	}
	return s + "]"
}

func rejectReason(err error) Reason {
	if re, ok := err.(*RejectError); ok {
		return re.Reason
	}
	return ReasonUnknown
}

func TestNewInvalidArgs(t *testing.T) {
	cases := []struct {
		name    string
		n       int
		maxLive int
		want    Reason
	}{
		{"n 为 0", 0, 0, ReasonInvalidArgument},
		{"n 为负数", -1, 0, ReasonInvalidArgument},
		{"maxLive 为负数", 3, -1, ReasonInvalidArgument},
		{"maxLive 小于 n", 3, 2, ReasonInvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := New(tc.n, tc.maxLive)
			if err == nil {
				t.Fatalf("New(%d,%d) 期望被拒绝，实际成功: %v", tc.n, tc.maxLive, v)
			}
			if got := rejectReason(err); got != tc.want {
				t.Fatalf("New(%d,%d) 原因=%v，期望 %v；判定依据: %v", tc.n, tc.maxLive, got, tc.want, err)
			}
			t.Logf("输入 New(n=%d,maxLive=%d) 按预期拒绝，依据: %v", tc.n, tc.maxLive, err)
		})
	}

	v, err := New(3, 0)
	if err != nil || v == nil {
		t.Fatalf("New(3,0) 应成功，实际 err=%v", err)
	}
}

func TestTieOrdering(t *testing.T) {
	v, _ := New(3, 0)
	// 三个同分 100 的行，验证分数相同时按键字典序升序排列。
	changes := []Change{
		{OpAdd, "cherry", 100},
		{OpAdd, "apple", 100},
		{OpAdd, "banana", 100},
		{OpAdd, "date", 50}, // 低分，榜外
		{OpAdd, "apricot", 200},
	}
	for _, c := range changes {
		entries, err := v.Apply(c)
		if err != nil {
			t.Fatalf("输入 %s 被意外拒绝: %v", describeChange(c), err)
		}
		t.Logf("输入 %s -> 输出 %v；当前榜单 %s", describeChange(c), entries, describeRows(v.Snapshot()))
	}

	got := v.Snapshot()
	want := []Row{
		{"apricot", 200}, // 最高分
		{"apple", 100},   // 同分中字典序最小
		{"banana", 100},  // 同分中字典序次之；cherry 因并列被挤出
	}
	if len(got) != len(want) {
		t.Fatalf("榜单长度=%d，期望 %d；实际 %s", len(got), len(want), describeRows(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 名=%v，期望 %v；实际榜单 %s（依据：分数降序、同分按键升序）",
				i+1, got[i], want[i], describeRows(got))
		}
	}
	t.Logf("判定依据：200 分独占榜首；三个 100 分按 apple<banana<cherry 取前二。最终 %s", describeRows(got))

	// 撤回 apricot 后，cherry（同分中排序最靠前的榜外行）补入第三名。
	c := Change{OpRetract, "apricot", 200}
	e, err := v.Apply(c)
	if err != nil {
		t.Fatalf("撤回被意外拒绝: %v", err)
	}
	t.Logf("输入 %s -> 输出 %v；当前榜单 %s", describeChange(c), e, describeRows(v.Snapshot()))
	got = v.Snapshot()
	want = []Row{{"apple", 100}, {"banana", 100}, {"cherry", 100}}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("补位后第 %d 名=%v，期望 %v；实际 %s", i+1, got[i], want[i], describeRows(got))
		}
	}
}

func TestRetractAndFill(t *testing.T) {
	v, _ := New(2, 0)
	// 建立 4 个存活行：a,b 在榜内，c,d 在榜外（保留以支持补位）。
	seed := []Change{
		{OpAdd, "a", 40},
		{OpAdd, "b", 30},
		{OpAdd, "c", 20},
		{OpAdd, "d", 10},
	}
	for _, c := range seed {
		if _, err := v.Apply(c); err != nil {
			t.Fatalf("种子输入 %s 被意外拒绝: %v", describeChange(c), err)
		}
	}
	t.Logf("种子完成：榜单 %s；存活行数 %d（榜外保留 c,d）", describeRows(v.Snapshot()), v.LiveCount())

	// 撤回榜内第一名 a：应输出 a 离开（旧名次 1）、c 补入（新名次 2）。
	c := Change{OpRetract, "a", 40}
	entries, err := v.Apply(c)
	if err != nil {
		t.Fatalf("输入 %s 被意外拒绝: %v", describeChange(c), err)
	}
	wantEntries := []Entry{
		{KindLeave, "a", 40, 1},
		{KindEnter, "c", 20, 2},
	}
	if !entriesEqual(entries, wantEntries) {
		t.Fatalf("输入 %s 输出=%v，期望 %v（依据：先离开后进入，榜外 c 排序最靠前）",
			describeChange(c), entries, wantEntries)
	}
	t.Logf("输入 %s -> 输出 %v；补位后榜单 %s", describeChange(c), entries, describeRows(v.Snapshot()))

	if got := v.Snapshot(); !rowsEqual(got, []Row{{"b", 30}, {"c", 20}}) {
		t.Fatalf("补位后榜单=%s，期望 [#1=b:30,#2=c:20]", describeRows(got))
	}
	if v.LiveCount() != 3 {
		t.Fatalf("存活行数=%d，期望 3", v.LiveCount())
	}

	// 撤回榜外行 d：榜单不变，无日志；但存活行数减少。
	c = Change{OpRetract, "d", 10}
	entries, err = v.Apply(c)
	if err != nil {
		t.Fatalf("输入 %s 被意外拒绝: %v", describeChange(c), err)
	}
	if len(entries) != 0 {
		t.Fatalf("撤回榜外行不应产生日志，实际 %v", entries)
	}
	t.Logf("输入 %s -> 无榜单变化；存活行数 %d", describeChange(c), v.LiveCount())
	if v.LiveCount() != 2 {
		t.Fatalf("存活行数=%d，期望 2", v.LiveCount())
	}
}

func rowsEqual(a, b []Row) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func entriesEqual(a, b []Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLeaveBeforeEnter(t *testing.T) {
	v, _ := New(2, 0)
	seed := []Change{
		{OpAdd, "a", 30},
		{OpAdd, "b", 20},
		{OpAdd, "c", 10}, // 榜外
	}
	for _, c := range seed {
		if _, err := v.Apply(c); err != nil {
			t.Fatalf("种子输入 %s 被意外拒绝: %v", describeChange(c), err)
		}
	}

	// 新增高分 x：x 进入第 1 名，b 从第 2 名离开。必须先 LEAVE 后 ENTER。
	c := Change{OpAdd, "x", 99}
	entries, err := v.Apply(c)
	if err != nil {
		t.Fatalf("输入 %s 被意外拒绝: %v", describeChange(c), err)
	}
	if len(entries) != 2 || entries[0].Kind != KindLeave || entries[1].Kind != KindEnter {
		t.Fatalf("输入 %s 输出=%v，期望 [LEAVE b, ENTER x]（依据：先离开后进入）",
			describeChange(c), entries)
	}
	if entries[0] != (Entry{KindLeave, "b", 20, 2}) {
		t.Fatalf("离开条目=%v，期望 b 以旧名次 2 离开", entries[0])
	}
	if entries[1] != (Entry{KindEnter, "x", 99, 1}) {
		t.Fatalf("进入条目=%v，期望 x 以新名次 1 进入", entries[1])
	}
	t.Logf("输入 %s -> 输出 [%s, %s]，顺序符合先离开后进入；榜单 %s",
		describeChange(c), describeEntry(entries[0]), describeEntry(entries[1]), describeRows(v.Snapshot()))

	// 新增低分不影响榜单：无日志。
	c = Change{OpAdd, "y", 1}
	entries, err = v.Apply(c)
	if err != nil {
		t.Fatalf("输入 %s 被意外拒绝: %v", describeChange(c), err)
	}
	if len(entries) != 0 {
		t.Fatalf("榜外新增不应产生日志，实际 %v", entries)
	}
	t.Logf("输入 %s -> 榜外，无日志；榜单 %s", describeChange(c), describeRows(v.Snapshot()))
}

func TestInvalidChanges(t *testing.T) {
	// 预置状态：键 a(10) 存活，maxLive=2。
	newView := func() *View {
		v, _ := New(2, 2)
		if _, err := v.Apply(Change{OpAdd, "a", 10}); err != nil {
			t.Fatalf("种子失败: %v", err)
		}
		return v
	}

	cases := []struct {
		name   string
		change Change
		want   Reason
	}{
		{"空键", Change{OpAdd, "", 10}, ReasonInvalidArgument},
		{"未知操作", Change{OpUnknown, "b", 10}, ReasonInvalidArgument},
		{"新增已存在的键", Change{OpAdd, "a", 99}, ReasonDuplicateKey},
		{"撤回不存在的键", Change{OpRetract, "ghost", 0}, ReasonRetractMissing},
		{"撤回分数不符", Change{OpRetract, "a", 11}, ReasonScoreMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newView()
			_, err := v.Apply(tc.change)
			if err == nil {
				t.Fatalf("输入 %s 应被拒绝，实际成功", describeChange(tc.change))
			}
			if got := rejectReason(err); got != tc.want {
				t.Fatalf("输入 %s 原因=%v，期望 %v；判定依据: %v",
					describeChange(tc.change), got, tc.want, err)
			}
			t.Logf("输入 %s 按预期拒绝（原因 %v），依据: %v", describeChange(tc.change), tc.want, err)
		})
	}

	// 存活行数超限：再加一行达到上限 2，第三行新增必须被拒。
	t.Run("存活行数超限", func(t *testing.T) {
		v := newView()
		if _, err := v.Apply(Change{OpAdd, "b", 5}); err != nil {
			t.Fatalf("第二次新增应成功，实际: %v", err)
		}
		c := Change{OpAdd, "c", 1}
		_, err := v.Apply(c)
		if err == nil {
			t.Fatalf("输入 %s 应因超限被拒，实际成功", describeChange(c))
		}
		if got := rejectReason(err); got != ReasonLiveLimitExceeded {
			t.Fatalf("输入 %s 原因=%v，期望 %v；依据: %v", describeChange(c), got, ReasonLiveLimitExceeded, err)
		}
		t.Logf("输入 %s 按预期拒绝（存活已达上限 2），依据: %v", describeChange(c), err)
	})
}

func TestRejectHasNoSideEffect(t *testing.T) {
	v, _ := New(2, 3)
	seed := []Change{
		{OpAdd, "a", 30},
		{OpAdd, "b", 20},
		{OpAdd, "c", 10}, // 榜外，但存活
	}
	for _, c := range seed {
		if _, err := v.Apply(c); err != nil {
			t.Fatalf("种子失败: %v", err)
		}
	}

	bad := []Change{
		{OpAdd, "a", 1},           // 重复键
		{OpAdd, "z", 1},           // 超限（存活已达上限 3）
		{OpRetract, "a", 99},      // 分数不符
		{OpRetract, "missing", 0}, // 键不存在
		{OpUnknown, "q", 0},       // 非法操作
		{OpAdd, "", 1},            // 空键
	}
	for _, c := range bad {
		snapBefore := v.Snapshot()
		logBefore := v.Log()
		liveBefore := v.LiveCount()

		entries, err := v.Apply(c)
		if err == nil {
			t.Fatalf("输入 %s 应被拒绝，实际成功", describeChange(c))
		}
		if entries != nil {
			t.Fatalf("被拒绝输入 %s 不应返回日志条目，实际 %v", describeChange(c), entries)
		}
		if v.LiveCount() != liveBefore {
			t.Fatalf("输入 %s 改变了存活行数：%d -> %d", describeChange(c), liveBefore, v.LiveCount())
		}
		if got := v.Snapshot(); !rowsEqual(got, snapBefore) {
			t.Fatalf("输入 %s 改变了榜单：%s -> %s", describeChange(c), describeRows(snapBefore), describeRows(got))
		}
		if got := v.Log(); !entriesEqual(got, logBefore) {
			t.Fatalf("输入 %s 改变了已产生日志：%v -> %v", describeChange(c), logBefore, got)
		}
		t.Logf("输入 %s 被拒（%v），存活/榜单/日志均未变：榜单 %s，日志条数 %d",
			describeChange(c), err, describeRows(snapBefore), len(logBefore))
	}

	// 拒绝之后再做一次合法变更，状态仍从拒绝前正确延续。
	c := Change{OpRetract, "a", 30}
	entries, err := v.Apply(c)
	if err != nil {
		t.Fatalf("拒绝后的合法变更失败: %v", err)
	}
	if !entriesEqual(entries, []Entry{{KindLeave, "a", 30, 1}, {KindEnter, "c", 10, 2}}) {
		t.Fatalf("拒绝后补位日志不符：%v", entries)
	}
	t.Logf("拒绝后的合法输入 %s -> %v，榜单 %s（状态正确延续）",
		describeChange(c), entries, describeRows(v.Snapshot()))
}

func TestDeterministic(t *testing.T) {
	// 包含并列、补位、榜外新增与若干会被拒绝的输入；重复键顺序用于检验字典序。
	changes := []Change{
		{OpAdd, "k9", 50},
		{OpAdd, "k1", 50}, // 同分，字典序决定先后
		{OpAdd, "k5", 50},
		{OpAdd, "k2", 50},
		{OpAdd, "low", 1},
		{OpAdd, "k1", 1},          // 重复键，拒绝
		{OpRetract, "missing", 1}, // 不存在，拒绝
		{OpRetract, "k1", 99},     // 分数不符，拒绝
		{OpRetract, "k9", 50},     // 合法撤回，触发补位
		{OpAdd, "hi", 100},        // 高分挤入
	}

	run := func() ([]Row, []Entry, int) {
		v, _ := New(3, 0)
		var outputs [][]Entry
		for _, c := range changes {
			e, err := v.Apply(c)
			if err == nil {
				outputs = append(outputs, e)
			}
		}
		// 拍平每次调用的输出，与累积日志一起比较。
		var flat []Entry
		for _, o := range outputs {
			flat = append(flat, o...)
		}
		return v.Snapshot(), flat, v.LiveCount()
	}

	snap0, out0, live0 := run()
	for i := 1; i < 5; i++ {
		snap, out, live := run()
		if live != live0 || !rowsEqual(snap, snap0) || !entriesEqual(out, out0) {
			t.Fatalf("第 %d 次重放结果不一致：\n快照 %s vs %s\n日志 %v vs %v\n存活 %d vs %d",
				i, describeRows(snap), describeRows(snap0), out, out0, live, live0)
		}
	}
	// 累积日志必须等于逐次返回输出的拼接（下游按顺序应用的依据）。
	v, _ := New(3, 0)
	for _, c := range changes {
		e, err := v.Apply(c)
		t.Logf("输入 %s -> 输出 %v，err=%v", describeChange(c), e, err)
	}
	if got := v.Log(); !entriesEqual(got, out0) {
		t.Fatalf("累积日志 %v 与逐次输出拼接 %v 不一致", got, out0)
	}
	t.Logf("5 次重放结果完全一致：最终榜单 %s，存活 %d，日志 %v", describeRows(snap0), live0, out0)
}

func TestConcurrentRead(t *testing.T) {
	v, _ := New(5, 0)

	// 校验一次快照内部逐字段自洽：行非空、按 (score desc, key asc) 严格有序。
	checkSnapshot := func(rows []Row) bool {
		for i := 1; i < len(rows); i++ {
			prev, cur := rows[i-1], rows[i]
			if cur.Key == "" {
				return false
			}
			if prev.Score < cur.Score {
				return false
			}
			if prev.Score == cur.Score && prev.Key >= cur.Key {
				return false
			}
		}
		return true
	}

	done := make(chan struct{})
	var readersWG sync.WaitGroup

	// 单个写者：交替新增与撤回，部分调用会被拒绝（重复键等）。
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			key := fmt.Sprintf("k%03d", i%30)
			if _, err := v.Apply(Change{OpAdd, key, int64(i % 17)}); err != nil {
				// 重复键等拒绝属于预期；换个唯一键继续制造变更。
				v.Apply(Change{OpAdd, fmt.Sprintf("u%05d", i), int64(i % 13)})
			}
			if i%7 == 0 {
				v.Apply(Change{OpRetract, key, int64(i % 17)})
			}
		}
	}()

	// 多个读者：每次拿到的快照都必须是某个一致状态（在 -race 下同时检测数据竞争）。
	const readers = 8
	const reads = 1000
	for r := 0; r < readers; r++ {
		readersWG.Add(1)
		go func() {
			defer readersWG.Done()
			for i := 0; i < reads; i++ {
				snap := v.Snapshot()
				if len(snap) > 5 || !checkSnapshot(snap) {
					t.Errorf("读到不一致快照: %s", describeRows(snap))
					return
				}
				_ = v.LiveCount()
				_ = v.Log()
			}
		}()
	}

	<-done
	readersWG.Wait()
	final := v.Snapshot()
	if !checkSnapshot(final) {
		t.Fatalf("最终快照不一致: %s", describeRows(final))
	}
	t.Logf("并发读写结束：存活 %d，榜单 %s（需配合 -race 运行）", v.LiveCount(), describeRows(final))
}

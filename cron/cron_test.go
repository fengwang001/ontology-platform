package cron

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func mustMinute(t *testing.T, y, mo, d, h, mi int) int64 {
	t.Helper()
	v, err := ToMinute(y, mo, d, h, mi)
	if err != nil {
		t.Fatalf("ToMinute(%d-%d-%d %d:%d) error: %v", y, mo, d, h, mi, err)
	}
	return v
}

func minuteLabel(tm int64) string {
	y, mo, d, h, mi, err := FromMinute(tm)
	if err != nil {
		return fmt.Sprintf("minute(%d,invalid)", tm)
	}
	return fmt.Sprintf("%04d-%02d-%02d %02d:%02d", y, mo, d, h, mi)
}

func TestTimeConversion(t *testing.T) {
	if v, err := ToMinute(2000, 1, 1, 0, 0); err != nil || v != 0 {
		t.Fatalf("epoch = %d, err=%v", v, err)
	}
	y, mo, d, h, mi, err := FromMinute(0)
	if err != nil || y != 2000 || mo != 1 || d != 1 || h != 0 || mi != 0 {
		t.Fatalf("FromMinute(0) = %v-%v-%v %v:%v err=%v", y, mo, d, h, mi, err)
	}
	if w := int((0/minutes + 6) % 7); w != 6 {
		t.Fatalf("2000-01-01 weekday = %d, want 6", w)
	}
	if leap := (mustMinute(t, 2000, 3, 1, 0, 0) - mustMinute(t, 2000, 2, 1, 0, 0)) / 1440; leap != 29 {
		t.Fatalf("Feb 2000 days = %d, want 29", leap)
	}
	if feb := (mustMinute(t, 2100, 3, 1, 0, 0) - mustMinute(t, 2100, 2, 1, 0, 0)) / 1440; feb != 28 {
		t.Fatalf("Feb 2100 days = %d, want 28", feb)
	}
	if _, err := ToMinute(2000, 2, 30, 0, 0); err != ErrIllegalTime {
		t.Fatalf("Feb 30 err = %v, want ErrIllegalTime", err)
	}
	for _, tc := range [][5]int{
		{2199, 12, 31, 23, 59},
		{2000, 1, 1, 0, 0},
		{2096, 2, 29, 12, 34},
	} {
		v := mustMinute(t, tc[0], tc[1], tc[2], tc[3], tc[4])
		y2, m2, d2, h2, mi2, ferr := FromMinute(v)
		if ferr != nil || [5]int{y2, m2, d2, h2, mi2} != tc {
			t.Fatalf("round trip %v -> %d -> %v,%v", tc, v, [5]int{y2, m2, d2, h2, mi2}, ferr)
		}
	}
	if _, _, _, _, _, err := FromMinute(-1); err != ErrIllegalTime {
		t.Fatalf("FromMinute(-1) err=%v", err)
	}
	if _, _, _, _, _, err := FromMinute(maxMinute + 1); err != ErrIllegalTime {
		t.Fatalf("FromMinute(max+1) err=%v", err)
	}
	if _, err := ToMinute(2200, 1, 1, 0, 0); err != ErrIllegalTime {
		t.Fatalf("year 2200 err=%v", err)
	}
}

func TestPromptFireExamples(t *testing.T) {
	f1, err := NextFire("0 0 13 * 5", 0)
	if err != nil {
		t.Fatal(err)
	}
	want1 := mustMinute(t, 2000, 1, 7, 0, 0)
	if f1 != want1 {
		t.Fatalf("or first = %v, want 2000-01-07", minuteLabel(f1))
	}
	f2, _ := NextFire("0 0 13 * 5", f1)
	if f2 != mustMinute(t, 2000, 1, 13, 0, 0) {
		t.Fatalf("or second = %v, want Jan 13", minuteLabel(f2))
	}
	f3, _ := NextFire("0 0 13 * 5", f2)
	if f3 != mustMinute(t, 2000, 1, 14, 0, 0) {
		t.Fatalf("or third = %v, want Jan 14", minuteLabel(f3))
	}

	g1, err := NextFire("0 0 */2 * 5", 0)
	if err != nil || g1 != mustMinute(t, 2000, 1, 7, 0, 0) {
		t.Fatalf("and first = %v err=%v", minuteLabel(g1), err)
	}
	g2, _ := NextFire("0 0 */2 * 5", g1)
	if g2 != mustMinute(t, 2000, 1, 21, 0, 0) {
		t.Fatalf("and second = %v, want Jan 21", minuteLabel(g2))
	}

	start := mustMinute(t, 2096, 2, 29, 0, 0)
	leap, err := NextFire("0 0 29 2 *", start)
	if err != nil {
		t.Fatal(err)
	}
	if leap != mustMinute(t, 2104, 2, 29, 0, 0) {
		t.Fatalf("leap next = %v, want 2104-02-29", minuteLabel(leap))
	}

	q := int64(0)
	for _, want := range []int64{5, 25, 45, 65} {
		got, gerr := NextFire("5/20 * * * *", q)
		if gerr != nil || got != want {
			t.Fatalf("5/20 after %d = %d err=%v, want %d", q, got, gerr, want)
		}
		q = got
	}
}

func TestNoNextAndStepBound(t *testing.T) {
	if _, err := NextFire("0 0 31 2 *", 0); err != ErrNoNext {
		t.Fatalf("Feb 31 err = %v, want ErrNoNext", err)
	}
	if lastDaySteps > dayStepMax {
		t.Fatalf("day steps = %d > %d", lastDaySteps, dayStepMax)
	}
	if _, err := NextFire("* * * * *", -1); err != ErrIllegalTime {
		t.Fatalf("t=-1 err=%v", err)
	}
	if _, err := NextFire("* * * * *", maxMinute+1); err != ErrIllegalTime {
		t.Fatalf("t=max+1 err=%v", err)
	}
}

func TestExactMatchReturnsNext(t *testing.T) {
	at := mustMinute(t, 2000, 1, 1, 1, 0)
	got, err := NextFire("0 * * * *", at)
	if err != nil || got != at+60 {
		t.Fatalf("exact-match next = %d err=%v, want %d", got, err, at+60)
	}
}

func TestWeekdaySemantics(t *testing.T) {
	got, err := NextFire("0 0 1 * 0", 0)
	if err != nil || got != mustMinute(t, 2000, 1, 2, 0, 0) {
		t.Fatalf("Sunday first = %v err=%v", minuteLabel(got), err)
	}
	if _, err := Parse("0 0 * * 7"); err != ErrRange {
		t.Fatalf("weekday 7 err = %v, want ErrRange", err)
	}
}

func Test31stSkipsShortMonths(t *testing.T) {
	first, err := NextFire("0 0 31 * *", 0)
	if err != nil || first != mustMinute(t, 2000, 1, 31, 0, 0) {
		t.Fatalf("31 first = %v err=%v", minuteLabel(first), err)
	}
	second, _ := NextFire("0 0 31 * *", first)
	if second != mustMinute(t, 2000, 3, 31, 0, 0) {
		t.Fatalf("31 second = %v, want Mar 31", minuteLabel(second))
	}
	third, _ := NextFire("0 0 31 * *", second)
	if third != mustMinute(t, 2000, 5, 31, 0, 0) {
		t.Fatalf("31 third = %v, want May 31", minuteLabel(third))
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		spec string
		want error
		why  string
	}{
		{"* * * *", ErrFieldCount, "字段数 4"},
		{"* * * * * *", ErrFieldCount, "字段数 6"},
		{"* *  * * *", ErrFieldCount, "双空格切分得到 6 段，先报字段个数"},
		{" * * * *", ErrSyntax, "开头空格产生 5 段且首字段为空，按空项报语法"},
		{"x * * * *", ErrSyntax, "非法字符"},
		{"1,,2 * * * *", ErrSyntax, "空项"},
		{"1, * * * *", ErrSyntax, "尾随逗号空项"},
		{"** * * * *", ErrSyntax, "星号位置非法"},
		{"60 * * * *", ErrRange, "分越界"},
		{"* 24 * * *", ErrRange, "时越界"},
		{"* * 32 * *", ErrRange, "日越界"},
		{"* * * 13 *", ErrRange, "月越界"},
		{"5-2 * * * *", ErrReversed, "范围反向"},
		{"5-3/0 * * * *", ErrReversed, "反向先于步长检查"},
		{"*/0 * * * *", ErrStep, "步长 0"},
		{"*/61 * * * *", ErrStep, "分步进超过 60"},
		{"* */25 * * *", ErrStep, "时步进超过 24"},
		{"* * * * */8", ErrStep, "周步进超过 7"},
		{"* * 32 99 *", ErrRange, "日先于月被检查"},
		{"70/2 * * * *", ErrRange, "取值先于步长检查"},
		{"a-b * * * *", ErrSyntax, "非数字"},
	}
	for _, c := range cases {
		if _, err := Parse(c.spec); err != c.want {
			t.Errorf("Parse(%q) err=%v, want %v（%s）", c.spec, err, c.want, c.why)
		}
	}
	for _, spec := range []string{"*/60 * * * *", "* * * * */7", "0,0,15,15,45 * * * *", "5/56 * * * *"} {
		if _, err := Parse(spec); err != nil {
			t.Errorf("Parse(%q) unexpected err=%v", spec, err)
		}
	}
}

func TestAStepUpperLimit(t *testing.T) {
	s, err := Parse("5/20 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	var got []int
	for v, ok := range s.fields[fieldMinute].values {
		if ok {
			got = append(got, v)
		}
	}
	want := []int{5, 25, 45}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("5/20 values = %v, want %v", got, want)
	}
	if s.fields[fieldMinute].wildcard {
		t.Fatalf("5/20 must not be wildcard")
	}
	sw, _ := Parse("*/20 * * * *")
	if !sw.fields[fieldMinute].wildcard {
		t.Fatalf("*/20 must be wildcard")
	}
}

func TestControllerForbidExample(t *testing.T) {
	j, err := NewJob("0 * * * *", 0, 30, Forbid)
	if err != nil {
		t.Fatal(err)
	}
	id, fired, err := j.Sync(185)
	if err != nil || !fired || id != 1 || j.Last() != 180 {
		t.Fatalf("Sync(185): id=%d fired=%v err=%v L=%d", id, fired, err, j.Last())
	}
	t.Logf("Sync(185): 窗口 t∈(0,185] 且 t>=155 => {180}，Forbid 活动空 -> 新建任务 %d，L=180", id)
	id, fired, err = j.Sync(245)
	if err != nil || fired || id != 0 || j.Last() != 240 || j.Skipped() != 1 {
		t.Fatalf("Sync(245): id=%d fired=%v err=%v L=%d skipped=%d", id, fired, err, j.Last(), j.Skipped())
	}
	t.Logf("Sync(245): 窗口 {240}，A 非空 -> 跳过 Skipped=1，仍推进 L=240")
}

func TestControllerTooManyExample(t *testing.T) {
	j, err := NewJob("0 * * * *", 240, -1, Allow)
	if err != nil {
		t.Fatal(err)
	}
	_, fired, err := j.Sync(6300)
	if err != ErrTooMany || fired {
		t.Fatalf("Sync(6300) err=%v fired=%v, want ErrTooMany", err, fired)
	}
	if j.Last() != 240 || j.Water() != 240 {
		t.Fatalf("rejected Sync changed state: L=%d water=%d", j.Last(), j.Water())
	}
	t.Logf("Sync(6300): 窗口 300..6300 共 101 个 -> ErrTooMany，L 与水位保持 240")
	id, fired, err := j.Sync(6240)
	if err != nil || !fired || id != 1 || j.Last() != 6240 {
		t.Fatalf("Sync(6240): id=%d fired=%v err=%v L=%d", id, fired, err, j.Last())
	}
	t.Logf("Sync(6240): 窗口 300..6240 共 100 个 -> 取最近一次 6240，新建任务 1")
}

func TestWindowBoundaries(t *testing.T) {
	j, _ := NewJob("0 * * * *", 60, -1, Allow)
	if _, fired, err := j.Sync(60); err != nil || fired || j.Last() != 60 {
		t.Fatalf("t==L should not count: fired=%v err=%v L=%d", fired, err, j.Last())
	}
	j2, _ := NewJob("0 * * * *", 0, -1, Allow)
	id, fired, _ := j2.Sync(60)
	if !fired || id != 1 || j2.Last() != 60 {
		t.Fatalf("t==now should count: id=%d fired=%v L=%d", id, fired, j2.Last())
	}
	in, _ := NewJob("0 * * * *", 0, 60, Allow)
	if _, fired, err := in.Sync(119); err != nil || !fired || in.Last() != 60 {
		t.Fatalf("t==now-D boundary: fired=%v err=%v L=%d", fired, err, in.Last())
	}
	edge, _ := NewJob("0 * * * *", 0, 59, Allow)
	if _, fired, err := edge.Sync(119); err != nil || !fired || edge.Last() != 60 {
		t.Fatalf("t==now-D exactly should count: fired=%v err=%v L=%d", fired, err, edge.Last())
	}
	out, _ := NewJob("0 * * * *", 0, 58, Allow)
	if _, fired, err := out.Sync(119); err != nil || fired || out.Last() != 0 {
		t.Fatalf("t<now-D must be excluded: fired=%v err=%v L=%d", fired, err, out.Last())
	}
}

func TestSuspend(t *testing.T) {
	j, _ := NewJob("0 * * * *", 0, -1, Allow)
	j.SetSuspend(true)
	if _, fired, err := j.Sync(300); err != nil || fired || j.Last() != 0 || j.Water() != 300 {
		t.Fatalf("suspended Sync: fired=%v err=%v L=%d water=%d", fired, err, j.Last(), j.Water())
	}
	t.Logf("suspend Sync(300): 无事发生，L=0，水位仍推进到 300")
	if _, _, err := j.Sync(200); err != ErrClockBack || j.Water() != 300 {
		t.Fatalf("suspended clockback err=%v water=%d", err, j.Water())
	}
	j.SetSuspend(false)
	id, fired, err := j.Sync(600)
	if err != nil || !fired || id != 1 || j.Last() != 600 {
		t.Fatalf("resume Sync(600): id=%d fired=%v err=%v L=%d", id, fired, err, j.Last())
	}
}

func TestReplacePolicy(t *testing.T) {
	j, _ := NewJob("0 * * * *", 0, -1, Replace)
	id1, _, _ := j.Sync(60)
	if id1 != 1 {
		t.Fatalf("first id=%d", id1)
	}
	id2, fired, err := j.Sync(180)
	if err != nil || !fired || id2 != 2 || j.Last() != 180 || j.Replaced() != 1 {
		t.Fatalf("replace: id=%d fired=%v err=%v L=%d replaced=%d", id2, fired, err, j.Last(), j.Replaced())
	}
	if ids := j.Active(); len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("active after replace = %v", ids)
	}
	t.Logf("Replace: 终止活动任务 1（Replaced=1），新建任务 2，L=180")
}

func TestNewJobAndFinish(t *testing.T) {
	if _, err := NewJob("bad spec", 0, -1, Allow); err != ErrInvalidArg {
		t.Fatalf("bad spec err=%v", err)
	}
	if _, err := NewJob("* * * * *", -1, -1, Allow); err != ErrInvalidArg {
		t.Fatalf("created=-1 err=%v", err)
	}
	if _, err := NewJob("* * * * *", maxMinute+1, -1, Allow); err != ErrInvalidArg {
		t.Fatalf("created=max+1 err=%v", err)
	}
	if _, err := NewJob("* * * * *", 0, -2, Allow); err != ErrInvalidArg {
		t.Fatalf("D=-2 err=%v", err)
	}
	if _, err := NewJob("* * * * *", 0, 1_000_000_001, Allow); err != ErrInvalidArg {
		t.Fatalf("D>1e9 err=%v", err)
	}
	if _, err := NewJob("* * * * *", 0, -1, Policy(0)); err != ErrInvalidArg {
		t.Fatalf("policy=0 err=%v", err)
	}
	if _, err := NewJob("* * * * *", 0, -1, Policy(4)); err != ErrInvalidArg {
		t.Fatalf("policy=4 err=%v", err)
	}
	j, _ := NewJob("0 * * * *", 0, -1, Allow)
	id, _, _ := j.Sync(60)
	if err := j.Finish(id+9, 60); err != ErrNoSuchTask {
		t.Fatalf("finish missing err=%v", err)
	}
	if err := j.Finish(id, -1); err != ErrIllegalTime {
		t.Fatalf("finish bad now err=%v", err)
	}
	if err := j.Finish(id, 120); err != nil || len(j.Active()) != 0 {
		t.Fatalf("finish err=%v active=%v", err, j.Active())
	}
}

func TestRejectedSyncKeepsState(t *testing.T) {
	j, _ := NewJob("0 * * * *", 0, 30, Forbid)
	_, _, _ = j.Sync(120)
	_, _, err := j.Sync(60)
	if err != ErrClockBack {
		t.Fatalf("clockback err=%v", err)
	}
	if j.Last() != 120 || j.Water() != 120 || len(j.Active()) != 1 || j.Skipped() != 0 {
		t.Fatalf("rejected Sync mutated state: L=%d water=%d active=%v skipped=%d",
			j.Last(), j.Water(), j.Active(), j.Skipped())
	}
	_, _, err = j.Sync(maxMinute + 1)
	if err != ErrIllegalTime || j.Water() != 120 {
		t.Fatalf("illegal time err=%v water=%d", err, j.Water())
	}
}

func TestNextFireCallBudget(t *testing.T) {
	j, _ := NewJob("0 * * * *", 240, -1, Allow)
	calls := 0
	old := nextFireHook
	nextFireHook = func(s *cronSpec, tm int64) (int64, error) {
		calls++
		return nextFireParsed(s, tm)
	}
	defer func() { nextFireHook = old }()
	if _, _, err := j.Sync(6300); err != ErrTooMany {
		t.Fatalf("err=%v", err)
	}
	if calls > 102 {
		t.Fatalf("NextFire calls = %d, budget 102", calls)
	}
	t.Logf("Sync(6300) 101 个触发场景下 NextFire 调用次数 = %d (<=102)", calls)
}

func TestReplayDeterminism(t *testing.T) {
	run := func() []string {
		j, _ := NewJob("0 */3 1,15 * 1-5", 1000, 5000, Replace)
		if j == nil {
			t.Fatal("NewJob returned nil")
		}
		var log []string
		ops := []int64{2000, 2000, 5000, 9000, 12000, 12001}
		for i, now := range ops {
			if i == 2 {
				j.SetSuspend(true)
			}
			if i == 3 {
				j.SetSuspend(false)
			}
			id, fired, err := j.Sync(now)
			log = append(log, fmt.Sprintf("Sync(%d)=(%d,%v,%v)L=%d", now, id, fired, err, j.Last()))
			if fired && i%2 == 0 {
				_ = j.Finish(id, now)
			}
		}
		log = append(log, fmt.Sprintf("active=%v skipped=%d replaced=%d",
			j.Active(), j.Skipped(), j.Replaced()))
		return log
	}
	a := run()
	b := run()
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Fatalf("replay differs:\n%v\n%v", a, b)
	}
}

func TestConcurrentAccess(t *testing.T) {
	j, _ := NewJob("0 * * * *", 0, 100000, Allow)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(base int64) {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				now := base + int64(k)*60
				id, fired, _ := j.Sync(now)
				if fired {
					_ = j.Finish(id, now)
				}
				_ = j.Last()
				_ = j.Active()
			}
		}(int64(g) * 20000)
	}
	wg.Wait()
}

func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20260101))
	minutes := []string{"*", "0", "0,30", "*/17", "5/20"}
	hours := []string{"*", "0", "*/6", "0,12"}
	days := []string{"*", "*/2", "*/5", "*/10", "1,15", "13", "1-31/7"}
	months := []string{"*", "*/3", "1,7"}
	weeks := []string{"*", "0", "1-5", "5", "*/2"}
	pick := func(opts []string) string { return opts[rng.Intn(len(opts))] }
	threeYears := int64(3 * 366 * 1440)
	for i := 0; i < 2000; i++ {
		spec := pick(minutes) + " " + pick(hours) + " " + pick(days) +
			" " + pick(months) + " " + pick(weeks)
		start := rng.Int63n(threeYears)
		got, gerr := NextFire(spec, start)
		want, ok := naiveNextFire(spec, start, 4000*1440)
		if !ok {
			t.Fatalf("case %d: naive found no match in 4000 days for %q after %s",
				i, spec, minuteLabel(start))
		}
		if gerr != nil || got != want {
			t.Fatalf("case %d: NextFire(%q, %s)=%s err=%v, naive=%s",
				i, spec, minuteLabel(start), minuteLabel(got), gerr, minuteLabel(want))
		}
		if lastDaySteps > dayStepMax {
			t.Fatalf("case %d: day steps %d > %d", i, lastDaySteps, dayStepMax)
		}
		if i < 8 || i%250 == 0 {
			t.Logf("case %d: spec=%q start=%s -> next=%s, daySteps=%d（判定：与逐分钟朴素扫描一致）",
				i, spec, minuteLabel(start), minuteLabel(got), lastDaySteps)
		}
	}
	t.Logf("随机对照 2000 组表达式/起点全部与朴素实现一致")
}

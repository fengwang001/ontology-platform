package mailthread

import (
	"errors"
	"reflect"
	"testing"
)

func mustAdd(t *testing.T, th *Threader, id string, refs []string, irt, subj string, ts int64) Result {
	t.Helper()
	r, err := th.Add(id, refs, irt, subj, ts)
	if err != nil {
		t.Fatalf("Add(%s) 意外失败: %v", id, err)
	}
	return r
}

func checkResult(t *testing.T, got Result, thread string, path Path, gone []string) {
	t.Helper()
	if got.Thread != thread || got.Path != path || !reflect.DeepEqual(got.Gone, gone) {
		t.Fatalf("结果不符: got={Thread:%s Path:%s Gone:%v}, want={%s %s %v}",
			got.Thread, got.Path, got.Gone, thread, path, gone)
	}
}

func mustThreadOf(t *testing.T, th *Threader, id string) string {
	t.Helper()
	tid, err := th.ThreadOf(id)
	if err != nil {
		t.Fatalf("ThreadOf(%s) 意外失败: %v", id, err)
	}
	return tid
}

func mustMembers(t *testing.T, th *Threader, tid string) []Member {
	t.Helper()
	ms, err := th.Members(tid)
	if err != nil {
		t.Fatalf("Members(%s) 意外失败: %v", tid, err)
	}
	return ms
}

// 规范中的完整示例（W=100，a..f）。
func TestSpecExample(t *testing.T) {
	th, err := New(100, 100)
	if err != nil {
		t.Fatal(err)
	}
	checkResult(t, mustAdd(t, th, "a", nil, "", "Hello", 10), "a", PathNew, nil)
	checkResult(t, mustAdd(t, th, "b", nil, "a", "Re: Hello", 20), "a", PathRef, nil)
	checkResult(t, mustAdd(t, th, "c", []string{"x", "y"}, "", "Re: Hello", 30), "c", PathRef, nil)
	checkResult(t, mustAdd(t, th, "d", nil, "", "Re: Re: Hello", 40), "c", PathSubject, nil)
	checkResult(t, mustAdd(t, th, "e", []string{"b", "c"}, "", "Fwd: Hello", 50), "a", PathRef, []string{"c"})
	checkResult(t, mustAdd(t, th, "f", []string{"a"}, "", "Hello", 5), "f", PathRef, []string{"a"})

	want := []Member{
		{ID: "f", TS: 5}, {ID: "a", TS: 10}, {ID: "b", TS: 20},
		{ID: "c", TS: 30}, {ID: "d", TS: 40}, {ID: "e", TS: 50},
	}
	if got := mustMembers(t, th, "f"); !reflect.DeepEqual(got, want) {
		t.Fatalf("Members(f)=%v, want %v", got, want)
	}
	if got := mustThreadOf(t, th, "x"); got != "f" {
		t.Fatalf("ThreadOf(x)=%s, want f", got)
	}
	if got := th.Threads(); !reflect.DeepEqual(got, []string{"f"}) {
		t.Fatalf("Threads()=%v, want [f]", got)
	}
}

// 三种途径各一例。
func TestThreePaths(t *testing.T) {
	th, _ := New(10, 100)
	checkResult(t, mustAdd(t, th, "m1", nil, "", "Topic", 1), "m1", PathNew, nil)
	checkResult(t, mustAdd(t, th, "m2", nil, "m1", "Re: Topic", 2), "m1", PathRef, nil)
	checkResult(t, mustAdd(t, th, "m3", nil, "", "Re: Topic", 3), "m1", PathSubject, nil)
}

// 占位被两封邮件引用而连通。
func TestPlaceholderConnects(t *testing.T) {
	th, _ := New(10, 100)
	mustAdd(t, th, "m1", []string{"p"}, "", "A", 1)
	mustAdd(t, th, "m2", []string{"p"}, "", "B", 2)
	if mustThreadOf(t, th, "m1") != mustThreadOf(t, th, "m2") {
		t.Fatal("m1 与 m2 应经占位 p 连通")
	}
	if mustThreadOf(t, th, "p") != mustThreadOf(t, th, "m1") {
		t.Fatal("占位 p 应与 m1 同线程")
	}
}

// 占位后补登记；L 为空但 id 此前是占位时不走主题回退。
func TestPlaceholderRegisteredLater(t *testing.T) {
	th, _ := New(100, 100)
	mustAdd(t, th, "base", nil, "", "Hello", 10)
	mustAdd(t, th, "m1", []string{"p"}, "", "Other", 20)
	// p 是占位，即使主题是回复主题且存在同主题线程 base，也走 ref 而非 subject。
	r := mustAdd(t, th, "p", nil, "", "Re: Hello", 30)
	checkResult(t, r, "m1", PathRef, nil)
	if mustThreadOf(t, th, "p") == "base" {
		t.Fatal("占位补登记不应走主题回退")
	}
	if got := th.Threads(); !reflect.DeepEqual(got, []string{"base", "m1"}) {
		t.Fatalf("Threads()=%v", got)
	}
}

// 占位补登记成为更早的根：线程编号更换，Gone 含旧编号。
func TestPlaceholderBecomesRoot(t *testing.T) {
	th, _ := New(100, 100)
	mustAdd(t, th, "m1", []string{"p"}, "", "A", 10)
	r := mustAdd(t, th, "p", nil, "", "A", 5)
	checkResult(t, r, "p", PathRef, []string{"m1"})
	if got := th.Threads(); !reflect.DeepEqual(got, []string{"p"}) {
		t.Fatalf("Threads()=%v", got)
	}
}

// W 取等通过、差 1 拒绝。
func TestWindowBoundary(t *testing.T) {
	th, _ := New(100, 100)
	mustAdd(t, th, "m1", nil, "", "Hello", 10)
	// ts - maxTS = 100 == W，恰等通过。
	checkResult(t, mustAdd(t, th, "m2", nil, "", "Re: Hello", 110), "m1", PathSubject, nil)

	th2, _ := New(100, 100)
	mustAdd(t, th2, "m1", nil, "", "Hello", 10)
	// ts - maxTS = 101 > W，差 1 拒绝，走 new。
	checkResult(t, mustAdd(t, th2, "m2", nil, "", "Re: Hello", 111), "m2", PathNew, nil)
}

// 线程内最小 ts 恰等于本邮件 ts：满足 minTS <= ts。
func TestMinTSEqual(t *testing.T) {
	th, _ := New(100, 100)
	mustAdd(t, th, "m1", nil, "", "Hello", 10)
	mustAdd(t, th, "m2", nil, "m1", "Re: Hello", 20)
	// 线程 minTS=10，本邮件 ts=10，恰等通过；maxTS=20，差为负。
	checkResult(t, mustAdd(t, th, "m3", nil, "", "Re: Hello", 10), "m1", PathSubject, nil)
}

// 线程内最大 ts 大于本邮件 ts：差为负，满足窗口条件。
func TestMaxTSGreaterThanMail(t *testing.T) {
	th, _ := New(10, 100)
	mustAdd(t, th, "m1", nil, "", "Hello", 10)
	mustAdd(t, th, "m2", nil, "m1", "Re: Hello", 100)
	// 线程 minTS=10 <= ts=50 < maxTS=100，差 -50 <= W。
	checkResult(t, mustAdd(t, th, "m3", nil, "", "Re: Hello", 50), "m1", PathSubject, nil)
}

// 最小 ts 大于本邮件 ts：不满足 minTS <= ts，走 new。
func TestMinTSGreaterThanMail(t *testing.T) {
	th, _ := New(1000, 100)
	mustAdd(t, th, "m1", nil, "", "Hello", 100)
	checkResult(t, mustAdd(t, th, "m2", nil, "", "Re: Hello", 50), "m2", PathNew, nil)
}

// 前缀变体：RE:、Fwd:、回复：叠加。
func TestPrefixVariants(t *testing.T) {
	cases := []struct {
		subj    string
		norm    string
		isReply bool
	}{
		{"Hello", "Hello", false},
		{"RE: Hello", "Hello", true},
		{"Fwd: Hello", "Hello", true},
		{"fw:Hello", "Hello", true},
		{"回复：Hello", "Hello", true},
		{"回复: Hello", "Hello", true},
		{"转发:Hello", "Hello", true},
		{"回复：回复： Hello", "Hello", true},
		{"Re: Fwd: 回复：Hello", "Hello", true},
		{"  Re:   Hello  ", "Hello", true},
		{"re : Hello", "re : Hello", false}, // 前缀与冒号间有空白，不是前缀
		{"Re:", "", false},                  // 结果为空，不是回复主题
		{"Re:  ", "", false},
		{"Re: re: x", "x", true},
		{"Hello  World", "Hello  World", false}, // 中间空白原样保留
		{"re: RE:", "", false},
	}
	for _, c := range cases {
		norm, isReply := normalizeSubject(c.subj)
		if norm != c.norm || isReply != c.isReply {
			t.Errorf("normalizeSubject(%q)=(%q,%v), want (%q,%v)",
				c.subj, norm, isReply, c.norm, c.isReply)
		}
	}
}

// 前缀变体叠加后与同主题线程归并。
func TestPrefixVariantsMerge(t *testing.T) {
	th, _ := New(100, 100)
	mustAdd(t, th, "m1", nil, "", "Hello", 10)
	checkResult(t, mustAdd(t, th, "m2", nil, "", "RE: Hello", 20), "m1", PathSubject, nil)
	checkResult(t, mustAdd(t, th, "m3", nil, "", "Fwd: Re: Hello", 30), "m1", PathSubject, nil)
	checkResult(t, mustAdd(t, th, "m4", nil, "", "回复：转发: Hello", 40), "m1", PathSubject, nil)
}

// 非回复主题不回退。
func TestNonReplyNoFallback(t *testing.T) {
	th, _ := New(100, 100)
	mustAdd(t, th, "m1", nil, "", "Hello", 10)
	checkResult(t, mustAdd(t, th, "m2", nil, "", "Hello", 20), "m2", PathNew, nil)
	// 规范化结果大小写不同也不匹配。
	checkResult(t, mustAdd(t, th, "m3", nil, "", "Re: hello", 30), "m3", PathNew, nil)
}

// 候选并列：最大 ts 相同，取线程编号字节序小者。
func TestCandidateTie(t *testing.T) {
	th, _ := New(100, 100)
	mustAdd(t, th, "b1", nil, "", "Hello", 10)
	mustAdd(t, th, "a1", nil, "", "Hello", 10)
	// 两线程 maxTS 均为 10，并列取编号小者 a1。
	checkResult(t, mustAdd(t, th, "m", nil, "", "Re: Hello", 20), "a1", PathSubject, nil)
}

// 多个候选取线程最大 ts 最大者。
func TestCandidateMaxTS(t *testing.T) {
	th, _ := New(100, 100)
	mustAdd(t, th, "t1", nil, "", "Hello", 10)
	mustAdd(t, th, "t2", nil, "", "Hello", 30)
	checkResult(t, mustAdd(t, th, "m", nil, "", "Re: Hello", 40), "t2", PathSubject, nil)
}

// 容量恰等通过、超 1 拒绝；占位也占名额。
func TestCapacity(t *testing.T) {
	th, _ := New(100, 3)
	mustAdd(t, th, "m1", []string{"p1"}, "", "A", 1) // 2 节点
	// 新增 1 节点（m2），总数 3 == N，恰等通过。
	checkResult(t, mustAdd(t, th, "m2", nil, "", "A", 2), "m2", PathNew, nil)
	// 新增 1 节点（m3），总数 4 > 3，拒绝。
	if _, err := th.Add("m3", nil, "", "A", 3); !errors.Is(err, ErrCapacity) {
		t.Fatalf("want ErrCapacity, got %v", err)
	}
	// 引用已存在节点不占新名额：m4 引用 m1、p1，新增仅 m4 自身，4 > 3 仍拒绝。
	if _, err := th.Add("m4", []string{"m1", "p1"}, "", "A", 4); !errors.Is(err, ErrCapacity) {
		t.Fatalf("want ErrCapacity, got %v", err)
	}

	// 超 1：新增 2 节点但只剩 1 名额。
	th2, _ := New(100, 3)
	mustAdd(t, th2, "m1", []string{"p1"}, "", "A", 1) // 2 节点
	if _, err := th2.Add("m2", []string{"p2"}, "", "A", 2); !errors.Is(err, ErrCapacity) {
		t.Fatalf("want ErrCapacity, got %v", err)
	}
}

// 拒绝顺序：参数非法 > 重复登记 > 容量不足，只报第一个。
func TestRejectOrder(t *testing.T) {
	if _, err := New(-1, 10); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("构造 W=-1: want ErrInvalidParam, got %v", err)
	}
	if _, err := New(0, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("构造 N=0: want ErrInvalidParam, got %v", err)
	}
	if _, err := New(MaxWindow+1, 10); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("构造 W 越界: want ErrInvalidParam, got %v", err)
	}
	if _, err := New(0, MaxNodes+1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("构造 N 越界: want ErrInvalidParam, got %v", err)
	}

	th, _ := New(100, 2)
	mustAdd(t, th, "m1", nil, "", "A", 1)

	// 参数非法优先于重复与容量。
	if _, err := th.Add("", nil, "", "A", 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("空 id: want ErrInvalidParam, got %v", err)
	}
	if _, err := th.Add("m1", nil, "", "A", MaxTS+1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("ts 越界且重复: want ErrInvalidParam, got %v", err)
	}
	if _, err := th.Add("m1", make([]string, 51), "", "A", 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("refs 超 50: want ErrInvalidParam, got %v", err)
	}
	if _, err := th.Add("m1", []string{""}, "", "A", 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("refs 含空串: want ErrInvalidParam, got %v", err)
	}
	if _, err := th.Add("m1", nil, "m1", "A", 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("inReplyTo 含自身: want ErrInvalidParam, got %v", err)
	}
	if _, err := th.Add("m1", []string{"m1"}, "", "A", 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("refs 含自身: want ErrInvalidParam, got %v", err)
	}
	// 重复优先于容量（容量此时也不足：新增 m2 需 1 名额，现占 1/2……
	// 构造恰满场景验证重复优先）。
	th2, _ := New(100, 1)
	mustAdd(t, th2, "m1", nil, "", "A", 1)
	if _, err := th2.Add("m1", nil, "", "A", 2); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("重复且容量满: want ErrDuplicate, got %v", err)
	}
	if _, err := th2.Add("m2", nil, "", "A", 2); !errors.Is(err, ErrCapacity) {
		t.Fatalf("容量满: want ErrCapacity, got %v", err)
	}
}

// 被拒不改状态。
func TestRejectedKeepsState(t *testing.T) {
	th, _ := New(100, 3)
	mustAdd(t, th, "m1", []string{"p1"}, "", "Hello", 10)
	before := th.Threads()
	beforeMembers := mustMembers(t, th, "m1")

	rejects := []error{
		mustErr(t, th, "", nil, "", "A", 1),
		mustErr(t, th, "x", nil, "", "A", MaxTS+1),
		mustErr(t, th, "m1", nil, "", "A", 20),
		mustErr(t, th, "m2", []string{"q1", "q2"}, "", "A", 30), // 容量不足
	}
	for i, err := range rejects {
		if err == nil {
			t.Fatalf("第 %d 个操作应被拒绝", i)
		}
	}
	if got := th.Threads(); !reflect.DeepEqual(got, before) {
		t.Fatalf("Threads 改变: %v -> %v", before, got)
	}
	if got := mustMembers(t, th, "m1"); !reflect.DeepEqual(got, beforeMembers) {
		t.Fatalf("Members 改变: %v -> %v", beforeMembers, got)
	}
	if got := mustThreadOf(t, th, "p1"); got != "m1" {
		t.Fatalf("占位线程改变: %s", got)
	}
	if _, err := th.ThreadOf("q1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("被拒操作不应创建节点: %v", err)
	}
}

func mustErr(t *testing.T, th *Threader, id string, refs []string, irt, subj string, ts int64) error {
	t.Helper()
	_, err := th.Add(id, refs, irt, subj, ts)
	return err
}

// 查询不存在。
func TestNotFound(t *testing.T) {
	th, _ := New(100, 10)
	mustAdd(t, th, "m1", nil, "", "A", 1)
	if _, err := th.ThreadOf("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ThreadOf: want ErrNotFound, got %v", err)
	}
	if _, err := th.Members("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Members: want ErrNotFound, got %v", err)
	}
}

// refs 重复项视为一项，且与 inReplyTo 去重。
func TestRefsDedup(t *testing.T) {
	th, _ := New(100, 3)
	// p 重复 3 次、inReplyTo 也是 p：只新增 m1 与 p 两个节点。
	mustAdd(t, th, "m1", []string{"p", "p", "p"}, "p", "A", 1)
	if got := len(th.nodes); got != 2 {
		t.Fatalf("节点数=%d, want 2", got)
	}
}

// 相同登记序列重放得到完全相同的线程、编号与 Gone。
func TestReplayDeterministic(t *testing.T) {
	type op struct {
		id   string
		refs []string
		irt  string
		subj string
		ts   int64
	}
	ops := []op{
		{"a", nil, "", "Hello", 10},
		{"b", nil, "a", "Re: Hello", 20},
		{"c", []string{"x", "y"}, "", "Re: Hello", 30},
		{"d", nil, "", "Re: Re: Hello", 40},
		{"e", []string{"b", "c"}, "", "Fwd: Hello", 50},
		{"f", []string{"a"}, "", "Hello", 5},
	}
	run := func() ([]Result, []string) {
		th, _ := New(100, 100)
		var rs []Result
		for _, o := range ops {
			r, err := th.Add(o.id, o.refs, o.irt, o.subj, o.ts)
			if err != nil {
				t.Fatal(err)
			}
			rs = append(rs, r)
		}
		return rs, th.Threads()
	}
	rs1, ts1 := run()
	rs2, ts2 := run()
	if !reflect.DeepEqual(rs1, rs2) || !reflect.DeepEqual(ts1, ts2) {
		t.Fatalf("重放不一致: %v/%v vs %v/%v", rs1, ts1, rs2, ts2)
	}
}

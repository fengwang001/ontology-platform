package ontology

import (
	"reflect"
	"testing"
)

func mustAdd(t *testing.T, m *Merger, id string, refs []string, irt, subj string, ts int64, want AddResult) {
	t.Helper()
	got, err := m.Add(id, refs, irt, subj, ts)
	if err != nil {
		t.Fatalf("Add(%s) error = %v", id, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Add(%s, refs=%v, irt=%q, subj=%q, ts=%d):\n got  = %+v\n want = %+v",
			id, refs, irt, subj, ts, got, want)
	}
	t.Logf("判定依据: Add id=%s refs=%v irt=%q subject=%q ts=%d -> 线程=%s 途径=%s Gone=%v",
		id, refs, irt, subj, ts, got.ThreadID, got.Way, got.Gone)
}

// TestSpecExample 复刻题目给出的完整示例。
func TestSpecExample(t *testing.T) {
	m, err := New(100, 1000)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, m, "a", nil, "", "Hello", 10, AddResult{"a", "new", []string{}})
	mustAdd(t, m, "b", nil, "a", "Re: Hello", 20, AddResult{"a", "ref", []string{}})
	mustAdd(t, m, "c", []string{"x", "y"}, "", "Re: Hello", 30, AddResult{"c", "ref", []string{}})
	// 候选有线程 a（maxTS 20，差 20）与线程 c（maxTS 30，差 10），取 c。
	mustAdd(t, m, "d", nil, "", "Re: Re: Hello", 40, AddResult{"c", "subject", []string{}})
	// 桥接邮件把线程 a 与 c 合并，根为 a。
	mustAdd(t, m, "e", []string{"b", "c"}, "", "Fwd: Hello", 50, AddResult{"a", "ref", []string{"c"}})
	// 更早邮件到达并入线程 a，f 成为新根。
	mustAdd(t, m, "f", []string{"a"}, "", "Hello", 5, AddResult{"f", "ref", []string{"a"}})

	members, err := m.Members("f")
	if err != nil {
		t.Fatal(err)
	}
	wantMembers := []string{"f", "a", "b", "c", "d", "e"}
	if !reflect.DeepEqual(members, wantMembers) {
		t.Fatalf("Members(f) = %v, want %v", members, wantMembers)
	}
	th, err := m.ThreadOf("x")
	if err != nil || th != "f" {
		t.Fatalf("ThreadOf(x) = %q, %v; want f", th, err)
	}
	if ths := m.Threads(); !reflect.DeepEqual(ths, []string{"f"}) {
		t.Fatalf("Threads() = %v", ths)
	}
}

// TestPlaceholderConnects 占位被两封邮件引用而连通。
func TestPlaceholderConnects(t *testing.T) {
	m, _ := New(100, 100)
	mustAdd(t, m, "a", []string{"p"}, "", "Topic", 10, AddResult{"a", "ref", []string{}})
	mustAdd(t, m, "b", []string{"p"}, "", "Other", 20, AddResult{"a", "ref", []string{}})
	th, _ := m.ThreadOf("p")
	if th != "a" {
		t.Fatalf("placeholder p thread = %q, want a", th)
	}
}

// TestPlaceholderBackfill 占位后补登记（L 空、id 此前占位 -> ref，不走主题回退）。
func TestPlaceholderBackfill(t *testing.T) {
	m, _ := New(100, 100)
	mustAdd(t, m, "a", []string{"p"}, "", "Hello", 10, AddResult{"a", "ref", []string{}})
	// 即使主题是回复主题且存在其他同主题候选，id 是占位也必须走 ref。
	mustAdd(t, m, "z", nil, "", "Hello", 5, AddResult{"z", "new", []string{}})
	mustAdd(t, m, "p", nil, "", "Re: Hello", 50, AddResult{"a", "ref", []string{}})
	if _, err := m.Members("z"); err != nil {
		t.Fatalf("z should remain its own thread: %v", err)
	}
	members, _ := m.Members("a")
	if !reflect.DeepEqual(members, []string{"a", "p"}) {
		t.Fatalf("members = %v", members)
	}
}

// TestWindowBoundary W 取等通过、差 1 失败；minTS 恰等于本邮件 ts；maxTS 更大时差为负。
func TestWindowBoundary(t *testing.T) {
	// 取等：maxTS=0, ts=100, W=100 -> 差恰为 W，通过。
	m, _ := New(100, 100)
	mustAdd(t, m, "a", nil, "", "Hello", 0, AddResult{"a", "new", []string{}})
	mustAdd(t, m, "b", nil, "", "Re: Hello", 100, AddResult{"a", "subject", []string{}})

	// 差 1：W=99 时差 100 不通过 -> new。
	m2, _ := New(99, 100)
	mustAdd(t, m2, "a", nil, "", "Hello", 0, AddResult{"a", "new", []string{}})
	mustAdd(t, m2, "b", nil, "", "Re: Hello", 100, AddResult{"b", "new", []string{}})

	// minTS 恰等于本邮件 ts：条件 minTS <= ts 取等满足。
	m3, _ := New(100, 100)
	mustAdd(t, m3, "late", nil, "", "Hello", 100, AddResult{"late", "new", []string{}})
	mustAdd(t, m3, "reply", nil, "", "Re: Hello", 100, AddResult{"late", "subject", []string{}})

	// maxTS 大于本邮件 ts：ts-maxTS 为负，窗口条件满足。
	m4, _ := New(100, 100)
	mustAdd(t, m4, "fut", nil, "", "Hello", 1000, AddResult{"fut", "new", []string{}})
	// 经引用加入 ts=900 的邮件，线程 minTS=900、maxTS=1000。
	mustAdd(t, m4, "mid", []string{"fut"}, "", "Hello", 900, AddResult{"mid", "ref", []string{"fut"}})
	// 本邮件 ts=950：minTS 900 <= 950，maxTS 1000 > 950（差 -50，满足）。
	mustAdd(t, m4, "past", nil, "", "Re: Hello", 950, AddResult{"mid", "subject", []string{}})
}

// TestSubjectPrefixes 前缀变体：RE:、Fwd:、回复：叠加。
func TestSubjectPrefixes(t *testing.T) {
	cases := []struct {
		in    string
		norm  string
		reply bool
	}{
		{"RE: Hello", "Hello", true},
		{"Fwd: Hello", "Hello", true},
		{"FW:  Hello", "Hello", true},
		{"回复：  你好", "你好", true},
		{"Re: Re: 回复: 你好", "你好", true},
		{"re : x", "re : x", false}, // 前缀与冒号之间有空白，不算前缀
		{"Hello", "Hello", false},
		{"Re: ", "", false}, // 去掉后为空 -> 不是回复主题
		{"  re:   ", "", false},
	}
	for _, c := range cases {
		got, stripped := normalizeSubject(c.in)
		reply := stripped && got != ""
		if got != c.norm || reply != c.reply {
			t.Fatalf("normalizeSubject(%q) = (%q, reply=%v), want (%q, reply=%v)",
				c.in, got, reply, c.norm, c.reply)
		}
	}
}

// TestNonReplyNoFallback 非回复主题绝不走主题回退，即使时间窗内有同主题线程。
func TestNonReplyNoFallback(t *testing.T) {
	m, _ := New(1000, 100)
	mustAdd(t, m, "a", nil, "", "Hello", 10, AddResult{"a", "new", []string{}})
	mustAdd(t, m, "b", nil, "", "Hello", 20, AddResult{"b", "new", []string{}})
}

// TestCandidateTie 候选并列取线程编号字节序小者；否则取 maxTS 最大者。
func TestCandidateTie(t *testing.T) {
	m, _ := New(1000, 100)
	mustAdd(t, m, "t1", nil, "", "Hello", 0, AddResult{"t1", "new", []string{}})
	mustAdd(t, m, "t2", nil, "", "Hello", 0, AddResult{"t2", "new", []string{}})
	// 两候选 maxTS 均为 0，并列 -> 编号字节序小者 t1。
	mustAdd(t, m, "zz", nil, "", "Re: Hello", 0, AddResult{"t1", "subject", []string{}})

	// maxTS 不同取 maxTS 大的，即使编号字节序更大。
	m2, _ := New(1000, 100)
	mustAdd(t, m2, "aaa", nil, "", "Hello", 0, AddResult{"aaa", "new", []string{}})
	mustAdd(t, m2, "zzz", nil, "", "Hello", 5, AddResult{"zzz", "new", []string{}})
	mustAdd(t, m2, "rrr", nil, "", "Re: Hello", 10, AddResult{"zzz", "subject", []string{}})
}

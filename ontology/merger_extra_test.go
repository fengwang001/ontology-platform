package ontology

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

// TestCapacity 容量恰等通过、超 1 拒绝，被拒不改状态。
func TestCapacity(t *testing.T) {
	m, _ := New(100, 4)
	// a + 两个占位 p、q = 3 <= 4，通过。
	mustAdd(t, m, "a", []string{"p", "q"}, "", "s", 0, AddResult{"a", "ref", []string{}})
	// 再引入两个新节点 b、r -> 5 > 4，容量不足。
	if _, err := m.Add("b", []string{"r"}, "", "Re: s", 10); !errors.Is(err, ErrCapacity) {
		t.Fatalf("want ErrCapacity, got %v", err)
	}
	// 被拒不改状态：b、r 均不存在。
	if _, err := m.ThreadOf("b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("b should not exist, got %v", err)
	}
	if _, err := m.ThreadOf("r"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("r should not exist, got %v", err)
	}
	// 只新增 b 一个节点 -> 4 == 4 恰等通过（已存在的 p 不重复占名额）。
	mustAdd(t, m, "b", []string{"p"}, "", "Re: s", 10, AddResult{"a", "ref", []string{}})
}

// TestRejectedNoStateChange 各类拒绝均不改变任何节点与线程。
func TestRejectedNoStateChange(t *testing.T) {
	m, _ := New(100, 50)
	mustAdd(t, m, "a", nil, "", "Hello", 10, AddResult{"a", "new", []string{}})

	if _, err := m.Add("", nil, "", "x", 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty id: %v", err)
	}
	if _, err := m.Add("b", nil, "", "x", -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ts<0: %v", err)
	}
	if _, err := m.Add("b", nil, "", "x", 1_000_000_000_001); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ts too big: %v", err)
	}
	big := make([]string, 51)
	for i := range big {
		big[i] = "x"
	}
	if _, err := m.Add("b", big, "", "x", 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("refs>50: %v", err)
	}
	if _, err := m.Add("b", []string{""}, "", "x", 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty ref: %v", err)
	}
	if _, err := m.Add("b", []string{"b"}, "", "x", 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("self in refs: %v", err)
	}
	if _, err := m.Add("b", nil, "b", "x", 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("self inReplyTo: %v", err)
	}
	if _, err := m.Add("a", nil, "", "Hello", 10); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}

	if got := m.Threads(); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("state changed after rejections: %v", got)
	}

	// 构造参数越界。
	if _, err := New(-1, 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("w<0: %v", err)
	}
	if _, err := New(1_000_000_001, 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("w too big: %v", err)
	}
	if _, err := New(0, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("n<1: %v", err)
	}
	if _, err := New(0, 100_001); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("n too big: %v", err)
	}

	// 查询不存在。
	if _, err := m.ThreadOf("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ThreadOf: %v", err)
	}
	if _, err := m.Members("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Members: %v", err)
	}
}

// TestRootChangeGone 更早邮件经引用并入时换根，Gone 含旧编号；
// 更早邮件经主题回退并入时同样换根。
func TestRootChangeGone(t *testing.T) {
	m, _ := New(1000, 100)
	mustAdd(t, m, "b", nil, "", "Hello", 20, AddResult{"b", "new", []string{}})
	mustAdd(t, m, "c", nil, "", "Re: Hello", 30, AddResult{"b", "subject", []string{}})
	// 更早邮件 a 经引用并入，换根 a，旧编号 b 进入 Gone。
	mustAdd(t, m, "a", []string{"c"}, "", "Hi", 10, AddResult{"a", "ref", []string{"b"}})

	// 主题回退导致换根：候选要求 minTS <= ts，而根即 minTS 邮件，
	// 故只有 ts 相同且新 id 字节序更小时才会换根。
	m2, _ := New(1000, 100)
	mustAdd(t, m2, "b", nil, "", "Hello", 20, AddResult{"b", "new", []string{}})
	mustAdd(t, m2, "a", nil, "", "Re: Hello", 20, AddResult{"a", "subject", []string{"b"}})
	th, _ := m2.ThreadOf("b")
	if th != "a" {
		t.Fatalf("expected root a after earlier subject join, got %q", th)
	}
	if ths := m2.Threads(); !reflect.DeepEqual(ths, []string{"a"}) {
		t.Fatalf("threads = %v", ths)
	}
}

// TestBridgeMergeGone 桥接邮件一次合并多个线程，Gone 含全部消失的旧编号。
func TestBridgeMergeGone(t *testing.T) {
	m, _ := New(1000, 100)
	mustAdd(t, m, "a", nil, "", "A", 1, AddResult{"a", "new", []string{}})
	mustAdd(t, m, "b", nil, "", "B", 2, AddResult{"b", "new", []string{}})
	mustAdd(t, m, "c", nil, "", "C", 3, AddResult{"c", "new", []string{}})
	// 桥接邮件引用 b、c，三线程合一，根 a（ts 最小）。
	mustAdd(t, m, "x", []string{"a", "b", "c"}, "", "X", 9, AddResult{"a", "ref", []string{"b", "c"}})
}

// TestRefsDedup refs 重复项视为一项（占名额时只算一次）。
func TestRefsDedup(t *testing.T) {
	m, _ := New(100, 2)
	// id=a 加一个唯一占位 p（重复列出）：共 2 个节点，恰等。
	mustAdd(t, m, "a", []string{"p", "p", "p"}, "", "s", 1, AddResult{"a", "ref", []string{}})
	members, err := m.Members("a")
	if err != nil || !reflect.DeepEqual(members, []string{"a"}) {
		t.Fatalf("members = %v, err=%v", members, err)
	}
	if th, err := m.ThreadOf("p"); err != nil || th != "a" {
		t.Fatalf("ThreadOf(p) = %q, %v", th, err)
	}
}

// TestConcurrent 并发调用结果等价于某个串行顺序（-race 下运行）。
func TestConcurrent(t *testing.T) {
	m, _ := New(1000, 500)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				id := string(rune('a'+g)) + "-" + itoa(i)
				refs := []string(nil)
				if i > 0 {
					refs = []string{string(rune('a'+g)) + "-" + itoa(i-1)}
				}
				if _, err := m.Add(id, refs, "", "Hello", int64(g*100+i)); err != nil {
					t.Errorf("Add: %v", err)
					return
				}
				_ = m.Threads()
				if _, err := m.ThreadOf(id); err != nil {
					t.Errorf("ThreadOf: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	// 每个 goroutine 形成一个链，共 8 个线程；不变量抽查。
	if len(m.Threads()) != 8 {
		t.Fatalf("threads = %d, want 8", len(m.Threads()))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

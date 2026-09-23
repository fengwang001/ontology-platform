package headers

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"ontology/policy"
)

func testRegistry() *policy.Registry {
	reg := policy.NewRegistry(policy.Policy{Dup: policy.DupFirst})
	reg.Register(policy.Policy{List: true, Dup: policy.DupMerge}, "Accept", "X-Multi")
	reg.Register(policy.Policy{Dup: policy.DupLast}, "X-Last")
	reg.Register(policy.Policy{Dup: policy.DupError}, "X-Once")
	return reg
}

// TestOrderAndDupPolicies 同名保序不去重；四种单值策略都可测。
func TestOrderAndDupPolicies(t *testing.T) {
	in := "B: 1\r\nX-Multi: a, b\r\nX-Multi: c\r\nX-Last: 1\r\nX-Last: 2\r\n" +
		"X-First: 1\r\nX-First: 2\r\nX-Once: 1\r\nX-Once: 2\r\nB: 2\r\n\r\n"
	s, err := Parse([]byte(in), testRegistry(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.GetAll("x-multi"); strings.Join(got, ",") != "a, b,c" {
		t.Errorf("X-Multi 保序: %v", got)
	}
	if got := s.GetAll("B"); strings.Join(got, ",") != "1,2" {
		t.Errorf("B 保序: %v", got)
	}
	cases := []struct {
		name, want string
		err        error
	}{
		{"X-Multi", "a, b, c", nil}, // 合并
		{"X-Last", "2", nil},        // 取末
		{"X-First", "1", nil},       // 取首（默认）
		{"X-Once", "", policy.ErrDuplicate},
	}
	for _, c := range cases {
		got, err := s.Get(c.name)
		if !errors.Is(err, c.err) || got != c.want {
			t.Errorf("Get(%q) = %q, %v; want %q, %v", c.name, got, err, c.want, c.err)
		}
	}
}

// TestQueries 只读查询：两次一致、不推进状态、不存在与空值可区分。
func TestQueries(t *testing.T) {
	s, err := Parse([]byte("A: 1\r\nA: 2\r\nEmpty:\r\n\r\n"), nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	type snapshot struct {
		len, countA, countMissing, bytes int
		norm, hasA, hasMissing, hasEmpty bool
	}
	read := func() snapshot {
		return snapshot{s.Len(), s.Count("A"), s.Count("Nope"), s.ByteLen(),
			s.Normalized(), s.Has("A"), s.Has("Nope"), s.Has("Empty")}
	}
	if r1, r2 := read(), read(); r1 != r2 {
		t.Errorf("连查两次结果不同: %+v vs %+v", r1, r2)
	}
	r := read()
	if r.len != 3 || r.countA != 2 || r.countMissing != 0 {
		t.Errorf("计数错误: %+v", r)
	}
	if r.hasMissing || !r.hasA || !r.hasEmpty {
		t.Errorf("存在性判定错误: %+v", r)
	}
	if got := s.GetAll("Nope"); got != nil {
		t.Errorf("不存在应返回 nil: %v", got)
	}
	if got := s.GetAll("Empty"); len(got) != 1 || got[0] != "" {
		t.Errorf("空值应返回 [\"\"]: %v", got)
	}
	if v, _ := s.Get("Nope"); v != "" {
		t.Errorf("不存在单值应为零值: %q", v)
	}
}

// TestSetAddDel 增删改语义与索引重建后的保序。
func TestSetAddDel(t *testing.T) {
	s := New(nil, Config{})
	_ = s.Add("A", "1")
	_ = s.Add("B", "2")
	_ = s.Add("A", "3")
	if err := s.Set("A", "9"); err != nil {
		t.Fatal(err)
	}
	if got := s.GetAll("A"); strings.Join(got, ",") != "9" {
		t.Errorf("Set 应替换全部同名: %v", got)
	}
	if n := s.Del("B"); n != 1 || s.Has("B") {
		t.Errorf("Del 删除数 %d 或残留", n)
	}
	_ = s.Add("A", "10")
	if got := s.GetAll("a"); strings.Join(got, ",") != "9,10" {
		t.Errorf("索引重建后保序破坏: %v", got)
	}
	if n := s.Del("missing"); n != 0 {
		t.Errorf("删除不存在应返回 0: %d", n)
	}
}

// TestFoldRoundTrip 配置折行宽度后，回写再解析值逐字节相同。
func TestFoldRoundTrip(t *testing.T) {
	values := []string{
		"alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu",
		"nospaceatalljustoneverylongtokenthatcannotbefoldedanywhereatall",
		"mixed words and averyveryverylongtokenwithoutanyspacesinitatall end",
	}
	for _, v := range values {
		s := New(nil, Config{FoldWidth: 40})
		if err := s.Set("X-Long", v); err != nil {
			t.Fatal(err)
		}
		back, err := Parse(s.Marshal(), nil, Config{})
		if err != nil {
			t.Fatalf("重解析失败: %v", err)
		}
		if got, _ := back.Get("X-Long"); got != v {
			t.Errorf("往返后值改变:\n got %q\nwant %q", got, v)
		}
	}
}

// TestFindComplexity N=50 与 N=5000 对照：比较数不随 N 线性增长。
func TestFindComplexity(t *testing.T) {
	compares := func(n int) int {
		s := New(nil, Config{MaxHeaders: n + 1})
		for i := 0; i < n; i++ {
			if err := s.Add(fmt.Sprintf("H-%d", i), "v"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.Get("H-7"); err != nil {
			t.Fatal(err)
		}
		return s.LastFindCompares()
	}
	c50, c5000 := compares(50), compares(5000)
	if c50 != c5000 {
		t.Errorf("比较数随 N 增长: N=50 为 %d, N=5000 为 %d", c50, c5000)
	}
	if c50 > 1 {
		t.Errorf("单名查找桶内比较数应 <=1: %d", c50)
	}
	// 同名 K 个时上界为 K。
	s := New(nil, Config{})
	for i := 0; i < 7; i++ {
		_ = s.Add("Same", fmt.Sprintf("%d", i))
	}
	_ = s.GetAll("Same")
	if got := s.LastFindCompares(); got != 7 {
		t.Errorf("同名桶比较数应为 7: %d", got)
	}
	if got := s.GetAll("Same"); strings.Join(got, ",") != "0,1,2,3,4,5,6" {
		t.Errorf("保序破坏: %v", got)
	}
}

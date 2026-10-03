package gate

import (
	"errors"
	"fmt"
	"testing"
)

// scriptedAck 按“操作序号 + 节点名”编排成功/失败，并记录调用序列。
type scriptedAck struct {
	failAt map[int]map[string]bool // raiseSeq -> 失败节点集合
	calls  []string
}

func (s *scriptedAck) ack(node string, target int) error {
	s.calls = append(s.calls, fmt.Sprintf("Ack(%s,%d)", node, target))
	seq := len(s.calls) // 仅用于错误信息
	_ = seq
	return nil
}

func newGateWithLogs() (*Gate, *[]string) {
	var calls []string
	ack := func(node string, target int) error {
		calls = append(calls, fmt.Sprintf("Ack(%s,%d)", node, target))
		return nil
	}
	rel := func(node string, target int) {
		calls = append(calls, fmt.Sprintf("Release(%s,%d)", node, target))
	}
	return New(ack, rel), &calls
}

func TestExampleLifecycle(t *testing.T) {
	g, calls := newGateWithLogs()
	if err := g.Join("a", 3); err != nil {
		t.Fatal(err)
	}
	if err := g.Raise(2); err != nil { // G 1->2
		t.Fatal(err)
	}
	if err := g.Raise(3); err != nil { // G 2->3
		t.Fatal(err)
	}
	etag, tags := "e", map[string]string{"t": "v"}
	if err := g.Put("a", "j", PutFields{Size: 5, Etag: &etag, Tags: tags}); err != nil {
		t.Fatal(err)
	}
	if st := g.Stats(); st.G != 3 || st.CountByLvl != [3]int{0, 0, 1} {
		t.Fatalf("Stats=%+v", st)
	}

	if err := g.Join("b", 2); err != nil { // b 为降级节点
		t.Fatal(err)
	}
	got, err := g.Get("b", "j")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Degraded || got.Level != 3 || got.Size != 5 || got.Etag == nil || *got.Etag != "e" || got.Tags != nil {
		t.Fatalf("降级 Get 视图错误: %+v", got)
	}
	if err := g.Put("b", "k", PutFields{Size: 1}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("降级节点写应拒绝: %v", err)
	}
	err = g.Rollback(2)
	var re *ResidualError
	if !errors.As(err, &re) || re.Count != 1 || re.HighLevel != 3 {
		t.Fatalf("应报有残留(1,3): %v", err)
	}
	if err := g.Delete("b", "j"); err != nil { // 降级节点也能删看不懂的记录
		t.Fatal(err)
	}
	if err := g.Rollback(2); err != nil || g.Stats().G != 2 {
		t.Fatalf("删除残留后回退应成功: %v G=%d", err, g.Stats().G)
	}
	x := "x"
	if err := g.Put("b", "k", PutFields{Size: 1, Etag: &x}); err != nil {
		t.Fatalf("恢复后 b 应可写: %v", err)
	}
	if st := g.Stats(); st.CountByLvl != [3]int{0, 1, 0} {
		t.Fatalf("新写记录级别应为 2: %+v", st)
	}
	err = g.Raise(3)
	var nb *NodeBehindError
	if !errors.As(err, &nb) || nb.Node != "b" {
		t.Fatalf("应报节点落后(b): %v", err)
	}
	if len(*calls) != 2 { // 节点落后在 Ack 之前，不应有任何 Ack
		t.Fatalf("落后判定前不应调用 Ack, calls=%v", *calls)
	}
}

func TestRaiseTwoPhaseFailure(t *testing.T) {
	mk := func(failNode string) (*Gate, *[]string, error) {
		var calls []string
		g := New(
			func(node string, target int) error {
				calls = append(calls, fmt.Sprintf("Ack(%s,%d)", node, target))
				if node == failNode {
					return errors.New("boom")
				}
				return nil
			},
			func(node string, target int) {
				calls = append(calls, fmt.Sprintf("Release(%s,%d)", node, target))
			},
		)
		for _, n := range []string{"a", "b", "c"} {
			if err := g.Join(n, 3); err != nil {
				t.Fatal(err)
			}
		}
		err := g.Raise(2)
		return g, &calls, err
	}

	g, calls, err := mk("b") // 中间失败
	var af *AckFailureError
	if !errors.As(err, &af) || af.Node != "b" {
		t.Fatalf("应确认失败(b): %v", err)
	}
	want := []string{"Ack(a,2)", "Ack(b,2)", "Release(a,2)"}
	if eq := stringsEq(*calls, want); !eq {
		t.Fatalf("调用序列=%v want=%v", *calls, want)
	}
	if g.Stats().G != 1 {
		t.Fatal("失败后 G 必须不变")
	}

	g, calls, err = mk("c") // 末个失败
	if !errors.As(err, &af) || af.Node != "c" {
		t.Fatalf("应确认失败(c): %v", err)
	}
	want = []string{"Ack(a,2)", "Ack(b,2)", "Ack(c,2)", "Release(b,2)", "Release(a,2)"}
	if !stringsEq(*calls, want) {
		t.Fatalf("调用序列=%v want=%v", *calls, want)
	}
	if g.Stats().G != 1 {
		t.Fatal("失败后 G 必须不变")
	}

	g, _, err = mk("a") // 首个失败：无 Release
	if !errors.As(err, &af) || af.Node != "a" {
		t.Fatalf("应确认失败(a): %v", err)
	}

	// 失败后可重试且成功
	var calls2 []string
	g2 := New(func(string, int) error { calls2 = append(calls2, "ack"); return nil }, nil)
	_ = g2.Join("a", 3)
	if err := g2.Raise(2); err != nil {
		t.Fatal(err)
	}
	if g2.Stats().G != 2 {
		t.Fatal("重试成功后 G 应为 2")
	}
}

func TestRaiseValidationOrder(t *testing.T) {
	g, _ := newGateWithLogs()
	// G=1 时 Raise(3) 与 Raise(1) 都是参数非法，而非无节点/节点落后
	if err := g.Raise(3); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Raise(3) G=1: %v", err)
	}
	if err := g.Raise(1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Raise(1): %v", err)
	}
	_ = g.Join("w", 2)
	_ = g.Join("x", 2)
	_ = g.Join("y", 3)
	if err := g.Raise(2); err != nil { // target==max 最小值，恰好通过
		t.Fatalf("target 恰等于最小 max 应通过: %v", err)
	}
	err := g.Raise(3)
	var nb *NodeBehindError
	if !errors.As(err, &nb) || nb.Node != "w" { // 并列取字节序最小
		t.Fatalf("并列应报 w: %v", err)
	}
	_ = g.Leave("w")
	_ = g.Leave("x")
	if err := g.Raise(3); err != nil { // 仅剩 y(max=3)
		t.Fatalf("落后者离开后应通过: %v", err)
	}
	if err := g.Raise(4); !errors.Is(err, ErrInvalid) {
		t.Fatalf("超过 3: %v", err)
	}

	empty, _ := newGateWithLogs()
	if err := empty.Raise(2); !errors.Is(err, ErrNoNode) {
		t.Fatalf("空集群应无节点: %v", err)
	}
}

func TestPutValidationOrderAndFieldDefaults(t *testing.T) {
	g, _ := newGateWithLogs() // G=1
	_ = g.Join("a", 3)
	// 参数非法最先（size 越界 + 不存在节点）
	if err := g.Put("zz", "k", PutFields{Size: 1e12 + 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("size 越界: %v", err)
	}
	if err := g.Put("zz", "", PutFields{Size: 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("空 key 优先: %v", err)
	}
	// 节点不在册 先于 字段不支持：G=1 且未注册节点带 etag
	e := "e"
	if err := g.Put("zz", "k", PutFields{Size: 1, Etag: &e}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("节点不在册应先报: %v", err)
	}
	// G=1：etag 最低级别 2，提供即拒绝
	if err := g.Put("a", "k", PutFields{Size: 1, Etag: &e}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("etag 不支持: %v", err)
	}
	// tags 在 G=2 仍不支持
	_ = g.Raise(2)
	if err := g.Put("a", "k", PutFields{Size: 1, Tags: map[string]string{}}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("空 tags 仍是“已提供”，应拒绝: %v", err)
	}
	// 空串 etag 与缺省不同：空串是已提供（G=2 下合法）
	empty := ""
	if err := g.Put("a", "k", PutFields{Size: 1, Etag: &empty}); err != nil {
		t.Fatalf("空串 etag 在 G=2 合法: %v", err)
	}
	got, _ := g.Get("a", "k")
	if got.Etag == nil || *got.Etag != "" || got.Tags != nil {
		t.Fatalf("空串/缺省区分错误: %+v", got)
	}
	// 缺省 etag 不返回
	if err := g.Put("a", "k2", PutFields{Size: 2}); err != nil {
		t.Fatal(err)
	}
	got, _ = g.Get("a", "k2")
	if got.Etag != nil {
		t.Fatalf("缺省 etag 不应出现: %+v", got)
	}
	// size 边界 0 与 1e12 合法
	if err := g.Put("a", "lo", PutFields{Size: 0}); err != nil {
		t.Fatalf("size=0: %v", err)
	}
	if err := g.Put("a", "hi", PutFields{Size: 1e12}); err != nil {
		t.Fatalf("size=1e12: %v", err)
	}
}

func TestGetDeleteErrors(t *testing.T) {
	g, _ := newGateWithLogs()
	_ = g.Join("a", 1)
	if _, err := g.Get("a", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Get 参数非法: %v", err)
	}
	if _, err := g.Get("z", "k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get 节点不在册: %v", err)
	}
	if _, err := g.Get("a", "k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get 键不存在: %v", err)
	}
	if err := g.Delete("z", "k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete 节点不在册: %v", err)
	}
	if err := g.Delete("a", "k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete 键不存在: %v", err)
	}
	if err := g.Join("a", 1); !errors.Is(err, ErrExists) {
		t.Fatalf("同名注册: %v", err)
	}
	if err := g.Leave("z"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Leave 不存在: %v", err)
	}
}

func TestRollbackValidation(t *testing.T) {
	g, _ := newGateWithLogs()
	_ = g.Join("a", 3)
	if err := g.Rollback(1); !errors.Is(err, ErrInvalid) { // target 必须 < G
		t.Fatalf("G=1 回退: %v", err)
	}
	_ = g.Raise(2)
	if err := g.Rollback(0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("target=0: %v", err)
	}
	if err := g.Rollback(2); !errors.Is(err, ErrInvalid) { // target 必须 < G
		t.Fatalf("target==G: %v", err)
	}
	if err := g.Rollback(3); !errors.Is(err, ErrInvalid) { // target>G
		t.Fatalf("target>G: %v", err)
	}
}

func stringsEq(a, b []string) bool {
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

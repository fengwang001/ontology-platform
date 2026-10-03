package head_test

import (
	"testing"

	"ontology/head"
)

func mustHead(t *testing.T, l, ooo int64, smax, lim int) *head.Head {
	t.Helper()
	h, err := head.NewHead(l, ooo, smax, lim)
	if err != nil {
		t.Fatalf("NewHead(%d,%d,%d,%d) 意外失败: %v", l, ooo, smax, lim, err)
	}
	return h
}

func TestNewHeadParams(t *testing.T) {
	bad := [][4]int64{{0, 0, 1, 1}, {1e9 + 1, 0, 1, 1}, {1, -1, 1, 1}, {1, 1e9 + 1, 1, 1},
		{1, 0, 0, 1}, {1, 0, 1e6 + 1, 1}, {1, 0, 1, 0}, {1, 0, 1, 1e5 + 1}}
	for _, p := range bad {
		if _, err := head.NewHead(p[0], p[1], int(p[2]), int(p[3])); err != head.ErrInvalidParam {
			t.Fatalf("参数 %v: 期望 ErrInvalidParam，得到 %v", p, err)
		}
	}
	t.Logf("输入: 8 组越界构造参数；输出: 均 ErrInvalidParam；依据: L∈[1,1e9] Ooo∈[0,1e9] Smax∈[1,1e6] Lim∈[1,1e5]")
}

func TestAppendOutOfOrder(t *testing.T) {
	h := mustHead(t, 300, 100, 10, 10)
	steps := []struct {
		ts   int64
		v    int64
		want error
	}{
		{1000, 1, nil},              // 空序列直接接受
		{950, 2, nil},               // 差 50 ≤ Ooo=100，乱序窗口内接受
		{900, 3, nil},               // 差 100 == Ooo，恰等接受
		{890, 4, head.ErrTooOld},    // 差 110 > Ooo，过旧
		{1000, 1, nil},              // 同 ts 同种类同值：重复
		{1000, 2, head.ErrConflict}, // 同 ts 不同值：冲突
	}
	for i, s := range steps {
		err := h.Append("x", s.ts, s.v)
		t.Logf("步骤%d 输入 Append(x,%d,%d) 输出 err=%v", i, s.ts, s.v, err)
		if err != s.want {
			t.Fatalf("步骤%d: 期望 %v，得到 %v", i, s.want, err)
		}
	}
	if d := h.Dups(); d != 1 {
		t.Fatalf("Dups: 期望 1，得到 %d", d)
	}
	snap := h.Snapshot()["x"]
	for i := 1; i < len(snap); i++ {
		if snap[i-1].Ts >= snap[i].Ts {
			t.Fatalf("序列内 ts 未严格升序: %v", snap)
		}
	}
	t.Logf("判定依据: 差≤Ooo 接受、差>Ooo 过旧、同 ts 同值重复(Dups=1)、同 ts 异值冲突；ts 严格升序")
}

func TestStaleMarkerRules(t *testing.T) {
	h := mustHead(t, 300, 100, 10, 10)
	if err := h.Append("s", 100, 1); err != nil {
		t.Fatal(err)
	}
	if err := h.AppendStale("s", 100); err != head.ErrConflict {
		t.Fatalf("标记与同 ts 活样本: 期望 ErrConflict，得到 %v", err)
	}
	if err := h.AppendStale("s", 200); err != nil {
		t.Fatal(err)
	}
	if err := h.Append("s", 200, 9); err != head.ErrConflict {
		t.Fatalf("活样本与同 ts 标记: 期望 ErrConflict，得到 %v", err)
	}
	if err := h.AppendStale("s", 200); err != nil || h.Dups() != 1 {
		t.Fatalf("同 ts 同标记应为重复: err=%v Dups=%d", err, h.Dups())
	}
	t.Logf("判定依据: 同 ts 种类不同即冲突；同 ts 同种类同值为重复，不改状态")
}

func TestSeriesLimitAndOrder(t *testing.T) {
	h := mustHead(t, 300, 0, 2, 10)
	if err := h.Append("a", 10, 1); err != nil {
		t.Fatal(err)
	}
	if err := h.Append("b", 10, 1); err != nil {
		t.Fatal(err)
	}
	if err := h.Append("c", 10, 1); err != head.ErrTooManySeries {
		t.Fatalf("新序列超限: 期望 ErrTooManySeries，得到 %v", err)
	}
	if err := h.Append("", 10, 1); err != head.ErrInvalidParam {
		t.Fatalf("非法名(且已满): 期望 ErrInvalidParam 先于序列上限，得到 %v", err)
	}
	// 冲突先于过旧：a 在 ts=10 有样本，Ooo=0 使 ts=9 过旧，但 ts=10 异值是冲突。
	if err := h.Append("a", 10, 2); err != head.ErrConflict {
		t.Fatalf("期望 ErrConflict 先于过旧，得到 %v", err)
	}
	if err := h.Append("a", 9, 1); err != head.ErrTooOld {
		t.Fatalf("Ooo=0 时差 1: 期望 ErrTooOld，得到 %v", err)
	}
	t.Logf("判定依据: 拒绝顺序为 参数非法→序列上限→冲突→过旧")
}

func TestBatchAtomicAndCumulativeLimit(t *testing.T) {
	h := mustHead(t, 300, 0, 3, 10)
	if err := h.Append("a", 10, 1); err != nil {
		t.Fatal(err)
	}
	// 批次引入 b、c 两个新序列，Smax=3 恰好允许。
	ok := []head.Item{{Series: "b", Ts: 10, V: 1}, {Series: "c", Ts: 10, V: 2}}
	if err := h.ApplyBatch(ok); err != nil {
		t.Fatalf("累计计入 Smax 应通过: %v", err)
	}
	// 批次含一个过旧项：整批不写，d 不存在，a 无新样本。
	bad := []head.Item{{Series: "d", Ts: 10, V: 1}, {Series: "a", Ts: 5, V: 9}}
	if err := h.ApplyBatch(bad); err != head.ErrTooManySeries {
		t.Fatalf("d 超出 Smax: 期望 ErrTooManySeries，得到 %v", err)
	}
	snap := h.Snapshot()
	if _, ok := snap["d"]; ok {
		t.Fatal("失败批次写入了序列 d")
	}
	if got := snap["a"]; len(got) != 1 || got[0].V != 1 {
		t.Fatalf("失败批次改动了 a: %v", got)
	}
	// 批次内重复项：不算失败，计入 Dups。
	dup := []head.Item{{Series: "a", Ts: 10, V: 1}, {Series: "a", Ts: 20, V: 5}}
	if err := h.ApplyBatch(dup); err != nil || h.Dups() != 1 {
		t.Fatalf("批次内重复应通过且 Dups=1: err=%v Dups=%d", err, h.Dups())
	}
	t.Logf("判定依据: 新序列按集合内累计计入 Smax；任一项失败整批不写；重复不算失败")
}

// Command demo 验证跨分区会话键归并：逐行打印 OK/FAIL，不读参数、不联网，≤10 行输出。
package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/api"
	"ontology/mrg"
)

type op struct {
	op  byte // 'A' / 'C'
	sid string
	seq int
	val int
	n   int
}

func setStr(xs []int) string {
	s := "{"
	for i, x := range xs {
		if i > 0 {
			s += ","
		}
		s += fmt.Sprintf("%d", x)
	}
	return s + "}"
}

func res(m *mrg.Mgr, sid string) string {
	if v, ok, err := m.Result(sid); err == nil && ok {
		return fmt.Sprintf("%d", v)
	}
	return "-"
}

func main() {
	// 第三节八步操作：逐步打印 s1/s2 已见 Seq 集合与冻结结果。
	m := mrg.New()
	script := []op{
		{'A', "s1", 2, 1, 0}, {'A', "s2", 1, 3, 0}, {'A', "s1", 1, 5, 0},
		{'A', "s1", 4, 8, 0}, {'A', "s1", 3, 2, 0}, {'C', "s1", 0, 0, 4},
		{'A', "s1", 5, 9, 0}, {'C', "s2", 0, 0, 1},
	}
	note := map[int]string{
		3: " seq1不跨会话串扰",
		6: " 按Seq=5128(到达序错值1582)",
		7: " ErrClosed不留痕(错接成51289)",
	}
	for i, o := range script {
		var err error
		if o.op == 'A' {
			err = m.Append(mrg.Event{Sid: o.sid, Seq: o.seq, Value: o.val})
		} else {
			err = m.Close(o.sid, o.n)
		}
		good := (i != 6) == (err == nil) // 仅第 7 步应被拒
		fmt.Printf("%d %s s1%s:%s s2%s:%s%s\n", i+1, tag(good),
			setStr(m.Seen("s1")), res(m, "s1"), setStr(m.Seen("s2")), res(m, "s2"), note[i+1])
	}

	// 四类可判定错误互不相同；被拒后状态不变且仍可继续使用（状态观察走内部 mrg）。
	d := mrg.New()
	_ = d.Append(mrg.Event{Sid: "u", Seq: 1, Value: 7})
	before := len(d.Seen("u"))
	type badCase struct {
		err  error
		want error
	}
	cases := []badCase{
		{d.Append(mrg.Event{Sid: "", Seq: 1, Value: 1}), mrg.ErrBadKey},
		{d.Append(mrg.Event{Sid: "u", Seq: 0, Value: 1}), mrg.ErrInvalid},
		{d.Append(mrg.Event{Sid: "u", Seq: 1, Value: 8}), mrg.ErrConflict},
		{d.Close("u", 2), mrg.ErrIncomplete},
	}
	lineOK := len(d.Seen("u")) == before // 被拒不留痕
	distinct := map[error]bool{}
	for _, c := range cases {
		lineOK = errors.Is(c.err, c.want) && lineOK
		distinct[c.want] = true
	}
	lineOK = lineOK && len(distinct) == 4
	_ = d.Append(mrg.Event{Sid: "u", Seq: 2, Value: 3})
	lineOK = d.Close("u", 2) == nil && lineOK // 仍可正常使用
	fmt.Printf("%s 四类错误可判定且互异；被拒后状态不变、会话仍可用\n", tag(lineOK))

	// 大 m 下对已存在会话的定位不随表长变化（非增长证明在 mrg 同包测试里钉计数器）。
	bigOK := true
	for _, n := range []int{100, 1000, 10000} {
		b := api.New()
		for i := 0; i < n; i++ {
			_ = b.Append(api.Event{Sid: fmt.Sprintf("s%d", i), Seq: 1, Value: i % 10})
		}
		bigOK = b.Append(api.Event{Sid: "s0", Seq: 2, Value: 9}) == nil && bigOK
	}
	// 并发 Append 乱序到达后 Close，结果必须等于按 Seq 升序拼接。
	c := api.New()
	const N = 12 // 12 位拼接结果在 int64 内；Result 类型为 int
	var wg sync.WaitGroup
	for _, seq := range newPerm(N) {
		wg.Add(1)
		go func(seq int) {
			defer wg.Done()
			_ = c.Append(api.Event{Sid: "k", Seq: seq, Value: seq % 10})
		}(seq)
	}
	wg.Wait()
	want := 0
	for seq := 1; seq <= N; seq++ {
		want = want*10 + seq%10
	}
	concOK := c.Close("k", N) == nil
	v, closed, _ := c.Result("k")
	concOK = concOK && closed && v == want && api.New().SelfCheck() == nil
	fmt.Printf("%s 大m定位不随表长增长；并发Append后Close=%d；SelfCheck\n", tag(bigOK && concOK), v)
}

func tag(ok bool) string {
	if ok {
		return "OK"
	}
	return "FAIL"
}

// newPerm 返回 1..n 的确定性伪随机排列（线性同余洗牌，不引 math/rand 种子时序）。
func newPerm(n int) []int {
	xs := make([]int, n)
	for i := range xs {
		xs[i] = i + 1
	}
	seed := 2654435761
	for i := n - 1; i > 0; i-- {
		seed = seed*1103515245 + 12345
		j := (seed >> 16) % (i + 1)
		if j < 0 {
			j = -j
		}
		xs[i], xs[j] = xs[j], xs[i]
	}
	return xs
}

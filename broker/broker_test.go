package broker

import (
	"errors"
	"strconv"
	"testing"
)

// TestEpochBumpLazy 证明 epoch 升级的“清空全部分区状态”是 O(1) 惰性失效：
// 升级后的检查条目数不随分区数 m 线性增长，恒不超过与 m 无关的小常数。
// checked 为非导出字段，本白盒测试是读取它的唯一位置，不经任何导出接口。
func TestEpochBumpLazy(t *testing.T) {
	const maxChecked = 2
	for _, m := range []int{100, 1000, 10000} {
		b := New(m, 4)
		for p := 0; p < m; p++ { // 同一 pid 在 epoch0 向全部 m 个分区各写一条 seq=0
			if _, _, err := b.Produce(1, 0, p, 0, ""); err != nil {
				t.Fatalf("m=%d seed write p=%d: %v", m, p, err)
			}
			if b.checked > maxChecked { // 每次只取目标分区这一个条目
				t.Fatalf("m=%d seed write checked=%d", m, b.checked)
			}
		}
		// epoch1 向分区 0 写 seq=0：若逐个分区遍历清空，checked 会随 m 增长。
		if off, _, err := b.Produce(1, 1, 0, 0, ""); err != nil || off != 1 {
			t.Fatalf("m=%d bump p0 got (%d,%v) want off=1,nil", m, off, err)
		}
		if b.checked > maxChecked {
			t.Fatalf("m=%d bump checked=%d grows with m", m, b.checked)
		}
		// 分区 m-1 的旧条目被惰性判定为已清空：首条 seq=0 必须接受。
		if off, _, err := b.Produce(1, 1, m-1, 0, ""); err != nil || off != 1 {
			t.Fatalf("m=%d bump p(m-1) got (%d,%v) want off=1,nil", m, off, err)
		}
		if b.checked > maxChecked {
			t.Fatalf("m=%d far partition checked=%d grows with m", m, b.checked)
		}
		// 分区 1 已被惰性清空，首条 seq=1 必须乱序拒绝，且检查数仍是常数。
		if _, _, err := b.Produce(1, 1, 1, 1, ""); !errors.Is(err, ErrOutOfOrder) {
			t.Fatalf("m=%d fresh partition seq=1 got %v want ErrOutOfOrder", m, err)
		}
		if b.checked > maxChecked {
			t.Fatalf("m=%d reject checked=%d grows with m", m, b.checked)
		}
	}
}

// TestBrokerDecisionTable 表驱动覆盖四类判定结果与重复位点，固定判定顺序。
func TestBrokerDecisionTable(t *testing.T) {
	type call struct {
		pid, ep, seq int64
		p            int
	}
	cases := []struct {
		name  string
		w     int
		calls []call
		want  []string // +off 追加，=off 重复，错误哨兵名
	}{
		{"append-dup-gap", 2,
			[]call{{1, 0, 0, 0}, {1, 0, 0, 0}, {1, 0, 3, 0}, {1, 0, 1, 0}, {1, 0, 2, 0}},
			[]string{"+0", "=0", "OOO", "+1", "+2"}},
		{"window-expiry", 1,
			[]call{{2, 0, 0, 0}, {2, 0, 1, 0}, {2, 0, 0, 0}, {2, 0, 2, 0}},
			[]string{"+0", "+1", "EXPD", "+2"}},
		{"bump-and-fence", 4,
			[]call{{3, 0, 0, 0}, {3, 1, 0, 0}, {3, 0, 1, 0}, {3, 1, 1, 1}},
			[]string{"+0", "+1", "FENCE", "OOO"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := New(2, c.w)
			for i, q := range c.calls {
				off, dup, err := b.Produce(q.pid, q.ep, q.p, q.seq, "")
				got := classify(off, dup, err)
				if got != c.want[i] {
					t.Fatalf("call %d got %s want %s", i, got, c.want[i])
				}
			}
		})
	}
}

func classify(off int64, dup bool, err error) string {
	switch {
	case errors.Is(err, ErrInvalid):
		return "INV"
	case errors.Is(err, ErrFenced):
		return "FENCE"
	case errors.Is(err, ErrOutOfOrder):
		return "OOO"
	case errors.Is(err, ErrDuplicateExpired):
		return "EXPD"
	case dup:
		return "=" + strconv.FormatInt(off, 10)
	default:
		return "+" + strconv.FormatInt(off, 10)
	}
}

// TestExactlyOnce 钉住不变量 2：同一 (pid,epoch,分区,seq) 至多落盘一次、
// 重复返回同一旧位点；同一 (pid,epoch,分区) 位点上的 seq 恰为 0,1,2…。
// 循环生成多档 (N,W,K)，并顺带断言四个哨兵互不相同。
func TestExactlyOnce(t *testing.T) {
	sentinels := []error{ErrInvalid, ErrFenced, ErrOutOfOrder, ErrDuplicateExpired}
	for i := range sentinels {
		for j := i + 1; j < len(sentinels); j++ {
			if sentinels[i] == sentinels[j] {
				t.Fatal("sentinel errors must be pairwise distinct")
			}
		}
	}
	for _, cfg := range [][3]int{{1, 3, 5}, {3, 4, 8}, {2, 1, 4}} {
		n, w, k := cfg[0], cfg[1], cfg[2]
		b := New(n, w)
		for p := 0; p < n; p++ {
			for s := int64(0); s < int64(k); s++ {
				o0, d0, e0 := b.Produce(1, 0, p, s, "")
				if e0 != nil || d0 {
					t.Fatalf("first write p=%d seq=%d (%v,%v)", p, s, d0, e0)
				}
				for r := 0; r < 2; r++ {
					if off, dup, err := b.Produce(1, 0, p, s, ""); err != nil || !dup || off != o0 {
						t.Fatalf("repeat p=%d seq=%d got (%d,%v,%v) want dup off=%d", p, s, off, dup, err, o0)
					}
				}
			}
			l := b.Log(p)
			if len(l) != k {
				t.Fatalf("p=%d len=%d want %d", p, len(l), k)
			}
			for i, rec := range l {
				if rec.Seq != int64(i) {
					t.Fatalf("p=%d log[%d].Seq=%d not contiguous", p, i, rec.Seq)
				}
			}
		}
	}
}

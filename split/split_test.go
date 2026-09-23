package split_test

import (
	"errors"
	"testing"

	"ontology/addr"
	"ontology/split"
)

func lcg(n int, seed uint64) []byte {
	b := make([]byte, n)
	x := seed
	for i := range b {
		x = x*6364136223846793005 + 1
		b[i] = byte(x>>32) | 1 // 避开全 0（全 0 必然命中谓词）
	}
	return b
}

func addrs(data []byte, sp []split.Span) []addr.Addr {
	a := make([]addr.Addr, len(sp))
	for i, s := range sp {
		a[i] = addr.Of(data[s.Start:s.End])
	}
	return a
}

func TestConfigErrors(t *testing.T) {
	cases := []struct {
	name string
	cfg  split.Config
	want error
	}{
		{"min>max", split.Config{4, 8, 4, 6}, split.ErrMinMax},
		{"window0", split.Config{0, 8, 32, 6}, split.ErrWindow},
		{"window>min", split.Config{8, 4, 32, 6}, split.ErrWindow},
		{"bits", split.Config{4, 8, 32, 0}, split.ErrBits},
	}
	for _, c := range cases {
		if _, err := split.New(c.cfg); !errors.Is(err, c.want) {
			t.Fatalf("%s: err=%v want %v", c.name, err, c.want)
		}
	}
	if errors.Is(split.ErrMinMax, split.ErrWindow) {
		t.Fatal("两类配置错误必须互不相同")
	}
}

// TestContiguous：偏移首尾相接；长度规则符合 DESIGN.md 第 4 节。
func TestContiguous(t *testing.T) {
	sp, _ := split.New(split.Config{16, 64, 256, 6})
	for _, n := range []int{1, 63, 64, 255, 4096, 20000} {
		data := lcg(n, 99)
		ss := sp.Cut(data)
		if len(ss) == 0 {
			t.Fatalf("n=%d 空流之外必须至少一块", n)
		}
		if ss[0].Start != 0 || ss[len(ss)-1].End != int64(n) {
			t.Fatalf("n=%d 区间未覆盖全流", n)
		}
		for i, s := range ss {
			l := int(s.End - s.Start)
			if i > 0 && s.Start != ss[i-1].End {
				t.Fatalf("n=%d 块间有空隙/重叠", n)
			}
			switch {
			case i < len(ss)-1 && (l < 64 || l > 256):
				t.Fatalf("n=%d 非末块长度 %d 越界", n, l)
			case i == len(ss)-1 && len(ss) > 1 && (l < 1 || l > 256+64-1):
				t.Fatalf("n=%d 末块长度 %d 越界", n, l)
			}
		}
	}
}

func TestTailRules(t *testing.T) {
	sp, _ := split.New(split.Config{16, 64, 256, 6})
	small := lcg(10, 7)
	if ss := sp.Cut(small); len(ss) != 1 || ss[0] != (split.Span{0, 10}) {
		t.Fatalf("流<min 应单独成 1 块，得到 %v", ss)
	}
	empty := sp.Cut(nil)
	if len(empty) != 0 {
		t.Fatal("空流必须 0 块")
	}
}

func TestForcedAtMax(t *testing.T) {
	sp, _ := split.New(split.Config{4, 10, 10, 31}) // 谓词几乎不命中
	data := lcg(25, 3)
	ss := sp.Cut(data)
	forced := false
	for _, s := range ss {
		if s.End-s.Start == 10 {
			forced = true
		}
	}
	if !forced {
		t.Fatalf("未在 max=10 处产生强制切点: %v", ss)
	}
}

// TestLocality：中部插入 1 字节，受影响块 ≤12（2*ceil(max/min)+3+1），
// 对齐之后远处块地址逐个相同。
func TestLocality(t *testing.T) {
	const min, max = 64, 256
	sp, _ := split.New(split.Config{16, min, max, 6})
	data := lcg(30000, 42)
	ins := len(data) / 2
	mod := make([]byte, 0, len(data)+1)
	mod = append(mod, data[:ins]...)
	mod = append(mod, 0x77)
	mod = append(mod, data[ins:]...)

	old, new := sp.Cut(data), sp.Cut(mod)
	oa, na := addrs(data, old), addrs(mod, new)

	// base：插入点前最后一个共同切点（在两侧块中分别定位）。
	bj := 0
	for bj+1 < len(old) && old[bj].End <= int64(ins) {
		bj++
	}
	bq := 0
	for bq+1 < len(new) && new[bq].End <= int64(ins) {
		bq++
	}
	// anchor：插入点之后第一块。
	j := 0
	for old[j].Start <= int64(ins) {
		j++
	}
	q := 0
	for q < len(new) &&
		(na[q] != oa[j] || new[q].Start != old[j].Start+1) {
		q++
	}
	if q >= len(new) {
		t.Fatal("未找到重新同步的远处块")
	}
	affected := (j - bj) + (q - bq)
	if affected > 2*(max/min)+1+3 {
		t.Fatalf("受影响块 %d 超过局部性常数 12", affected)
	}
	oj, nq := j, q
	for ; oj < len(old); oj, nq = oj+1, nq+1 {
		if nq >= len(new) || na[nq] != oa[oj] {
			t.Fatalf("远处块 oj=%d 地址不一致", oj)
		}
		if new[nq].Start != old[oj].Start+1 {
			t.Fatalf("远处块偏移未按 +1 对齐")
		}
	}
}

package field

import (
	"math"
	"testing"

	"ontology/poly"
)

// TestPowMulCountLog 断言平方乘的域乘法次数 ≤ 2*ceil(log2(e))+1（对数增长），
// 证明不是「自乘 e 次」（那会是 e 量级）。直接读非导出计数器，不经任何导出接口。
func TestPowMulCountLog(t *testing.T) {
	var exps []uint64
	for e := uint64(100); e <= 10000; e = e*3/2 + 1 { // 100..10000 若干档
		exps = append(exps, e)
	}
	for _, e := range exps {
		if _, err := Pow(0x53, e); err != nil {
			t.Fatalf("Pow(0x53,%d): %v", e, err)
		}
		got := lastPowMuls.Load()
		ceil := int64(0)
		for n := uint64(1); n < e; n <<= 1 {
			ceil++
		}
		if bound := 2*ceil + 1; got > bound {
			t.Errorf("Pow(0x53,%d) muls=%d > bound=%d", e, got, bound)
		}
		if got >= int64(e) { // 自乘 e 次实现必然 >= e
			t.Errorf("Pow(0x53,%d) muls=%d looks linear", e, got)
		}
	}
}

func TestPowExponentLimit(t *testing.T) {
	cases := []struct {
		e       uint64
		wantErr bool
	}{
		{0, false},
		{1<<63 - 1, false},
		{1 << 63, false},
		{1<<63 + 1, true},
		{math.MaxUint64, true},
	}
	for _, c := range cases {
		_, err := Pow(0x02, c.e)
		if c.wantErr && err != ErrExponentRange {
			t.Errorf("Pow(0x02,%d) err=%v, want ErrExponentRange", c.e, err)
		}
		if !c.wantErr && err != nil {
			t.Errorf("Pow(0x02,%d) unexpected err=%v", c.e, err)
		}
	}
}

func TestPowZeroBase(t *testing.T) {
	cases := []struct {
		a    uint8
		e    uint64
		want uint8
	}{
		{0x00, 0, 0x01}, // 约定 Pow(0,0)==1
		{0x00, 5, 0x00},
		{0x01, 0, 0x01},
		{0x53, 0, 0x01},
	}
	for _, c := range cases {
		if got, err := Pow(c.a, c.e); err != nil || got != c.want {
			t.Errorf("Pow(%02X,%d)=%02X,%v want %02X", c.a, c.e, got, err, c.want)
		}
	}
}

// TestReducerRejectsReducible：poly 自检——非不可约字节构建失败，合法字节成功。
func TestReducerRejectsReducible(t *testing.T) {
	cases := []struct {
		mod     uint8
		wantErr bool
	}{
		{0x1B, false}, // AES 的 0x11B
		{0x1D, false}, // 0x11D 同样不可约（只是不是本域）
		{0x00, true},  // x^8
		{0x01, true},  // x^8+1 = (x+1)^8
		{0x02, true},  // x^8+x 含因子 x
	}
	for _, c := range cases {
		_, err := poly.NewReducer(c.mod)
		if c.wantErr && err != poly.ErrNotIrreducible {
			t.Errorf("NewReducer(%02X) err=%v, want ErrNotIrreducible", c.mod, err)
		}
		if !c.wantErr && err != nil {
			t.Errorf("NewReducer(%02X) unexpected err=%v", c.mod, err)
		}
	}
}

package match

import (
	"errors"
	"testing"
)

func TestNewValidatesValueOutsideMask(t *testing.T) {
	cases := []struct {
		name           string
		v0, m0, v1, m1 uint32
		wantErr        bool
	}{
		{"全通配", 0, 0, 0, 0, false},
		{"精确匹配", 0x0A000000, 0xFFFFFFFF, 1, 0xFFFFFFFF, false},
		{"值恰等于掩码", 0xFF00FF00, 0xFF00FF00, 0, 0, false},
		{"字段0值越出掩码", 0x00000001, 0xFF000000, 0, 0, true},
		{"字段1值越出掩码", 0, 0, 0x00000001, 0xFFFFFFFE, true},
		{"掩码外最高位置位", 0x80000000, 0x7FFFFFFF, 0, 0, true},
	}
	for _, c := range cases {
		m, err := New(c.v0, c.m0, c.v1, c.m1)
		if c.wantErr {
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("%s: 期望 ErrInvalid，得到 %v", c.name, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: 意外错误 %v", c.name, err)
		}
		if !m.Valid() {
			t.Errorf("%s: 构造结果不合法", c.name)
		}
		t.Logf("%s: 输入 (%08x/%08x, %08x/%08x) wantErr=%v 判定=值&^掩码", c.name, c.v0, c.m0, c.v1, c.m1, c.wantErr)
	}
}

func TestHit(t *testing.T) {
	cases := []struct {
		name string
		m    Match
		pkt  Packet
		want bool
	}{
		{"全通配命中任意", Must(0, 0, 0, 0), Packet{0xDEADBEEF, 1}, true},
		{"精确命中", Must(0x0A000000, 0xFF000000, 0, 0), Packet{0x0A010203, 0}, true},
		{"掩码内不符", Must(0x0A000000, 0xFF000000, 0, 0), Packet{0x0B000000, 0}, false},
		{"掩码外差异不影响", Must(0x0A000000, 0xFF000000, 0, 0), Packet{0x0AFFFFFF, 0}, true},
		{"字段1不命中", Must(0, 0, 0x0000FFFF, 0x0000FFFF), Packet{0, 0x0000FFFE}, false},
		{"两字段同时精确", Must(1, 0xFFFFFFFF, 2, 0xFFFFFFFF), Packet{1, 2}, true},
	}
	for _, c := range cases {
		got := c.m.Hit(c.pkt)
		if got != c.want {
			t.Errorf("%s: Hit=%v 期望 %v", c.name, got, c.want)
		}
		t.Logf("%s: pkt=%08x,%08x Hit=%v 判定=逐字段 报文&掩码==值", c.name, c.pkt[0], c.pkt[1], got)
	}
}

func TestOverlaps(t *testing.T) {
	a := Must(0x0A000000, 0xFF000000, 0, 0)
	b := Must(0x0A010000, 0xFFFF0000, 0, 0)
	cases := []struct {
		name string
		x, y Match
		want bool
	}{
		{"题面例子A与B重叠", a, b, true},
		{"相同匹配重叠", a, a, true},
		{"全通配与任意重叠", Must(0, 0, 0, 0), b, true},
		{"共同掩码位上冲突不重叠", Must(0x0A000000, 0xFF000000, 0, 0), Must(0x0B000000, 0xFF000000, 0, 0), false},
		{"差异不在共同掩码位上仍重叠", Must(0x0A000000, 0xFF000000, 0, 0), Must(0x00010000, 0x00FF0000, 0, 0), true},
		{"单比特掩码相同重叠", Must(0x80000000, 0x80000000, 0, 0), Must(0x80000000, 0xFFFFFFFF, 0, 0), true},
		{"单比特掩码冲突不重叠", Must(0x00000001, 0x00000001, 0, 0), Must(0x00000000, 0x00000001, 0, 0), false},
		{"字段0重叠字段1冲突不重叠", Must(0, 0, 0x00000001, 0x00000001), Must(0, 0, 0, 0x00000001), false},
		{"对称性", b, a, true},
	}
	for _, c := range cases {
		got := c.x.Overlaps(c.y)
		if got != c.want {
			t.Errorf("%s: Overlaps=%v 期望 %v", c.name, got, c.want)
		}
		t.Logf("%s: Overlaps=%v 判定=逐字段 (v1^v2)&m1&m2==0", c.name, got)
	}
}

func TestEqual(t *testing.T) {
	a := Must(0x0A000000, 0xFF000000, 0, 0)
	cases := []struct {
		name string
		x, y Match
		want bool
	}{
		{"完全相同", a, Must(0x0A000000, 0xFF000000, 0, 0), true},
		{"掩码不同不相等", a, Must(0x0A000000, 0xFFFF0000, 0, 0), false},
		{"值不同不相等", a, Must(0x0A000001, 0xFF000001, 0, 0), false},
		{"字段1不同不相等", a, Must(0x0A000000, 0xFF000000, 1, 0xFFFFFFFF), false},
	}
	for _, c := range cases {
		if got := c.x.Equal(c.y); got != c.want {
			t.Errorf("%s: Equal=%v 期望 %v", c.name, got, c.want)
		}
	}
}

func TestContains(t *testing.T) {
	a := Must(0x0A000000, 0xFF000000, 0, 0)
	b := Must(0x0A010000, 0xFFFF0000, 0, 0)
	cases := []struct {
		name string
		x, y Match
		want bool
	}{
		{"A包含B（题面例子）", a, b, true},
		{"B不包含A", b, a, false},
		{"相等互相包含", a, a, true},
		{"全通配包含一切", Must(0, 0, 0, 0), b, true},
		{"值在包含方掩码上不符", Must(0x0B000000, 0xFF000000, 0, 0), b, false},
		{"字段1掩码非子集则不包含", Must(0, 0, 0x0000FFFF, 0x0000FFFF), Must(0, 0, 0, 0), false},
		{"字段1值不符则不包含", Must(0, 0, 0x000000FF, 0x0000FFFF), Must(0, 0, 0x0000FFFF, 0x0000FFFF), false},
	}
	for _, c := range cases {
		got := c.x.Contains(c.y)
		if got != c.want {
			t.Errorf("%s: Contains=%v 期望 %v", c.name, got, c.want)
		}
		t.Logf("%s: Contains=%v 判定=逐字段 掩码子集且值一致", c.name, got)
	}
}

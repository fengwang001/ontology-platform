package row

import (
	"errors"
	"math"
	"testing"
)

func TestEncodeDecode(t *testing.T) {
	cases := []Row{
		{"a", 1.5},
		{"", 0},
		{"空键值测试", -273.15},
		{"inf", math.Inf(1)},
		{"-inf", math.Inf(-1)},
		{"negzero", math.Copysign(0, -1)},
	}
	for _, c := range cases {
		got, err := Decode(Encode(c))
		if err != nil || got.Key != c.Key || math.Float64bits(got.Val) != math.Float64bits(c.Val) {
			t.Errorf("%+v: 往返失败 got=%+v err=%v", c, got, err)
		}
	}
}

func TestDecodeError(t *testing.T) {
	good := Encode(Row{"k", 1})
	cases := []struct {
		name string
		data []byte
	}{
		{"空输入", nil},
		{"键长前缀残缺", []byte{0x80}},
		{"键体残缺", good[:2]},
		{"数值残缺", good[:len(good)-1]},
		{"尾部多余", append(append([]byte{}, good...), 0)},
	}
	for _, c := range cases {
		if _, err := Decode(c.data); !errors.Is(err, ErrDecode) {
			t.Errorf("%s: 期望 ErrDecode, 得到 %v", c.name, err)
		}
	}
}

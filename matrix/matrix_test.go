package matrix

import (
	"errors"
	"strings"
	"testing"
)

func comboStrings(combos []Combo) []string {
	out := make([]string, len(combos))
	for i, c := range combos {
		out[i] = c.String()
	}
	return out
}

func equalStrings(a, b []string) bool {
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

func TestExpandTable(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want []string
		base []bool
	}{
		{
			name: "规格示例：exclude 先于 include、追加不被扩充、空轴部分只写基础组合",
			cfg: Config{
				Axes: []Axis{
					{Key: "os", Values: []string{"linux", "win"}},
					{Key: "ver", Values: []string{"1", "2"}},
				},
				Exclude: []map[string]string{{"os": "win", "ver": "1"}},
				Include: []map[string]string{
					{"os": "linux", "tag": "a"},
					{"ver": "2", "tag": "b"},
					{"os": "mac", "ver": "2"},
					{"os": "mac", "tag": "c"},
					{"experimental": "true"},
				},
			},
			want: []string{
				"os=linux,ver=1,experimental=true,tag=a",
				"os=linux,ver=2,experimental=true,tag=b",
				"os=win,ver=2,experimental=true,tag=b",
				"os=mac,ver=2",
				"os=mac,tag=c",
			},
			base: []bool{true, true, true, false, false},
		},
		{
			name: "exclude 先于 include：只匹配被剔除组合的 include 整体追加",
			cfg: Config{
				Axes:    []Axis{{Key: "os", Values: []string{"linux", "win"}}},
				Exclude: []map[string]string{{"os": "win"}},
				Include: []map[string]string{{"os": "win", "tag": "x"}},
			},
			want: []string{"os=linux", "os=win,tag=x"},
			base: []bool{true, false},
		},
		{
			name: "轴值不被改写而附加键被后项覆盖",
			cfg: Config{
				Axes: []Axis{{Key: "os", Values: []string{"a", "b"}}},
				Include: []map[string]string{
					{"os": "a", "tag": "1"},
					{"tag": "2"},
				},
			},
			want: []string{"os=a,tag=2", "os=b,tag=2"},
			base: []bool{true, true},
		},
		{
			name: "追加组合不被后续 include 项扩充",
			cfg: Config{
				Axes: []Axis{
					{Key: "os", Values: []string{"linux"}},
					{Key: "ver", Values: []string{"1", "2"}},
				},
				Include: []map[string]string{
					{"os": "mac", "ver": "2"},
					{"ver": "2", "tag": "z"},
				},
			},
			want: []string{
				"os=linux,ver=1",
				"os=linux,ver=2,tag=z",
				"os=mac,ver=2",
			},
			base: []bool{true, true, false},
		},
		{
			name: "空轴部分在基础组合为空时变为追加",
			cfg: Config{
				Axes:    []Axis{{Key: "os", Values: []string{"a"}}},
				Exclude: []map[string]string{{"os": "a"}},
				Include: []map[string]string{{"tag": "x"}},
			},
			want: []string{"tag=x"},
			base: []bool{false},
		},
		{
			name: "include 匹配且附加部分为空：展开不变也不追加",
			cfg: Config{
				Axes: []Axis{
					{Key: "os", Values: []string{"linux", "win"}},
					{Key: "ver", Values: []string{"1", "2"}},
				},
				Include: []map[string]string{{"os": "linux", "ver": "1"}},
			},
			want: []string{
				"os=linux,ver=1",
				"os=linux,ver=2",
				"os=win,ver=1",
				"os=win,ver=2",
			},
			base: []bool{true, true, true, true},
		},
		{
			name: "exclude 值不在轴取值内是合法的（匹配不到）",
			cfg: Config{
				Axes:    []Axis{{Key: "os", Values: []string{"a"}}},
				Exclude: []map[string]string{{"os": "zzz"}},
			},
			want: []string{"os=a"},
			base: []bool{true},
		},
		{
			name: "相同 include 连续两项无匹配则各追加一次",
			cfg: Config{
				Axes: []Axis{{Key: "os", Values: []string{"a"}}},
				Include: []map[string]string{
					{"os": "mac", "tag": "c"},
					{"os": "mac", "tag": "c"},
				},
			},
			want: []string{"os=a", "os=mac,tag=c", "os=mac,tag=c"},
			base: []bool{true, false, false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			combos, err := Expand(tc.cfg)
			if err != nil {
				t.Fatalf("Expand 报错: %v", err)
			}
			got := comboStrings(combos)
			if !equalStrings(got, tc.want) {
				t.Fatalf("展开结果不符\n got: %q\nwant: %q", got, tc.want)
			}
			for i, c := range combos {
				if c.Base() != tc.base[i] {
					t.Errorf("作业 %d Base()=%v, want %v", i, c.Base(), tc.base[i])
				}
			}
			t.Logf("输入=%+v 输出=%q 判定依据=按笛卡尔积→exclude→include 逐步展开", tc.cfg, got)
		})
	}
}

func TestExpandExperimental(t *testing.T) {
	combos, err := Expand(Config{
		Axes: []Axis{{Key: "os", Values: []string{"a", "b"}}},
		Include: []map[string]string{
			{"os": "a", "experimental": "true"},
			{"os": "b", "experimental": "True"},
		},
	})
	if err != nil {
		t.Fatalf("Expand 报错: %v", err)
	}
	if !combos[0].Experimental() {
		t.Errorf("experimental=true 应为试验性作业")
	}
	if combos[1].Experimental() {
		t.Errorf("experimental=True（非恰为 true）不应为试验性作业")
	}
}

func TestExpandDeterministic(t *testing.T) {
	cfg := Config{
		Axes: []Axis{
			{Key: "os", Values: []string{"linux", "win"}},
			{Key: "ver", Values: []string{"1", "2"}},
		},
		Exclude: []map[string]string{{"os": "win", "ver": "1"}},
		Include: []map[string]string{
			{"os": "linux", "tag": "a", "experimental": "true"},
			{"os": "mac", "ver": "2"},
		},
	}
	first, err := Expand(cfg)
	if err != nil {
		t.Fatalf("Expand 报错: %v", err)
	}
	for round := 0; round < 20; round++ {
		again, err := Expand(cfg)
		if err != nil {
			t.Fatalf("Expand 报错: %v", err)
		}
		if !equalStrings(comboStrings(first), comboStrings(again)) {
			t.Fatalf("相同配置展开结果不是逐字节相同: %q vs %q",
				comboStrings(first), comboStrings(again))
		}
	}
}

func values(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "v" + itoa(i)
	}
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

func TestExpandSizeLimits(t *testing.T) {
	// 恰 256：8*8*4 = 256，合法。
	ok256 := Config{Axes: []Axis{
		{Key: "a", Values: values(8)},
		{Key: "b", Values: values(8)},
		{Key: "c", Values: values(4)},
	}}
	combos, err := Expand(ok256)
	if err != nil {
		t.Fatalf("恰 256 应合法, got %v", err)
	}
	if len(combos) != 256 {
		t.Fatalf("组合数 = %d, want 256", len(combos))
	}

	// 257：256 个基础组合 + 1 个追加，矩阵过大。
	tooBig := ok256
	tooBig.Include = []map[string]string{{"a": "not-in-axis"}}
	if _, err := Expand(tooBig); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("257 应报矩阵过大, got %v", err)
	}

	// 空矩阵：基础组合被 exclude 全部剔除且无 include。
	empty := Config{
		Axes:    []Axis{{Key: "os", Values: []string{"a", "b"}}},
		Exclude: []map[string]string{{"os": "a"}, {"os": "b"}},
	}
	if _, err := Expand(empty); !errors.Is(err, ErrEmpty) {
		t.Fatalf("应报空矩阵, got %v", err)
	}
}

func TestExpandInvalidParams(t *testing.T) {
	longKey := strings.Repeat("k", 33)
	longVal := strings.Repeat("v", 65)
	manyRules := make([]map[string]string, 65)
	for i := range manyRules {
		manyRules[i] = map[string]string{"os": "a"}
	}
	cases := []struct {
		name string
		cfg  Config
	}{
		{"0 个轴", Config{}},
		{"6 个轴", Config{Axes: []Axis{
			{Key: "a", Values: []string{"1"}}, {Key: "b", Values: []string{"1"}},
			{Key: "c", Values: []string{"1"}}, {Key: "d", Values: []string{"1"}},
			{Key: "e", Values: []string{"1"}}, {Key: "f", Values: []string{"1"}},
		}}},
		{"轴键重复", Config{Axes: []Axis{
			{Key: "a", Values: []string{"1"}}, {Key: "a", Values: []string{"2"}},
		}}},
		{"轴键为空", Config{Axes: []Axis{{Key: "", Values: []string{"1"}}}}},
		{"轴键 33 字节", Config{Axes: []Axis{{Key: longKey, Values: []string{"1"}}}}},
		{"轴无取值", Config{Axes: []Axis{{Key: "a"}}}},
		{"轴 11 个取值", Config{Axes: []Axis{{Key: "a", Values: values(11)}}}},
		{"轴取值重复", Config{Axes: []Axis{{Key: "a", Values: []string{"1", "1"}}}}},
		{"取值 65 字节", Config{Axes: []Axis{{Key: "a", Values: []string{longVal}}}}},
		{"65 个 exclude", Config{
			Axes:    []Axis{{Key: "os", Values: []string{"a"}}},
			Exclude: manyRules,
		}},
		{"空 exclude 项", Config{
			Axes:    []Axis{{Key: "os", Values: []string{"a"}}},
			Exclude: []map[string]string{{}},
		}},
		{"exclude 键非轴键", Config{
			Axes:    []Axis{{Key: "os", Values: []string{"a"}}},
			Exclude: []map[string]string{{"tag": "x"}},
		}},
		{"exclude 值 65 字节", Config{
			Axes:    []Axis{{Key: "os", Values: []string{"a"}}},
			Exclude: []map[string]string{{"os": longVal}},
		}},
		{"65 个 include", Config{
			Axes:    []Axis{{Key: "os", Values: []string{"a"}}},
			Include: manyRules,
		}},
		{"空 include 项", Config{
			Axes:    []Axis{{Key: "os", Values: []string{"a"}}},
			Include: []map[string]string{{}},
		}},
		{"include 键 33 字节", Config{
			Axes:    []Axis{{Key: "os", Values: []string{"a"}}},
			Include: []map[string]string{{longKey: "x"}},
		}},
		{"include 值 65 字节", Config{
			Axes:    []Axis{{Key: "os", Values: []string{"a"}}},
			Include: []map[string]string{{"tag": longVal}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Expand(tc.cfg); !errors.Is(err, ErrInvalidParam) {
				t.Fatalf("应报参数非法, got %v", err)
			}
		})
	}
}

func TestExpandRejectOrder(t *testing.T) {
	// 参数非法 > 矩阵过大：轴键重复且笛卡尔积超过 256。
	both := Config{Axes: []Axis{
		{Key: "a", Values: values(10)},
		{Key: "a", Values: values(10)},
		{Key: "b", Values: values(10)},
	}}
	if _, err := Expand(both); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("参数非法应优先于矩阵过大, got %v", err)
	}
	// 矩阵过大 > 空矩阵：参数合法时不会同时过大与为空，此处验证过大不被误报为空。
	big := Config{Axes: []Axis{
		{Key: "a", Values: values(10)},
		{Key: "b", Values: values(10)},
		{Key: "c", Values: values(10)},
	}}
	if _, err := Expand(big); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("应报矩阵过大, got %v", err)
	}
}

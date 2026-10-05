package matrix

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func kv(k, v string) Pair { return Pair{Key: k, Value: v} }

func job(pairs ...Pair) Job {
	return Job{Pairs: pairs, Experimental: isExperimental(pairs)}
}

// 规格示例：exclude 先于 include、附加键被后项覆盖、追加组合不被后续项
// 扩充、空轴部分匹配全部留存基础组合、试验性判定。
func TestSpecExample(t *testing.T) {
	cfg := Config{
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
	}
	m, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := []Job{
		job(kv("os", "linux"), kv("ver", "1"), kv("tag", "a"), kv("experimental", "true")),
		job(kv("os", "linux"), kv("ver", "2"), kv("tag", "b"), kv("experimental", "true")),
		job(kv("os", "win"), kv("ver", "2"), kv("tag", "b"), kv("experimental", "true")),
		job(kv("os", "mac"), kv("ver", "2")),
		job(kv("os", "mac"), kv("tag", "c")),
	}
	if !reflect.DeepEqual(m.Jobs, want) {
		t.Fatalf("jobs mismatch:\n got %+v\nwant %+v", m.Jobs, want)
	}
}

func TestExpand(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want []Job
	}{
		{
			name: "笛卡尔积次序：首轴变化最慢",
			cfg: Config{Axes: []Axis{
				{Key: "a", Values: []string{"1", "2"}},
				{Key: "b", Values: []string{"x", "y"}},
			}},
			want: []Job{
				job(kv("a", "1"), kv("b", "x")),
				job(kv("a", "1"), kv("b", "y")),
				job(kv("a", "2"), kv("b", "x")),
				job(kv("a", "2"), kv("b", "y")),
			},
		},
		{
			name: "exclude 先于 include：被剔除的组合不再被匹配",
			cfg: Config{
				Axes:    []Axis{{Key: "os", Values: []string{"linux", "win"}}},
				Exclude: []map[string]string{{"os": "linux"}},
				Include: []map[string]string{{"os": "linux", "tag": "a"}},
			},
			want: []Job{
				job(kv("os", "win")),
				job(kv("os", "linux"), kv("tag", "a")),
			},
		},
		{
			name: "exclude 值不在轴取值内是合法的，只是匹配不到",
			cfg: Config{
				Axes:    []Axis{{Key: "os", Values: []string{"linux", "win"}}},
				Exclude: []map[string]string{{"os": "mac"}},
			},
			want: []Job{
				job(kv("os", "linux")),
				job(kv("os", "win")),
			},
		},
		{
			name: "轴值不被改写，附加键被后项覆盖",
			cfg: Config{
				Axes: []Axis{{Key: "os", Values: []string{"linux"}}},
				Include: []map[string]string{
					{"os": "linux", "tag": "a"},
					{"os": "linux", "tag": "b", "x": "1"},
				},
			},
			want: []Job{
				job(kv("os", "linux"), kv("tag", "b"), kv("x", "1")),
			},
		},
		{
			name: "纯轴部分 include 匹配成功且不追加",
			cfg: Config{Axes: []Axis{
				{Key: "os", Values: []string{"linux", "win"}},
				{Key: "ver", Values: []string{"1", "2"}},
			},
				Include: []map[string]string{{"os": "linux", "ver": "1"}},
			},
			want: []Job{
				job(kv("os", "linux"), kv("ver", "1")),
				job(kv("os", "linux"), kv("ver", "2")),
				job(kv("os", "win"), kv("ver", "1")),
				job(kv("os", "win"), kv("ver", "2")),
			},
		},
		{
			name: "追加组合不被后续项扩充，键值相同也再追加",
			cfg: Config{
				Axes: []Axis{{Key: "os", Values: []string{"linux"}}},
				Include: []map[string]string{
					{"os": "mac"},
					{"os": "mac", "tag": "c"},
					{"os": "mac"},
				},
			},
			want: []Job{
				job(kv("os", "linux")),
				job(kv("os", "mac")),
				job(kv("os", "mac"), kv("tag", "c")),
				job(kv("os", "mac")),
			},
		},
		{
			name: "空轴部分在基础组合全部被剔除时变为追加",
			cfg: Config{
				Axes:    []Axis{{Key: "os", Values: []string{"linux"}}},
				Exclude: []map[string]string{{"os": "linux"}},
				Include: []map[string]string{{"tag": "x"}},
			},
			want: []Job{
				job(kv("tag", "x")),
			},
		},
		{
			name: "轴名为 experimental 且值恰为 true 时是试验性作业",
			cfg: Config{Axes: []Axis{
				{Key: "experimental", Values: []string{"true", "false", "truex"}},
			}},
			want: []Job{
				job(kv("experimental", "true")),
				job(kv("experimental", "false")),
				job(kv("experimental", "truex")),
			},
		},
		{
			name: "追加组合可因附加 experimental=true 成为试验性作业",
			cfg: Config{
				Axes:    []Axis{{Key: "os", Values: []string{"linux"}}},
				Include: []map[string]string{{"os": "mac", "experimental": "true"}},
			},
			want: []Job{
				job(kv("os", "linux")),
				job(kv("os", "mac"), kv("experimental", "true")),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := New(tc.cfg)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if !reflect.DeepEqual(m.Jobs, tc.want) {
				t.Fatalf("jobs mismatch:\n got %+v\nwant %+v", m.Jobs, tc.want)
			}
		})
	}
}

// 恰 256 个组合合法，257 个报矩阵过大。
func TestBoundary256And257(t *testing.T) {
	axes := []Axis{
		{Key: "a", Values: []string{"1", "2", "3", "4"}},
		{Key: "b", Values: []string{"1", "2", "3", "4"}},
		{Key: "c", Values: []string{"1", "2", "3", "4"}},
		{Key: "d", Values: []string{"1", "2", "3", "4"}},
	}
	m, err := New(Config{Axes: axes})
	if err != nil {
		t.Fatalf("256 jobs should be legal: %v", err)
	}
	if len(m.Jobs) != 256 {
		t.Fatalf("got %d jobs, want 256", len(m.Jobs))
	}
	// 追加一个组合到 257。
	_, err = New(Config{
		Axes:    axes,
		Include: []map[string]string{{"a": "zzz"}},
	})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("257 jobs: got %v, want ErrTooLarge", err)
	}
	// 基础组合本身超过 256。
	big := append([]Axis{}, axes...)
	big[0] = Axis{Key: "a", Values: []string{"1", "2", "3", "4", "5"}}
	if _, err = New(Config{Axes: big}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("320 jobs: got %v, want ErrTooLarge", err)
	}
}

func TestEmptyMatrix(t *testing.T) {
	_, err := New(Config{
		Axes:    []Axis{{Key: "os", Values: []string{"linux"}}},
		Exclude: []map[string]string{{"os": "linux"}},
	})
	if !errors.Is(err, ErrEmpty) {
		t.Fatalf("got %v, want ErrEmpty", err)
	}
}

func TestInvalidParams(t *testing.T) {
	valid := Config{Axes: []Axis{{Key: "os", Values: []string{"linux"}}}}
	cases := []struct {
		name string
		mut  func(*Config)
	}{
		{"零个轴", func(c *Config) { c.Axes = nil }},
		{"六个轴", func(c *Config) {
			c.Axes = []Axis{
				{Key: "a", Values: []string{"1"}}, {Key: "b", Values: []string{"1"}},
				{Key: "c", Values: []string{"1"}}, {Key: "d", Values: []string{"1"}},
				{Key: "e", Values: []string{"1"}}, {Key: "f", Values: []string{"1"}},
			}
		}},
		{"轴键重复", func(c *Config) {
			c.Axes = []Axis{{Key: "os", Values: []string{"1"}}, {Key: "os", Values: []string{"2"}}}
		}},
		{"轴键为空", func(c *Config) { c.Axes = []Axis{{Key: "", Values: []string{"1"}}} }},
		{"轴键 33 字节", func(c *Config) {
			c.Axes = []Axis{{Key: strings.Repeat("k", 33), Values: []string{"1"}}}
		}},
		{"轴无值", func(c *Config) { c.Axes = []Axis{{Key: "os"}} }},
		{"轴 11 个值", func(c *Config) {
			c.Axes = []Axis{{Key: "os", Values: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"}}}
		}},
		{"轴值重复", func(c *Config) { c.Axes = []Axis{{Key: "os", Values: []string{"1", "1"}}} }},
		{"轴值 65 字节", func(c *Config) {
			c.Axes = []Axis{{Key: "os", Values: []string{strings.Repeat("v", 65)}}}
		}},
		{"exclude 65 项", func(c *Config) {
			c.Exclude = make([]map[string]string, 65)
			for i := range c.Exclude {
				c.Exclude[i] = map[string]string{"os": "x"}
			}
		}},
		{"exclude 空项", func(c *Config) { c.Exclude = []map[string]string{{}} }},
		{"exclude 键非轴键", func(c *Config) { c.Exclude = []map[string]string{{"tag": "x"}} }},
		{"exclude 值超长", func(c *Config) {
			c.Exclude = []map[string]string{{"os": strings.Repeat("v", 65)}}
		}},
		{"include 65 项", func(c *Config) {
			c.Include = make([]map[string]string, 65)
			for i := range c.Include {
				c.Include[i] = map[string]string{"tag": "x"}
			}
		}},
		{"include 空项", func(c *Config) { c.Include = []map[string]string{{}} }},
		{"include 键超长", func(c *Config) {
			c.Include = []map[string]string{{strings.Repeat("k", 33): "x"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid
			tc.mut(&cfg)
			if _, err := New(cfg); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v, want ErrInvalid", err)
			}
		})
	}
	// 边界合法：键 32 字节、值 64 字节、exclude/include 各 64 项。
	cfg := Config{Axes: []Axis{{Key: strings.Repeat("k", 32), Values: []string{strings.Repeat("v", 64), ""}}}}
	for i := 0; i < 64; i++ {
		cfg.Exclude = append(cfg.Exclude, map[string]string{strings.Repeat("k", 32): "no-such-" + strings.Repeat("v", 54) + string(rune('0'+i%10))})
		cfg.Include = append(cfg.Include, map[string]string{"t" + strings.Repeat("x", 31): ""})
	}
	if _, err := New(cfg); err != nil {
		t.Fatalf("boundary config should be legal: %v", err)
	}
}

// 拒绝次序：参数非法优先于矩阵过大与空矩阵。
func TestRejectionOrder(t *testing.T) {
	tooLarge := Config{Axes: []Axis{
		{Key: "a", Values: []string{"1", "2", "3", "4"}},
		{Key: "b", Values: []string{"1", "2", "3", "4"}},
		{Key: "c", Values: []string{"1", "2", "3", "4"}},
		{Key: "d", Values: []string{"1", "2", "3", "4"}},
		{Key: "e", Values: []string{"1", "2", "3", "4"}},
	}}
	if _, err := New(tooLarge); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
	invalidAndTooLarge := tooLarge
	invalidAndTooLarge.Exclude = []map[string]string{{"not-axis": "x"}}
	if _, err := New(invalidAndTooLarge); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid+tooLarge: got %v, want ErrInvalid", err)
	}
	invalidAndEmpty := Config{
		Axes:    []Axis{{Key: "os", Values: []string{"linux"}}},
		Exclude: []map[string]string{{"os": "linux"}, {}},
	}
	if _, err := New(invalidAndEmpty); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid+empty: got %v, want ErrInvalid", err)
	}
}

// 相同配置展开结果逐字节相同（map 迭代序不影响输出）。
func TestDeterministic(t *testing.T) {
	cfg := Config{
		Axes: []Axis{
			{Key: "os", Values: []string{"linux", "win"}},
			{Key: "ver", Values: []string{"1", "2"}},
		},
		Exclude: []map[string]string{{"os": "win", "ver": "1"}},
		Include: []map[string]string{
			{"os": "linux", "tag": "a", "z": "1", "y": "2"},
			{"os": "mac", "x": "1", "w": "2"},
			{"experimental": "true"},
		},
	}
	first, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < 50; i++ {
		m, err := New(cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if !reflect.DeepEqual(m.Jobs, first.Jobs) {
			t.Fatalf("run %d differs:\n got %+v\nwant %+v", i, m.Jobs, first.Jobs)
		}
	}
}

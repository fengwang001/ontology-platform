package plan

import (
	"errors"
	"fmt"
	"testing"
)

func hasSet(names ...string) func(string) bool {
	m := map[string]bool{}
	for _, s := range names {
		m[s] = true
	}
	return func(s string) bool { return m[s] }
}

func simulate(t *testing.T, have map[string]bool, steps []Step) {
	t.Helper()
	for i, s := range steps {
		if !have[s.Old] {
			t.Fatalf("步骤 %d: 旧名 %q 不存在", i, s.Old)
		}
		if have[s.New] {
			t.Fatalf("步骤 %d: 目标名 %q 已存在，发生覆盖", i, s.New)
		}
		delete(have, s.Old)
		have[s.New] = true
	}
}

func TestBuildConflicts(t *testing.T) {
	cases := []struct {
		name  string
		names []string
		reqs  []Request
		want  error
	}{
		{"目标已存在", []string{"a", "x"}, []Request{{"a", "x"}}, ErrTargetExists},
		{"重复新名", []string{"a", "b"}, []Request{{"a", "c"}, {"b", "c"}}, ErrDuplicateTarget},
		{"重复旧名", []string{"a"}, []Request{{"a", "x"}, {"a", "y"}}, ErrDuplicateSource},
		{"旧名不存在", []string{"a"}, []Request{{"ghost", "x"}}, ErrMissingSource},
		{"目标会被搬走则合法", []string{"a", "b"}, []Request{{"a", "b"}, {"b", "c"}}, nil},
		{"自环不遮蔽占用", []string{"a", "b"}, []Request{{"a", "a"}, {"b", "a"}}, ErrTargetExists},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := map[string]bool{}
			for _, s := range tc.names {
				before[s] = true
			}
			_, err := Build(tc.reqs, hasSet(tc.names...))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want errors.Is %v", err, tc.want)
			}
			after := map[string]bool{}
			for _, s := range tc.names {
				after[s] = true
			}
			for s := range before {
				if !after[s] {
					t.Fatalf("冲突检测修改了命名空间: %q", s)
				}
			}
		})
	}
}

func TestBuildShapes(t *testing.T) {
	cases := []struct {
		name     string
		names    []string
		reqs     []Request
		wantSeq  []string
		wantTemp int
	}{
		{"空请求", []string{"a"}, nil, nil, 0},
		{"单请求", []string{"a"}, []Request{{"a", "b"}}, []string{"a>b"}, 0},
		{"链", []string{"a", "b"}, []Request{{"a", "b"}, {"b", "c"}}, []string{"b>c", "a>b"}, 0},
		{"自环无操作", []string{"a"}, []Request{{"a", "a"}}, nil, 0},
		{"二元环", []string{"a", "b"}, []Request{{"a", "b"}, {"b", "a"}},
			[]string{"a>~tmp-0", "b>a", "~tmp-0>b"}, 1},
		{"三元环", []string{"a", "b", "c"}, []Request{{"a", "b"}, {"b", "c"}, {"c", "a"}},
			[]string{"a>~tmp-0", "c>a", "b>c", "~tmp-0>b"}, 1},
		{"空串名字", []string{""}, []Request{{"", "x"}}, []string{">x"}, 0},
		{"路径分隔符", []string{"a/b", `c\d`}, []Request{{"a/b", `c\d`}, {`c\d`, "e"}}, []string{`c\d>e`, "a/b>c\\d"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Build(tc.reqs, hasSet(tc.names...))
			if err != nil {
				t.Fatal(err)
			}
			if p.Temps != tc.wantTemp {
				t.Fatalf("Temps=%d, want %d", p.Temps, tc.wantTemp)
			}
			var got []string
			for _, s := range p.Steps {
				got = append(got, s.Old+">"+s.New)
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.wantSeq) {
				t.Fatalf("steps=%v, want %v", got, tc.wantSeq)
			}
			have := map[string]bool{}
			for _, s := range tc.names {
				have[s] = true
			}
			simulate(t, have, p.Steps)
			if len(have) != len(tc.names) {
				t.Fatalf("执行后名字数=%d, want %d", len(have), len(tc.names))
			}
		})
	}
}

func TestBuildCycleTempCount(t *testing.T) {
	var names []string
	var reqs []Request
	for i := 0; i < 10; i++ {
		a := fmt.Sprintf("a%d", i)
		b := fmt.Sprintf("b%d", i)
		names = append(names, a, b)
		reqs = append(reqs, Request{a, b}, Request{b, a})
	}
	p, err := Build(reqs, hasSet(names...))
	if err != nil {
		t.Fatal(err)
	}
	if p.Temps != 10 {
		t.Fatalf("10 个环用了 %d 个临时名", p.Temps)
	}
	have := map[string]bool{}
	for _, s := range names {
		have[s] = true
	}
	simulate(t, have, p.Steps)
}

func TestBuildTempPreoccupied(t *testing.T) {
	names := []string{"a", "b"}
	for i := 0; i < 100; i++ {
		names = append(names, fmt.Sprintf("~tmp-%d", i))
	}
	p, err := Build([]Request{{"a", "b"}, {"b", "a"}}, hasSet(names...))
	if err != nil {
		t.Fatal(err)
	}
	if p.Temps != 1 || p.Steps[0].New != "~tmp-100" {
		t.Fatalf("临时名未跳过占用: %+v", p.Steps)
	}
	have := map[string]bool{}
	for _, s := range names {
		have[s] = true
	}
	simulate(t, have, p.Steps)
}

func TestBuildLookupsLinear(t *testing.T) {
	const n = 50000
	names := make([]string, 0, n)
	reqs := make([]Request, 0, n)
	for i := 0; i < n; i++ {
		names = append(names, fmt.Sprintf("n%d", i))
		reqs = append(reqs, Request{fmt.Sprintf("n%d", i), fmt.Sprintf("m%d", i)})
	}
	p, err := Build(reqs, hasSet(names...))
	if err != nil {
		t.Fatal(err)
	}
	if limit := 4 * (n + len(names)); p.Lookups() > limit {
		t.Fatalf("查找次数 %d 超过上限 %d", p.Lookups(), limit)
	}
}

func TestBuildDeterministic(t *testing.T) {
	base := []Request{{"a", "b"}, {"b", "c"}, {"x", "y"}, {"p", "q"}, {"q", "r"}}
	want, err := Build(base, hasSet("a", "b", "x", "p", "q"))
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 20; round++ {
		shuffled := make([]Request, len(base))
		for i := range base {
			shuffled[i] = base[(i+round)%len(base)]
		}
		shuffled[0], shuffled[len(shuffled)-1] = shuffled[len(shuffled)-1], shuffled[0]
		got, err := Build(shuffled, hasSet("a", "b", "x", "p", "q"))
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(got.Steps) != fmt.Sprint(want.Steps) {
			t.Fatalf("第 %d 轮打乱后步骤序列不同: %v", round, got.Steps)
		}
	}
}

func TestBuildManyRequestsFewNames(t *testing.T) {
	reqs := make([]Request, 0, 10000)
	for i := 0; i < 10000; i++ {
		reqs = append(reqs, Request{"a", fmt.Sprintf("x%d", i)})
	}
	_, err := Build(reqs, hasSet("a"))
	if !errors.Is(err, ErrDuplicateSource) {
		t.Fatalf("err=%v, want ErrDuplicateSource", err)
	}
}

package ontology

import (
	"bufio"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func nonNil(s []int) []int {
	if s == nil {
		return []int{}
	}
	return s
}

func intersect(a, b []int) bool {
	set := map[int]struct{}{}
	for _, x := range a {
		set[x] = struct{}{}
	}
	for _, x := range b {
		if _, ok := set[x]; ok {
			return true
		}
	}
	return false
}

// TestDifferentialAgainstNaive 与朴素模型对照 2000 组随机读序列，
// Cp 取多种小值；日志打印每组输入、输出与判定依据。
func TestDifferentialAgainstNaive(t *testing.T) {
	logPath := filepath.Join(os.TempDir(), "readahead_diff.log")
	lf, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	w := bufio.NewWriter(lf)
	defer w.Flush()

	rng := rand.New(rand.NewSource(20261001))
	cpChoices := []int{1, 1, 2, 2, 3, 4, 5, 8}

	const totalCases = 2000
	for tc := 0; tc < totalCases; tc++ {
		n := 1 + rng.Intn(40)
		i := 1 + rng.Intn(5)
		m := i + rng.Intn(8)
		cp := cpChoices[rng.Intn(len(cpChoices))]

		impl, err := NewReadahead(n, i, m, cp)
		if err != nil {
			t.Fatalf("case %d: NewReadahead(%d,%d,%d,%d): %v", tc, n, i, m, cp, err)
		}
		na := newNaive(n, i, m, cp)

		buf := bufio.NewWriter(w)
		fmt.Fprintln(buf, "================================================")
		fmt.Fprintf(buf, "case %d: N=%d I=%d M=%d Cp=%d\n", tc, n, i, m, cp)

		steps := 3 + rng.Intn(20)
		for s := 0; s < steps; s++ {
			p, nn := genRead(rng, n)

			d1, a1, e1, err1 := impl.Read(p, nn)
			d2, a2, e2, code := na.step(p, nn)

			// 先把朴素模型判定日志落盘，便于失败时直接查看输入、输出与依据。
			buf.WriteString(na.log.String())
			na.log.Reset()

			switch code {
			case 1:
				if !errors.Is(err1, ErrInvalidArg) {
					buf.Flush()
					t.Fatalf("case %d step %d Read(%d,%d): 期望 ErrInvalidArg, got %v (日志 %s)",
						tc, s, p, nn, err1, logPath)
				}
			case 2:
				if !errors.Is(err1, ErrOutOfRange) {
					buf.Flush()
					t.Fatalf("case %d step %d Read(%d,%d): 期望 ErrOutOfRange, got %v (日志 %s)",
						tc, s, p, nn, err1, logPath)
				}
			default:
				if err1 != nil {
					buf.Flush()
					t.Fatalf("case %d step %d Read(%d,%d): 未期望错误 %v", tc, s, p, nn, err1)
				}
				if !reflect.DeepEqual(nonNil(d1), d2) ||
					!reflect.DeepEqual(nonNil(a1), a2) ||
					!reflect.DeepEqual(nonNil(e1), e2) {
					buf.Flush()
					t.Fatalf("case %d step %d Read(%d,%d) 输出不一致:\n impl demand=%v ahead=%v evicted=%v\n naive demand=%v ahead=%v evicted=%v (日志 %s)",
						tc, s, p, nn, d1, a1, e1, d2, a2, e2, logPath)
				}
				if got := impl.State(); !reflect.DeepEqual(got, na.snapshot()) {
					buf.Flush()
					t.Fatalf("case %d step %d Read(%d,%d) 状态不一致:\n impl=%+v\n naive=%+v (日志 %s)",
						tc, s, p, nn, got, na.snapshot(), logPath)
				}

				// 全局不变量。
				if len(impl.lru) > cp {
					t.Fatalf("case %d: 缓存页数 %d>Cp=%d", tc, len(impl.lru), cp)
				}
				if st := impl.State(); st.Window.Wsz > m {
					t.Fatalf("case %d: wsz=%d>M=%d", tc, st.Window.Wsz, m)
				}
				for _, q := range d1 {
					if q < 0 || q >= n {
						t.Fatalf("case %d: demand 页越界 %d", tc, q)
					}
				}
				for _, q := range a1 {
					if q < 0 || q >= n {
						t.Fatalf("case %d: ahead 页越界 %d", tc, q)
					}
				}
				if intersect(d1, a1) {
					t.Fatalf("case %d: demand 与 ahead 相交: %v %v", tc, d1, a1)
				}
			}

			// 随机插入 DropCache / State，两边必须同样演化。
			if rng.Intn(6) == 0 {
				impl.DropCache()
				na.dropCache()
				buf.WriteString(na.log.String())
				na.log.Reset()
			}
			if rng.Intn(8) == 0 {
				_ = impl.State()
			}
		}
		buf.Flush()
	}
	t.Logf("对照 %d 组随机读序列全部一致；完整输入/输出/判定日志：%s", totalCases, logPath)
}

func genRead(rng *rand.Rand, n int) (int, int) {
	switch rng.Intn(10) {
	case 0: // 非法参数
		if rng.Intn(2) == 0 {
			return -1 - rng.Intn(3), 1 + rng.Intn(3)
		}
		return rng.Intn(n + 1), -rng.Intn(2)
	case 1: // 可能越界
		return rng.Intn(n + 2), 1 + rng.Intn(5)
	default:
		p := rng.Intn(n)
		nn := 1 + rng.Intn(5)
		if p+nn > n {
			nn = n - p
		}
		if nn < 1 {
			nn = 1
		}
		return p, nn
	}
}

type readResult struct {
	d, a, e []int
	err     error
	st      State
}

// TestReplayDeterministic 验证相同读序列重放得到完全相同的输出与状态。
func TestReplayDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(777))
	const n, i, m, cp = 60, 2, 16, 5

	run := func() []readResult {
		r, _ := NewReadahead(n, i, m, cp)
		var out []readResult
		for k := 0; k < 400; k++ {
			p, nn := genRead(rng, n)
			d, a, e, err := r.Read(p, nn)
			out = append(out, readResult{
				d:   append([]int(nil), d...),
				a:   append([]int(nil), a...),
				e:   append([]int(nil), e...),
				err: err,
				st:  r.State(),
			})
			if k%9 == 0 {
				r.DropCache()
			}
		}
		return out
	}

	rng.Seed(777)
	first := run()
	rng.Seed(777)
	second := run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("相同序列重放结果不同（长度 %d vs %d）", len(first), len(second))
	}
}

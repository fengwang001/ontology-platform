package api_test

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
	"ontology/ljoin"
)

func c(s ljoin.Side, o ljoin.Op, id, k string) ljoin.Change {
	return ljoin.Change{Side: s, Op: o, ID: id, Key: k}
}

func nineSteps() []ljoin.Change {
	L, R, I, D := ljoin.L, ljoin.R, ljoin.Ins, ljoin.Del
	return []ljoin.Change{c(R, I, "r1", "x"), c(L, I, "l1", "x"), c(L, I, "l2", "y"), c(L, I, "l3", "y"),
		c(R, I, "r2", "y"), c(R, I, "r3", "y"), c(R, D, "r2", ""), c(R, D, "r3", ""), c(R, D, "r1", "")}
}

// naiveView 对跟踪中的两表做朴素左外连接。
func naiveView(l, r map[string]string) map[api.VRow]int {
	byKey := map[string][]string{}
	for id, k := range r {
		byKey[k] = append(byKey[k], id)
	}
	out := map[api.VRow]int{}
	for id, k := range l {
		if rs := byKey[k]; len(rs) > 0 {
			for _, rid := range rs {
				out[api.VRow{L: id, R: rid}] = 1
			}
		} else {
			out[api.VRow{L: id}] = 1
		}
	}
	return out
}

// 随机合法变更序列：多档种子循环生成，逐批对拍朴素连接（不变量 1）。
func TestViewMatchesNaiveJoin(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 2026} {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			j, tb := api.New(1000), [2]map[string]string{{}, {}}
			for b := 0; b < 20; b++ {
				var batch []ljoin.Change
				for n := 0; n < 5; n++ {
					s := rng.Intn(2)
					pre := []string{"l", "r"}[s]
					id := fmt.Sprintf("%s%d", pre, rng.Intn(8))
					_, dup := tb[s][id]
					switch {
					case dup && rng.Intn(2) == 0:
						batch = append(batch, c(ljoin.Side(s), ljoin.Del, id, ""))
						delete(tb[s], id)
					case !dup:
						k := fmt.Sprintf("k%d", rng.Intn(4))
						batch = append(batch, c(ljoin.Side(s), ljoin.Ins, id, k))
						tb[s][id] = k
					}
				}
				if _, err := j.Apply(batch); err != nil {
					t.Fatal(err)
				}
				if !maps.Equal(j.View(), naiveView(tb[0], tb[1])) {
					t.Fatalf("批 %d: 视图与朴素连接不一致", b)
				}
			}
		})
	}
}

// 四类故障注入各有可判定且互不相同的哨兵错误。
func TestErrorsDistinct(t *testing.T) {
	cases := map[ljoin.Change]error{
		c(ljoin.L, ljoin.Ins, "", "x"):   ljoin.ErrEmptyField,
		c(ljoin.L, ljoin.Ins, "l1", "y"): ljoin.ErrDupID,
		c(ljoin.R, ljoin.Del, "rx", ""):  ljoin.ErrNoID,
		c(ljoin.R, ljoin.Ins, "r9", "x"): ljoin.ErrTooMany,
	}
	for chg, want := range cases {
		j := api.New(2)
		_, err := j.Apply([]ljoin.Change{c(ljoin.L, ljoin.Ins, "l1", "x"), c(ljoin.R, ljoin.Ins, "r1", "x")})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := j.Apply([]ljoin.Change{chg}); !errors.Is(err, want) {
			t.Fatalf("%v: got %v want %v", chg, err, want)
		}
	}
	sents := []error{ljoin.ErrEmptyField, ljoin.ErrDupID, ljoin.ErrNoID, ljoin.ErrTooMany}
	for i, a := range sents {
		for _, b := range sents[i+1:] {
			if errors.Is(a, b) || a == b {
				t.Fatalf("哨兵错误不互异: %v vs %v", a, b)
			}
		}
	}
}

// 批中任一条被拒则整批不生效，之后仍可正常使用（不变量 4）。
func TestRejectNoTrace(t *testing.T) {
	j := api.New(10)
	if _, err := j.Apply([]ljoin.Change{c(ljoin.L, ljoin.Ins, "l1", "x"), c(ljoin.R, ljoin.Ins, "r1", "x")}); err != nil {
		t.Fatal(err)
	}
	before := j.View()
	bad := []ljoin.Change{c(ljoin.L, ljoin.Ins, "l2", "x"), c(ljoin.L, ljoin.Ins, "l2", "z")}
	if _, err := j.Apply(bad); !errors.Is(err, ljoin.ErrDupID) {
		t.Fatalf("got %v", err)
	}
	if !maps.Equal(j.View(), before) {
		t.Fatal("被拒批次留下痕迹")
	}
	if _, err := j.Apply([]ljoin.Change{c(ljoin.L, ljoin.Ins, "l2", "x")}); err != nil {
		t.Fatal("被拒后无法继续使用:", err)
	}
}

// 并发只读：N 个 goroutine 同时 View/SelfCheck，视图逐行相同。
func TestConcurrentViews(t *testing.T) {
	j := api.New(1000)
	if _, err := j.Apply(nineSteps()); err != nil {
		t.Fatal(err)
	}
	want := j.View()
	var wg sync.WaitGroup
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 50; n++ {
				if !maps.Equal(j.View(), want) {
					t.Error("视图不一致")
				}
				if err := j.SelfCheck(); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
}

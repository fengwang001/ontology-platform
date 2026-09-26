package main

import (
	"fmt"
	"math/rand"
	"os"
	"sync"
	"time"

	"ontology/api"
	"ontology/dist"
	"ontology/ops"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
	} else {
		fmt.Println("OK  ", name)
	}
}

func randStr(r *rand.Rand, n int) string {
	const alpha = "abcd"
	b := make([]byte, n)
	for i := range b {
		b[i] = alpha[r.Intn(len(alpha))]
	}
	return string(b)
}

// bandTime 返回对长度 n 的随机串反复计算距离的最短耗时（计数器不可导出，用耗时侧面验证）。
func bandTime(n int) time.Duration {
	r := rand.New(rand.NewSource(int64(n)))
	a, b := randStr(r, n), randStr(r, n)
	best := time.Hour
	for i := 0; i < 5; i++ {
		t0 := time.Now()
		_, _ = dist.Distance(a, b, 3)
		if d := time.Since(t0); d < best {
			best = d
		}
	}
	return best
}

func main() {
	d, err := dist.Distance("abc", "yabd", 2)
	check("dist: d(abc,yabd)=2", err == nil && d == 2)
	_, err = dist.Distance("abc", "yabd", 1)
	check("dist: k=1 触发 ErrExceedsCap", err == dist.ErrExceedsCap)

	sc, err := ops.EditScript("abc", "yabd", 2)
	got, aerr := ops.Apply("abc", sc)
	check("ops: 脚本应用后 a 变 b 且长度=距离", err == nil && aerr == nil && got == "yabd" && len(sc) == 2)

	c, err := api.New(2)
	check("api: New(2) 成功且 SelfCheck 通过", err == nil && c.SelfCheck() == nil)
	_, e1 := api.New(-1)
	_, e2 := c.Distance(string(make([]byte, 1<<20)), "x")
	c1, _ := api.New(1)
	_, e3 := c1.Distance("abc", "yabd")
	check("api: 三类错误可判定且互不相同",
		e1 == api.ErrNegativeK && e2 == api.ErrTooLong && e3 == api.ErrExceedsCap &&
			e1 != e2 && e2 != e3 && e1 != e3)
	d2, err := c.Distance("abc", "yabd")
	check("api: 被拒后状态不变仍可正常计算", err == nil && d2 == 2)

	// n 放大 4 倍，耗时若随 n² 增长应为 ~16 倍；band 只算 2k+1 带宽，应 ~4 倍。
	t1, t2 := bandTime(20000), bandTime(80000)
	check("dist: 大 n 下单元数不随 n² 增长(耗时比<9)", t2 < t1*9)

	// 并发：8 个 goroutine 与串行结果逐对相同。
	cr, _ := api.New(10)
	r := rand.New(rand.NewSource(7))
	pairs := make([][2]string, 32)
	want := make([]int, len(pairs))
	for i := range pairs {
		pairs[i] = [2]string{randStr(r, r.Intn(9)), randStr(r, r.Intn(9))}
		want[i], _ = cr.Distance(pairs[i][0], pairs[i][1])
	}
	res := make([][]int, 8)
	var wg sync.WaitGroup
	for g := range res {
		res[g] = make([]int, len(pairs))
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i, p := range pairs {
				res[g][i], _ = cr.Distance(p[0], p[1])
			}
		}(g)
	}
	wg.Wait()
	same := true
	for g := range res {
		for i := range pairs {
			same = same && res[g][i] == want[i]
		}
	}
	check("api: 并发结果与串行逐对相同", same)

	if failed {
		os.Exit(1)
	}
}

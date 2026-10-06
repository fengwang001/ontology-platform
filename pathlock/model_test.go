package pathlock

import (
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// oracle 是独立的朴素对照模型：map + 全量扫描，刻意不共享 Service 的
// 任何数据结构，作为随机操作序列下的裁决基准。
type oracle struct {
	locks  map[string]string // path -> owner
	nextID int64
}

func newOracle() *oracle { return &oracle{locks: map[string]string{}} }

func isAnc(a, b string) bool { return b != a && strings.HasPrefix(b, a+"/") }

func (o *oracle) acquire(user, np string) (bool, int, string) {
	if owner, ok := o.locks[np]; ok {
		if owner == user {
			return false, ErrCodeSelfHeld, np
		}
		return false, ErrCodeOtherHeld, np
	}
	cpath := ""
	conflict := false
	for p, owner := range o.locks {
		if (isAnc(np, p) || isAnc(p, np)) && owner != user {
			conflict = true
			if cpath == "" || len(p) < len(cpath) ||
				(len(p) == len(cpath) && p < cpath) {
				cpath = p
			}
		}
	}
	if conflict {
		return false, ErrCodeAncestorConflict, cpath
	}
	o.nextID++
	o.locks[np] = user
	return true, 0, ""
}

func (o *oracle) verify(user string, nps []string) []Conflict {
	conf := map[string]string{}
	for _, np := range nps {
		if owner, ok := o.locks[np]; ok && owner != user {
			conf[np] = owner
		}
		for p, owner := range o.locks {
			if owner != user && (isAnc(np, p) || isAnc(p, np)) {
				conf[p] = owner
			}
		}
	}
	out := make([]Conflict, 0, len(conf))
	for p, owner := range conf {
		out = append(out, Conflict{Path: p, Owner: owner})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func codeOf(err error) int {
	if err == nil {
		return -1
	}
	if le, ok := err.(*LockError); ok {
		return le.Code
	}
	return -2
}

func dedupPaths(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, x := range in {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// TestDifferentialRandom 随机操作序列，逐裁决比对 Service 与朴素模型，
// 并在结束时比对整个锁世界。
func TestDifferentialRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261006, 42))
	users := []string{"u1", "u2", "u3", "admin"}
	svc := NewService()
	orc := newOracle()
	svc.now = func() time.Time { return time.Unix(0, orc.nextID) }

	names := []string{"a", "b", "c", "a0", "a1", "x", "y"}
	randomPath := func() string {
		depth := 1 + rng.IntN(4)
		parts := make([]string, depth)
		for i := range parts {
			parts[i] = names[rng.IntN(len(names))]
		}
		return strings.Join(parts, "/")
	}

	const steps = 4000
	for step := 0; step < steps; step++ {
		user := users[rng.IntN(len(users))]
		p := randomPath()
		np, perr := Normalize(p)

		switch rng.IntN(4) {
		case 0, 1:
			_, serr := svc.Acquire(user, p)
			if perr != nil {
				if codeOf(serr) != ErrCodeInvalidPath {
					t.Fatalf("step %d 非法路径裁决错误: %v", step, serr)
				}
				continue
			}
			ok, code, cpath := orc.acquire(user, np)
			got := codeOf(serr)
			if ok {
				if serr != nil {
					t.Fatalf("step %d 模型允许服务拒绝: %v", step, serr)
				}
			} else {
				if got != code {
					t.Fatalf("step %d acquire(%q,%q) code 服务=%d 模型=%d",
						step, user, np, got, code)
				}
				le := asLockErr(t, serr)
				if le.hasLock && le.Conflict.Path != cpath {
					t.Fatalf("step %d 冲突锁 服务=%q 模型=%q",
						step, le.Conflict.Path, cpath)
				}
			}
			t.Logf("step %d acquire user=%q path=%q 实际code=%d 判定=与朴素模型一致",
				step, user, np, got)

		case 2:
			id, exists := "", false
			if perr == nil {
				if l, ok := treapGet(svc.root, np); ok {
					id, exists = l.ID, true
				}
			}
			force := rng.IntN(2) == 0
			_, serr := svc.Release(user, id, force)
			if force && !IsAdmin(user) {
				if codeOf(serr) != ErrCodeNotAdmin {
					t.Fatalf("step %d 应 not-admin: %v", step, serr)
				}
				continue
			}
			owner, inModel := orc.locks[np]
			if !exists || !inModel {
				if codeOf(serr) != ErrCodeLockNotFound {
					t.Fatalf("step %d 缺失锁应 not-found: %v", step, serr)
				}
				continue
			}
			if !force && owner != user {
				if codeOf(serr) != ErrCodeNotOwner {
					t.Fatalf("step %d 应 not-owner: %v", step, serr)
				}
				continue
			}
			if serr != nil {
				t.Fatalf("step %d 模型允许释放服务拒绝: %v", step, serr)
			}
			delete(orc.locks, np)
			t.Logf("step %d release user=%q path=%q force=%v 实际=成功",
				step, user, np, force)

		case 3:
			batch := []string{np}
			if rng.IntN(2) == 0 {
				batch = append(batch, randomPath())
			}
			norm := make([]string, 0, len(batch))
			bad := perr != nil
			for _, bp := range batch {
				x, e := Normalize(bp)
				if e != nil {
					bad = true
					break
				}
				norm = append(norm, x)
			}
			res, serr := svc.Verify(user, batch, false)
			if bad {
				if codeOf(serr) != ErrCodeInvalidPath {
					t.Fatalf("step %d 非法批次路径裁决错误", step)
				}
				continue
			}
			mconf := orc.verify(user, dedupPaths(norm))
			sconf := []Conflict{}
			if !res.Allowed {
				sconf = res.Conflicts
			}
			if len(mconf) != len(sconf) {
				t.Fatalf("step %d 冲突数 服务=%d 模型=%d", step, len(sconf), len(mconf))
			}
			for i := range mconf {
				if mconf[i] != sconf[i] {
					t.Fatalf("step %d 冲突[%d] 服务=%+v 模型=%+v",
						step, i, sconf[i], mconf[i])
				}
			}
			t.Logf("step %d verify user=%q batch=%v 实际allowed=%v 判定=与朴素模型一致",
				step, user, dedupPaths(norm), res.Allowed)
		}
	}

	if len(svc.pathOf) != len(orc.locks) {
		t.Fatalf("终态锁数 服务=%d 模型=%d", len(svc.pathOf), len(orc.locks))
	}
	for p, owner := range orc.locks {
		l, ok := treapGet(svc.root, p)
		if !ok {
			t.Fatalf("模型有锁 %q 服务缺失", p)
		}
		if l.Owner != owner {
			t.Fatalf("锁 %q 持有者 服务=%q 模型=%q", p, l.Owner, owner)
		}
	}
	t.Logf("差分测试完成: %d 步, 终态 %d 把锁, 与朴素模型完全一致",
		steps, len(orc.locks))
}

func pad4(i int) string {
	s := "000000" + itoa(i)
	return s[len(s)-6:]
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

// TestConcurrentAcquire 两用户并发对同一批键（并叠加共同祖先 root）
// 加锁：每个键最终恰好一个成功者，总成功数等于键数。
func TestConcurrentAcquire(t *testing.T) {
	s := NewService()
	const n = 200
	keys := make([]string, n)
	for i := range keys {
		keys[i] = "root/" + pad4(i)
	}
	var wg sync.WaitGroup
	for _, user := range []string{"alice", "bob"} {
		wg.Add(2)
		go func(user string) {
			defer wg.Done()
			for _, k := range keys {
				_, _ = s.Acquire(user, k)
			}
		}(user)
		go func(user string) {
			defer wg.Done()
			_, _ = s.Acquire(user, "root")
		}(user)
	}
	wg.Wait()

	owners := map[string]int{}
	for _, k := range keys {
		l, ok := treapGet(s.root, k)
		if !ok {
			t.Fatalf("键 %q 无人持有", k)
		}
		owners[l.Owner]++
	}
	if _, ok := treapGet(s.root, "root"); !ok {
		t.Fatal("共同祖先 root 无人持有")
	}
	total := owners["alice"] + owners["bob"]
	t.Logf("并发结果: alice=%d bob=%d root有主 total=%d 判定=每键唯一且 total==%d",
		owners["alice"], owners["bob"], total, n)
	if total != n {
		t.Fatalf("成功数 %d != 键数 %d（必须恰好一个成功）", total, n)
	}
}

// TestPerformanceNotLinear 在无关目录堆 n 把锁，测量目标路径上的
// 加锁/校验单次耗时；数据量 4 倍增长时，耗时增长应远低于 4 倍。
func TestPerformanceNotLinear(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过性能测试")
	}
	measure := func(n int) (acq, ver int64) {
		s := NewService()
		for i := 0; i < n; i++ {
			path := "other/t" + pad4(i%10000) + "/d" + pad4(i)
			if _, err := s.Acquire("noise", path); err != nil {
				t.Fatalf("预置锁失败 i=%d: %v", i, err)
			}
		}
		const rounds = 30
		start := time.Now()
		for i := 0; i < rounds; i++ {
			l, err := s.Acquire("probe", "target/path")
			if err == nil {
				if _, err := s.Release("probe", l.ID, false); err != nil {
					t.Fatal(err)
				}
			}
		}
		acq = int64(time.Since(start) / rounds)
		start = time.Now()
		for i := 0; i < rounds; i++ {
			if _, err := s.Verify("probe", []string{"target/path"}, false); err != nil {
				t.Fatal(err)
			}
		}
		ver = int64(time.Since(start) / rounds)
		return
	}

	a1, v1 := measure(2500)
	a2, v2 := measure(10000)
	t.Logf("n=2500: 加锁=%v/次 校验=%v/次", time.Duration(a1), time.Duration(v1))
	t.Logf("n=10000: 加锁=%v/次 校验=%v/次；判定=4倍数据耗时比<3",
		time.Duration(a2), time.Duration(v2))
	if a2 > a1*3 || v2 > v1*3 {
		t.Fatalf("单次开销疑似随总锁数线性增长: %v->%v, %v->%v",
			time.Duration(a1), time.Duration(a2), time.Duration(v1), time.Duration(v2))
	}
}

// BenchmarkAcquire 提供标准基准，便于直接观察加锁复杂度。
func BenchmarkAcquire(b *testing.B) {
	s := NewService()
	for i := 0; i < 100000; i++ {
		if _, err := s.Acquire("noise", "other/t"+pad4(i%100000)+"/d"+pad4(i)); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l, err := s.Acquire("probe", "target/path")
		if err == nil {
			if _, err := s.Release("probe", l.ID, false); err != nil {
				b.Fatal(err)
			}
		} else if codeOf(err) != ErrCodeSelfHeld {
			b.Fatal(err)
		}
	}
}

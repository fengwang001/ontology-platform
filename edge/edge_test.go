package edge_test

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/edge"
	"ontology/purge"
	"ontology/quota"
)

type plane struct {
	meter *quota.Meter
	store *purge.Store
	cache *edge.Cache
}

func newPlane(t *testing.T, lmax int, tenants map[string]quota.Quotas) *plane {
	t.Helper()
	m := quota.NewMeter()
	for id, q := range tenants {
		if err := m.Register(id, q); err != nil {
			t.Fatal(err)
		}
	}
	s := purge.NewStore(m)
	c, err := edge.NewCache(m, s, lmax)
	if err != nil {
		t.Fatal(err)
	}
	return &plane{meter: m, store: s, cache: c}
}

// 题目例题一：Qu=3，Qd=1，租户 t 的刷新与新鲜度流程。
func TestWorkedExamplePurge(t *testing.T) {
	p := newPlane(t, 10, map[string]quota.Quotas{
		"t": {URL: 3, Dir: 1, Prewarm: 3},
	})
	const now = int64(1000)

	if err := p.cache.Fill(now, "t", "/img/a.jpg"); err != nil {
		t.Fatal(err)
	}
	if got := p.cache.Fresh("t", "/img/a.jpg"); got != edge.Fresh {
		t.Fatalf("filled=0 且无规则应新鲜, got %v", got)
	}

	epoch, err := p.store.Purge(now, "t", []string{"/img/", "/img/a.jpg", "/img/a.jpg", "/css/x.css"})
	if err != nil || epoch != 1 {
		t.Fatalf("Purge = (%d, %v), want (1, nil)", epoch, err)
	}
	if u := p.meter.Used(now, "t", quota.KindURL); u != 1 {
		t.Fatalf("URL 已用量 = %d, want 1（/img/a.jpg 被 /img/ 覆盖不计费）", u)
	}
	if d := p.meter.Used(now, "t", quota.KindDir); d != 1 {
		t.Fatalf("目录已用量 = %d, want 1", d)
	}
	if got := p.cache.Fresh("t", "/img/a.jpg"); got != edge.Stale {
		t.Fatalf("被 /img/ 规则匹配应不新鲜, got %v", got)
	}

	if err := p.cache.Fill(now, "t", "/img/a.jpg"); err != nil {
		t.Fatal(err)
	}
	if got := p.cache.Fresh("t", "/img/a.jpg"); got != edge.Fresh {
		t.Fatalf("filled 恰等纪元 1 应新鲜, got %v", got)
	}

	if _, err := p.store.Purge(now, "t", []string{"/img/b.jpg", "/js/"}); !errors.Is(err, quota.ErrDirQuota) {
		t.Fatalf("目录配额不足, got %v", err)
	}
	if u := p.meter.Used(now, "t", quota.KindURL); u != 1 {
		t.Fatalf("整批拒绝后 URL 已用量 = %d, want 1", u)
	}
	if got := p.store.Epoch(); got != 1 {
		t.Fatalf("整批拒绝后纪元 = %d, want 1", got)
	}

	epoch, err = p.store.Purge(now, "t", []string{"/img/a.jpg", "/img/b.jpg"})
	if err != nil || epoch != 2 {
		t.Fatalf("恰等 Qu 应接受: (%d, %v), want (2, nil)", epoch, err)
	}
	if got := p.cache.Fresh("t", "/img/a.jpg"); got != edge.Stale {
		t.Fatalf("纪元 2 规则匹配应再次不新鲜, got %v", got)
	}

	if _, err := p.store.Purge(now, "t", []string{"/x"}); !errors.Is(err, quota.ErrURLQuota) {
		t.Fatalf("URL 配额不足, got %v", err)
	}

	// 进入下一日，三项已用量归零。
	next := now + quota.DaySeconds
	for _, k := range []quota.Kind{quota.KindURL, quota.KindDir, quota.KindPrewarm} {
		if got := p.meter.Used(next, "t", k); got != 0 {
			t.Fatalf("跨日后 kind=%d 已用量 = %d, want 0", k, got)
		}
	}
}

// 题目例题二：Qw=3 的预热去重、已新鲜跳过与不退配额。
func TestWorkedExamplePrewarm(t *testing.T) {
	p := newPlane(t, 10, map[string]quota.Quotas{
		"t": {URL: 3, Dir: 1, Prewarm: 3},
	})
	const now = int64(1000)

	if err := p.cache.Fill(now, "t", "/img/a.jpg"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.store.Purge(now, "t", []string{"/img/", "/css/x.css"}); err != nil {
		t.Fatal(err)
	}
	if err := p.cache.Fill(now, "t", "/img/a.jpg"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.store.Purge(now, "t", []string{"/img/a.jpg", "/img/b.jpg"}); err != nil {
		t.Fatal(err)
	}

	w, err := p.cache.Prewarm(now, "t", []string{"/img/a.jpg", "/v/1.ts", "/v/1.ts"})
	if err != nil || w != 2 {
		t.Fatalf("Prewarm = (%d, %v), want (2, nil)", w, err)
	}
	w, err = p.cache.Prewarm(now, "t", []string{"/v/1.ts", "/v/2.ts"})
	if err != nil || w != 1 {
		t.Fatalf("Prewarm = (%d, %v), want (1, nil)（1.ts 已在队列）", w, err)
	}
	if got := p.meter.Used(now, "t", quota.KindPrewarm); got != 3 {
		t.Fatalf("预热已用量 = %d, want 3", got)
	}

	if err := p.cache.Fill(now, "t", "/v/1.ts"); err != nil {
		t.Fatal(err)
	}
	res, err := p.cache.Tick(now, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Filled, []string{"/img/a.jpg"}) {
		t.Fatalf("Filled = %v, want [/img/a.jpg]", res.Filled)
	}
	if !slices.Equal(res.Skipped, []string{"/v/1.ts"}) {
		t.Fatalf("Skipped = %v, want [/v/1.ts]", res.Skipped)
	}
	if got := p.cache.QueueLen(); got != 1 {
		t.Fatalf("队列剩余 = %d, want 1（/v/2.ts）", got)
	}
	// 跳过与执行都不退配额。
	if got := p.meter.Used(now, "t", quota.KindPrewarm); got != 3 {
		t.Fatalf("Tick 后预热已用量 = %d, want 3", got)
	}
}

// /img、/img/、/imgs/ 三者的覆盖关系必须严格区分。
func TestPathDistinction(t *testing.T) {
	p := newPlane(t, 10, map[string]quota.Quotas{
		"t": {URL: 10, Dir: 10},
	})
	const now = int64(0)
	for _, u := range []string{"/img", "/img/a", "/imgs/a"} {
		if err := p.cache.Fill(now, "t", u); err != nil {
			t.Fatal(err)
		}
	}
	// 目录 /img/ 只覆盖以 /img/ 为前缀的项。
	if _, err := p.store.Purge(now, "t", []string{"/img/"}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		url  string
		want edge.State
	}{
		{"/img", edge.Fresh},    // 目录 /img/ 不覆盖 URL /img
		{"/img/a", edge.Stale},  // 被 /img/ 覆盖
		{"/imgs/a", edge.Fresh}, // 不覆盖 /imgs/a
	}
	for _, c := range cases {
		if got := p.cache.Fresh("t", c.url); got != c.want {
			t.Errorf("Purge(/img/) 后 Fresh(%q) = %v, want %v", c.url, got, c.want)
		}
	}
	// URL 规则 /img 只匹配自身，不匹配 /img/a。
	if _, err := p.store.Purge(now, "t", []string{"/img"}); err != nil {
		t.Fatal(err)
	}
	if got := p.cache.Fresh("t", "/img"); got != edge.Stale {
		t.Errorf("URL 规则 /img 应使 /img 过期, got %v", got)
	}
	if got := p.cache.Fresh("t", "/imgs/a"); got != edge.Fresh {
		t.Errorf("URL 规则 /img 不应影响 /imgs/a, got %v", got)
	}
}

// 根目录刷新使所有条目过期。
func TestRootDirPurge(t *testing.T) {
	p := newPlane(t, 10, map[string]quota.Quotas{"t": {URL: 10, Dir: 10}})
	const now = int64(0)
	for _, u := range []string{"/a", "/x/y/z"} {
		if err := p.cache.Fill(now, "t", u); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.store.Purge(now, "t", []string{"/"}); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"/a", "/x/y/z"} {
		if got := p.cache.Fresh("t", u); got != edge.Stale {
			t.Errorf("根目录刷新后 Fresh(%q) = %v, want Stale", u, got)
		}
	}
}

// 条目不存在与过期在返回值上可区分。
func TestFreshMissingVsStale(t *testing.T) {
	p := newPlane(t, 10, map[string]quota.Quotas{"t": {URL: 10, Dir: 10}})
	const now = int64(0)
	if got := p.cache.Fresh("t", "/none"); got != edge.Missing {
		t.Fatalf("不存在的条目 = %v, want Missing", got)
	}
	if got := p.cache.Fresh("ghost", "/none"); got != edge.Missing {
		t.Fatalf("不存在的租户 = %v, want Missing", got)
	}
	if err := p.cache.Fill(now, "t", "/a"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.store.Purge(now, "t", []string{"/a"}); err != nil {
		t.Fatal(err)
	}
	if got := p.cache.Fresh("t", "/a"); got != edge.Stale {
		t.Fatalf("过期条目 = %v, want Stale", got)
	}
}

// 各类拒绝只报第一个，且被拒绝的操作不改任何状态。
func TestRejectOrder(t *testing.T) {
	p := newPlane(t, 2, map[string]quota.Quotas{
		"t": {URL: 1, Dir: 0, Prewarm: 3},
	})
	if err := p.cache.Fill(100, "t", "/used"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.store.Purge(100, "t", []string{"/used"}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.cache.Prewarm(100, "t", []string{"/q1", "/q2"}); err != nil {
		t.Fatal(err) // 队列占满 lmax=2，预热已用 1
	}

	t.Run("Purge", func(t *testing.T) {
		cases := []struct {
			name  string
			now   int64
			id    string
			items []string
			want  error
		}{
			{"参数非法优先于时钟回退", 99, "ghost", []string{"//"}, purge.ErrInvalidArgument},
			{"时钟回退优先于租户不存在", 99, "ghost", []string{"/a"}, quota.ErrClockBackward},
			{"租户不存在优先于配额", 100, "ghost", []string{"/a"}, quota.ErrTenantNotFound},
			{"先判 URL 后判目录", 100, "t", []string{"/u1", "/d/"}, quota.ErrURLQuota},
			{"URL 够则报目录", 100, "t", []string{"/d/"}, quota.ErrDirQuota},
		}
		for _, c := range cases {
			if _, err := p.store.Purge(c.now, c.id, c.items); !errors.Is(err, c.want) {
				t.Errorf("%s: got %v, want %v", c.name, err, c.want)
			}
		}
	})

	t.Run("Prewarm", func(t *testing.T) {
		cases := []struct {
			name string
			now  int64
			id   string
			urls []string
			want error
		}{
			{"含目录参数非法", 99, "ghost", []string{"/d/"}, edge.ErrInvalidArgument},
			{"时钟回退优先于租户不存在", 99, "ghost", []string{"/a"}, quota.ErrClockBackward},
			{"租户不存在优先于配额", 100, "ghost", []string{"/a"}, quota.ErrTenantNotFound},
			{"预热配额优先于队列已满", 100, "t", []string{"/a", "/b"}, quota.ErrPrewarmQuota},
			{"队列已满", 100, "t", []string{"/a"}, edge.ErrQueueFull},
		}
		for _, c := range cases {
			if _, err := p.cache.Prewarm(c.now, c.id, c.urls); !errors.Is(err, c.want) {
				t.Errorf("%s: got %v, want %v", c.name, err, c.want)
			}
		}
	})

	t.Run("Fill", func(t *testing.T) {
		if err := p.cache.Fill(99, "ghost", "/d/"); !errors.Is(err, edge.ErrInvalidArgument) {
			t.Errorf("目录路径参数非法: got %v", err)
		}
		if err := p.cache.Fill(99, "ghost", "/a"); !errors.Is(err, quota.ErrClockBackward) {
			t.Errorf("时钟回退优先于租户不存在: got %v", err)
		}
		if err := p.cache.Fill(100, "ghost", "/a"); !errors.Is(err, quota.ErrTenantNotFound) {
			t.Errorf("租户不存在: got %v", err)
		}
	})

	t.Run("Tick", func(t *testing.T) {
		if _, err := p.cache.Tick(99, 0); !errors.Is(err, edge.ErrInvalidArgument) {
			t.Errorf("budget 越界应报参数非法: got %v", err)
		}
		if _, err := p.cache.Tick(99, 1); !errors.Is(err, quota.ErrClockBackward) {
			t.Errorf("时钟回退: got %v", err)
		}
	})

	// 上述拒绝全部不留痕：时钟、纪元、已用量、队列均不变。
	if got := p.meter.MaxNow(); got != 100 {
		t.Errorf("拒绝后时钟 = %d, want 100", got)
	}
	if got := p.store.Epoch(); got != 1 {
		t.Errorf("拒绝后纪元 = %d, want 1", got)
	}
	if got := p.meter.Used(100, "t", quota.KindURL); got != 1 {
		t.Errorf("拒绝后 URL 已用量 = %d, want 1", got)
	}
	if got := p.meter.Used(100, "t", quota.KindPrewarm); got != 2 {
		t.Errorf("拒绝后预热已用量 = %d, want 2", got)
	}
	if got := p.cache.QueueLen(); got != 2 {
		t.Errorf("拒绝后队列长度 = %d, want 2", got)
	}
}

// 已新鲜的 URL 照常入队并计费；刷新不移除队列中的预热项。
func TestPrewarmFreshEnqueuedAndPurgeKeepsQueue(t *testing.T) {
	p := newPlane(t, 10, map[string]quota.Quotas{
		"t": {URL: 10, Dir: 10, Prewarm: 10},
	})
	const now = int64(0)
	if err := p.cache.Fill(now, "t", "/fresh"); err != nil {
		t.Fatal(err)
	}
	w, err := p.cache.Prewarm(now, "t", []string{"/fresh"})
	if err != nil || w != 1 {
		t.Fatalf("已新鲜的 URL 照常入队并计费: (%d, %v), want (1, nil)", w, err)
	}
	// 刷新不移除队列中的预热项。
	if _, err := p.store.Purge(now, "t", []string{"/fresh"}); err != nil {
		t.Fatal(err)
	}
	if got := p.cache.QueueLen(); got != 1 {
		t.Fatalf("刷新后队列长度 = %d, want 1", got)
	}
	// 被刷新后出队时应执行 Fill 而非跳过。
	res, err := p.cache.Tick(now, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Filled, []string{"/fresh"}) || len(res.Skipped) != 0 {
		t.Fatalf("Tick = %+v, want Filled=[/fresh]", res)
	}
	// 出队后同 URL 可再次入队。
	if w, err := p.cache.Prewarm(now, "t", []string{"/fresh"}); err != nil || w != 1 {
		t.Fatalf("出队后应可再次入队: (%d, %v)", w, err)
	}
}

// Tick 的两个清单各自按出队次序。
func TestTickFIFOOrder(t *testing.T) {
	p := newPlane(t, 10, map[string]quota.Quotas{
		"t": {URL: 10, Dir: 10, Prewarm: 10},
	})
	const now = int64(0)
	if err := p.cache.Fill(now, "t", "/b"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.cache.Prewarm(now, "t", []string{"/a", "/b", "/c", "/d"}); err != nil {
		t.Fatal(err)
	}
	res, err := p.cache.Tick(now, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Filled, []string{"/a", "/c"}) {
		t.Fatalf("Filled = %v, want [/a /c]", res.Filled)
	}
	if !slices.Equal(res.Skipped, []string{"/b"}) {
		t.Fatalf("Skipped = %v, want [/b]", res.Skipped)
	}
	if got := p.cache.QueueLen(); got != 1 {
		t.Fatalf("队列剩余 = %d, want 1", got)
	}
}

// Fresh 的 visited 上界：规则 100 条与 10000 条两档对照。
func TestFreshVisitedBound(t *testing.T) {
	for _, nRules := range []int{100, 10000} {
		p := newPlane(t, 10, map[string]quota.Quotas{
			"t": {URL: quota.MaxQuota, Dir: quota.MaxQuota},
		})
		for done := 0; done < nRules; {
			var items []string
			for i := 0; i < purge.MaxItems && done < nRules; i++ {
				if done%2 == 0 {
					items = append(items, fmt.Sprintf("/r%06d", done))
				} else {
					items = append(items, fmt.Sprintf("/d%06d/", done))
				}
				done++
			}
			if _, err := p.store.Purge(0, "t", items); err != nil {
				t.Fatal(err)
			}
		}
		const url = "/a/b/c/d" // 4 段
		if err := p.cache.Fill(0, "t", url); err != nil {
			t.Fatal(err)
		}
		p.cache.Fresh("t", url)
		if got := p.store.LastVisited(); got > 4+2 {
			t.Errorf("规则 %d 条: visited = %d, want <= 段数+2 = 6", nRules, got)
		}
	}
}

// 并发调用等价于某个串行顺序：纪元数恰等于被接受的 Purge 数，
// 且不变量（已用量不超配额、队列中同租户同 URL 至多一项）保持。
func TestConcurrentSerializable(t *testing.T) {
	p := newPlane(t, edge.MaxQueueLen, map[string]quota.Quotas{
		"a": {URL: quota.MaxQuota, Dir: quota.MaxQuota, Prewarm: quota.MaxQuota},
		"b": {URL: quota.MaxQuota, Dir: quota.MaxQuota, Prewarm: quota.MaxQuota},
	})
	var accepted atomic.Uint64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			id := []string{"a", "b"}[g%2]
			for i := 0; i < 500; i++ {
				u := fmt.Sprintf("/u%d", rng.Intn(50))
				switch rng.Intn(5) {
				case 0:
					if _, err := p.store.Purge(0, id, []string{u}); err == nil {
						accepted.Add(1)
					}
				case 1:
					_ = p.cache.Fill(0, id, u)
				case 2:
					_ = p.cache.Fresh(id, u)
				case 3:
					_, _ = p.cache.Prewarm(0, id, []string{u})
				case 4:
					_, _ = p.cache.Tick(0, 5)
				}
			}
		}(g)
	}
	wg.Wait()
	if got := p.store.Epoch(); got != accepted.Load() {
		t.Errorf("纪元 = %d, want 被接受的 Purge 数 %d（连续无洞）", got, accepted.Load())
	}
	for _, id := range []string{"a", "b"} {
		for _, k := range []quota.Kind{quota.KindURL, quota.KindDir, quota.KindPrewarm} {
			if got := p.meter.Used(0, id, k); got > quota.MaxQuota {
				t.Errorf("租户 %s kind=%d 已用量 %d 超过配额", id, k, got)
			}
		}
	}
}

// naive 是逐条规则做前缀比较的朴素模拟，用于随机对照。
type naive struct {
	lmax     int
	quotas   map[string]quota.Quotas
	used     map[string]map[int64][3]uint64
	maxNow   int64
	epoch    uint64
	urlRules map[string]map[string]uint64
	dirRules map[string]map[string]uint64
	entries  map[string]map[string]uint64
	queue    []naiveItem
	inQueue  map[naiveItem]bool
}

type naiveItem struct{ tenant, url string }

func newNaive(lmax int, qs map[string]quota.Quotas) *naive {
	return &naive{
		lmax:     lmax,
		quotas:   qs,
		used:     make(map[string]map[int64][3]uint64),
		urlRules: make(map[string]map[string]uint64),
		dirRules: make(map[string]map[string]uint64),
		entries:  make(map[string]map[string]uint64),
		inQueue:  make(map[naiveItem]bool),
	}
}

func (n *naive) usage(id string, now int64) [3]uint64 {
	return n.used[id][quota.DayOf(now)]
}

func (n *naive) charge(id string, now int64, k quota.Kind, v uint64) {
	day := quota.DayOf(now)
	m := n.used[id]
	if m == nil {
		m = make(map[int64][3]uint64)
		n.used[id] = m
	}
	u := m[day]
	u[k] += v
	m[day] = u
}

func (n *naive) purge(now int64, id string, items []string) (uint64, error) {
	if !quota.ValidNow(now) || len(items) == 0 || len(items) > purge.MaxItems {
		return 0, purge.ErrInvalidArgument
	}
	for _, it := range items {
		if !purge.ValidPath(it) {
			return 0, purge.ErrInvalidArgument
		}
	}
	urls, dirs := purge.Normalize(items)
	if now < n.maxNow {
		return 0, quota.ErrClockBackward
	}
	q, ok := n.quotas[id]
	if !ok {
		return 0, quota.ErrTenantNotFound
	}
	u := n.usage(id, now)
	if u[quota.KindURL]+uint64(len(urls)) > q.URL {
		return 0, quota.ErrURLQuota
	}
	if u[quota.KindDir]+uint64(len(dirs)) > q.Dir {
		return 0, quota.ErrDirQuota
	}
	n.charge(id, now, quota.KindURL, uint64(len(urls)))
	n.charge(id, now, quota.KindDir, uint64(len(dirs)))
	n.maxNow = now
	n.epoch++
	if n.urlRules[id] == nil {
		n.urlRules[id] = make(map[string]uint64)
		n.dirRules[id] = make(map[string]uint64)
	}
	for _, p := range urls {
		if e, ok := n.urlRules[id][p]; !ok || e < n.epoch {
			n.urlRules[id][p] = n.epoch
		}
	}
	for _, p := range dirs {
		if e, ok := n.dirRules[id][p]; !ok || e < n.epoch {
			n.dirRules[id][p] = n.epoch
		}
	}
	return n.epoch, nil
}

// matchEpoch 逐条规则做前缀比较（O(规则数) 的朴素实现）。
func (n *naive) matchEpoch(id, url string) uint64 {
	var max uint64
	for p, e := range n.urlRules[id] {
		if p == url && e > max {
			max = e
		}
	}
	for p, e := range n.dirRules[id] {
		if strings.HasPrefix(url, p) && e > max {
			max = e
		}
	}
	return max
}

func (n *naive) fill(now int64, id, url string) error {
	if !quota.ValidNow(now) || !purge.ValidPath(url) || purge.IsDir(url) {
		return edge.ErrInvalidArgument
	}
	if now < n.maxNow {
		return quota.ErrClockBackward
	}
	if _, ok := n.quotas[id]; !ok {
		return quota.ErrTenantNotFound
	}
	n.maxNow = now
	m := n.entries[id]
	if m == nil {
		m = make(map[string]uint64)
		n.entries[id] = m
	}
	m[url] = n.epoch
	return nil
}

func (n *naive) fresh(id, url string) edge.State {
	filled, ok := n.entries[id][url]
	if !ok {
		return edge.Missing
	}
	if filled >= n.matchEpoch(id, url) {
		return edge.Fresh
	}
	return edge.Stale
}

func (n *naive) prewarm(now int64, id string, urls []string) (int, error) {
	if !quota.ValidNow(now) || len(urls) == 0 || len(urls) > edge.MaxPrewarmItems {
		return 0, edge.ErrInvalidArgument
	}
	for _, u := range urls {
		if !purge.ValidPath(u) || purge.IsDir(u) {
			return 0, edge.ErrInvalidArgument
		}
	}
	seen := make(map[string]bool, len(urls))
	var todo []string
	for _, u := range urls {
		if seen[u] {
			continue
		}
		seen[u] = true
		if n.inQueue[naiveItem{id, u}] {
			continue
		}
		todo = append(todo, u)
	}
	w := uint64(len(todo))
	if now < n.maxNow {
		return 0, quota.ErrClockBackward
	}
	q, ok := n.quotas[id]
	if !ok {
		return 0, quota.ErrTenantNotFound
	}
	if n.usage(id, now)[quota.KindPrewarm]+w > q.Prewarm {
		return 0, quota.ErrPrewarmQuota
	}
	if len(n.queue)+len(todo) > n.lmax {
		return 0, edge.ErrQueueFull
	}
	n.charge(id, now, quota.KindPrewarm, w)
	n.maxNow = now
	for _, u := range todo {
		it := naiveItem{id, u}
		n.queue = append(n.queue, it)
		n.inQueue[it] = true
	}
	return len(todo), nil
}

func (n *naive) tick(now int64, budget int) (edge.TickResult, error) {
	var res edge.TickResult
	if !quota.ValidNow(now) || budget < 1 || budget > edge.MaxTickBudget {
		return res, edge.ErrInvalidArgument
	}
	if now < n.maxNow {
		return res, quota.ErrClockBackward
	}
	n.maxNow = now
	k := budget
	if k > len(n.queue) {
		k = len(n.queue)
	}
	items := n.queue[:k]
	n.queue = n.queue[k:]
	for _, it := range items {
		delete(n.inQueue, it)
		if n.fresh(it.tenant, it.url) == edge.Fresh {
			res.Skipped = append(res.Skipped, it.url)
		} else {
			if err := n.fill(now, it.tenant, it.url); err != nil {
				return res, err
			}
			res.Filled = append(res.Filled, it.url)
		}
	}
	return res, nil
}

var pathPool = []string{
	"/",
	"/img", "/img/", "/imgs/", "/img/a.jpg", "/img/b.jpg", "/imgs/a",
	"/css/", "/css/x.css", "/js/", "/js/app.js",
	"/v/", "/v/1.ts", "/v/2.ts",
	"/a/", "/a/b/", "/a/b/c.jpg", "/a/b/c/d/e.ts",
	"/x", "/y", "/z/", "/deep/very/long/path/file",
}

var urlPool = func() []string {
	var urls []string
	for _, p := range pathPool {
		if !purge.IsDir(p) {
			urls = append(urls, p)
		}
	}
	return urls
}()

// 1500 组随机操作序列与朴素模拟对照：计费数、新鲜度与 Tick 清单
// 必须逐项一致；日志打印每步的输入、输出与判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 1500
	for seed := int64(0); seed < sequences; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runSequence(t, seed)
		})
	}
}

func runSequence(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	tenants := []string{"t1", "t2", "t3"}
	qs := make(map[string]quota.Quotas)
	for _, id := range tenants {
		qs[id] = quota.Quotas{
			URL:     uint64(rng.Intn(7)),
			Dir:     uint64(rng.Intn(4)),
			Prewarm: uint64(rng.Intn(7)),
		}
	}
	lmax := 1 + rng.Intn(10)
	t.Logf("输入: tenants=%v quotas=%v lmax=%d", tenants, qs, lmax)

	m := quota.NewMeter()
	for id, q := range qs {
		if err := m.Register(id, q); err != nil {
			t.Fatal(err)
		}
	}
	store := purge.NewStore(m)
	cache, err := edge.NewCache(m, store, lmax)
	if err != nil {
		t.Fatal(err)
	}
	nv := newNaive(lmax, qs)

	checkState := func(op int, now int64) {
		t.Helper()
		for _, id := range tenants {
			for _, k := range []quota.Kind{quota.KindURL, quota.KindDir, quota.KindPrewarm} {
				got, want := m.Used(now, id, k), nv.usage(id, now)[k]
				if got != want {
					t.Fatalf("op=%d 租户 %s kind=%d 已用量 = %d, 朴素模拟 = %d", op, id, k, got, want)
				}
			}
		}
		if got, want := store.Epoch(), nv.epoch; got != want {
			t.Fatalf("op=%d 纪元 = %d, 朴素模拟 = %d", op, got, want)
		}
		if got, want := cache.QueueLen(), len(nv.queue); got != want {
			t.Fatalf("op=%d 队列长度 = %d, 朴素模拟 = %d", op, got, want)
		}
	}

	var now int64
	for op := 0; op < 40; op++ {
		if rng.Intn(3) == 0 {
			now += int64(rng.Intn(300000)) // 可能跨日
		}
		id := tenants[rng.Intn(len(tenants))]
		switch rng.Intn(6) {
		case 0, 1: // Purge
			k := 1 + rng.Intn(5)
			items := make([]string, k)
			for i := range items {
				items[i] = pathPool[rng.Intn(len(pathPool))]
			}
			gotE, gotErr := store.Purge(now, id, items)
			wantE, wantErr := nv.purge(now, id, items)
			t.Logf("op=%d Purge(now=%d, %s, %v) -> (%d, %v), 朴素模拟 (%d, %v)", op, now, id, items, gotE, gotErr, wantE, wantErr)
			if gotE != wantE || gotErr != wantErr {
				t.Fatalf("op=%d Purge 不一致: (%d, %v) vs (%d, %v)", op, gotE, gotErr, wantE, wantErr)
			}
		case 2: // Fill
			u := urlPool[rng.Intn(len(urlPool))]
			gotErr := cache.Fill(now, id, u)
			wantErr := nv.fill(now, id, u)
			t.Logf("op=%d Fill(now=%d, %s, %s) -> %v, 朴素模拟 %v", op, now, id, u, gotErr, wantErr)
			if gotErr != wantErr {
				t.Fatalf("op=%d Fill 不一致: %v vs %v", op, gotErr, wantErr)
			}
		case 3: // Fresh
			u := pathPool[rng.Intn(len(pathPool))]
			got := cache.Fresh(id, u)
			want := nv.fresh(id, u)
			t.Logf("op=%d Fresh(%s, %s) -> %v, 朴素模拟 %v（依据: 匹配规则最大纪元）", op, id, u, got, want)
			if got != want {
				t.Fatalf("op=%d Fresh 不一致: %v vs %v", op, got, want)
			}
		case 4: // Prewarm
			k := 1 + rng.Intn(5)
			urls := make([]string, k)
			for i := range urls {
				if rng.Intn(10) == 0 {
					urls[i] = pathPool[rng.Intn(len(pathPool))] // 偶尔混入目录触发参数非法
				} else {
					urls[i] = urlPool[rng.Intn(len(urlPool))]
				}
			}
			gotW, gotErr := cache.Prewarm(now, id, urls)
			wantW, wantErr := nv.prewarm(now, id, urls)
			t.Logf("op=%d Prewarm(now=%d, %s, %v) -> (%d, %v), 朴素模拟 (%d, %v)", op, now, id, urls, gotW, gotErr, wantW, wantErr)
			if gotW != wantW || gotErr != wantErr {
				t.Fatalf("op=%d Prewarm 不一致: (%d, %v) vs (%d, %v)", op, gotW, gotErr, wantW, wantErr)
			}
		case 5: // Tick
			budget := 1 + rng.Intn(6)
			got, gotErr := cache.Tick(now, budget)
			want, wantErr := nv.tick(now, budget)
			t.Logf("op=%d Tick(now=%d, %d) -> (%+v, %v), 朴素模拟 (%+v, %v)", op, now, budget, got, gotErr, want, wantErr)
			if gotErr != wantErr ||
				!slices.Equal(got.Filled, want.Filled) ||
				!slices.Equal(got.Skipped, want.Skipped) {
				t.Fatalf("op=%d Tick 不一致: (%+v, %v) vs (%+v, %v)", op, got, gotErr, want, wantErr)
			}
		}
		checkState(op, now)
	}
}

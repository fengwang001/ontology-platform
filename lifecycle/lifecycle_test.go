package lifecycle

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"testing"
)

func exampleCfg() Config {
	return Config{P0: 10, P1: 4, P2: 1, A: 30, B: 90, M1: 30, M2: 90, R1: 2, R2: 5, Q: 5}
}

func wantInt(t *testing.T, name string, got *big.Int, want int64) {
	t.Helper()
	if got == nil || got.Cmp(big.NewInt(want)) != 0 {
		t.Fatalf("%s = %v, want %d", name, got, want)
	}
}

func feesZero(f Fees) bool {
	return f.Storage.Sign() == 0 && f.Exit.Sign() == 0 && f.Retriev.Sign() == 0
}

func feesEqual(a, c Fees) bool {
	return a.Storage.Cmp(c.Storage) == 0 && a.Exit.Cmp(c.Exit) == 0 && a.Retriev.Cmp(c.Retriev) == 0
}

// 题目给出的示例。
func TestExample(t *testing.T) {
	b, err := New(exampleCfg())
	if err != nil {
		t.Fatal(err)
	}

	f, err := b.Put("k", 10, 0)
	if err != nil || !feesZero(f) {
		t.Fatalf("Put = %+v, %v; want zero fees", f, err)
	}

	f, err = b.Get("k", 40)
	if err != nil {
		t.Fatal(err)
	}
	wantInt(t, "storage", f.Storage, 3400)
	wantInt(t, "exit", f.Exit, 800)
	wantInt(t, "retrieval", f.Retriev, 10)

	f, err = b.Delete("k", 200)
	if err != nil {
		t.Fatal(err)
	}
	wantInt(t, "storage", f.Storage, 6100)
	wantInt(t, "exit", f.Exit, 200)
	wantInt(t, "retrieval", f.Retriev, 0)
	if f.Storage.Int64()+f.Exit.Int64() != 6300 {
		t.Fatalf("total = %d, want 6300", f.Storage.Int64()+f.Exit.Int64())
	}
}

// Δ 恰等于 A 为凉层且停留 0；少 1 为热层。Δ 恰等于 B 为冷层停留 0。
func TestLayerBoundaries(t *testing.T) {
	cfg := exampleCfg()
	b, _ := New(cfg)
	b.Put("hotEdge", 10, 0)
	b.Put("coolEdge", 10, 0)
	b.Put("coldEdge", 10, 0)

	f, err := b.Get("hotEdge", 29) // Δ=29=A-1，热层
	if err != nil {
		t.Fatal(err)
	}
	wantInt(t, "hot-edge exit", f.Exit, 0)
	wantInt(t, "hot-edge storage", f.Storage, 10*(10*29))

	f, err = b.Get("coolEdge", 30) // Δ=30=A，凉层停留 0
	if err != nil {
		t.Fatal(err)
	}
	wantInt(t, "cool-edge exit", f.Exit, 30*4*10)
	wantInt(t, "cool-edge storage", f.Storage, 10*(10*30))

	f, err = b.Delete("coldEdge", 90) // Δ=90=B，冷层停留 0
	if err != nil {
		t.Fatal(err)
	}
	wantInt(t, "cold-edge exit", f.Exit, 90*1*10)
	wantInt(t, "cold-edge storage", f.Storage, 10*(10*30+4*60))

	if Layer(0, 30, cfg.A, cfg.B) != LayerCool ||
		Layer(0, 29, cfg.A, cfg.B) != LayerHot ||
		Layer(0, 90, cfg.A, cfg.B) != LayerCold {
		t.Fatal("Layer boundary classification wrong")
	}
}

// 停留恰等于最短停留时补费为 0。
func TestExitZeroAtMinStay(t *testing.T) {
	b1, _ := New(exampleCfg())
	b1.Put("c", 3, 0)
	f, err := b1.Delete("c", 60) // 凉层停留 30 == m1
	if err != nil {
		t.Fatal(err)
	}
	wantInt(t, "cool min-stay exit", f.Exit, 0)

	b2, _ := New(exampleCfg())
	b2.Put("d", 3, 0)
	f, err = b2.Delete("d", 180) // 冷层停留 90 == m2
	if err != nil {
		t.Fatal(err)
	}
	wantInt(t, "cold min-stay exit", f.Exit, 0)
}

// 已转出凉层（当前冷层）不产生凉层补费，只按冷层当前停留补费。
func TestTransferredLayerNoExit(t *testing.T) {
	b, _ := New(exampleCfg())
	b.Put("k", 10, 0)
	f, err := b.Delete("k", 91) // 冷层停留 1，补费 (90-1)*1*10
	if err != nil {
		t.Fatal(err)
	}
	wantInt(t, "exit only for current cold layer", f.Exit, 89*1*10)
}

// 热层读取不消耗免费额度。
func TestHotGetNoQuota(t *testing.T) {
	b, _ := New(exampleCfg())
	b.Put("k", 10, 0)
	f, err := b.Get("k", 10)
	if err != nil {
		t.Fatal(err)
	}
	wantInt(t, "hot retrieval", f.Retriev, 0)
	if q := b.QuotaState(); q.Period != 0 || q.Used != 0 {
		t.Fatalf("quota changed on hot get: %+v", q)
	}
}

// 免费额度被多个对象按调用次序共用，并发生部分消耗（fg 小于 size）。
func TestQuotaSharedPartial(t *testing.T) {
	b, _ := New(exampleCfg())
	b.Put("a", 10, 0)
	b.Put("b", 10, 0)

	fa, err := b.Get("a", 40) // 周期 1，fg=min(10,5)=5
	if err != nil {
		t.Fatal(err)
	}
	wantInt(t, "a retrieval", fa.Retriev, 5*2)
	if q := b.QuotaState(); q.Used != 5 {
		t.Fatalf("used = %d, want 5", q.Used)
	}

	fb, err := b.Get("b", 41) // 剩余 0，fg=0
	if err != nil {
		t.Fatal(err)
	}
	wantInt(t, "b retrieval", fb.Retriev, 10*2)
	if q := b.QuotaState(); q.Used != 5 {
		t.Fatalf("used = %d, still want 5", q.Used)
	}
}

// 部分消耗还发生在“剩余额度小于 size”时：fg < size。
func TestQuotaPartialFraction(t *testing.T) {
	b, _ := New(exampleCfg())
	b.Put("a", 3, 0)
	b.Put("b", 10, 0)
	// now=30 进入周期 1。
	fa, _ := b.Get("a", 30) // fg=min(3,5)=3，剩 2
	wantInt(t, "a all free", fa.Retriev, 0)
	fb, _ := b.Get("b", 31) // fg=min(10,2)=2，付费 8
	wantInt(t, "b partial free", fb.Retriev, 8*2)
	if q := b.QuotaState(); q.Used != 5 {
		t.Fatalf("used = %d, want 5", q.Used)
	}
}

// 跨周期（now 整除 30 的边界）额度清零。
func TestPeriodRollover(t *testing.T) {
	b, _ := New(exampleCfg())
	b.Put("b", 10, 0)
	b.Put("c", 10, 0)
	b.Put("a", 10, 0)
	fa, err := b.Get("a", 30) // 周期 1，凉层，fg=5
	if err != nil {
		t.Fatal(err)
	}
	wantInt(t, "period1 retrieval", fa.Retriev, 5*2)
	if q := b.QuotaState(); q.Period != 1 || q.Used != 5 {
		t.Fatalf("quota = %+v, want period1 used5", q)
	}

	fb, err := b.Get("b", 59) // 仍周期 1，额度耗尽
	if err != nil {
		t.Fatal(err)
	}
	wantInt(t, "period1 exhausted", fb.Retriev, 10*2)

	fc, err := b.Get("c", 60) // 周期 2，清零，fg=5
	if err != nil {
		t.Fatal(err)
	}
	wantInt(t, "period2 retrieval", fc.Retriev, 5*2)
	if q := b.QuotaState(); q.Period != 2 || q.Used != 5 {
		t.Fatalf("quota = %+v, want period2 used5", q)
	}
}

// 覆盖写对旧对象收存储费与补费，新对象 la 重置。
func TestOverwrite(t *testing.T) {
	b, _ := New(exampleCfg())
	b.Put("k", 10, 0)
	f, err := b.Put("k", 7, 40)
	if err != nil {
		t.Fatal(err)
	}
	wantInt(t, "overwrite storage", f.Storage, 3400)
	wantInt(t, "overwrite exit", f.Exit, 800)
	wantInt(t, "overwrite retrieval", f.Retriev, 0)

	obj, ok := b.Lookup("k")
	if !ok || obj.Size != 7 || obj.LA != 40 {
		t.Fatalf("object after overwrite = %+v, %v", obj, ok)
	}
	// 新对象当前为热层：有 5 天热层存储费，无补费与检索费。
	f, err = b.Get("k", 45)
	if err != nil || f.Exit.Sign() != 0 || f.Retriev.Sign() != 0 ||
		f.Storage.Cmp(big.NewInt(7*10*5)) != 0 {
		t.Fatalf("post-overwrite get = %+v, %v", f, err)
	}
}

// Get 后 la 重置使层级回到热层。
func TestGetResetsLA(t *testing.T) {
	b, _ := New(exampleCfg())
	b.Put("k", 10, 0)
	if _, err := b.Get("k", 100); err != nil {
		t.Fatal(err)
	}
	obj, _ := b.Lookup("k")
	if obj.LA != 100 {
		t.Fatalf("la = %d, want 100", obj.LA)
	}
	if Layer(obj.LA, 100, 30, 90) != LayerHot {
		t.Fatal("should be hot right after get")
	}
}

// GetMany 同键重复，后一次看到重置后的 la，费用不同；等价于逐个 Get。
func TestGetManyRepeatedKey(t *testing.T) {
	mk := func() *Billing {
		b, _ := New(exampleCfg())
		b.Put("k", 10, 0)
		return b
	}
	b1 := mk()
	got, err := b1.GetMany([]string{"k", "k"}, 40)
	if err != nil {
		t.Fatal(err)
	}
	wantInt(t, "many[0] storage", got[0].Storage, 3400)
	wantInt(t, "many[0] exit", got[0].Exit, 800)
	wantInt(t, "many[0] retrieval", got[0].Retriev, 10)
	wantInt(t, "many[1] storage", got[1].Storage, 0)
	wantInt(t, "many[1] exit", got[1].Exit, 0)
	wantInt(t, "many[1] retrieval", got[1].Retriev, 0)
	if feesEqual(got[0], got[1]) {
		t.Fatal("repeated key fees unexpectedly equal")
	}

	b2 := mk()
	g1, _ := b2.Get("k", 40)
	g2, _ := b2.Get("k", 40)
	if !feesEqual(got[0], g1) || !feesEqual(got[1], g2) {
		t.Fatalf("GetMany != sequential Get")
	}
}

type stateSnap struct {
	maxNow int64
	period int64
	used   int64
	objs   string
}

func snapshot(b *Billing) stateSnap {
	q := b.QuotaState()
	keys := b.Keys()
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		o, _ := b.Lookup(k)
		parts = append(parts, fmt.Sprintf("%s=%d@%d", k, o.Size, o.LA))
	}
	return stateSnap{b.MaxNow(), q.Period, q.Used, strings.Join(parts, ",")}
}

// GetMany 一键不存在时整体拒绝，不改任何状态。
func TestGetManyAllOrNothing(t *testing.T) {
	b, _ := New(exampleCfg())
	b.Put("a", 10, 0)
	before := snapshot(b)
	got, err := b.GetMany([]string{"a", "missing"}, 40)
	if !errors.Is(err, ErrNotFound) || got != nil {
		t.Fatalf("GetMany = %v, %v; want ErrNotFound", got, err)
	}
	if after := snapshot(b); after != before {
		t.Fatalf("state changed on rejected GetMany:\nbefore %+v\nafter  %+v", before, after)
	}
}

// 拒绝（参数非法、时钟回退、不存在）不改状态，并按顺序只报第一个原因。
func TestRejectionsNoStateChange(t *testing.T) {
	b, _ := New(exampleCfg())
	b.Put("k", 10, 0)
	if _, err := b.Get("k", 40); err != nil {
		t.Fatal(err)
	}
	before := snapshot(b)

	check := func(name string, expect error, fn func() (Fees, error)) {
		t.Helper()
		f, err := fn()
		if !errors.Is(err, expect) {
			t.Fatalf("%s: err = %v, want %v", name, err, expect)
		}
		if !feesZero(f) {
			t.Fatalf("%s: fees = %+v, want zero", name, f)
		}
		if after := snapshot(b); after != before {
			t.Fatalf("%s changed state:\nbefore %+v\nafter  %+v", name, before, after)
		}
	}

	check("empty key", ErrInvalidArgs, func() (Fees, error) { return b.Put("", 1, 41) })
	check("long key", ErrInvalidArgs, func() (Fees, error) { return b.Put(strings.Repeat("x", 65), 1, 41) })
	check("size 0", ErrInvalidArgs, func() (Fees, error) { return b.Put("n", 0, 41) })
	check("size big", ErrInvalidArgs, func() (Fees, error) { return b.Put("n", 1e6+1, 41) })
	check("now neg", ErrInvalidArgs, func() (Fees, error) { return b.Put("n", 1, -1) })
	// 同时参数非法+时钟回退，只报参数非法。
	check("args before clock", ErrInvalidArgs, func() (Fees, error) { return b.Put("n", 0, 0) })
	// 时钟回退先于不存在。
	check("clock rollback put", ErrClockRollback, func() (Fees, error) { return b.Put("n", 1, 39) })
	check("clock rollback get", ErrClockRollback, func() (Fees, error) { return b.Get("ghost", 39) })
	check("not found get", ErrNotFound, func() (Fees, error) { return b.Get("ghost", 41) })
	check("not found delete", ErrNotFound, func() (Fees, error) { return b.Delete("ghost", 41) })

	if _, err := b.GetMany([]string{"k", "ghost"}, 39); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("GetMany rollback err = %v", err)
	}
	if after := snapshot(b); after != before {
		t.Fatalf("GetMany rollback changed state")
	}
}

// 配置越界整体拒绝。
func TestInvalidConfig(t *testing.T) {
	base := exampleCfg()
	bad := []Config{
		base, // 对照：合法
	}
	good := bad[0]
	if _, err := New(good); err != nil {
		t.Fatalf("base config should be valid: %v", err)
	}
	mutate := func(fn func(*Config)) Config {
		c := base
		fn(&c)
		return c
	}
	cases := []Config{
		mutate(func(c *Config) { c.P0 = -1 }),
		mutate(func(c *Config) { c.P2 = 1e6 + 1 }),
		mutate(func(c *Config) { c.A = 0 }),
		mutate(func(c *Config) { c.B = c.A }),
		mutate(func(c *Config) { c.B = 1e6 + 1 }),
		mutate(func(c *Config) { c.M1 = -1 }),
		mutate(func(c *Config) { c.R2 = -1 }),
		mutate(func(c *Config) { c.Q = -1 }),
		mutate(func(c *Config) { c.Q = 1e12 + 1 }),
	}
	for i, c := range cases {
		if _, err := New(c); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d: err = %v, want ErrInvalidConfig", i, err)
		}
	}
}

// Settle 纯查询与 Get 中的存储/补费一致。
func TestSettlePure(t *testing.T) {
	cfg := exampleCfg()
	f, err := Settle(cfg, Object{Size: 10, LA: 40}, 200)
	if err != nil {
		t.Fatal(err)
	}
	wantInt(t, "settle storage", f.Storage, 6100)
	wantInt(t, "settle exit", f.Exit, 200)
	wantInt(t, "settle retrieval", f.Retriev, 0)

	if _, err := Settle(cfg, Object{Size: 10, LA: 40}, 39); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("settle now<la err = %v", err)
	}
}

// 极端量级：Δ 可达 10^9，费用远超 int64，必须仍精确非负。
func TestExtremePrecision(t *testing.T) {
	cfg := Config{P0: 1e6, P1: 1e6, P2: 1e6, A: 1, B: 2, M1: 1e6, M2: 1e6, R1: 1e6, R2: 1e6, Q: 0}
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Put("k", 1e6, 0); err != nil {
		t.Fatal(err)
	}
	f, err := b.Delete("k", 1e9)
	if err != nil {
		t.Fatal(err)
	}
	// 热 1 天 + 凉 1 天 + 冷 (10^9-2) 天 = 10^9 天，全部 p=10^6，size=10^6。
	want, ok := big.NewInt(0).SetString("1000000000000000000000", 10) // 10^21
	if !ok {
		t.Fatal("bad literal")
	}
	if f.Storage.Cmp(want) != 0 {
		t.Fatalf("storage = %s, want %s", f.Storage.String(), want.String())
	}
	if f.Storage.Sign() < 0 || f.Exit.Sign() < 0 || f.Retriev.Sign() < 0 {
		t.Fatal("fees must be non-negative")
	}
}

package ontology

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// countingUpstream 按(查询输入)确定性地决定应答：同一序列内随机但可复现。
type countingUpstream struct {
	mu    sync.Mutex
	rng   *rand.Rand
	calls int
	log   []string
	tag   string
}

func newCountingUpstream(seed int64, tag string) *countingUpstream {
	return &countingUpstream{rng: rand.New(rand.NewSource(seed)), tag: tag}
}

func (u *countingUpstream) Count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls
}

func (u *countingUpstream) Resolve(q Query) (Answer, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls++
	u.log = append(u.log, fmt.Sprintf("[%s] UP#%d %s type=%d fam=%d src=%d",
		u.tag, u.calls, q.Name, q.Rrtype, q.Family, q.SrcPrefix))

	// 约 12% 概率返回上游失败（不写缓存）。
	if u.rng.Intn(8) == 0 {
		return Answer{}, errBoom
	}
	kinds := []Kind{KindRecords, KindRecords, KindRecords, KindNoName, KindNoType}
	kind := kinds[u.rng.Intn(len(kinds))]
	ttl := []int{0, 1, 2, 5, 10, 60}[u.rng.Intn(6)]
	maxBits := 32
	if q.Family == FamilyV6 {
		maxBits = 128
	}
	scope := u.rng.Intn(maxBits + 1)
	ans := Answer{Kind: kind, TTL: ttl, ScopePrefix: scope}
	if kind == KindRecords {
		ans.Records = []byte(fmt.Sprintf("rec-%d-%d", u.calls, ttl))
	}
	return ans, nil
}

type diffStep struct {
	op      int // 0=查询, 1=推进时钟
	advance time.Duration
	q       Query
}

func randomStep(rng *rand.Rand) diffStep {
	if rng.Intn(5) == 0 {
		return diffStep{op: 1, advance: time.Duration(1+rng.Intn(6)) * time.Second}
	}
	v6 := rng.Intn(2) == 0
	var fam AddrFamily
	var client Addr
	maxBits := 32
	if v6 {
		fam = FamilyV6
		maxBits = 128
		b := make(Addr, 16)
		for i := range b {
			b[i] = byte(rng.Intn(256))
		}
		client = b
	} else {
		fam = FamilyV4
		client = Addr{byte(rng.Intn(4)), byte(rng.Intn(4)), byte(rng.Intn(8)), byte(rng.Intn(8))}
	}
	names := []string{"example.com.", "EXAMPLE.com", "a.b.c", "A.B.C.", "nx.test.", "mixed.CoM"}
	return diffStep{op: 0, q: Query{
		Name:      names[rng.Intn(len(names))],
		Rrtype:    []uint16{1, 1, 28, 15}[rng.Intn(4)],
		Family:    fam,
		Client:    client,
		SrcPrefix: rng.Intn(maxBits + 1),
	}}
}

func resultKey(r Result, err error) string {
	if err != nil {
		return "ERR:" + err.Error()
	}
	return fmt.Sprintf("%s:%s", kindName(r.Kind), string(r.Records))
}

func snapshotsEqual(a, b []string) bool {
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

// TestDifferentialAgainstNaiveModel 对 1200 组随机序列逐拍比较生产实现与朴素模型：
// 返回值/错误、上游调用次数、存活条目快照三者必须完全一致；每步输入/输出/依据
// 记录到日志文件（失败时也打印到测试日志）。
func TestDifferentialAgainstNaiveModel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping differential in short mode")
	}
	const sequences, steps, k = 1200, 40, 4
	logPath := os.Getenv("DIFF_LOG")
	if logPath == "" {
		logPath = "differential.log"
	}
	lf, ferr := os.Create(logPath)
	if ferr != nil {
		t.Fatal(ferr)
	}
	defer lf.Close()

	for seq := 0; seq < sequences; seq++ {
		seed := int64(seq*7919 + 17)
		rng := rand.New(rand.NewSource(seed))
		clk := newFakeClock()
		upReal := newCountingUpstream(seed, "real")
		upNaive := newCountingUpstream(seed, "naive")
		real := NewCache(k, clk, upReal)
		naive := newNaiveModel(k, clk, upNaive) // 独立实例、相同种子 -> 应答序列一致

		var logb strings.Builder
		fmt.Fprintf(&logb, "===== sequence %d seed=%d =====\n", seq, seed)

		failf := func(format string, args ...any) {
			t.Helper()
			lf.WriteString(logb.String())
			t.Fatalf("seq %d: %s\n%s", seq, fmt.Sprintf(format, args...), logb.String())
		}

		for st := 0; st < steps; st++ {
			step := randomStep(rng)
			if step.op == 1 {
				clk.advance(step.advance)
				fmt.Fprintf(&logb, "step %d: advance %s (now t=%ds)\n",
					st, step.advance, int(clk.Now().Sub(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)).Seconds()))
				continue
			}
			q := step.q
			fmt.Fprintf(&logb, "step %d: QUERY name=%q type=%d fam=%d client=%s src=%d\n",
				st, q.Name, q.Rrtype, q.Family, ipString(q.Family, q.Client), q.SrcPrefix)

			beforeReal, beforeNaive := upReal.Count(), upNaive.Count()
			r1, e1 := real.Query(q)
			r2, e2 := naive.Query(q)
			k1, k2 := resultKey(r1, e1), resultKey(r2, e2)
			if k1 != k2 {
				failf("result mismatch at step %d: real=%s naive=%s", st, k1, k2)
			}
			if upReal.Count()-beforeReal != upNaive.Count()-beforeNaive {
				failf("upstream call count mismatch at step %d: real=%d naive=%d (total %d vs %d)",
					st, upReal.Count()-beforeReal, upNaive.Count()-beforeNaive,
					upReal.Count(), upNaive.Count())
			}
			basis := "cache-hit"
			if upReal.Count()-beforeReal == 1 {
				basis = "upstream"
			}
			if strings.HasPrefix(k1, "ERR:") {
				basis = "error"
			}
			fmt.Fprintf(&logb, "    -> %s [%s]\n", k1, basis)

			s1 := real.debugLiveEntries()
			s2 := naive.liveEntries(clk.Now())
			if !snapshotsEqual(s1, s2) {
				fmt.Fprintf(&logb, "    real snapshot:  %q\n", s1)
				fmt.Fprintf(&logb, "    naive snapshot: %q\n", s2)
				failf("entry snapshot mismatch at step %d", st)
			}
		}

		// 序列结束后：上游总调用次数须一致；并显式核对每个桶条目数不超过 K。
		if upReal.Count() != upNaive.Count() {
			lf.WriteString(logb.String())
			t.Fatalf("seq %d: total upstream calls real=%d naive=%d",
				seq, upReal.Count(), upNaive.Count())
		}
		for _, snap := range [][]string{real.debugLiveEntries(), naive.liveEntries(clk.Now())} {
			counts := map[string]int{}
			for _, line := range snap {
				// 行首为 name/type
				parts := strings.SplitN(line, "/", 3)
				counts[parts[0]+"/"+parts[1]]++
			}
			for nk2, n := range counts {
				if n > k {
					lf.WriteString(logb.String())
					t.Fatalf("seq %d: bucket %s has %d entries > K=%d", seq, nk2, n, k)
				}
			}
		}
		lf.WriteString(logb.String())
	}
	t.Logf("differential done: %d sequences, %d steps each, log=%s", sequences, steps, logPath)
}

// TestMaskAndCovers 直接锁定前缀工具的边界（全零、非字节对齐、越界清零语义）。
func TestMaskAndCovers(t *testing.T) {
	m := maskPrefix(v4(255, 255, 255, 255), 20)
	if !bytes.Equal(m, v4(255, 255, 240, 0)) { // 20 位 = 11111111.11111111.1111....
		t.Fatalf("/20 mask = %v", m)
	}
	m = maskPrefix(v4(10, 20, 30, 40), 0)
	if !bytes.Equal(m, v4(0, 0, 0, 0)) {
		t.Fatalf("/0 mask = %v", m)
	}
	if !prefixCovers(v4(10, 20, 16, 0), 20, v4(10, 20, 31, 255)) {
		t.Fatal("/20 should cover 10.20.31.255")
	}
	if prefixCovers(v4(10, 20, 16, 0), 20, v4(10, 20, 32, 1)) {
		t.Fatal("/20 should not cover 10.20.32.1")
	}
	v6 := v6h(0x20, 0x01, 0x0d, 0xb8, 0xff)
	m6 := maskPrefix(v6, 34)
	if m6[4] != 0xc0 || m6[5] != 0 {
		t.Fatalf("/34 v6 mask = % x", m6[:6])
	}
	if normalizeName("ExAmPle.COM.") != "example.com" {
		t.Fatal("normalize failed")
	}
	if normalizeName("a..") != "a." {
		t.Fatalf("only one trailing dot stripped, got %q", normalizeName("a.."))
	}
}

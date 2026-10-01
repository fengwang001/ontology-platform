package cache

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// op 是一条可重放的操作。
type op struct {
	kind     string // "put" / "lookup" / "dump" / "len"
	key      string
	manifest []Pair
	result   string
	current  map[string]string
}

var (
	fuzzKeys  = []string{"k0", "k1", "k2", "k3", "k4"}
	fuzzPaths = []string{"a", "b", "c", "d", "e", "f"}
)

func canonicalDigest(path string) string { return "D_" + path }

// genManifest 生成随机清单：路径为池子中随机子集（升序），
// 摘要大概率取规范值以便后续命中，小概率取异值。
func genManifest(rng *rand.Rand) []Pair {
	perm := rng.Perm(len(fuzzPaths))
	size := 1 + rng.Intn(3)
	picked := make([]string, 0, size)
	for _, idx := range perm[:size] {
		picked = append(picked, fuzzPaths[idx])
	}
	sortStrings(picked)
	manifest := make([]Pair, 0, len(picked))
	for _, p := range picked {
		digest := canonicalDigest(p)
		if rng.Intn(4) == 0 {
			digest = "X_" + p
		}
		manifest = append(manifest, Pair{Path: p, Digest: digest})
	}
	return manifest
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// genCurrent 生成随机构建现状：每个路径大概率存在，
// 摘要大概率取规范值，小概率被篡改。
func genCurrent(rng *rand.Rand) map[string]string {
	current := make(map[string]string)
	for _, p := range fuzzPaths {
		if rng.Intn(4) == 0 {
			continue // 文件不存在
		}
		digest := canonicalDigest(p)
		if rng.Intn(4) == 0 {
			digest = "tampered_" + p
		}
		current[p] = digest
	}
	return current
}

// genOps 生成一条随机操作序列（含少量非法 Put 以核对拒绝行为）。
func genOps(rng *rand.Rand) []op {
	n := 20 + rng.Intn(30)
	ops := make([]op, 0, n)
	for i := 0; i < n; i++ {
		switch rng.Intn(10) {
		case 0, 1, 2, 3: // Put
			ops = append(ops, op{
				kind:     "put",
				key:      fuzzKeys[rng.Intn(len(fuzzKeys))],
				manifest: genManifest(rng),
				result:   fmt.Sprintf("r%d", rng.Intn(1000)),
			})
		case 4: // 非法 Put
			ops = append(ops, genInvalidPut(rng))
		case 5, 6, 7: // Lookup
			ops = append(ops, op{
				kind:    "lookup",
				key:     fuzzKeys[rng.Intn(len(fuzzKeys))],
				current: genCurrent(rng),
			})
		case 8: // Dump
			key := fuzzKeys[rng.Intn(len(fuzzKeys))]
			if rng.Intn(4) == 0 {
				key = ""
			}
			ops = append(ops, op{kind: "dump", key: key})
		default: // Len
			ops = append(ops, op{kind: "len"})
		}
	}
	return ops
}

func genInvalidPut(rng *rand.Rand) op {
	bad := op{kind: "put", key: "k0", manifest: mf("a", "da"), result: "r"}
	switch rng.Intn(6) {
	case 0:
		bad.key = ""
	case 1:
		bad.manifest = nil
	case 2:
		bad.manifest = mf("", "da")
	case 3:
		bad.manifest = mf("b", "db", "a", "da")
	case 4:
		bad.manifest = mf("a", "")
	case 5:
		bad.result = ""
	}
	return bad
}

// applyOp 对两个实现同时施加一条操作并比较输出，
// 返回该操作的日志行（输入、输出与判定依据）。
func applyOp(t *testing.T, c *Cache, n *naiveCache, o op) string {
	t.Helper()
	switch o.kind {
	case "put":
		errC := c.Put(o.key, o.manifest, o.result)
		errN := n.put(o.key, o.manifest, o.result)
		if (errC == nil) != (errN == nil) || (errC != nil && errC != errN) {
			t.Fatalf("Put 输出不一致: Cache=%v naive=%v, 输入=%+v", errC, errN, o)
		}
		basis := "新增条目并按键内->全局次序淘汰"
		if errC != nil {
			basis = "校验失败, 整体拒绝且不改 tick 与条目"
		}
		return fmt.Sprintf("输入=Put(%q, %v, %q) 输出=err:%v 判定依据=%s",
			o.key, o.manifest, o.result, errC, basis)
	case "lookup":
		resC, hitC, errC := c.Lookup(o.key, o.current)
		resN, hitN, errN := n.lookup(o.key, o.current)
		if resC != resN || hitC != hitN || (errC == nil) != (errN == nil) {
			t.Fatalf("Lookup 输出不一致: Cache=(%q,%v,%v) naive=(%q,%v,%v), 输入=%+v",
				resC, hitC, errC, resN, hitN, errN, o)
		}
		basis := "未命中: 该键无条目清单在现状下逐对匹配, 不改 tick"
		if hitC {
			basis = "命中: 取该键现状下逐对匹配且 last 最大者, 刷新 tick 与 last"
		}
		return fmt.Sprintf("输入=Lookup(%q, %v) 输出=(%q, hit=%v, err:%v) 判定依据=%s",
			o.key, o.current, resC, hitC, errC, basis)
	case "dump":
		gotC := c.Dump(o.key)
		gotN := n.dump(o.key)
		if !entriesEqual(gotC, gotN) {
			t.Fatalf("Dump 输出不一致: Cache=%v naive=%v, 输入=%+v", gotC, gotN, o)
		}
		return fmt.Sprintf("输入=Dump(%q) 输出=%v 判定依据=按 last 降序, 空键或不存在返回空列表",
			o.key, gotC)
	default:
		gotC := c.Len()
		gotN := len(n.all)
		if gotC != gotN {
			t.Fatalf("Len 输出不一致: Cache=%d naive=%d", gotC, gotN)
		}
		return fmt.Sprintf("输入=Len() 输出=%d 判定依据=全局条目总数", gotC)
	}
}

func entriesEqual(a, b []Entry) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// checkState 在每条操作后比较两个实现的完整可观测状态
// （tick、Len、每个键的 Dump），等价于比较淘汰序列。
func checkState(t *testing.T, c *Cache, n *naiveCache) {
	t.Helper()
	if c.tick != n.tick {
		t.Fatalf("tick 不一致: Cache=%d naive=%d", c.tick, n.tick)
	}
	if c.Len() != len(n.all) {
		t.Fatalf("Len 不一致: Cache=%d naive=%d", c.Len(), len(n.all))
	}
	for _, key := range fuzzKeys {
		if got, want := c.Dump(key), n.dump(key); !entriesEqual(got, want) {
			t.Fatalf("键 %s 状态不一致:\nCache=%v\nnaive=%v", key, got, want)
		}
	}
}

// 与线性表朴素实现对拍 2000 组随机操作序列，
// 日志打印每条操作的输入、输出与判定依据。
func TestDifferentialRandom(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)))
		m := 1 + rng.Intn(4)
		capN := 1 + rng.Intn(12)
		c, err := New(m, capN)
		if err != nil {
			t.Fatal(err)
		}
		n := newNaive(m, capN)
		ops := genOps(rng)
		t.Logf("seq=%d 参数: M=%d Cap=%d 操作数=%d", seq, m, capN, len(ops))
		for i, o := range ops {
			line := applyOp(t, c, n, o)
			t.Logf("seq=%d op=%d %s", seq, i, line)
			checkState(t, c, n)
		}
		t.Logf("seq=%d 终态: tick=%d Len=%d 判定一致", seq, c.tick, c.Len())
	}
}

// 相同操作序列重放得到完全相同的命中结果、淘汰序列与 tick。
func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	ops := genOps(rng)

	run := func() ([]string, uint64, map[string][]Entry) {
		c, err := New(3, 8)
		if err != nil {
			t.Fatal(err)
		}
		n := newNaive(3, 8)
		logs := make([]string, 0, len(ops))
		for _, o := range ops {
			logs = append(logs, applyOp(t, c, n, o))
		}
		dumps := make(map[string][]Entry)
		for _, key := range fuzzKeys {
			dumps[key] = c.Dump(key)
		}
		return logs, c.tick, dumps
	}

	logs1, tick1, dumps1 := run()
	logs2, tick2, dumps2 := run()
	if !reflect.DeepEqual(logs1, logs2) {
		t.Fatal("重放的输出序列不一致")
	}
	if tick1 != tick2 {
		t.Fatalf("重放的 tick 不一致: %d vs %d", tick1, tick2)
	}
	if !reflect.DeepEqual(dumps1, dumps2) {
		t.Fatalf("重放的淘汰序列不一致:\n%v\nvs\n%v", dumps1, dumps2)
	}
	t.Logf("重放两次: %d 条操作的输出、tick=%d 与淘汰序列完全一致", len(ops), tick1)
}

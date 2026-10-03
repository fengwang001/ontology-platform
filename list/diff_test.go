package list_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/authz"
	"ontology/keyindex"
	"ontology/list"
)

// modelKey 描述朴素扫描用的一条数据。
type modelKey struct {
	key      string
	versions map[int]bool // 版本号 => 是否删除标记
}

func modelVisible(k string, grants []string) bool {
	for _, g := range grants {
		if g == "" || strings.HasPrefix(k, g) {
			return true
		}
	}
	return false
}

// naiveScan 按题意逐键朴素扫描，生成锚点之后的全部条目（已按正确顺序）。
// 先按 startAfter 过滤，再判授权：startAfter 落在组中间时公共前缀只由其后的可见键产生。
func naiveScan(keys []modelKey, kind string, prefix, delim, startAfter string, grants []string) []string {
	var out []string
	seenGroup := map[string]bool{}
	for _, mk := range keys {
		if startAfter != "" && mk.key <= startAfter {
			continue
		}
		if !modelVisible(mk.key, grants) {
			continue
		}
		if !strings.HasPrefix(mk.key, prefix) {
			continue
		}
		rest := mk.key[len(prefix):]
		if delim != "" {
			if i := strings.Index(rest, delim); i >= 0 {
				p := prefix + rest[:i+1]
				if !seenGroup[p] {
					seenGroup[p] = true
					out = append(out, "P:"+p)
				}
				continue
			}
		}
		if kind == "obj" {
			top := 0
			for n := range mk.versions {
				if n > top {
					top = n
				}
			}
			if top > 0 && !mk.versions[top] {
				out = append(out, "K:"+mk.key)
			}
		} else {
			nums := make([]int, 0, len(mk.versions))
			for n := range mk.versions {
				nums = append(nums, n)
			}
			// 降序
			for i := 0; i < len(nums); i++ {
				for j := i + 1; j < len(nums); j++ {
					if nums[j] > nums[i] {
						nums[i], nums[j] = nums[j], nums[i]
					}
				}
			}
			for _, n := range nums {
				out = append(out, fmt.Sprintf("V:%s:%d:%v", mk.key, n, mk.versions[n]))
			}
		}
	}
	return out
}

func entryCode(e list.Entry) string {
	if e.IsPrefix {
		return "P:" + e.Key
	}
	if e.Version != 0 || e.Deleted {
		return fmt.Sprintf("V:%s:%d:%v", e.Key, e.Version, e.Deleted)
	}
	return "K:" + e.Key
}

func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	const cases = 1500

	for iter := 0; iter < cases; iter++ {
		// 生成键：两层目录结构 + 扁平键，小字母表制造大量公共前缀。
		nKeys := rng.Intn(40) + 1
		mkMap := map[string]map[int]bool{}
		allKeys := make([]string, 0, nKeys)
		genKey := func() string {
			switch rng.Intn(3) {
			case 0:
				return string(rune('a' + rng.Intn(6)))
			case 1:
				return fmt.Sprintf("%c/%c", 'a'+rng.Intn(4), 'a'+rng.Intn(6))
			default:
				return fmt.Sprintf("%c/%c/%d", 'a'+rng.Intn(3), 'a'+rng.Intn(4), rng.Intn(5))
			}
		}
		for i := 0; i < nKeys; i++ {
			k := genKey()
			if _, ok := mkMap[k]; ok {
				continue
			}
			nv := rng.Intn(3) + 1
			vers := map[int]bool{}
			n := rng.Intn(6) + 1
			for v := 0; v < nv; v++ {
				vers[n] = rng.Intn(3) == 0
				n += rng.Intn(3) + 1
			}
			mkMap[k] = vers
			allKeys = append(allKeys, k)
		}

		// 索引装载。
		ix := keyindex.New()
		model := make([]modelKey, 0, len(mkMap))
		for k, vers := range mkMap {
			for n, deleted := range vers {
				ix.Put(k, n, deleted)
			}
			model = append(model, modelKey{key: k, versions: vers})
		}
		sortModel(model)

		// 请求参数。
		prefix := []string{"", "a", "a/", "b", "a/b/"}[rng.Intn(5)]
		delim := []string{"", "/"}[rng.Intn(2)]
		max := rng.Intn(6) + 1

		// 授权：空、全可见、随机前缀。
		var grants []string
		switch rng.Intn(4) {
		case 0:
			grants = nil
		case 1:
			grants = []string{""}
		default:
			pool := []string{"", "a", "a/", "b", "a/b", "c", "a/a/"}
			g := rng.Intn(3) + 1
			for i := 0; i < g; i++ {
				grants = append(grants, pool[rng.Intn(len(pool))])
			}
		}

		var startAfter string
		if len(allKeys) > 0 && rng.Intn(2) == 0 {
			startAfter = allKeys[rng.Intn(len(allKeys))]
		}
		kind := []string{"obj", "ver"}[rng.Intn(2)]

		req := list.Request{
			Prefix: prefix, Delimiter: delim, StartAfter: startAfter,
			Max: max, Grants: grants,
		}

		// 朴素模型：先按授权过滤，再逐键扫描。
		norm := authz.Normalize(grants)
		modelSeq := naiveScan(model, kind, prefix, delim, startAfter, norm)

		// 真实分页遍历。
		var realSeq []string
		pageEdges := []int{0}
		pageTrunc := []bool{}
		pageReq := req
		pages := 0
		for {
			var (
				res *list.Result
				err error
			)
			if kind == "obj" {
				res, err = list.ListObjects(ix, pageReq)
			} else {
				res, err = list.ListVersions(ix, pageReq)
			}
			if err != nil {
				t.Fatalf("iter=%d 意外错误 %v；输入 prefix=%q delim=%q start=%q max=%d grants=%q kind=%s",
					iter, err, prefix, delim, startAfter, max, grants, kind)
			}
			for _, e := range res.Entries {
				realSeq = append(realSeq, entryCode(e))
			}
			pageEdges = append(pageEdges, len(realSeq))
			pageTrunc = append(pageTrunc, res.IsTruncated)
			pages++
			if !res.IsTruncated {
				break
			}
			if pages > 100 {
				t.Fatalf("iter=%d 页数失控", iter)
			}
			pageReq.Token = res.NextToken
			pageReq.StartAfter = ""
		}

		// 判定 1：条目序列与朴素扫描完全一致（不重不漏、顺序一致）。
		if !eqStrings(realSeq, modelSeq) {
			t.Fatalf("iter=%d 结果不符\n输入 prefix=%q delim=%q start=%q max=%d grants=%q(norm=%q) kind=%s\n朴素=%v\n真实=%v",
				iter, prefix, delim, startAfter, max, grants, norm, kind, modelSeq, realSeq)
		}
		// 判定 2：每页截断标记与模型分块一致。
		for p, trunc := range pageTrunc {
			consumed := pageEdges[p+1]
			wantTrunc := consumed < len(modelSeq)
			if trunc != wantTrunc {
				t.Fatalf("iter=%d page=%d 截断=%v 期望 %v (consumed=%d total=%d)",
					iter, p, trunc, wantTrunc, consumed, len(modelSeq))
			}
			if p < pages-1 {
				gotN := pageEdges[p+1] - pageEdges[p]
				if gotN != max {
					t.Fatalf("iter=%d page=%d 非末页条目数=%d 期望 max=%d", iter, p, gotN, max)
				}
			}
		}

		// 判定 3：seeks 上界（仅 ListObjects 首页，无令牌的等价重放）。
		if kind == "obj" {
			ix.ResetSeeks()
			before := ix.Seeks()
			_, _ = list.ListObjects(ix, req)
			used := ix.Seeks() - before
			nEntries := max
			if len(modelSeq) < max {
				nEntries = len(modelSeq)
			}
			markers := countCurrentMarkers(model, norm, prefix)
			bound := 2*(nEntries+len(norm)+markers) + 2
			if int(used) > bound {
				t.Fatalf("iter=%d seeks=%d 超过上界 2*(%d+%d+%d)+2=%d",
					iter, used, nEntries, len(norm), markers, bound)
			}
		}

		// 判定 4：相同输入重放结果完全相同。
		replayReq := req
		var replay []string
		for {
			var res *list.Result
			if kind == "obj" {
				res, _ = list.ListObjects(ix, replayReq)
			} else {
				res, _ = list.ListVersions(ix, replayReq)
			}
			for _, e := range res.Entries {
				replay = append(replay, entryCode(e))
			}
			if !res.IsTruncated {
				break
			}
			replayReq.Token = res.NextToken
			replayReq.StartAfter = ""
		}
		if !eqStrings(replay, realSeq) {
			t.Fatalf("iter=%d 重放不一致", iter)
		}

		t.Logf("iter=%d 输入={prefix:%q delim:%q start:%q max:%d grants:%q kind:%s} 输出=%v 判定=与朴素扫描一致、截断正确、seeks 合界、重放相同",
			iter, prefix, delim, startAfter, max, grants, kind, realSeq)
	}
}

func countCurrentMarkers(model []modelKey, grants []string, prefix string) int {
	n := 0
	for _, mk := range model {
		if !modelVisible(mk.key, grants) || !strings.HasPrefix(mk.key, prefix) {
			continue
		}
		top, topDel := 0, false
		for v, del := range mk.versions {
			if v > top {
				top, topDel = v, del
			}
		}
		if topDel {
			n++
		}
	}
	return n
}

func sortModel(m []modelKey) {
	for i := 0; i < len(m); i++ {
		for j := i + 1; j < len(m); j++ {
			if m[j].key < m[i].key {
				m[i], m[j] = m[j], m[i]
			}
		}
	}
}

func eqStrings(a, b []string) bool {
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

func TestSeeksGroupSizeInvariant(t *testing.T) {
	const groups = 100
	build := func(perGroup int) (*keyindex.Index, int64) {
		ix := keyindex.New()
		for g := 0; g < groups; g++ {
			gname := fmt.Sprintf("g%02d/", g)
			for j := 0; j < perGroup; j++ {
				ix.Put(fmt.Sprintf("%sk%05d", gname, j), 1, false)
			}
		}
		ix.ResetSeeks()
		// 每页 10 个公共前缀，遍历全部 100 组，统计总寻址。
		req := list.Request{Delimiter: "/", Max: 10, Grants: []string{""}}
		totalEntries := 0
		pages := 0
		for {
			res, err := list.ListObjects(ix, req)
			if err != nil {
				t.Fatal(err)
			}
			totalEntries += len(res.Entries)
			pages++
			if !res.IsTruncated {
				break
			}
			req.Token = res.NextToken
		}
		if totalEntries != groups {
			t.Fatalf("perGroup=%d 条目=%d 期望 %d 个公共前缀", perGroup, totalEntries, groups)
		}
		return ix, ix.Seeks()
	}

	_, seeks10 := build(10)
	_, seeks10k := build(10000)
	t.Logf("100组×10键 seeks=%d；100组×10000键 seeks=%d（应相等，不逐键扫组内）", seeks10, seeks10k)
	if seeks10 != seeks10k {
		t.Fatalf("两档寻址次数不等: %d vs %d", seeks10, seeks10k)
	}

	// 同时验证上界：首页 10 条 + 1 授权 + 0 标记。
	ix := keyindex.New()
	for g := 0; g < groups; g++ {
		for j := 0; j < 10000; j++ {
			ix.Put(fmt.Sprintf("g%02d/k%05d", g, j), 1, false)
		}
	}
	ix.ResetSeeks()
	before := ix.Seeks()
	_, err := list.ListObjects(ix, list.Request{Delimiter: "/", Max: 10, Grants: []string{""}})
	if err != nil {
		t.Fatal(err)
	}
	used := ix.Seeks() - before
	bound := 2*(10+1+0) + 2
	if int(used) > bound {
		t.Fatalf("首页 seeks=%d 超过上界 %d", used, bound)
	}
	t.Logf("首页 seeks=%d ≤ 上界 %d（判定依据=succ 整组跳过+授权起点寻址）", used, bound)
}

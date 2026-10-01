package indexorder

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// 本文件用一套按规则逐列比对的朴素实现与 Registry.Choose 对拍，
// 覆盖 2000 组随机索引集合与查询，并在日志中打印输入、输出与判定依据。

func dirLabel(d ScanDirection) string {
	if d == Forward {
		return "正向"
	}
	if d == 0 {
		return "-"
	}
	return "反向"
}

func itemLabel(it ColumnItem) string {
	dir := "ASC"
	if it.Dir == Desc {
		dir = "DESC"
	}
	nulls := "NF"
	if it.Nulls == NullsLast {
		nulls = "NL"
	}
	return fmt.Sprintf("%s %s %s", it.Column, dir, nulls)
}

func itemsLabel(items []ColumnItem) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = itemLabel(it)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// naiveSatisfies 逐列比对的朴素满足判定，并返回判定依据。
func naiveSatisfies(idx Index, eqSet map[string]bool, need []ColumnItem, dir ScanDirection) (bool, string) {
	next := 0
	for i, c := range idx.Items {
		if next == len(need) {
			return true, fmt.Sprintf("need 在列项%d 前已全部匹配", i)
		}
		if eqSet[c.Column] {
			continue
		}
		n := need[next]
		if c.Column != n.Column {
			return false, fmt.Sprintf("列项%d 列名 %q 与 need[%d] 列名 %q 不符", i, c.Column, next, n.Column)
		}
		wantDir, wantNulls := n.Dir, n.Nulls
		if dir == Backward {
			wantDir = flipDirection(n.Dir)
			wantNulls = flipNulls(n.Nulls)
		}
		if c.Dir != wantDir || c.Nulls != wantNulls {
			return false, fmt.Sprintf("列项%d(%s) 方向/空值位置与 need[%d] 期望不符", i, c.Column, next)
		}
		next++
	}
	if next == len(need) {
		return true, "need 全部匹配"
	}
	return false, fmt.Sprintf("索引列项走完，need 还剩 %d 项", len(need)-next)
}

// naiveChoose 是朴素参考实现：显式构造候选、排序选取，并返回判定依据。
func naiveChoose(indexes []Index, eq []string, order []ColumnItem) (string, ScanDirection, error, string) {
	eqSet := map[string]bool{}
	for _, c := range eq {
		eqSet[c] = true
	}
	seen := map[string]bool{}
	var need []ColumnItem
	for _, it := range order {
		if eqSet[it.Column] || seen[it.Column] {
			continue
		}
		seen[it.Column] = true
		need = append(need, it)
	}

	type cand struct {
		name  string
		dir   ScanDirection
		nItem int
	}
	var cands []cand
	var reasons []string
	for _, idx := range indexes {
		okF, whyF := naiveSatisfies(idx, eqSet, need, Forward)
		if okF {
			cands = append(cands, cand{idx.Name, Forward, len(idx.Items)})
			reasons = append(reasons, fmt.Sprintf("索引 %s 正向满足(%s)", idx.Name, whyF))
			continue
		}
		okB, whyB := naiveSatisfies(idx, eqSet, need, Backward)
		if okB {
			cands = append(cands, cand{idx.Name, Backward, len(idx.Items)})
			reasons = append(reasons, fmt.Sprintf("索引 %s 反向满足(%s)", idx.Name, whyB))
			continue
		}
		reasons = append(reasons, fmt.Sprintf("索引 %s 不满足(正向:%s;反向:%s)", idx.Name, whyF, whyB))
	}
	if len(cands) == 0 {
		return "", 0, ErrNoSatisfyingIndex, strings.Join(reasons, "; ")
	}
	sort.Slice(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.dir != b.dir {
			return a.dir == Forward
		}
		if a.nItem != b.nItem {
			return a.nItem < b.nItem
		}
		return a.name < b.name
	})
	best := cands[0]
	why := strings.Join(reasons, "; ") + fmt.Sprintf("; 选中 %s(%s)", best.name, dirLabel(best.dir))
	return best.name, best.dir, nil, why
}

var randColumns = []string{"a", "b", "c", "d", "e"}

func randDir(rng *rand.Rand) Direction {
	if rng.Intn(2) == 0 {
		return Asc
	}
	return Desc
}

func randNulls(rng *rand.Rand) NullsPos {
	if rng.Intn(2) == 0 {
		return NullsFirst
	}
	return NullsLast
}

// randIndex 生成一个合法索引：列名不重复、方向与空值位置合法。
func randIndex(rng *rand.Rand, name string) Index {
	cols := append([]string(nil), randColumns...)
	rng.Shuffle(len(cols), func(i, j int) { cols[i], cols[j] = cols[j], cols[i] })
	n := 1 + rng.Intn(4)
	items := make([]ColumnItem, 0, n)
	for _, c := range cols[:n] {
		items = append(items, item(c, randDir(rng), randNulls(rng)))
	}
	return Index{Name: name, Items: items}
}

func randEq(rng *rand.Rand) []string {
	cols := append([]string(nil), randColumns...)
	rng.Shuffle(len(cols), func(i, j int) { cols[i], cols[j] = cols[j], cols[i] })
	return cols[:rng.Intn(4)]
}

// randOrder 生成随机 ORDER BY，允许重复列名以覆盖去重规则。
func randOrder(rng *rand.Rand) []ColumnItem {
	n := rng.Intn(6)
	order := make([]ColumnItem, 0, n)
	for i := 0; i < n; i++ {
		order = append(order, item(randColumns[rng.Intn(len(randColumns))], randDir(rng), randNulls(rng)))
	}
	return order
}

func formatIndexes(indexes []Index) string {
	parts := make([]string, len(indexes))
	for i, idx := range indexes {
		parts[i] = idx.Name + itemsLabel(idx.Items)
	}
	return strings.Join(parts, " ")
}

// TestCompareWithNaive 对拍 2000 组随机索引集合与查询。
func TestCompareWithNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	for trial := 0; trial < 2000; trial++ {
		nIdx := 1 + rng.Intn(5)
		indexes := make([]Index, 0, nIdx)
		reg := NewRegistry()
		for i := 0; i < nIdx; i++ {
			idx := randIndex(rng, fmt.Sprintf("idx%02d", rng.Intn(8)))
			if err := reg.Register(idx); err != nil {
				// 名字撞车：朴素侧同样不收录，保持两侧集合一致。
				continue
			}
			indexes = append(indexes, idx)
		}
		// 随机 Drop 一个已登记索引，覆盖已 Drop 不再被选。
		if len(indexes) > 1 && rng.Intn(3) == 0 {
			pos := rng.Intn(len(indexes))
			if err := reg.Drop(indexes[pos].Name); err != nil {
				t.Fatalf("trial %d: Drop(%q) 失败: %v", trial, indexes[pos].Name, err)
			}
			indexes = append(indexes[:pos], indexes[pos+1:]...)
		}

		eq := randEq(rng)
		order := randOrder(rng)

		gotName, gotDir, gotErr := reg.Choose(eq, order)
		wantName, wantDir, wantErr, why := naiveChoose(indexes, eq, order)

		t.Logf("trial %d\n  输入: 索引=[%s] eq=%v order=%s\n  输出: 实现=(%q,%s,err=%v) 朴素=(%q,%s,err=%v)\n  判定依据: %s",
			trial, formatIndexes(indexes), eq, itemsLabel(order),
			gotName, dirLabel(gotDir), gotErr, wantName, dirLabel(wantDir), wantErr, why)

		if !errors.Is(gotErr, wantErr) {
			t.Fatalf("trial %d: 错误不一致: got %v, want %v", trial, gotErr, wantErr)
		}
		if gotErr == nil && (gotName != wantName || gotDir != wantDir) {
			t.Fatalf("trial %d: 结果不一致: got (%q,%v), want (%q,%v)", trial, gotName, gotDir, wantName, wantDir)
		}
	}
}

// TestRegistrationOrderIndependence 同一索引集合与同一查询的选择结果
// 与登记顺序无关。
func TestRegistrationOrderIndependence(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	indexes := make([]Index, 6)
	for i := range indexes {
		indexes[i] = randIndex(rng, fmt.Sprintf("idx%d", i))
	}
	eq := randEq(rng)
	order := randOrder(rng)

	var wantName string
	var wantDir ScanDirection
	var wantErr error
	for perm := 0; perm < 50; perm++ {
		shuffled := append([]Index(nil), indexes...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		reg := NewRegistry()
		for _, idx := range shuffled {
			if err := reg.Register(idx); err != nil {
				t.Fatalf("Register(%q) 失败: %v", idx.Name, err)
			}
		}
		name, dir, err := reg.Choose(eq, order)
		if perm == 0 {
			wantName, wantDir, wantErr = name, dir, err
			continue
		}
		if name != wantName || dir != wantDir || !errors.Is(err, wantErr) {
			t.Fatalf("perm %d: got (%q,%v,%v), want (%q,%v,%v)", perm, name, dir, err, wantName, wantDir, wantErr)
		}
	}
}

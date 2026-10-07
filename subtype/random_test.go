package subtype_test

import (
	"math/rand"
	"testing"

	"ontology/subtype"
)

// 本文件实现随机类型对生成与对照测试：
// 对大量随机类型对，同时用主判定器（共归纳 + 缓存）和独立朴素模型
// （递归展开到足够深度后比较）判定，比较两者结果，
// 并在日志中打印每次输入、输出与判定依据。

// typeGen 随机类型生成器。
type typeGen struct {
	rnd   *rand.Rand
	names []string
}

func newTypeGen(seed int64, numNames int) *typeGen {
	names := make([]string, numNames)
	for i := range names {
		names[i] = string(rune('A' + i))
	}
	return &typeGen{rnd: rand.New(rand.NewSource(seed)), names: names}
}

var propNamePool = []string{"a", "b", "c", "d"}

// genDef 生成命名类型的定义。为避免无保护循环，
// 定义的顶层（及联合成员）只允许对象、函数或基本类型，
// 引用只出现在对象属性与函数参数/返回值等受保护位置。
func (g *typeGen) genDef() subtype.Type {
	switch g.rnd.Intn(10) {
	case 0, 1:
		return g.genFunc(2)
	case 2:
		n := 1 + g.rnd.Intn(2)
		members := make([]subtype.Type, n)
		for i := range members {
			if g.rnd.Intn(2) == 0 {
				members[i] = g.genObject(2)
			} else {
				members[i] = g.genPrim()
			}
		}
		return subtype.Or(members...)
	case 3:
		return g.genPrim()
	default:
		return g.genObject(2)
	}
}

// genType 生成任意类型表达式（用于待比较的左右两侧）。
func (g *typeGen) genType(depth int) subtype.Type {
	if depth <= 0 {
		if len(g.names) > 0 && g.rnd.Intn(2) == 0 {
			return subtype.Ref{Name: g.names[g.rnd.Intn(len(g.names))]}
		}
		return g.genPrim()
	}
	switch g.rnd.Intn(12) {
	case 0, 1, 2:
		return g.genObject(depth)
	case 3:
		return g.genFunc(depth)
	case 4:
		n := g.rnd.Intn(3)
		members := make([]subtype.Type, n)
		for i := range members {
			members[i] = g.genType(depth - 1)
		}
		return subtype.Or(members...)
	case 5, 6, 7:
		if len(g.names) > 0 {
			return subtype.Ref{Name: g.names[g.rnd.Intn(len(g.names))]}
		}
		return g.genPrim()
	case 8:
		return subtype.Top{}
	case 9:
		return subtype.Bottom{}
	default:
		return g.genPrim()
	}
}

func (g *typeGen) genPrim() subtype.Type {
	switch g.rnd.Intn(4) {
	case 0:
		return subtype.Int{}
	case 1:
		return subtype.Float{}
	case 2:
		return subtype.Str{}
	default:
		return subtype.Bool{}
	}
}

func (g *typeGen) genObject(depth int) subtype.Object {
	n := g.rnd.Intn(4)
	perm := g.rnd.Perm(len(propNamePool))
	props := make([]subtype.Prop, 0, n)
	for i := 0; i < n; i++ {
		props = append(props, subtype.Prop{
			Name:     propNamePool[perm[i]],
			Type:     g.genType(depth - 1),
			Optional: g.rnd.Intn(3) == 0,
			ReadOnly: g.rnd.Intn(3) == 0,
		})
	}
	return subtype.Obj(props...)
}

func (g *typeGen) genFunc(depth int) subtype.Func {
	n := g.rnd.Intn(3)
	params := make([]subtype.Type, n)
	for i := range params {
		params[i] = g.genType(depth - 1)
	}
	return subtype.Fn(g.genType(depth-1), params...)
}

// TestRandomCrossCheck 随机对照测试。
func TestRandomCrossCheck(t *testing.T) {
	const iterations = 3000
	g := newTypeGen(20261007, 4)
	mismatches := 0
	for i := 0; i < iterations; i++ {
		// 每轮重新生成环境与待比较类型对。
		r := subtype.NewRegistry()
		defs := make(map[string]subtype.Type)
		for _, name := range g.names {
			def := g.genDef()
			defs[name] = def
			if err := r.Register(name, def); err != nil {
				t.Fatalf("第 %d 轮登记 %s 失败: %v", i, name, err)
			}
		}
		left := g.genType(3)
		right := g.genType(3)

		got, stats, err := r.CheckWithStats(left, right)
		if err != nil {
			t.Fatalf("第 %d 轮判定返回意外错误: %v\n左: %v\n右: %v", i, err, left, right)
		}
		want, err := naiveSubtype(defs, left, right)
		if err != nil {
			t.Fatalf("第 %d 轮朴素模型失败: %v", i, err)
		}
		// 判定依据：主判定器结果、朴素模型结果、命名对统计与上界。
		t.Logf("轮次 %d | 输入: %v <: %v | 输出: %v | 朴素模型: %v | 依据: 两模型一致=%v, 命名对 %d/%d (上界 %d)",
			i, left, right, got, want, got == want,
			stats.DistinctPairs, stats.PairChecks, stats.PairBound())
		if got != want {
			mismatches++
			t.Errorf("第 %d 轮结果不一致: 主判定器=%v 朴素模型=%v\n定义: %v\n左: %v\n右: %v",
				i, got, want, defs, left, right)
		}
		// 命名对数量上界必须始终成立。
		if stats.DistinctPairs > stats.PairBound() {
			t.Fatalf("第 %d 轮命名对数量 %d 超过上界 %d", i, stats.DistinctPairs, stats.PairBound())
		}
		// 同一对类型反复判定结果必须一致。
		again, err := r.Check(left, right)
		if err != nil {
			t.Fatal(err)
		}
		if again != got {
			t.Fatalf("第 %d 轮重复判定结果不一致: %v vs %v", i, got, again)
		}
	}
	if mismatches > 0 {
		t.Fatalf("共 %d/%d 轮结果不一致", mismatches, iterations)
	}
}

// TestRandomRecursiveCrossCheck 专注于递归的对照测试：
// 左右两侧始终是对命名类型的引用，直接考验共归纳判定。
func TestRandomRecursiveCrossCheck(t *testing.T) {
	const iterations = 2000
	g := newTypeGen(20261008, 4)
	for i := 0; i < iterations; i++ {
		r := subtype.NewRegistry()
		defs := make(map[string]subtype.Type)
		for _, name := range g.names {
			def := g.genDef()
			defs[name] = def
			if err := r.Register(name, def); err != nil {
				t.Fatalf("第 %d 轮登记 %s 失败: %v", i, name, err)
			}
		}
		left := subtype.Ref{Name: g.names[g.rnd.Intn(len(g.names))]}
		right := subtype.Ref{Name: g.names[g.rnd.Intn(len(g.names))]}

		got, stats, err := r.CheckWithStats(left, right)
		if err != nil {
			t.Fatalf("第 %d 轮判定返回意外错误: %v\n左: %v\n右: %v", i, err, left, right)
		}
		want, err := naiveSubtype(defs, left, right)
		if err != nil {
			t.Fatalf("第 %d 轮朴素模型失败: %v", i, err)
		}
		t.Logf("轮次 %d | 输入: %v <: %v | 输出: %v | 朴素模型: %v | 依据: 两模型一致=%v, 命名对 %d/%d (上界 %d)",
			i, left, right, got, want, got == want,
			stats.DistinctPairs, stats.PairChecks, stats.PairBound())
		if got != want {
			t.Errorf("第 %d 轮结果不一致: 主判定器=%v 朴素模型=%v\n定义: %v\n左: %v\n右: %v",
				i, got, want, defs, left, right)
		}
		if stats.DistinctPairs > stats.PairBound() {
			t.Fatalf("第 %d 轮命名对数量 %d 超过上界 %d", i, stats.DistinctPairs, stats.PairBound())
		}
	}
}

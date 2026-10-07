package ontology

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"
)

// naiveOracle 是与生产实现相互独立的“朴素继承规则快照模型”：
// 每次变更后把整份规则图与父链拷贝为一个新快照；导出时只读取发起那一刻的快照。
// 线性化采用派生优先的有序 DFS（与生产语义一致但独立实现），规则按最近声明命中。
type naiveRule struct {
	declaring string
	subject   string
	attribute string
	effect    Effect
}

type naiveSnapshot struct {
	parents map[string][]string
	rules   map[naiveRule]int // 规则键 -> 内容版本号（内容寻址：同内容同号）
}

type naiveOracle struct {
	types    map[string]bool
	subjects map[string]bool
	snaps    []*naiveSnapshot
	contents map[int]naiveRule
	nextVer  int
}

func newNaiveOracle() *naiveOracle {
	o := &naiveOracle{
		types:    map[string]bool{},
		subjects: map[string]bool{},
		contents: map[int]naiveRule{},
	}
	o.snaps = append(o.snaps, &naiveSnapshot{parents: map[string][]string{}, rules: map[naiveRule]int{}})
	return o
}

func (o *naiveOracle) cloneLatest() *naiveSnapshot {
	cur := o.snaps[len(o.snaps)-1]
	cp := &naiveSnapshot{parents: map[string][]string{}, rules: map[naiveRule]int{}}
	for k, v := range cur.parents {
		cp.parents[k] = append([]string(nil), v...)
	}
	for k, v := range cur.rules {
		cp.rules[k] = v
	}
	return cp
}

func (o *naiveOracle) commit(s *naiveSnapshot) { o.snaps = append(o.snaps, s) }

func (o *naiveOracle) versionFor(r naiveRule) int {
	for vid, content := range o.contents {
		if content == r {
			return vid
		}
	}
	o.nextVer++
	o.contents[o.nextVer] = r
	return o.nextVer
}

func (o *naiveOracle) reachable(cur string, parents map[string][]string) []string {
	order := []string{}
	seen := map[string]bool{}
	var dfs func(string)
	dfs = func(t string) {
		if seen[t] {
			return
		}
		seen[t] = true
		order = append(order, t)
		for _, parent := range parents[t] {
			dfs(parent)
		}
	}
	dfs(cur)
	return order
}

// evaluate 在给定快照上逐属性判定，返回 属性->(是否排除, 规则版本号)。
func (o *naiveOracle) evaluate(snap *naiveSnapshot, typeID, subject string, attrs []string) map[string]struct {
	excluded bool
	version  int
} {
	chain := o.reachable(typeID, snap.parents)
	out := map[string]struct {
		excluded bool
		version  int
	}{}
	for _, attr := range attrs {
		for _, t := range chain {
			if vid, ok := snap.rules[naiveRule{declaring: t, subject: subject, attribute: attr}]; ok {
				out[attr] = struct {
					excluded bool
					version  int
				}{o.contents[vid].effect == EffectDeny, vid}
				break
			}
		}
	}
	return out
}

// TestDifferentialAgainstNaiveOracle 随机构造规则变更与导出交错序列，
// 将生产实现的每次导出固化结果与朴素模型在同一时刻的判定逐一对照。
func TestDifferentialAgainstNaiveOracle(t *testing.T) {
	seed := time.Now().UnixNano()
	rng := rand.New(rand.NewSource(seed))
	t.Logf("differential seed=%d", seed)

	ctx := context.Background()
	p := New()
	o := newNaiveOracle()

	typeNames := []string{"T0", "T1", "T2", "T3", "T4"}
	subjectNames := []string{"s0", "s1", "s2"}
	attrNames := []string{"x", "y", "z"}

	// 先创建全部类型（父链随机），保持无环：Ti 的父只能来自索引更小的类型。
	sort.Strings(typeNames)
	for i, tn := range typeNames {
		var parents []string
		if i > 0 {
			for j := 0; j < i; j++ {
				if rng.Intn(2) == 0 {
					parents = append(parents, typeNames[j])
				}
			}
		}
		mustOK(t, p.CreateType(ctx, tn, parents))
		o.types[tn] = true
		s := o.cloneLatest()
		s.parents[tn] = append([]string(nil), parents...)
		o.commit(s)
	}
	for _, sn := range subjectNames {
		mustOK(t, p.CreateSubject(ctx, sn))
		o.subjects[sn] = true
		s := o.cloneLatest()
		o.commit(s)
	}

	exportCount := 0
	for step := 0; step < 600; step++ {
		action := rng.Intn(4)
		tn := typeNames[rng.Intn(len(typeNames))]
		sn := subjectNames[rng.Intn(len(subjectNames))]
		an := attrNames[rng.Intn(len(attrNames))]

		switch action {
		case 0, 1: // put DENY / ALLOW
			effect := []Effect{EffectDeny, EffectAllow}[rng.Intn(2)]
			vid, err := p.PutRule(ctx, tn, sn, an, effect)
			mustOK(t, err)
			effectByVersion.Store(vid, effect)
			s := o.cloneLatest()
			key := naiveRule{declaring: tn, subject: sn, attribute: an}
			s.rules[key] = o.versionFor(naiveRule{declaring: tn, subject: sn, attribute: an, effect: effect})
			o.commit(s)
		case 2: // delete
			_, err := p.DeleteRule(ctx, tn, sn, an)
			mustOK(t, err)
			s := o.cloneLatest()
			delete(s.rules, naiveRule{declaring: tn, subject: sn, attribute: an})
			o.commit(s)
		case 3: // export：与朴素模型当前快照对照
			exportCount++
			id := fmt.Sprintf("d-%d", exportCount)
			rec, err := p.Export(ctx, ExportRequest{
				ExportID: id, TypeID: tn, SubjectID: sn, Attributes: append([]string(nil), attrNames...),
			})
			mustOK(t, err)
			curSnap := o.snaps[len(o.snaps)-1]
			want := o.evaluate(curSnap, tn, sn, attrNames)

			gotExcluded := map[string]*ExclusionRecord{}
			for _, ex := range rec.Excluded {
				gotExcluded[ex.Attribute] = ex
			}
			for _, an2 := range attrNames {
				w := want[an2]
				_, isExcluded := gotExcluded[an2]
				if w.excluded != isExcluded {
					t.Fatalf("step=%d export=%s %s/%s/%s: excluded got=%v want=%v (seed=%d)",
						step, id, tn, sn, an2, isExcluded, w.excluded, seed)
				}
				if w.excluded {
					traced, terr := p.Trace(ctx, id, an2)
					mustOK(t, terr)
					if traced.Rule.Effect != o.contents[w.version].effect ||
						traced.Rule.DeclaringType != o.contents[w.version].declaring ||
						traced.Rule.Attribute != an2 || traced.Rule.Subject != sn {
						t.Fatalf("step=%d frozen content mismatch got=%+v want=%+v (seed=%d)",
							step, traced.Rule, o.contents[w.version], seed)
					}
				}
			}
		}
	}
	if exportCount == 0 {
		t.Fatalf("random sequence produced no exports")
	}
	t.Logf("differial compared %d exports against the naive snapshot oracle", exportCount)
}

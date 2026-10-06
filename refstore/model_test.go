package refstore

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// naiveModel 是独立的朴素对照模型：全部用直接了当的写法实现规格，
// 不做任何性能优化（线性扫规则、无剪枝遍历、O(n²) 批内检查），
// 用于与优化后的服务端实现对拍。
type naiveModel struct {
	refs    map[string]string
	commits map[string][]string
	rules   []Rule
	audit   []AuditRecord
}

func naiveNamespace(ref string) (namespace, bool) {
	if strings.HasPrefix(ref, "refs/heads/") {
		return nsBranch, true
	}
	if strings.HasPrefix(ref, "refs/tags/") {
		return nsTag, true
	}
	return 0, false
}

func naiveValidRefName(ref string) bool {
	if ref == "" {
		return false
	}
	for i := 0; i < len(ref); i++ {
		if ref[i] < 0x20 || ref[i] == 0x7f {
			return false
		}
	}
	segs := strings.Split(ref, "/")
	for _, seg := range segs {
		if seg == "" || seg[0] == '.' || strings.HasSuffix(seg, ".lock") {
			return false
		}
	}
	return true
}

func naiveConflict(a, b string) bool {
	as, bs := strings.Split(a, "/"), strings.Split(b, "/")
	shorter, longer := as, bs
	if len(bs) < len(as) {
		shorter, longer = bs, as
	}
	if len(shorter) >= len(longer) {
		return false
	}
	for i := range shorter {
		if shorter[i] != longer[i] {
			return false
		}
	}
	return true
}

// naiveReachable 报告 newID 是否为 oldID 的后代（含相等）：无剪枝全遍历。
func (m *naiveModel) naiveReachable(oldID, newID string) bool {
	seen := map[string]bool{}
	stack := []string{newID}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == oldID {
			return true
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		stack = append(stack, m.commits[id]...)
	}
	return false
}

func naivePatternMatch(pattern, ref string) bool {
	ps, rs := strings.Split(pattern, "/"), strings.Split(ref, "/")
	if len(ps) != len(rs) {
		return false
	}
	for i := range ps {
		if ps[i] != "*" && ps[i] != rs[i] {
			return false
		}
	}
	return true
}

func (m *naiveModel) restrictions(ref string) (noCreate, noDelete, noNonFF, allowOnly bool, allowed map[string]bool) {
	for _, r := range m.rules {
		if !naivePatternMatch(r.Pattern, ref) {
			continue
		}
		noCreate = noCreate || r.NoCreate
		noDelete = noDelete || r.NoDelete
		noNonFF = noNonFF || r.NoNonFF
		if r.AllowOnly {
			allowOnly = true
			if allowed == nil {
				allowed = map[string]bool{}
			}
			for _, u := range r.Users {
				allowed[u] = true
			}
		}
	}
	return
}

func (m *naiveModel) judge(user string, in Instruction) Verdict {
	rej := func(r Reason) Verdict { return Verdict{Reason: r} }
	if in.Old == "" && in.New == "" {
		return rej(ReasonInvalidArgument)
	}
	ns, ok := naiveNamespace(in.Ref)
	if !ok {
		return rej(ReasonInvalidArgument)
	}
	if !naiveValidRefName(in.Ref) {
		return rej(ReasonInvalidRefName)
	}
	if _, exists := m.commits[in.New]; in.New != "" && !exists {
		return rej(ReasonObjectNotFound)
	}
	if m.refs[in.Ref] != in.Old {
		return rej(ReasonOldValueMismatch)
	}
	if in.Old == in.New {
		return Verdict{OK: true}
	}
	noCreate, noDelete, noNonFF, allowOnly, allowed := m.restrictions(in.Ref)
	if in.Old == "" && noCreate {
		return rej(ReasonProtectedNoCreate)
	}
	if in.New == "" && noDelete {
		return rej(ReasonProtectedNoDelete)
	}
	if allowOnly && !allowed[user] {
		return rej(ReasonProtectedAllowlist)
	}
	if ns == nsTag {
		if in.Old != "" && in.New != "" {
			return rej(ReasonTagNoUpdate)
		}
		return Verdict{OK: true}
	}
	if in.Old != "" && in.New != "" && !m.naiveReachable(in.Old, in.New) {
		if noNonFF {
			return rej(ReasonProtectedNoNonFF)
		}
		if !in.Force {
			return rej(ReasonNonFastForward)
		}
	}
	return Verdict{OK: true}
}

func (m *naiveModel) push(user string, ins []Instruction) BatchResult {
	res := BatchResult{Verdicts: make([]Verdict, len(ins))}
	failed := false
	for i, in := range ins {
		res.Verdicts[i] = m.judge(user, in)
		if !res.Verdicts[i].OK {
			failed = true
		}
	}
	if !failed {
		bad := make([]bool, len(ins))
		for i := range ins {
			for j := i + 1; j < len(ins); j++ {
				if ins[i].Ref == ins[j].Ref {
					bad[i], bad[j] = true, true
				}
			}
		}
		for i := range ins {
			if ins[i].Old != "" {
				continue
			}
			for j := range ins {
				if i != j && naiveConflict(ins[i].Ref, ins[j].Ref) {
					bad[i], bad[j] = true, true
				}
			}
			for existing := range m.refs {
				if naiveConflict(ins[i].Ref, existing) {
					bad[i] = true
				}
			}
		}
		for i := range bad {
			if bad[i] {
				res.Verdicts[i] = Verdict{Reason: ReasonBatchConflict}
				failed = true
			}
		}
	}
	if failed {
		for i := range res.Verdicts {
			if res.Verdicts[i].OK {
				res.Verdicts[i] = Verdict{Reason: ReasonNotExecuted}
			}
		}
		return res
	}
	rec := AuditRecord{Seq: uint64(len(m.audit)) + 1, User: user}
	for _, in := range ins {
		rec.Updates = append(rec.Updates, RefUpdate{Ref: in.Ref, Old: m.refs[in.Ref], New: in.New})
		if in.New == "" {
			delete(m.refs, in.Ref)
		} else {
			m.refs[in.Ref] = in.New
		}
	}
	m.audit = append(m.audit, rec)
	res.Applied = true
	res.AuditSeq = rec.Seq
	return res
}

// randomRefName 生成随机引用名，覆盖合法、非法、冲突等各种形态。
func randomRefName(rng *rand.Rand, k int) string {
	base := fmt.Sprintf("r%d", rng.Intn(6))
	switch rng.Intn(12) {
	case 0:
		return "refs/heads/" + base + "/sub" // 可能与 refs/heads/rN 冲突
	case 1:
		return "refs/tags/" + base
	case 2:
		return "refs/tags/" + base + "/deep/x"
	case 3:
		return "refs/heads//bad" // 非法：连续分隔符
	case 4:
		return "refs/heads/.bad" // 非法：点开头
	case 5:
		return "refs/heads/bad.lock" // 非法：.lock 结尾
	case 6:
		return "other/" + base // 非法：命名空间之外
	case 7:
		return "refs/heads/" + base + "/" // 非法：分隔符结尾
	default:
		return fmt.Sprintf("refs/heads/%s", base)
	}
}

func randomRule(rng *rand.Rand) Rule {
	ns := "heads"
	if rng.Intn(2) == 0 {
		ns = "tags"
	}
	pattern := "refs/" + ns
	for i := 0; i < 1+rng.Intn(2); i++ {
		if rng.Intn(2) == 0 {
			pattern += "/*"
		} else {
			pattern += fmt.Sprintf("/r%d", rng.Intn(6))
		}
	}
	r := Rule{
		Pattern:  pattern,
		NoCreate: rng.Intn(4) == 0,
		NoDelete: rng.Intn(4) == 0,
		NoNonFF:  rng.Intn(4) == 0,
	}
	if rng.Intn(3) == 0 {
		r.AllowOnly = true
		users := []string{"alice", "bob", "carol"}
		for _, u := range users {
			if rng.Intn(2) == 0 {
				r.Users = append(r.Users, u)
			}
		}
	}
	return r
}

// TestRandomizedAgainstNaiveModel 用随机提交图与随机指令批对拍朴素模型。
func TestRandomizedAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	users := []string{"alice", "bob", "carol"}
	for trial := 0; trial < 200; trial++ {
		// 随机提交图：每个提交带 0-2 个指向更早提交的父。
		n := 15 + rng.Intn(25)
		g := NewGraph()
		m := &naiveModel{refs: map[string]string{}, commits: map[string][]string{}}
		ids := make([]string, 0, n)
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("c%d", i)
			var parents []string
			for k := 0; k < rng.Intn(3) && len(ids) > 0; k++ {
				parents = append(parents, ids[rng.Intn(len(ids))])
			}
			if err := g.Add(id, parents...); err != nil {
				t.Fatal(err)
			}
			m.commits[id] = parents
			ids = append(ids, id)
		}
		// 随机抽查可达性与朴素模型一致。
		for k := 0; k < 50; k++ {
			a, b := ids[rng.Intn(n)], ids[rng.Intn(n)]
			if got, want := g.IsDescendantOrEqual(a, b), m.naiveReachable(a, b); got != want {
				t.Fatalf("trial %d: 可达性不一致 %s->%s got %v want %v", trial, a, b, got, want)
			}
		}
		// 随机受保护规则。
		var rules []Rule
		for i := 0; i < rng.Intn(6); i++ {
			rules = append(rules, randomRule(rng))
		}
		m.rules = rules
		s := NewServer(g)
		s.SetRules(rules)

		for batch := 0; batch < 30; batch++ {
			var ins []Instruction
			for k := 0; k < 1+rng.Intn(4); k++ {
				ref := randomRefName(rng, k)
				var old string
				switch rng.Intn(4) {
				case 0:
					old = "" // 期望不存在
				case 1:
					old = m.refs[ref] // 正确的当前值（可能为空）
				case 2:
					old = ids[rng.Intn(n)] // 随机提交，多半不匹配
				default:
					old = "bogus"
				}
				var newVal string
				switch rng.Intn(4) {
				case 0:
					newVal = "" // 删除
				case 1:
					newVal = "missing" // 不存在的对象
				default:
					newVal = ids[rng.Intn(n)]
				}
				ins = append(ins, Instruction{Ref: ref, Old: old, New: newVal, Force: rng.Intn(3) == 0})
			}
			user := users[rng.Intn(len(users))]
			got := s.Push(user, ins)
			want := m.push(user, ins)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("trial %d batch %d 裁决不一致\n输入: user=%s ins=%+v\n服务端: %+v\n朴素模型: %+v",
					trial, batch, user, ins, got, want)
			}
			if !reflect.DeepEqual(s.Refs(), m.refs) {
				t.Fatalf("trial %d batch %d 引用状态不一致\n服务端: %v\n朴素模型: %v", trial, batch, s.Refs(), m.refs)
			}
			if !reflect.DeepEqual(s.Audit(), m.audit) {
				t.Fatalf("trial %d batch %d 审计不一致\n服务端: %v\n朴素模型: %v", trial, batch, s.Audit(), m.audit)
			}
		}
		if trial < 5 || trial%50 == 0 {
			t.Logf("trial %d: 图规模 %d、规则 %d 条、30 批随机指令，裁决/引用/审计与朴素模型全部一致（判定依据：规格逐条对拍）",
				trial, n, len(rules))
		}
	}
}

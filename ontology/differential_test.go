package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// naive 是独立维护的朴素参照实现：保留全部历史版本、不做任何索引与
// 备忘，每次判定都从原始记录暴力重算。它与 Engine 的唯一约定是公开
// 语义（快照规则、合并规则、错误优先级），实现路径完全独立。
type naive struct {
	clock        uint64
	lowWater     uint64
	keepVersions int
	schema       map[string]map[string]bool // type -> attr set
	hist         map[string]map[string][]naiveVersion
	rules        []TagRule
	grants       []Grant
}

type naiveVersion struct {
	version uint64
	value   Value
}

func newNaive() *naive {
	return &naive{
		keepVersions: 8,
		schema:       map[string]map[string]bool{},
		hist:         map[string]map[string][]naiveVersion{},
	}
}

func naiveKey(typeName, id string) string { return typeName + "/" + id }

func (n *naive) write(typeName, id string, attrs map[string]Value) {
	n.clock++
	key := naiveKey(typeName, id)
	if n.hist[key] == nil {
		n.hist[key] = map[string][]naiveVersion{}
	}
	for name, v := range attrs {
		n.hist[key][name] = append(n.hist[key][name], naiveVersion{n.clock, v})
	}
}

func (n *naive) valueAt(typeName, id, attr string, version uint64) (Value, bool) {
	chain := n.hist[naiveKey(typeName, id)][attr]
	var out Value
	found := false
	for _, e := range chain { // 暴力扫描全部历史
		if e.version <= version {
			out, found = e.value, true
		}
	}
	return out, found
}

// prune 复刻可读水位规则但不真正裁剪（朴素地保留一切）。
func (n *naive) prune() {
	for _, inst := range n.hist {
		for _, chain := range inst {
			if len(chain) > n.keepVersions {
				if v := chain[len(chain)-n.keepVersions-1].version; v > n.lowWater {
					n.lowWater = v
				}
			}
		}
	}
}

// naiveEval 是独立重写的表达式求值（不共享被测代码路径）。
func naiveEval(e Expr, attr func(string) Value, tag func(string) bool) Value {
	switch x := e.(type) {
	case Const:
		return x.V
	case Attr:
		return attr(x.Name)
	case TagRef:
		return tag(x.Tag)
	case Not:
		if b, ok := naiveEval(x.X, attr, tag).(bool); ok {
			return !b
		}
		return nil
	case BinOp:
		l := naiveEval(x.L, attr, tag)
		r := naiveEval(x.R, attr, tag)
		switch x.Op {
		case OpAnd:
			lb, lok := l.(bool)
			rb, rok := r.(bool)
			if lok && rok {
				return lb && rb
			}
		case OpOr:
			lb, lok := l.(bool)
			rb, rok := r.(bool)
			if lok && rok {
				return lb || rb
			}
		case OpEq:
			return naiveEq(l, r)
		case OpNe:
			return !naiveEq(l, r)
		default:
			if c, ok := naiveCmp(l, r); ok {
				switch x.Op {
				case OpLt:
					return c < 0
				case OpLe:
					return c <= 0
				case OpGt:
					return c > 0
				case OpGe:
					return c >= 0
				}
			}
		}
		return nil
	}
	return nil
}

func naiveEq(l, r Value) bool {
	if li, ok := l.(int64); ok {
		if rf, ok := r.(float64); ok {
			return float64(li) == rf
		}
	}
	if lf, ok := l.(float64); ok {
		if ri, ok := r.(int64); ok {
			return lf == float64(ri)
		}
	}
	return l == r
}

func naiveCmp(l, r Value) (int, bool) {
	num := func(v Value) (float64, bool) {
		switch x := v.(type) {
		case int64:
			return float64(x), true
		case float64:
			return x, true
		}
		return 0, false
	}
	if lf, ok := num(l); ok {
		if rf, ok := num(r); ok {
			switch {
			case lf < rf:
				return -1, true
			case lf > rf:
				return 1, true
			}
			return 0, true
		}
	}
	if ls, ok := l.(string); ok {
		if rs, ok := r.(string); ok {
			switch {
			case ls < rs:
				return -1, true
			case ls > rs:
				return 1, true
			}
			return 0, true
		}
	}
	return 0, false
}

// carried 暴力重算标签状态：对每条规则直接求值，不做备忘之外的任何优化。
func (n *naive) carried(typeName, id, tag string, version uint64, depth int) bool {
	if depth > 64 { // 防御：参照实现不假设无环（规则集由测试保证无环）
		return false
	}
	for _, r := range n.rules {
		if r.Tag != tag || r.ObjectType != typeName {
			continue
		}
		v := naiveEval(r.Expr,
			func(name string) Value {
				val, _ := n.valueAt(typeName, id, name, version)
				return val
			},
			func(dep string) bool { return n.carried(typeName, id, dep, version, depth+1) })
		b, _ := v.(bool)
		return b
	}
	return false
}

// read 复刻读取语义：错误优先级、deny-wins 合并、无标签默认允许。
func (n *naive) read(subject, typeName, id, attr string, version uint64) (Value, bool, string) {
	attrs, ok := n.schema[typeName]
	if !ok || !attrs[attr] {
		return nil, false, "unknown-attribute"
	}
	if version < n.lowWater {
		return nil, false, "snapshot-expired"
	}
	var carried []string
	seen := map[string]bool{}
	for _, r := range n.rules {
		if r.ObjectType != typeName || seen[r.Tag] {
			continue
		}
		seen[r.Tag] = true
		if n.carried(typeName, id, r.Tag, version, 0) {
			carried = append(carried, r.Tag)
		}
	}
	if len(carried) == 0 {
		v, _ := n.valueAt(typeName, id, attr, version)
		return v, true, ""
	}
	allow := false
	for _, tag := range carried {
		for _, g := range n.grants {
			if g.Tag != tag || g.Subject != subject || !g.appliesTo(attr) {
				continue
			}
			switch g.Read {
			case EffectDeny:
				return nil, false, "denied"
			case EffectAllow:
				allow = true
			}
		}
	}
	if !allow {
		return nil, false, "denied"
	}
	v, _ := n.valueAt(typeName, id, attr, version)
	return v, true, ""
}

// TestDifferential 在大量随机属性取值与操作序列上，将 Engine 的判定
// 结果与朴素参照实现逐项对照。
func TestDifferential(t *testing.T) {
	rulePool := [][]TagRule{
		{},
		{{Tag: "hi", ObjectType: "Doc", Expr: BinOp{Op: OpGe, L: Attr{"level"}, R: Const{int64(3)}}}},
		{
			{Tag: "hi", ObjectType: "Doc", Expr: BinOp{Op: OpGe, L: Attr{"level"}, R: Const{int64(3)}}},
			{Tag: "hr", ObjectType: "Doc", Expr: BinOp{Op: OpEq, L: Attr{"dept"}, R: Const{"hr"}}},
		},
		{
			{Tag: "hi", ObjectType: "Doc", Expr: BinOp{Op: OpGe, L: Attr{"level"}, R: Const{int64(3)}}},
			{Tag: "hr", ObjectType: "Doc", Expr: BinOp{Op: OpEq, L: Attr{"dept"}, R: Const{"hr"}}},
			{Tag: "combo", ObjectType: "Doc", Expr: BinOp{Op: OpAnd, L: TagRef{"hi"}, R: Not{TagRef{"hr"}}}},
		},
		{
			{Tag: "odd", ObjectType: "Doc", Expr: BinOp{Op: OpEq,
				L: BinOp{Op: OpGe, L: Attr{"level"}, R: Const{int64(2)}}, R: Const{true}}},
			{Tag: "named", ObjectType: "Doc", Expr: BinOp{Op: OpOr,
				L: BinOp{Op: OpEq, L: Attr{"dept"}, R: Const{"eng"}},
				R: BinOp{Op: OpLt, L: Attr{"level"}, R: Const{int64(1)}}}},
		},
	}
	grantPool := [][]Grant{
		{},
		{{Tag: "hi", Subject: "alice", Read: EffectAllow, Write: EffectAllow}},
		{
			{Tag: "hi", Subject: "alice", Read: EffectAllow, Write: EffectAllow},
			{Tag: "hr", Subject: "alice", Read: EffectDeny},
		},
		{
			{Tag: "hi", Subject: "alice", Read: EffectDeny},
			{Tag: "combo", Subject: "alice", Read: EffectAllow, Write: EffectAllow},
			{Tag: "hr", Subject: "alice", Read: EffectAllow, Scope: []string{"title"}},
		},
		{
			{Tag: "odd", Subject: "alice", Read: EffectAllow, Write: EffectAllow},
			{Tag: "named", Subject: "alice", Read: EffectDeny},
			{Tag: "named", Subject: "alice", Read: EffectAllow, Scope: []string{"dept"}},
		},
	}
	attrs := []string{"level", "dept", "title"}
	subjects := []string{"alice", "bob"}

	for seed := int64(0); seed < 12; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			eng := mustEngine(t)
			ref := newNaive()
			ref.schema["Doc"] = map[string]bool{"level": true, "dept": true, "title": true}
			var snapshots []Snapshot
			var snapVersions []uint64

			randomValue := func(attr string) Value {
				switch attr {
				case "level":
					return int64(rng.Intn(6))
				case "dept":
					return []string{"hr", "eng", "ops"}[rng.Intn(3)]
				default:
					return fmt.Sprintf("t%d", rng.Intn(4))
				}
			}

			for step := 0; step < 1500; step++ {
				switch rng.Intn(10) {
				case 0, 1, 2: // 写入
					attr := attrs[rng.Intn(len(attrs))]
					vals := map[string]Value{attr: randomValue(attr)}
					subject := subjects[rng.Intn(len(subjects))]
					_, _, err := eng.Write(subject, "Doc", "d1", vals)
					if err == nil {
						ref.write("Doc", "d1", vals)
					} else if err != ErrDenied {
						t.Fatalf("step %d: 意外写错误 %v", step, err)
					}
				case 3: // 开启快照
					s := eng.BeginSnapshot()
					snapshots = append(snapshots, s)
					snapVersions = append(snapVersions, s.Version())
				case 4: // 规则变更（合法规则集）
					rules := rulePool[rng.Intn(len(rulePool))]
					if err := eng.ReplaceRules(rules); err != nil {
						t.Fatalf("step %d: ReplaceRules: %v", step, err)
					}
					ref.rules = append([]TagRule(nil), rules...)
				case 5: // 授权表变更
					grants := grantPool[rng.Intn(len(grantPool))]
					eng.SetGrants(grants)
					ref.grants = append([]Grant(nil), grants...)
				case 6: // 垃圾回收
					eng.Prune()
					ref.prune()
				default: // 读取并对照
					attr := attrs[rng.Intn(len(attrs))]
					subject := subjects[rng.Intn(len(subjects))]
					var snap Snapshot
					var version uint64
					latest := len(snapshots) == 0 || rng.Intn(2) == 0
					if latest {
						snap = Latest()
						version = ref.clock
					} else {
						i := rng.Intn(len(snapshots))
						snap = snapshots[i]
						version = snapVersions[i]
					}
					gotV, gotDec, gotErr := eng.Read(subject, "Doc", "d1", attr, snap)
					wantV, wantOK, wantErr := ref.read(subject, "Doc", "d1", attr, version)
					compareRead(t, step, gotV, gotErr, gotDec, wantV, wantOK, wantErr)
				}
			}
		})
	}
}

func compareRead(t *testing.T, step int, gotV Value, gotErr error, gotDec Decision, wantV Value, wantOK bool, wantErr string) {
	t.Helper()
	switch wantErr {
	case "unknown-attribute", "snapshot-expired":
		if gotErr == nil || gotErr == ErrDenied {
			t.Fatalf("step %d: 期望错误 %s, got %v", step, wantErr, gotErr)
		}
		wantKind := ErrKindUnknownAttribute
		if wantErr == "snapshot-expired" {
			wantKind = ErrKindSnapshotExpired
		}
		if k, ok := KindOf(gotErr); !ok || k != wantKind {
			t.Fatalf("step %d: 期望错误类别 %v, got %v", step, wantKind, gotErr)
		}
		return
	case "denied":
		if gotErr != ErrDenied {
			t.Fatalf("step %d: 参照实现拒绝但被测引擎放行 (v=%v)", step, gotV)
		}
		return
	}
	if gotErr != nil {
		t.Fatalf("step %d: 参照实现放行但被测引擎拒绝: %v", step, gotErr)
	}
	if !reflect.DeepEqual(gotV, wantV) {
		t.Fatalf("step %d: 取值不一致 got=%v want=%v", step, gotV, wantV)
	}
	if !gotDec.Allowed {
		t.Fatalf("step %d: 判定日志与返回不一致: %+v", step, gotDec)
	}
}

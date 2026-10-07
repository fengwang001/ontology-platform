package ontology

// naive_test.go 包含一个独立维护的朴素参照实现（naiveStore），
// 以及将主实现与参照实现在大量随机操作序列上逐项对照的差分测试。
//
// 参照实现刻意采用与主实现不同的求值方式：
// 主实现按判定点过滤声明并做特异性排序；
// 参照实现先把所有范围展开成 判定点->声明列表 的扁平映射，再逐点暴力比较。

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

type naiveStore struct {
	tenants       map[string]bool
	types         map[string]ObjectTypeDef
	basisRequired map[string]bool
	defaults      map[[2]string][]Statement
	overrides     map[[3]string][]Statement
}

func newNaiveStore() *naiveStore {
	return &naiveStore{
		tenants:       map[string]bool{},
		types:         map[string]ObjectTypeDef{},
		basisRequired: map[string]bool{},
		defaults:      map[[2]string][]Statement{},
		overrides:     map[[3]string][]Statement{},
	}
}

// naiveExpand 把声明展开为 判定点 -> (声明, 范围集合) 的扁平映射。
func naiveExpand(stmts []Statement, universe []string) map[string][]naiveEntry {
	out := map[string][]naiveEntry{}
	for _, st := range stmts {
		set := map[string]bool{}
		switch st.Scope.Kind {
		case ScopeAttr, ScopePredicate:
			set[st.Scope.Name] = true
		case ScopeAttrSet, ScopePredicateSet:
			for _, m := range st.Scope.Members {
				set[m] = true
			}
		default:
			for _, u := range universe {
				set[u] = true
			}
		}
		for point := range set {
			out[point] = append(out[point], naiveEntry{stmt: st, set: set})
		}
	}
	return out
}

type naiveEntry struct {
	stmt Statement
	set  map[string]bool
}

// naiveResolve 对单个判定点在扁平展开表上暴力裁决。
// 返回效果、声明 ID、是否命中，以及冲突/歧义错误。
func naiveResolve(flat map[string][]naiveEntry, point, layer string) (Effect, string, bool, error) {
	apps := flat[point]
	if len(apps) == 0 {
		return Deny, "", false, nil
	}
	// 暴力求极小元：不存在另一个适用声明的范围是其严格子集。
	var mins []naiveEntry
	for i, a := range apps {
		dominated := false
		for j, b := range apps {
			if i == j {
				continue
			}
			if subsetOf(b.set, a.set) && !subsetOf(a.set, b.set) {
				dominated = true
				break
			}
		}
		if !dominated {
			mins = append(mins, a)
		}
	}
	// 按首次出现顺序对相同范围分组。
	type group struct {
		effect Effect
		id     string
		set    map[string]bool
	}
	var groups []group
	for _, m := range mins {
		placed := false
		for gi := range groups {
			if equalSets(groups[gi].set, m.set) {
				if groups[gi].effect != m.stmt.Effect {
					return Deny, "", false, newError(ErrOverrideConflict,
						"naive: %s 层判定点 %q 冲突", layer, point)
				}
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, group{effect: m.stmt.Effect, id: m.stmt.ID, set: m.set})
		}
	}
	for i := 1; i < len(groups); i++ {
		if groups[i].effect != groups[0].effect {
			return Deny, "", false, newError(ErrMergeAmbiguous,
				"naive: %s 层判定点 %q 合并不唯一", layer, point)
		}
	}
	return groups[0].effect, groups[0].id, true, nil
}

func subsetOf(a, b map[string]bool) bool {
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func equalSets(a, b map[string]bool) bool {
	return subsetOf(a, b) && subsetOf(b, a)
}

// naiveDecide 是参照实现的判定：逐判定点展开、合并两层。
func (n *naiveStore) naiveDecide(req AccessRequest) (Decision, error) {
	if !n.tenants[req.SubjectTenant] || !n.tenants[req.InstanceTenant] {
		return Decision{}, newError(ErrNotFound, "naive: 租户不存在")
	}
	def, ok := n.types[req.Type]
	if !ok {
		return Decision{}, newError(ErrNotFound, "naive: 类型不存在")
	}
	attrUniverse := append([]string(nil), def.Attrs...)
	var predUniverse []string
	for _, p := range def.Predicates {
		predUniverse = append(predUniverse, p.ID)
	}
	defAttrs, defRows := splitByRow(n.defaults[[2]string{req.Type, req.SubjectClass}])
	ovrAttrs, ovrRows := splitByRow(n.overrides[[3]string{req.InstanceTenant, req.Type, req.SubjectClass}])

	flatDA := naiveExpand(defAttrs, attrUniverse)
	flatDR := naiveExpand(defRows, predUniverse)
	flatOA := naiveExpand(ovrAttrs, attrUniverse)
	flatOR := naiveExpand(ovrRows, predUniverse)

	basis := map[string]string{}
	dec := Decision{Attrs: map[string]bool{}, Trace: Trace{
		DefaultEntriesExamined:  len(n.defaults[[2]string{req.Type, req.SubjectClass}]),
		OverrideEntriesExamined: len(n.overrides[[3]string{req.InstanceTenant, req.Type, req.SubjectClass}]),
		Basis:                   basis,
	}}

	var conflictErr, ambErr error
	resolve := func(row bool, name string) (Effect, bool) {
		key := "attr:" + name
		fo, fd := flatOA, flatDA
		if row {
			key = "pred:" + name
			fo, fd = flatOR, flatDR
		}
		eff, id, found, err := naiveResolve(fo, name, "override")
		if err != nil {
			naiveCollect(err, &conflictErr, &ambErr)
			return Deny, false
		}
		if found {
			basis[key] = "override:" + id
			return eff, true
		}
		eff, id, found, err = naiveResolve(fd, name, "default")
		if err != nil {
			naiveCollect(err, &conflictErr, &ambErr)
			return Deny, false
		}
		if found {
			basis[key] = "default:" + id
			return eff, true
		}
		basis[key] = "implicit-deny"
		return Deny, true
	}

	attrEff := map[string]Effect{}
	for _, a := range attrUniverse {
		if eff, ok := resolve(false, a); ok {
			attrEff[a] = eff
		}
	}
	predEff := map[string]Effect{}
	for _, p := range predUniverse {
		if eff, ok := resolve(true, p); ok {
			predEff[p] = eff
		}
	}
	if conflictErr != nil {
		return Decision{}, conflictErr
	}
	if ambErr != nil {
		return Decision{}, ambErr
	}
	for _, a := range attrUniverse {
		dec.Attrs[a] = attrEff[a] == Allow
	}
	if len(def.Predicates) == 0 {
		dec.RowAllowed = true
		basis["@row"] = "no-predicates"
	} else {
		for _, p := range def.Predicates {
			if predEff[p.ID] == Allow && matchPredicate(p, req.Instance) {
				dec.RowAllowed = true
				basis["@row"] = "predicate:" + p.ID
				break
			}
		}
		if !dec.RowAllowed {
			basis["@row"] = "no-matching-allowed-predicate"
		}
	}
	return dec, nil
}

func naiveCollect(err error, conflictErr, ambErr *error) {
	if e, ok := err.(*Error); ok {
		switch e.Code {
		case ErrOverrideConflict:
			if *conflictErr == nil {
				*conflictErr = err
			}
		case ErrMergeAmbiguous:
			if *ambErr == nil {
				*ambErr = err
			}
		}
	}
}

// naiveValidateScopes 与主实现一致的作用域校验（朴素重写）。
func naiveValidateScopes(def *ObjectTypeDef, stmts []Statement) error {
	attrOK := map[string]bool{}
	for _, a := range def.Attrs {
		attrOK[a] = true
	}
	predOK := map[string]bool{}
	for _, p := range def.Predicates {
		predOK[p.ID] = true
	}
	for _, st := range stmts {
		var names []string
		switch st.Scope.Kind {
		case ScopeAttr:
			names = []string{st.Scope.Name}
			if !attrOK[st.Scope.Name] {
				return newError(ErrNotFound, "naive: 属性不存在")
			}
		case ScopePredicate:
			names = []string{st.Scope.Name}
			if !predOK[st.Scope.Name] {
				return newError(ErrNotFound, "naive: 谓词不存在")
			}
		case ScopeAttrSet:
			names = st.Scope.Members
			for _, m := range names {
				if !attrOK[m] {
					return newError(ErrNotFound, "naive: 属性不存在")
				}
			}
		case ScopePredicateSet:
			names = st.Scope.Members
			for _, m := range names {
				if !predOK[m] {
					return newError(ErrNotFound, "naive: 谓词不存在")
				}
			}
		}
	}
	return nil
}

// naiveValidateLayer 用展开表校验单层规则一致性。
func naiveValidateLayer(def *ObjectTypeDef, stmts []Statement) error {
	attrUniverse := append([]string(nil), def.Attrs...)
	var predUniverse []string
	for _, p := range def.Predicates {
		predUniverse = append(predUniverse, p.ID)
	}
	attrs, rows := splitByRow(stmts)
	flatA := naiveExpand(attrs, attrUniverse)
	flatR := naiveExpand(rows, predUniverse)
	for _, a := range attrUniverse {
		if _, _, _, err := naiveResolve(flatA, a, "default"); err != nil {
			return err
		}
	}
	for _, p := range predUniverse {
		if _, _, _, err := naiveResolve(flatR, p, "default"); err != nil {
			return err
		}
	}
	return nil
}

// naiveIsLoosening 朴素判定放宽：展开默认层后逐点比较。
func naiveIsLoosening(def *ObjectTypeDef, defaults []Statement, st Statement) bool {
	if st.Effect != Allow {
		return false
	}
	attrUniverse := append([]string(nil), def.Attrs...)
	var predUniverse []string
	for _, p := range def.Predicates {
		predUniverse = append(predUniverse, p.ID)
	}
	defAttrs, defRows := splitByRow(defaults)
	var flat map[string][]naiveEntry
	var points map[string]bool
	if st.Scope.IsRow() {
		flat = naiveExpand(defRows, predUniverse)
		points = naiveScopeSet(st.Scope, predUniverse)
	} else {
		flat = naiveExpand(defAttrs, attrUniverse)
		points = naiveScopeSet(st.Scope, attrUniverse)
	}
	for point := range points {
		eff, _, found, err := naiveResolve(flat, point, "default")
		if err != nil || !found || eff == Deny {
			return true
		}
	}
	return false
}

func naiveScopeSet(sc Scope, universe []string) map[string]bool {
	switch sc.Kind {
	case ScopeAttr, ScopePredicate:
		return map[string]bool{sc.Name: true}
	case ScopeAttrSet, ScopePredicateSet:
		m := map[string]bool{}
		for _, s := range sc.Members {
			m[s] = true
		}
		return m
	default:
		m := map[string]bool{}
		for _, u := range universe {
			m[u] = true
		}
		return m
	}
}

func (n *naiveStore) putDefault(typeName, class string, stmts []Statement) error {
	def, ok := n.types[typeName]
	if !ok {
		return newError(ErrNotFound, "naive: 类型不存在")
	}
	if err := naiveValidateScopes(&def, stmts); err != nil {
		return err
	}
	if err := naiveValidateLayer(&def, stmts); err != nil {
		return err
	}
	n.defaults[[2]string{typeName, class}] = cloneStatements(stmts)
	return nil
}

func (n *naiveStore) putOverride(tenant, typeName, class string, stmts []Statement) error {
	if !n.tenants[tenant] {
		return newError(ErrNotFound, "naive: 租户不存在")
	}
	def, ok := n.types[typeName]
	if !ok {
		return newError(ErrNotFound, "naive: 类型不存在")
	}
	if err := naiveValidateScopes(&def, stmts); err != nil {
		return err
	}
	if n.basisRequired[typeName] {
		defaults := n.defaults[[2]string{typeName, class}]
		for _, st := range stmts {
			if st.Basis == "" && naiveIsLoosening(&def, defaults, st) {
				return newError(ErrMissingBasis, "naive: 放宽缺少授权依据")
			}
		}
	}
	n.overrides[[3]string{tenant, typeName, class}] = cloneStatements(stmts)
	return nil
}

func (n *naiveStore) revokeOverride(tenant, typeName, class string) error {
	if !n.tenants[tenant] {
		return newError(ErrNotFound, "naive: 租户不存在")
	}
	if _, ok := n.types[typeName]; !ok {
		return newError(ErrNotFound, "naive: 类型不存在")
	}
	delete(n.overrides, [3]string{tenant, typeName, class})
	return nil
}

// ---------- 随机差分测试 ----------

var diffTypes = []ObjectTypeDef{
	{
		Name:  "T0",
		Attrs: []string{"a0", "a1", "a2", "a3"},
		Predicates: []Predicate{
			{ID: "q0", Field: "dept", Op: "eq", Value: "x"},
			{ID: "q1", Field: "level", Op: "ge", Value: 5},
		},
	},
	{
		Name:  "T1",
		Attrs: []string{"a0", "a1"},
		Predicates: []Predicate{
			{ID: "q2", Field: "tag", Op: "contains", Value: "z"},
		},
	},
}

func randomScope(rng *rand.Rand, def *ObjectTypeDef) Scope {
	attrs := def.Attrs
	var preds []string
	for _, p := range def.Predicates {
		preds = append(preds, p.ID)
	}
	pickAttrs := func(k int) []string {
		perm := rng.Perm(len(attrs))[:k]
		out := []string{}
		for _, i := range perm {
			out = append(out, attrs[i])
		}
		return out
	}
	pickPreds := func(k int) []string {
		perm := rng.Perm(len(preds))[:k]
		out := []string{}
		for _, i := range perm {
			out = append(out, preds[i])
		}
		return out
	}
	switch rng.Intn(6) {
	case 0:
		return Scope{Kind: ScopeAttr, Name: attrs[rng.Intn(len(attrs))]}
	case 1:
		return Scope{Kind: ScopeAttrSet, Members: pickAttrs(1 + rng.Intn(len(attrs)))}
	case 2:
		return Scope{Kind: ScopeAttrWildcard}
	case 3:
		return Scope{Kind: ScopePredicate, Name: preds[rng.Intn(len(preds))]}
	case 4:
		return Scope{Kind: ScopePredicateSet, Members: pickPreds(1 + rng.Intn(len(preds)))}
	default:
		return Scope{Kind: ScopePredicateWildcard}
	}
}

func randomStatements(rng *rand.Rand, def *ObjectTypeDef, idGen *int) []Statement {
	n := rng.Intn(4)
	var out []Statement
	for i := 0; i < n; i++ {
		*idGen++
		st := Statement{
			ID:     fmt.Sprintf("s%d", *idGen),
			Scope:  randomScope(rng, def),
			Effect: Effect(rng.Intn(2)),
		}
		if rng.Intn(2) == 0 {
			st.Basis = fmt.Sprintf("grant-%d", *idGen)
		}
		out = append(out, st)
	}
	return out
}

func randomInstance(rng *rand.Rand) Instance {
	tags := []string{"alpha", "zeta", "beta-z", "gamma"}
	return Instance{
		ID: fmt.Sprintf("inst-%d", rng.Intn(20)),
		Attrs: map[string]any{
			"dept":  []string{"x", "y"}[rng.Intn(2)],
			"level": rng.Intn(10),
			"tag":   tags[rng.Intn(len(tags))],
		},
	}
}

// TestDifferentialRandomSequences 在大量随机生成的租户与规则变更序列上，
// 将主实现与朴素参照实现逐项对照（错误码、判定结果、层级依据、考察条目数）。
func TestDifferentialRandomSequences(t *testing.T) {
	const seeds = 60
	const opsPerSeed = 400
	tenants := []string{"t0", "t1", "t2", "t3", "t4"}
	classes := []string{"c0", "c1"}

	for seed := int64(0); seed < seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		main := NewStore()
		naive := newNaiveStore()
		for _, tn := range tenants {
			main.AddTenant(tn)
			naive.tenants[tn] = true
		}
		for _, def := range diffTypes {
			main.DefineObjectType(def)
			naive.types[def.Name] = def
			req := rng.Intn(2) == 0
			if err := main.SetLooseningBasisRequired(def.Name, req); err != nil {
				t.Fatal(err)
			}
			naive.basisRequired[def.Name] = req
		}
		idGen := 0

		for op := 0; op < opsPerSeed; op++ {
			def := diffTypes[rng.Intn(len(diffTypes))]
			class := classes[rng.Intn(len(classes))]
			tenant := tenants[rng.Intn(len(tenants))]
			if rng.Intn(20) == 0 {
				tenant = "ghost"
			}
			typeName := def.Name
			if rng.Intn(20) == 0 {
				typeName = "Ghost"
			}

			switch rng.Intn(10) {
			case 0, 1: // putDefault
				stmts := randomStatements(rng, &def, &idGen)
				errM := main.PutDefault(typeName, class, stmts)
				errN := naive.putDefault(typeName, class, stmts)
				if errCode(errM) != errCode(errN) {
					t.Fatalf("seed=%d op=%d putDefault 错误码不一致: main=%v naive=%v",
						seed, op, errM, errN)
				}
			case 2, 3, 4: // putOverride
				stmts := randomStatements(rng, &def, &idGen)
				errM := main.PutOverride(tenant, typeName, class, stmts)
				errN := naive.putOverride(tenant, typeName, class, stmts)
				if errCode(errM) != errCode(errN) {
					t.Fatalf("seed=%d op=%d putOverride 错误码不一致: main=%v naive=%v",
						seed, op, errM, errN)
				}
			case 5: // revokeOverride
				errM := main.RevokeOverride(tenant, typeName, class)
				errN := naive.revokeOverride(tenant, typeName, class)
				if errCode(errM) != errCode(errN) {
					t.Fatalf("seed=%d op=%d revoke 错误码不一致: main=%v naive=%v",
						seed, op, errM, errN)
				}
			default: // decide
				req := AccessRequest{
					SubjectTenant:  tenants[rng.Intn(len(tenants))],
					SubjectClass:   class,
					Type:           typeName,
					InstanceTenant: tenant,
					Instance:       randomInstance(rng),
				}
				if rng.Intn(30) == 0 {
					req.SubjectTenant = "ghost"
				}
				decM, errM := main.Decide(req)
				decN, errN := naive.naiveDecide(req)
				if errCode(errM) != errCode(errN) {
					t.Fatalf("seed=%d op=%d decide 错误码不一致: main=%v naive=%v req=%+v",
						seed, op, errM, errN, req)
				}
				if errM != nil {
					continue
				}
				if decM.RowAllowed != decN.RowAllowed ||
					!reflect.DeepEqual(decM.Attrs, decN.Attrs) ||
					!reflect.DeepEqual(decM.Trace, decN.Trace) {
					t.Fatalf("seed=%d op=%d decide 结果不一致:\nmain=%+v\nnaive=%+v\nreq=%+v",
						seed, op, decM, decN, req)
				}
			}
		}
	}
}

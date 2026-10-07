package binding

import (
	"errors"
	"sort"
)

// ErrInvalidDeclaration 在绑定声明本身不合法时由声明入口返回，
// 例如方向取值非法或对无界取值域声明了无法全量核验的显式映射。
var ErrInvalidDeclaration = errors.New("binding: invalid declaration")

const missingKey = "\x00missing"

// Verdict 是一次绑定兼容性核验的结论分类。
type Verdict string

const (
	// VerdictCompatible 对应方式满足声明的方向性要求。
	VerdictCompatible Verdict = "compatible"
	// VerdictFieldDeleted 一侧绑定依据字段已被删除，绑定定义失效。
	// 该判定优先于其余所有不兼容分类。
	VerdictFieldDeleted Verdict = "field_deleted"
	// VerdictIncomparableTypes 两侧取值类型在当前版本无法比较，无法判定。
	VerdictIncomparableTypes Verdict = "incomparable_types"
	// VerdictNonBijective 双向声明下对应关系不是双向唯一
	// （存在碰撞、漏映射，或缺失取值无对应）。
	VerdictNonBijective Verdict = "non_bijective"
	// VerdictOneWayOnly 声明为双向，但实际只有单向存在确定对应。
	VerdictOneWayOnly Verdict = "one_way_only"
	// VerdictDirectionDenied 声明为单向绑定，却从反方向发起查询/写入。
	VerdictDirectionDenied Verdict = "direction_denied"
)

// CheckBasis 记录对应关系检查所依据的证据，供事后核对。
type CheckBasis struct {
	LeftDomain        []string
	RightDomain       []string
	ForwardCollisions []string
	ReverseCollisions []string
	UnmappedLeft      []string
	RightWithoutImage []string
	MissingHandled    bool
}

// Result 是一次核验的完整结果。
type Result struct {
	Verdict       Verdict
	Reason        string
	Basis         CheckBasis
	LeftVersion   int64
	RightVersion  int64
	Direction     Direction
	InstancesLive int
	InstancesSeen int
}

// Check 是无状态的纯核验函数：依据两侧字段定义与绑定声明判定兼容性。
func Check(left, right *FieldDef, spec BindingSpec) Result {
	res := Result{Direction: spec.Direction}
	if !validDirection(spec.Direction) {
		res.Verdict = VerdictNonBijective
		res.Reason = "unknown binding direction"
		return res
	}
	if !left.Type.comparableTo(right.Type) {
		res.Verdict = VerdictIncomparableTypes
		res.Reason = "field types are not comparable in current versions"
		return res
	}

	leftDomain, err := domainKeys(left)
	if err != nil {
		res.Verdict = VerdictIncomparableTypes
		res.Reason = err.Error()
		return res
	}
	rightDomain, err := domainKeys(right)
	if err != nil {
		res.Verdict = VerdictIncomparableTypes
		res.Reason = err.Error()
		return res
	}

	relation, basis, err := buildCorrespondence(left, right, leftDomain, rightDomain, spec.Correspondence)
	if err != nil {
		res.Verdict = VerdictIncomparableTypes
		res.Reason = err.Error()
		res.Basis = basis
		return res
	}

	_, structural := relation[structuralIdentity]
	forwardFn := isForwardFunction(relation, left, leftDomain) &&
		(!structural || missingSatisfied(left, right, spec.Correspondence.Missing))
	reverseFn := isReverseFunction(relation, right, rightDomain) &&
		(!structural || missingSatisfied(right, left, spec.Correspondence.Missing))
	// 反向“全函数”与反向“可对应”是两件事：
	// 单向 RightToLeft 声明只要求给定的右→左对应本身是全函数，
	// 即每个右值恰好有一个左值前像（不要求左域被覆盖）。
	reverseTotal := reversePreimageTotal(relation, right, rightDomain)

	switch spec.Direction {
	case LeftToRight:
		if forwardFn {
			res.Verdict = VerdictCompatible
		} else {
			res.Verdict = VerdictNonBijective
			res.Reason = "declared left-to-right mapping is not a deterministic total function"
		}
	case RightToLeft:
		if reverseTotal {
			res.Verdict = VerdictCompatible
		} else {
			res.Verdict = VerdictNonBijective
			res.Reason = "declared right-to-left mapping is not a deterministic total function"
		}
	case Bidirectional:
		switch {
		case forwardFn && reverseFn:
			res.Verdict = VerdictCompatible
		case forwardFn || reverseFn:
			res.Verdict = VerdictOneWayOnly
			res.Reason = "bidirectional binding declared but only one direction is deterministically mappable"
		default:
			res.Verdict = VerdictNonBijective
			res.Reason = "correspondence is not bijective in either direction"
		}
	}
	res.Basis = basis
	return res
}

func validDirection(d Direction) bool {
	return d == Bidirectional || d == LeftToRight || d == RightToLeft
}

// missingSatisfied 报告 from 侧的缺失取值（若存在）是否能确定地映射到 to 侧。
func missingSatisfied(from, to *FieldDef, p MissingPolicy) bool {
	if !from.Nullable {
		return true
	}
	return to.Nullable && p.MapMissing
}

// domainKeys 返回字段非缺失取值的规范键集合；nil 表示无界结构域。
func domainKeys(def *FieldDef) ([]string, error) {
	if len(def.Allowed) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(def.Allowed))
	keys := make([]string, 0, len(def.Allowed))
	for _, v := range def.Allowed {
		k, err := v.CanonicalKey()
		if err != nil {
			return nil, err
		}
		if _, dup := seen[k]; dup {
			return nil, errors.New("duplicate value in field allowed set: " + k)
		}
		seen[k] = struct{}{}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

func fullDomain(def *FieldDef, keys []string) []string {
	if !def.Nullable {
		return keys
	}
	out := make([]string, 0, len(keys)+1)
	out = append(out, keys...)
	out = append(out, missingKey)
	sort.Strings(out)
	return out
}

// buildCorrespondence 构造前向/反向映射并同步收集检查证据。
func buildCorrespondence(left, right *FieldDef, leftKeys, rightKeys []string, c Correspondence) (map[string]map[string]struct{}, CheckBasis, error) {
	basis := CheckBasis{
		LeftDomain:  fullDomain(left, leftKeys),
		RightDomain: fullDomain(right, rightKeys),
	}
	// relation 是声明出来的对应关系：relation[lk] 为该左值对应的全部右值。
	// 方向语义在 isForwardFunction / isReverseFunction 中判定：
	// 多对一是合法的单向函数，同一左值对应多个右值才是前向冲突。
	relation := map[string]map[string]struct{}{}
	add := func(lk, rk string) {
		if relation[lk] == nil {
			relation[lk] = map[string]struct{}{}
		}
		relation[lk][rk] = struct{}{}
	}

	if len(c.Mapping) == 0 {
		// 恒等对应：同种类类型、枚举取值域完全一致时才成立。
		if left.Type.Kind != right.Type.Kind {
			return nil, basis, errors.New("identity correspondence requires same value type kind; declare an explicit value mapping")
		}
		if leftKeys == nil || rightKeys == nil {
			// 两侧同为无界结构域，恒等映射结构上即双射，无冲突可言。
			basis.UnmappedLeft = nil
			basis.RightWithoutImage = nil
			if leftKeys == nil && rightKeys == nil {
				// 无界域：以哨兵表示结构恒等，函数性在方向判定时直接成立。
				relation[structuralIdentity] = map[string]struct{}{structuralIdentity: {}}
			}
		} else {
			if !sameSet(leftKeys, rightKeys) {
				for _, k := range leftKeys {
					if !contains(rightKeys, k) {
						basis.UnmappedLeft = append(basis.UnmappedLeft, k)
					}
				}
				for _, k := range rightKeys {
					if !contains(leftKeys, k) {
						basis.RightWithoutImage = append(basis.RightWithoutImage, k)
					}
				}
			}
			for _, k := range intersect(leftKeys, rightKeys) {
				add(k, k)
			}
		}
	} else {
		if leftKeys == nil || rightKeys == nil {
			return nil, basis, errors.New("explicit mapping requires finite (enum-constrained) value domains on both sides")
		}
		leftSet := make(map[string]struct{}, len(leftKeys))
		for _, k := range leftKeys {
			leftSet[k] = struct{}{}
		}
		rightSet := make(map[string]struct{}, len(rightKeys))
		for _, k := range rightKeys {
			rightSet[k] = struct{}{}
		}
		for _, pair := range c.Mapping {
			lk, err := pair.Left.CanonicalKey()
			if err != nil {
				return nil, basis, err
			}
			rk, err := pair.Right.CanonicalKey()
			if err != nil {
				return nil, basis, err
			}
			if _, ok := leftSet[lk]; !ok {
				return nil, basis, errors.New("mapping source value outside left allowed set: " + lk)
			}
			if _, ok := rightSet[rk]; !ok {
				return nil, basis, errors.New("mapping target value outside right allowed set: " + rk)
			}
			add(lk, rk)
		}
	}

	applyMissing(left, right, add, c.Missing, &basis)

	leftFull := fullDomain(left, leftKeys)
	rightFull := fullDomain(right, rightKeys)
	for _, lk := range leftFull {
		images := relation[lk]
		switch len(images) {
		case 0:
			basis.UnmappedLeft = append(basis.UnmappedLeft, lk)
		case 1:
		default:
			basis.ForwardCollisions = append(basis.ForwardCollisions, lk)
		}
	}
	preimages := map[string]map[string]struct{}{}
	for lk, images := range relation {
		if lk == structuralIdentity {
			continue
		}
		for rk := range images {
			if preimages[rk] == nil {
				preimages[rk] = map[string]struct{}{}
			}
			preimages[rk][lk] = struct{}{}
		}
	}
	for _, rk := range rightFull {
		switch len(preimages[rk]) {
		case 0:
			basis.RightWithoutImage = append(basis.RightWithoutImage, rk)
		case 1:
		default:
			basis.ReverseCollisions = append(basis.ReverseCollisions, rk)
		}
	}
	sort.Strings(basis.ForwardCollisions)
	sort.Strings(basis.ReverseCollisions)
	sort.Strings(basis.UnmappedLeft)
	sort.Strings(basis.RightWithoutImage)
	return relation, basis, nil
}

const structuralIdentity = "\x00identity"

func applyMissing(left, right *FieldDef, add func(string, string), p MissingPolicy, basis *CheckBasis) {
	switch {
	case left.Nullable && right.Nullable && p.MapMissing:
		add(missingKey, missingKey)
		basis.MissingHandled = true
	case left.Nullable != right.Nullable && p.MapMissing:
		// 只有一侧存在缺失取值，缺失无法映射到对端：保持未映射，
		// 交由全量性检查判定。
		basis.MissingHandled = false
	default:
		basis.MissingHandled = false
	}
}

// isForwardFunction 判定 relation 是否构成左域 → 右域 的全函数：
// 每个左值（含缺失）恰好对应一个右值。
func isForwardFunction(relation map[string]map[string]struct{}, left *FieldDef, leftKeys []string) bool {
	if _, structural := relation[structuralIdentity]; structural {
		// 无界结构域恒等：结构上为双射；缺失对称性由 applyMissing 的证据单独决定。
		return true
	}
	for _, lk := range fullDomain(left, leftKeys) {
		if len(relation[lk]) != 1 {
			return false
		}
	}
	return true
}

// isReverseFunction 判定 relation 的逆是否构成右域 → 左域 的全函数：
// 每个右值（含缺失）恰好有一个左值前像。
func isReverseFunction(relation map[string]map[string]struct{}, right *FieldDef, rightKeys []string) bool {
	if _, structural := relation[structuralIdentity]; structural {
		return true
	}
	preimages := map[string]int{}
	for lk, images := range relation {
		if lk == structuralIdentity {
			continue
		}
		for rk := range images {
			preimages[rk]++
		}
	}
	for _, rk := range fullDomain(right, rightKeys) {
		if preimages[rk] != 1 {
			return false
		}
	}
	return true
}

// reversePreimageTotal 只要求每个右值恰好有一个前像（含缺失规则），
// 不要求左域被完全覆盖——这正是单向 RightToLeft 声明的全部要求。
func reversePreimageTotal(relation map[string]map[string]struct{}, right *FieldDef, rightKeys []string) bool {
	if _, structural := relation[structuralIdentity]; structural {
		return true
	}
	preimages := map[string]int{}
	for lk, images := range relation {
		if lk == structuralIdentity {
			continue
		}
		for rk := range images {
			preimages[rk]++
		}
	}
	for _, rk := range fullDomain(right, rightKeys) {
		if preimages[rk] != 1 {
			return false
		}
	}
	return true
}

func sameSet(a, b []string) bool {
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

func contains(set []string, k string) bool {
	i := sort.SearchStrings(set, k)
	return i < len(set) && set[i] == k
}

func intersect(a, b []string) []string {
	out := []string{}
	for _, k := range a {
		if contains(b, k) {
			out = append(out, k)
		}
	}
	return out
}

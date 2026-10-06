package overload

import (
	"io"
	"math/rand"
	"strconv"
	"testing"
)

type naiveResult struct {
	selected  *Declaration
	ambiguous []Declaration
	noMatch   bool
}

type naiveWorld struct {
	promotions map[TypeID][]TypeID
	user       map[TypeID][]TypeID
}

func naiveResolve(world naiveWorld, declarations []Declaration, call Call) naiveResult {
	type candidate struct {
		decl     Declaration
		ranks    []Rank
		variadic bool
		defaults int
	}
	candidates := []candidate{}
	for _, declaration := range declarations {
		fixed := len(declaration.Params)
		variadic := fixed > 0 && declaration.Params[fixed-1].Variadic
		if variadic {
			fixed--
		}
		minimum := 0
		for i := 0; i < fixed; i++ {
			if !declaration.Params[i].Default {
				minimum++
			}
		}
		if len(call.Args) < minimum || (!variadic && len(call.Args) > fixed) {
			continue
		}
		ranks := make([]Rank, len(call.Args))
		naivePathInfo := make([][2]uint8, len(call.Args))
		ok := true
		for i, argument := range call.Args {
			rank, info, valid := world.convert(argument, parameterAt(declaration, i).Type)
			if !valid {
				ok = false
				break
			}
			ranks[i] = rank
			naivePathInfo[i] = info
		}
		if !ok {
			continue
		}
		defaults := 0
		for i := len(call.Args); i < fixed; i++ {
			if declaration.Params[i].Default {
				defaults++
			}
		}
		candidates = append(candidates, candidate{
			decl:     declaration,
			ranks:    ranks,
			variadic: variadic && len(call.Args) > fixed,
			defaults: defaults,
		})
	}
	if len(candidates) == 0 {
		return naiveResult{noMatch: true}
	}
	front := naiveFront(candidates, func(a, b candidate) int {
		leftBetter, rightBetter := false, false
		for i := range a.ranks {
			if a.ranks[i] < b.ranks[i] {
				leftBetter = true
			}
			if a.ranks[i] > b.ranks[i] {
				rightBetter = true
			}
			if leftBetter && rightBetter {
				return 0
			}
		}
		return naiveOrder(leftBetter, rightBetter)
	})
	if len(front) == 1 {
		return naiveResult{selected: &front[0].decl}
	}
	nonVariadic := naiveFilter(front, func(c candidate) bool { return !c.variadic })
	if len(nonVariadic) == 1 {
		return naiveResult{selected: &nonVariadic[0].decl}
	}
	if len(nonVariadic) > 1 {
		front = nonVariadic
	}
	bestDefaults := front[0].defaults
	for _, item := range front[1:] {
		if item.defaults < bestDefaults {
			bestDefaults = item.defaults
		}
	}
	defaultFront := naiveFilter(front, func(c candidate) bool { return c.defaults == bestDefaults })
	if len(defaultFront) == 1 {
		return naiveResult{selected: &defaultFront[0].decl}
	}
	if len(defaultFront) > 1 {
		front = defaultFront
	}
	specialized := naiveFront(front, func(a, b candidate) int {
		leftBetter, rightBetter := false, false
		for position := range call.Args {
			left := parameterAt(a.decl, position).Type
			right := parameterAt(b.decl, position).Type
			lr, _, lok := world.convert(left, right)
			rl, _, rok := world.convert(right, left)
			_ = lr
			_ = rl
			if lok && !rok {
				leftBetter = true
			}
			if rok && !lok {
				rightBetter = true
			}
			if !lok && !rok {
				return 0
			}
			if leftBetter && rightBetter {
				return 0
			}
		}
		return naiveOrder(leftBetter, rightBetter)
	})
	if len(specialized) == 1 {
		return naiveResult{selected: &specialized[0].decl}
	}
	result := naiveResult{ambiguous: []Declaration{}}
	for _, item := range specialized {
		result.ambiguous = append(result.ambiguous, item.decl)
	}
	return result
}

func naiveOrder(leftBetter, rightBetter bool) int {
	if leftBetter && !rightBetter {
		return -1
	}
	if rightBetter && !leftBetter {
		return 1
	}
	return 0
}

func naiveFront[T any](items []T, better func(T, T) int) []T {
	front := []T{}
	for i := range items {
		dominated := false
		for j := range items {
			if i != j && better(items[j], items[i]) < 0 {
				dominated = true
				break
			}
		}
		if !dominated {
			front = append(front, items[i])
		}
	}
	return front
}

func naiveFilter[T any](items []T, keep func(T) bool) []T {
	result := []T{}
	for _, item := range items {
		if keep(item) {
			result = append(result, item)
		}
	}
	return result
}

func (w naiveWorld) convert(from, to TypeID) (Rank, [2]uint8, bool) {
	if from == to {
		return RankIdentity, [2]uint8{}, true
	}
	if distance, ok := w.shortestPromotion(from, to); ok {
		return RankPromotion, [2]uint8{distance, 0}, true
	}
	bestBefore := uint8(255)
	bestAfter := uint8(255)
	foundOne := false
	tryOne := func(source TypeID, before uint8) {
		for _, via := range w.user[source] {
			if via == to {
				foundOne = true
				if before < bestBefore || (before == bestBefore && bestAfter > 0) {
					bestBefore, bestAfter = before, 0
				}
			}
			for _, target := range w.promotions[via] {
				if target == to {
					foundOne = true
					if before < bestBefore || (before == bestBefore && bestAfter > 1) {
						bestBefore, bestAfter = before, 1
					}
				}
			}
		}
	}
	tryOne(from, 0)
	for _, source := range w.promotions[from] {
		tryOne(source, 1)
	}
	if foundOne {
		return RankUser, [2]uint8{bestBefore, bestAfter}, true
	}
	return RankNone, [2]uint8{}, false
}

func (w naiveWorld) shortestPromotion(from, to TypeID) (uint8, bool) {
	type state struct {
		typeID TypeID
		depth  uint8
	}
	queue := []state{{typeID: from}}
	seen := map[TypeID]bool{from: true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.typeID == to {
			return current.depth, true
		}
		for _, next := range w.promotions[current.typeID] {
			if next == to {
				return current.depth + 1, true
			}
			if !seen[next] && current.depth < 8 {
				seen[next] = true
				queue = append(queue, state{typeID: next, depth: current.depth + 1})
			}
		}
	}
	return 0, false
}

func TestRandomDifferentialAgainstNaiveModel(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		random := rand.New(rand.NewSource(seed))
		typeCount := 3 + random.Intn(5)
		types := make([]TypeID, typeCount)
		for i := range types {
			types[i] = TypeID("t" + strconv.Itoa(i))
		}
		r := NewRegistry(WithLogWriter(io.Discard))
		world := naiveWorld{promotions: map[TypeID][]TypeID{}, user: map[TypeID][]TypeID{}}
		for _, typ := range types {
			_ = r.DefineType(typ)
		}
		for i := 0; i < typeCount*2; i++ {
			from := types[random.Intn(typeCount)]
			to := types[random.Intn(typeCount)]
			if from == to {
				continue
			}
			if random.Intn(2) == 0 {
				if err := r.AddPromotion(from, to); err == nil {
					world.promotions[from] = appendUnique(world.promotions[from], to)
				}
			} else {
				_ = r.AddUserConversion(from, to)
				world.user[from] = appendUnique(world.user[from], to)
			}
		}
		declarationCount := 1 + random.Intn(5)
		declarations := []Declaration{}
		for i := 0; i < declarationCount; i++ {
			declaration := randomDeclaration(random, types)
			if err := r.AddDeclaration(declaration); err == nil {
				declarations = append(declarations, declaration)
			}
		}
		args := make([]TypeID, random.Intn(4))
		for i := range args {
			args[i] = types[random.Intn(typeCount)]
		}
		call := Call{Name: "f", Args: args}
		actual := r.Resolve("f", call)
		expected := naiveResolve(world, declarations, call)
		assertNaiveMatch(t, seed, expected, actual)
	}
}

func appendUnique(values []TypeID, value TypeID) []TypeID {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func randomDeclaration(random *rand.Rand, types []TypeID) Declaration {
	paramCount := 1 + random.Intn(3)
	params := make([]Param, paramCount)
	defaultStart := paramCount
	if random.Intn(2) == 0 {
		defaultStart = random.Intn(paramCount + 1)
	}
	for i := 0; i < paramCount; i++ {
		params[i] = Param{Type: types[random.Intn(len(types))], Default: i >= defaultStart}
	}
	if random.Intn(3) == 0 {
		params[paramCount-1].Variadic = true
	}
	return Declaration{Name: "f", Params: params}
}

func assertNaiveMatch(t *testing.T, seed int64, expected naiveResult, actual Report) {
	t.Helper()
	if expected.noMatch != actual.NoMatch {
		t.Fatalf("seed=%d no-match mismatch expected=%v actual=%+v", seed, expected.noMatch, actual)
	}
	if expected.selected != nil {
		if actual.Selected == nil || signatureKey(*expected.selected) != signatureKey(*actual.Selected) {
			t.Fatalf("seed=%d selected mismatch expected=%+v actual=%+v", seed, expected.selected, actual.Selected)
		}
		return
	}
	if len(expected.ambiguous) != len(actual.AmbiguousCandidates) {
		t.Fatalf("seed=%d ambiguity count mismatch expected=%+v actual=%+v", seed, expected.ambiguous, actual.AmbiguousCandidates)
	}
}

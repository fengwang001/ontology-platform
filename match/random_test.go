package match

import (
	"math/rand"
	"strings"
	"testing"
)

type value struct {
	name     string
	children []*value
}

type recordedArm struct {
	pattern Pattern
	guarded bool
	result  ArmResult
}

func TestRandomOracleComparison(t *testing.T) {
	rng := rand.New(rand.NewSource(1162))
	for iteration := 0; iteration < 2000; iteration++ {
		checker := NewChecker()
		typeCount := 1 + rng.Intn(3)
		typeNames := make([]string, typeCount)
		definitions := make([][]Constructor, typeCount)
		for typeIndex := 0; typeIndex < typeCount; typeIndex++ {
			typeNames[typeIndex] = "T" + string(rune('0'+typeIndex))
			constructorCount := 1 + rng.Intn(3)
			definitions[typeIndex] = make([]Constructor, constructorCount)
			for constructorIndex := 0; constructorIndex < constructorCount; constructorIndex++ {
				name := typeNames[typeIndex] + "C" + string(rune('0'+constructorIndex))
				fieldCount := 0
				if constructorIndex > 0 {
					fieldCount = 1
					if rng.Intn(8) == 0 {
						fieldCount = 2
					}
				}
				fields := make([]string, fieldCount)
				for fieldIndex := range fields {
					if typeIndex == 0 {
						fields[fieldIndex] = typeNames[0]
					} else {
						fields[fieldIndex] = typeNames[rng.Intn(typeIndex+1)]
					}
					_ = fieldIndex
				}
				definitions[typeIndex][constructorIndex] = Constructor{Name: name, FieldTypes: fields}
			}
			if err := checker.DefineType(typeNames[typeIndex], definitions[typeIndex]); err != nil {
				t.Fatalf("iteration %d DefineType %s: %v", iteration, typeNames[typeIndex], err)
			}
		}

		rootType := typeNames[rng.Intn(typeCount)]
		session, err := checker.NewSession(rootType)
		if err != nil {
			t.Fatalf("iteration %d NewSession: %v", iteration, err)
		}
		var records []recordedArm
		armCount := 1 + rng.Intn(12)
		maxDepth := 0
		for armIndex := 0; armIndex < armCount; armIndex++ {
			pattern := randomPattern(rng, checker, rootType, 0, 2)
			if depth := patternDepth(pattern); depth > maxDepth {
				maxDepth = depth
			}
			guarded := rng.Intn(4) == 0
			result, err := checker.AddArm(session, pattern, guarded)
			if err != nil {
				t.Fatalf("iteration %d AddArm: %v; pattern=%s", iteration, err, patternText(pattern))
			}
			records = append(records, recordedArm{pattern: pattern, guarded: guarded, result: result})

			values := generateValues(checker, rootType, maxDepth+2)
			wantBranches := naiveBranchRedundancy(checker, records, values)
			if !equalBools(result.BranchRedundant, wantBranches) {
				t.Fatalf("iteration %d arm %d branches=%v want=%v\n%s",
					iteration, armIndex+1, result.BranchRedundant, wantBranches,
					randomFailureLog(rootType, definitions, records, values, nil))
			}

			missingValue, naiveExhaustive := firstUnmatchedValue(checker, records, values)
			checkResult := mustCheck(t, checker, session)
			callsBefore := checker.MissingCalls()
			if checkResult.Exhaustive != naiveExhaustive {
				t.Fatalf("iteration %d exhaustive=%v want=%v\n%s",
					iteration, checkResult.Exhaustive, naiveExhaustive,
					randomFailureLog(rootType, definitions, records, values, missingValue))
			}
			if !naiveExhaustive && checkResult.Text != patternText(checkResult.Counterexample.Pattern) {
				t.Fatalf("iteration %d non-reproducible counterexample text=%q pattern=%s\n%s",
					iteration, checkResult.Text, patternText(checkResult.Counterexample.Pattern),
					randomFailureLog(rootType, definitions, records, values, missingValue))
			}
			if !naiveExhaustive {
				witnessValue := firstValueMatching(checkResult.Counterexample.Pattern, values, checker)
				if witnessValue != nil && valueCovered(checker, records, witnessValue) {
					t.Fatalf("iteration %d counterexample is covered: %s\n%s",
						iteration, checkResult.Text,
						randomFailureLog(rootType, definitions, records, values, missingValue))
				}
				if witnessValue == nil && len(unguardedRecords(records)) != 0 {
					t.Fatalf("iteration %d counterexample had no bounded witness: %s\n%s",
						iteration, checkResult.Text,
						randomFailureLog(rootType, definitions, records, values, missingValue))
				}
			}
			cachedResult, err := checker.Check(session)
			if err != nil {
				t.Fatalf("iteration %d cached Check: %v", iteration, err)
			}
			if cachedResult.Text != checkResult.Text || cachedResult.Exhaustive != checkResult.Exhaustive {
				t.Fatalf("iteration %d cached result changed", iteration)
			}
			if calls := checker.MissingCalls(); calls != callsBefore {
				t.Fatalf("iteration %d second Check recomputed Missing: %d -> %d", iteration, callsBefore, calls)
			}
		}

		t.Logf("iteration=%d root=%s arms=%d maxDepth=%d definitions=[%s] arms=[%s]",
			iteration, rootType, len(records), maxDepth, definitionsText(definitions), recordsText(records))
	}
}

func randomPattern(rng *rand.Rand, checker *Checker, typeName string, depth, maxDepth int) Pattern {
	if depth == maxDepth {
		return Wildcard{}
	}
	choice := rng.Intn(10)
	datatype := checker.types[typeName]
	if choice < 3 || (choice < 7 && depth == maxDepth-1) {
		return Wildcard{}
	}
	if choice < 7 {
		declaration := datatype.constructors[rng.Intn(len(datatype.constructors))]
		children := make([]Pattern, len(declaration.FieldTypes))
		for i, fieldType := range declaration.FieldTypes {
			children[i] = randomPattern(rng, checker, fieldType, depth+1, maxDepth)
		}
		return ConstructorPattern{Name: declaration.Name, Children: children}
	}
	branchCount := 1 + rng.Intn(3)
	branches := make([]Pattern, branchCount)
	for i := range branches {
		branches[i] = randomPattern(rng, checker, typeName, depth+1, maxDepth)
	}
	return OrPattern{Branches: branches}
}

func generateValues(checker *Checker, typeName string, maxDepth int) []*value {
	cache := make(map[string][]*value)
	return generateValuesCached(checker, typeName, maxDepth, cache)
}

func generateValuesCached(checker *Checker, typeName string, depth int, cache map[string][]*value) []*value {
	key := typeName + ":" + string(rune('0'+depth))
	if values, ok := cache[key]; ok {
		return values
	}
	var values []*value
	for _, declaration := range checker.types[typeName].constructors {
		childValues := make([][]*value, len(declaration.FieldTypes))
		canConstruct := true
		for i, fieldType := range declaration.FieldTypes {
			if depth == 0 {
				canConstruct = false
				break
			}
			childValues[i] = generateValuesCached(checker, fieldType, depth-1, cache)
			if len(childValues[i]) == 0 {
				canConstruct = false
			}
		}
		if !canConstruct {
			continue
		}
		for _, combination := range valueCombinations(childValues, 0, nil) {
			values = append(values, &value{name: declaration.Name, children: combination})
		}
	}
	cache[key] = values
	return values
}

func valueCombinations(children [][]*value, index int, prefix []*value) [][]*value {
	if index == len(children) {
		return [][]*value{append([]*value(nil), prefix...)}
	}
	var combinations [][]*value
	for _, child := range children[index] {
		combinations = append(combinations, valueCombinations(children, index+1, append(prefix, child))...)
	}
	return combinations
}

func naiveBranchRedundancy(checker *Checker, records []recordedArm, values []*value) []bool {
	current := records[len(records)-1]
	branches := topLevelBranches(current.pattern)
	redundant := make([]bool, len(branches))
	for i, branch := range branches {
		redundant[i] = true
		for _, candidate := range values {
			if !matchesPattern(branch, candidate, checker) {
				continue
			}
			if !valueCovered(checker, records[:len(records)-1], candidate) &&
				!valueCoveredByBranches(checker, branches[:i], candidate) {
				redundant[i] = false
				break
			}
		}
	}
	return redundant
}

func valueCovered(checker *Checker, records []recordedArm, candidate *value) bool {
	for _, record := range records {
		if !record.guarded && matchesPattern(record.pattern, candidate, checker) {
			return true
		}
	}
	return false
}

func valueCoveredByBranches(checker *Checker, branches []Pattern, candidate *value) bool {
	for _, branch := range branches {
		if matchesPattern(branch, candidate, checker) {
			return true
		}
	}
	return false
}

func firstUnmatchedValue(checker *Checker, records []recordedArm, values []*value) (*value, bool) {
	for _, candidate := range values {
		if !valueCovered(checker, records, candidate) {
			return candidate, false
		}
	}
	return nil, true
}

func firstValueMatching(pattern Pattern, values []*value, checker *Checker) *value {
	for _, candidate := range values {
		if matchesPattern(pattern, candidate, checker) {
			return candidate
		}
	}
	return nil
}

func unguardedRecords(records []recordedArm) []recordedArm {
	var unguarded []recordedArm
	for _, record := range records {
		if !record.guarded {
			unguarded = append(unguarded, record)
		}
	}
	return unguarded
}

func matchesPattern(pattern Pattern, candidate *value, checker *Checker) bool {
	switch p := pattern.(type) {
	case Wildcard:
		return true
	case ConstructorPattern:
		if p.Name != candidate.name || len(p.Children) != len(candidate.children) {
			return false
		}
		for i, child := range p.Children {
			if !matchesPattern(child, candidate.children[i], checker) {
				return false
			}
		}
		return true
	case OrPattern:
		for _, branch := range p.Branches {
			if matchesPattern(branch, candidate, checker) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func equalBools(left, right []bool) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func valueText(candidate *value) string {
	if candidate == nil {
		return ""
	}
	if len(candidate.children) == 0 {
		return candidate.name
	}
	children := make([]string, len(candidate.children))
	for i, child := range candidate.children {
		children[i] = valueText(child)
	}
	return candidate.name + "(" + strings.Join(children, ", ") + ")"
}

func definitionsText(definitions [][]Constructor) string {
	var parts []string
	for _, constructors := range definitions {
		for _, declaration := range constructors {
			parts = append(parts, declaration.Name+"("+strings.Join(declaration.FieldTypes, ",")+")")
		}
	}
	return strings.Join(parts, ";")
}

func recordsText(records []recordedArm) string {
	parts := make([]string, len(records))
	for i, record := range records {
		parts[i] = patternText(record.pattern)
		if record.guarded {
			parts[i] += "{guarded}"
		}
		parts[i] += "=>" + boolsText(record.result.BranchRedundant)
	}
	return strings.Join(parts, ";")
}

func boolsText(values []bool) string {
	parts := make([]string, len(values))
	for i, value := range values {
		if value {
			parts[i] = "R"
		} else {
			parts[i] = "U"
		}
	}
	return strings.Join(parts, "")
}

func randomFailureLog(rootType string, definitions [][]Constructor, records []recordedArm, values []*value, missing *value) string {
	return "root=" + rootType +
		"\ndefinitions=" + definitionsText(definitions) +
		"\narms=" + recordsText(records) +
		"\nnaive-missing=" + valueText(missing) +
		"\nsample-values=" + sampleValuesText(values)
}

func sampleValuesText(values []*value) string {
	limit := 20
	if len(values) < limit {
		limit = len(values)
	}
	parts := make([]string, limit)
	for i := 0; i < limit; i++ {
		parts[i] = valueText(values[i])
	}
	return strings.Join(parts, ";")
}

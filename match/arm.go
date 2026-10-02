package match

import "fmt"

func (c *Checker) AddArm(sessionID int, pattern Pattern, guarded bool) (ArmResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if patternDepth(pattern) > maxPatternDepth || !shapeValid(pattern) {
		return ArmResult{}, ErrInvalidArgument
	}

	currentSession, ok := c.sessions[sessionID]
	if !ok {
		return ArmResult{}, ErrSessionNotFound
	}
	if len(currentSession.arms) >= maxArms || patternDepth(pattern) > maxPatternDepth {
		return ArmResult{}, ErrInvalidArgument
	}
	if err := validatePattern(pattern, currentSession.typeName, c); err != nil {
		return ArmResult{}, err
	}

	topBranches := topLevelBranches(pattern)
	branchRows := make([][][]Pattern, len(topBranches))
	count := 0
	for i, branch := range topBranches {
		branchRows[i] = expandPattern(branch)
		count += len(branchRows[i])
	}
	if count > maxPatternExpansion || currentSession.expansionCount+count > maxSessionExpansion {
		return ArmResult{}, ErrExpansionLimit
	}

	var previousRows [][]Pattern
	for _, previous := range currentSession.arms {
		if !previous.guarded {
			previousRows = append(previousRows, previous.rows...)
		}
	}

	result := ArmResult{
		Index:           len(currentSession.arms) + 1,
		BranchRedundant: make([]bool, len(topBranches)),
	}
	typeNames := []string{currentSession.typeName}
	allRows := make([][]Pattern, 0, count)
	for i, rows := range branchRows {
		branchUseful := false
		if len(previousRows) == 0 || firstRowUseful(c, previousRows, uniqueRows(rows), typeNames) {
			branchUseful = true
		}
		result.BranchRedundant[i] = !branchUseful
		for _, row := range rows {
			previousRows = append(previousRows, append([]Pattern(nil), row...))
			allRows = append(allRows, append([]Pattern(nil), row...))
		}
	}
	result.Redundant = allRedundant(result.BranchRedundant)

	currentSession.arms = append(currentSession.arms, arm{
		rows:      allRows,
		guarded:   guarded,
		redundant: result.Redundant,
	})
	currentSession.expansionCount += count
	if !guarded && !result.Redundant {
		currentSession.cacheValid = false
	}
	return result, nil
}

func firstRowUseful(checker *Checker, previousRows [][]Pattern, rows [][]Pattern, typeNames []string) bool {
	for _, row := range rows {
		if checker.useful(previousRows, row, typeNames) {
			return true
		}
	}
	return false
}

func uniqueRows(rows [][]Pattern) [][]Pattern {
	seen := make(map[string]bool, len(rows))
	unique := make([][]Pattern, 0, len(rows))
	for _, row := range rows {
		key := fmt.Sprintf("%#v", row)
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, row)
	}
	return unique
}

func topLevelBranches(pattern Pattern) []Pattern {
	if or, ok := pattern.(OrPattern); ok {
		return append([]Pattern(nil), or.Branches...)
	}
	return []Pattern{pattern}
}

func allRedundant(values []bool) bool {
	for _, value := range values {
		if !value {
			return false
		}
	}
	return true
}

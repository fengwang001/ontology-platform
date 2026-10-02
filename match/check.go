package match

import "strings"

func (c *Checker) Check(sessionID int) (CheckResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	currentSession, ok := c.sessions[sessionID]
	if !ok {
		return CheckResult{}, ErrSessionNotFound
	}
	if currentSession.cacheValid {
		return currentSession.cache, nil
	}

	var rows [][]Pattern
	for _, currentArm := range currentSession.arms {
		if !currentArm.guarded {
			rows = append(rows, currentArm.rows...)
		}
	}
	result := CheckResult{Exhaustive: true}
	if witness, hasMissing := c.missing(rows, []string{currentSession.typeName}); hasMissing {
		result.Exhaustive = false
		pattern := witness[0]
		result.Counterexample = &Counterexample{Pattern: pattern}
		result.Text = patternText(pattern)
	}
	currentSession.cache = result
	currentSession.cacheValid = true
	return result, nil
}

func patternText(pattern Pattern) string {
	switch p := pattern.(type) {
	case Wildcard:
		return "_"
	case ConstructorPattern:
		if len(p.Children) == 0 {
			return p.Name
		}
		children := make([]string, len(p.Children))
		for i, child := range p.Children {
			children[i] = patternText(child)
		}
		return p.Name + "(" + strings.Join(children, ", ") + ")"
	case OrPattern:
		branches := make([]string, len(p.Branches))
		for i, branch := range p.Branches {
			branches[i] = patternText(branch)
		}
		return "Or(" + strings.Join(branches, ", ") + ")"
	default:
		return ""
	}
}

package exam

// PaperState: Draft -> Published; Published -> Invalid when a contained
// question is retired; Invalid -> Published when repaired by replacement.
type PaperState int

const (
	Draft PaperState = iota
	Published
	Invalid
)

func (s PaperState) String() string {
	switch s {
	case Draft:
		return "draft"
	case Published:
		return "published"
	case Invalid:
		return "invalid"
	}
	return "unknown"
}

// DifficultyRange is a closed interval [Min, Max] on the number of
// questions of one difficulty level.
type DifficultyRange struct {
	Min, Max int
}

// PaperConfig holds the whole-paper assembly constraints.
type PaperConfig struct {
	TargetScore   int
	KnowledgeReq  map[string]int
	DifficultyReq map[int]DifficultyRange
}

// BoundQuestion is a question as seen by a paper. In a draft, Version is 0
// and the attributes are unused: the draft refers to the question and
// resolves its latest version at publish time. Once published, the bound
// version and its attributes are frozen here.
type BoundQuestion struct {
	QuestionID string
	Version    int
	Score      int
	Difficulty int
	Knowledge  map[string]bool
}

// Paper aggregates its bound questions and the assembly constraints.
type Paper struct {
	ID     string
	State  PaperState
	Config PaperConfig
	Items  map[string]*BoundQuestion
}

// attrs is the effective attribute view of one paper item.
type attrs struct {
	score int
	diff  int
	know  map[string]bool
}

func checkTotalScore(paper string, ids []string, vals []attrs, target int) *Error {
	total := 0
	for _, v := range vals {
		total += v.score
	}
	if total != target {
		return newError(ErrTotalScore, paper, ids, "total score %d != target %d", total, target)
	}
	return nil
}

func checkKnowledge(paper string, ids []string, vals []attrs, req map[string]int) *Error {
	for kp, need := range req {
		var covering []string
		for i, v := range vals {
			if v.know[kp] {
				covering = append(covering, ids[i])
			}
		}
		if len(covering) < need {
			return newError(ErrKnowledgeCoverage, paper, covering,
				"knowledge point %q covered by %d questions, need %d", kp, len(covering), need)
		}
	}
	return nil
}

func checkDifficulty(paper string, ids []string, vals []attrs, req map[int]DifficultyRange) *Error {
	for level, r := range req {
		var at []string
		for i, v := range vals {
			if v.diff == level {
				at = append(at, ids[i])
			}
		}
		if len(at) < r.Min || len(at) > r.Max {
			return newError(ErrDifficulty, paper, at,
				"difficulty %d has %d questions, need [%d,%d]", level, len(at), r.Min, r.Max)
		}
	}
	return nil
}

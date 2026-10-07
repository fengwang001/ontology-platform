package snapshot

import "fmt"

const AutoBoundary int64 = -1

type BoundaryManager struct{}

func NewBoundaryManager() *BoundaryManager { return &BoundaryManager{} }

func (m *BoundaryManager) Latest(commits []int64) int64 {
	if len(commits) == 0 {
		return 0
	}
	return commits[len(commits)-1]
}

func (m *BoundaryManager) Resolve(requested int64, commits []int64) (int64, error) {
	latest := m.Latest(commits)
	if requested == AutoBoundary {
		return latest, nil
	}
	if requested < 0 {
		return 0, boundaryError("requested boundary %d is negative", requested)
	}
	if requested > latest {
		return 0, boundaryError("requested boundary %d is after the latest accepted commit %d", requested, latest)
	}
	if requested != 0 && !contains(commits, requested) {
		return 0, boundaryError("requested boundary %d is not an accepted commit position", requested)
	}
	return requested, nil
}

func (m *BoundaryManager) Classify(commitLSN, boundary int64) (inSnapshot bool) {
	return commitLSN <= boundary
}

func contains(values []int64, target int64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func boundaryError(format string, args ...any) *ExportError {
	return &ExportError{
		Class:   ClassBoundary,
		Rule:    "boundary-is-zero-or-an-existing-commit-lsn",
		Message: fmt.Sprintf(format, args...),
	}
}

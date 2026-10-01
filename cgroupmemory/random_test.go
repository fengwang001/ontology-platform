package cgroupmemory

import (
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type modelGroup struct {
	min   int64
	low   int64
	usage int64
}

type naiveModel struct {
	groups map[string]*modelGroup
}

type modelReclaimEvent struct {
	path  string
	bytes int64
	pass  int
}

type modelReclaimResult struct {
	events       []modelReclaimEvent
	reclaimed    int64
	insufficient bool
}

func newNaiveModel() *naiveModel {
	return &naiveModel{groups: map[string]*modelGroup{"/": {}}}
}

func modelValidPath(path string) bool {
	if path == "/" {
		return true
	}
	if len(path) < 2 || path[0] != '/' || strings.HasSuffix(path, "/") {
		return false
	}
	for _, segment := range strings.Split(path[1:], "/") {
		if segment == "" {
			return false
		}
	}
	return true
}

func modelParent(path string) string {
	index := strings.LastIndexByte(path, '/')
	if index == 0 {
		return "/"
	}
	return path[:index]
}

func modelValidProtect(min, low int64) bool {
	return 0 <= min && min <= low && low <= maxProtect
}

func (m *naiveModel) create(path string, min, low int64) error {
	if !modelValidPath(path) || !modelValidProtect(min, low) {
		return ErrInvalidArgument
	}
	if path == "/" {
		return ErrRootNotOperable
	}
	if _, exists := m.groups[path]; exists {
		return ErrAlreadyExists
	}
	parent, exists := m.groups[modelParent(path)]
	if !exists {
		return ErrParentNotFound
	}
	if modelParent(path) != "/" && len(m.children(modelParent(path))) == 0 && parent.usage > 0 {
		return ErrParentHasUsage
	}
	m.groups[path] = &modelGroup{min: min, low: low}
	return nil
}

func (m *naiveModel) remove(path string) error {
	if !modelValidPath(path) {
		return ErrInvalidArgument
	}
	if path == "/" {
		return ErrRootNotOperable
	}
	if _, exists := m.groups[path]; !exists {
		return ErrNotFound
	}
	if len(m.children(path)) > 0 {
		return ErrHasChildren
	}
	delete(m.groups, path)
	return nil
}

func (m *naiveModel) setProtect(path string, min, low int64) error {
	if !modelValidPath(path) || !modelValidProtect(min, low) {
		return ErrInvalidArgument
	}
	if path == "/" {
		return ErrRootNotOperable
	}
	group, exists := m.groups[path]
	if !exists {
		return ErrNotFound
	}
	group.min = min
	group.low = low
	return nil
}

func (m *naiveModel) setUsage(path string, bytes int64) error {
	if !modelValidPath(path) || bytes < 0 || bytes > maxProtect {
		return ErrInvalidArgument
	}
	if path == "/" {
		return ErrRootNotOperable
	}
	group, exists := m.groups[path]
	if !exists {
		return ErrNotFound
	}
	if len(m.children(path)) > 0 {
		return ErrNotLeaf
	}
	group.usage = bytes
	return nil
}

func (m *naiveModel) effective(path string) (int64, int64, error) {
	if !modelValidPath(path) {
		return 0, 0, ErrInvalidArgument
	}
	if path == "/" {
		return 0, 0, ErrRootNotOperable
	}
	if _, exists := m.groups[path]; !exists {
		return 0, 0, ErrNotFound
	}
	values := m.effectiveMap()
	value := values[path]
	return value.low, value.min, nil
}

func (m *naiveModel) reclaim(need int64) modelReclaimResult {
	values := m.effectiveMap()
	leaves := m.leafPaths()
	usages := make(map[string]int64, len(leaves))
	for _, path := range leaves {
		usages[path] = m.groups[path].usage
	}

	events := make([]modelReclaimEvent, 0)
	reclaimed := int64(0)
	remaining := need

	first := make([]modelReclaimEvent, 0)
	for _, path := range leaves {
		over := usages[path] - values[path].low
		if over > 0 {
			first = append(first, modelReclaimEvent{path: path, bytes: over, pass: 1})
		}
	}
	modelSortEvents(first)
	for index := range first {
		if remaining == 0 {
			break
		}
		bytes := minInt64(remaining, first[index].bytes)
		usages[first[index].path] -= bytes
		remaining -= bytes
		reclaimed += bytes
		events = append(events, modelReclaimEvent{path: first[index].path, bytes: bytes, pass: 1})
	}

	if remaining > 0 {
		second := make([]modelReclaimEvent, 0)
		for _, path := range leaves {
			over := usages[path] - values[path].min
			if over > 0 {
				second = append(second, modelReclaimEvent{path: path, bytes: over, pass: 2})
			}
		}
		modelSortEvents(second)
		for index := range second {
			if remaining == 0 {
				break
			}
			bytes := minInt64(remaining, second[index].bytes)
			usages[second[index].path] -= bytes
			remaining -= bytes
			reclaimed += bytes
			events = append(events, modelReclaimEvent{path: second[index].path, bytes: bytes, pass: 2})
		}
	}

	for path, usage := range usages {
		m.groups[path].usage = usage
	}
	return modelReclaimResult{events: events, reclaimed: reclaimed, insufficient: remaining > 0}
}

func (m *naiveModel) children(path string) []string {
	prefix := path
	if prefix != "/" {
		prefix += "/"
	}
	children := make([]string, 0)
	for candidate := range m.groups {
		if candidate == "/" || !strings.HasPrefix(candidate, prefix) {
			continue
		}
		rest := strings.TrimPrefix(candidate, prefix)
		if !strings.Contains(rest, "/") {
			children = append(children, candidate)
		}
	}
	sort.Strings(children)
	return children
}

func (m *naiveModel) leafPaths() []string {
	leaves := make([]string, 0)
	for path := range m.groups {
		if path != "/" && len(m.children(path)) == 0 {
			leaves = append(leaves, path)
		}
	}
	sort.Strings(leaves)
	return leaves
}

type modelEffective struct {
	low int64
	min int64
}

func (m *naiveModel) effectiveMap() map[string]modelEffective {
	values := make(map[string]modelEffective)
	var walk func(string, *big.Int, *big.Int)
	walk = func(path string, parentLow *big.Int, parentMin *big.Int) {
		for _, child := range m.children(path) {
			usage := m.usage(child)
			lowCandidate := modelMinBig(usage, big.NewInt(m.groups[child].low))
			minCandidate := modelMinBig(usage, big.NewInt(m.groups[child].min))

			var effectiveLow *big.Int
			var effectiveMin *big.Int
			if path == "/" {
				effectiveLow = lowCandidate
				effectiveMin = minCandidate
			} else {
				effectiveLow = modelShare(parentLow, lowCandidate, m.childCandidateSum(path, false))
				effectiveMin = modelShare(parentMin, minCandidate, m.childCandidateSum(path, true))
			}
			if effectiveMin.Cmp(effectiveLow) > 0 {
				effectiveMin = new(big.Int).Set(effectiveLow)
			}
			values[child] = modelEffective{low: effectiveLow.Int64(), min: effectiveMin.Int64()}
			walk(child, effectiveLow, effectiveMin)
		}
	}
	walk("/", nil, nil)
	return values
}

func (m *naiveModel) childCandidateSum(path string, useMin bool) *big.Int {
	total := new(big.Int)
	for _, child := range m.children(path) {
		limit := m.groups[child].low
		if useMin {
			limit = m.groups[child].min
		}
		total.Add(total, modelMinBig(m.usage(child), big.NewInt(limit)))
	}
	return total
}

func (m *naiveModel) usage(path string) *big.Int {
	children := m.children(path)
	if len(children) == 0 {
		return big.NewInt(m.groups[path].usage)
	}
	total := new(big.Int)
	for _, child := range children {
		total.Add(total, m.usage(child))
	}
	return total
}

func modelShare(parent *big.Int, candidate *big.Int, sum *big.Int) *big.Int {
	if sum.Sign() == 0 || sum.Cmp(parent) <= 0 {
		return new(big.Int).Set(candidate)
	}
	return new(big.Int).Quo(new(big.Int).Mul(parent, candidate), sum)
}

func modelMinBig(left, right *big.Int) *big.Int {
	if left.Cmp(right) <= 0 {
		return new(big.Int).Set(left)
	}
	return new(big.Int).Set(right)
}

func modelSortEvents(events []modelReclaimEvent) {
	sort.Slice(events, func(i, j int) bool {
		if events[i].bytes != events[j].bytes {
			return events[i].bytes > events[j].bytes
		}
		return events[i].path < events[j].path
	})
}

func TestRandomOperationsAgainstNaiveModel(t *testing.T) {
	for sequence := 0; sequence < 2000; sequence++ {
		random := rand.New(rand.NewSource(int64(sequence + 1)))
		calc := New()
		model := newNaiveModel()
		paths := []string{}
		segments := []string{"a", "b", "aa", "ab", "p", "x", "y"}

		t.Logf("sequence=%d reason=start comparison against independent big-integer naive simulation", sequence)
		for step := 0; step < 70; step++ {
			operation := random.Intn(7)
			var input string

			switch operation {
			case 0, 1:
				path := randomPath(random, paths, segments)
				minValue := random.Int63n(maxProtect + 1)
				lowValue := minValue + random.Int63n(maxProtect-minValue+1)
				if operation == 0 {
					input = fmt.Sprintf("Create(%q,%d,%d)", path, minValue, lowValue)
					got := calc.Create(path, minValue, lowValue)
					want := model.create(path, minValue, lowValue)
					assertModelError(t, sequence, step, input, got, want)
					if got == nil {
						paths = append(paths, path)
					}
				} else {
					input = fmt.Sprintf("SetProtect(%q,%d,%d)", path, minValue, lowValue)
					assertModelError(t, sequence, step, input,
						calc.SetProtect(path, minValue, lowValue),
						model.setProtect(path, minValue, lowValue))
				}
			case 2:
				path := randomPath(random, paths, segments)
				bytes := random.Int63n(maxProtect + 1)
				input = fmt.Sprintf("SetUsage(%q,%d)", path, bytes)
				assertModelError(t, sequence, step, input, calc.SetUsage(path, bytes), model.setUsage(path, bytes))
			case 3:
				path := randomPath(random, paths, segments)
				input = fmt.Sprintf("Remove(%q)", path)
				got := calc.Remove(path)
				want := model.remove(path)
				assertModelError(t, sequence, step, input, got, want)
				if got == nil {
					paths = removePath(paths, path)
				}
			case 4:
				path := randomPath(random, paths, segments)
				input = fmt.Sprintf("Effective(%q)", path)
				gotLow, gotMin, gotErr := calc.Effective(path)
				wantLow, wantMin, wantErr := model.effective(path)
				assertModelError(t, sequence, step, input, gotErr, wantErr)
				if gotErr == nil && (gotLow != wantLow || gotMin != wantMin) {
					t.Fatalf("sequence=%d step=%d input=%s output=(%d,%d) want=(%d,%d) reason=effective mismatch", sequence, step, input, gotLow, gotMin, wantLow, wantMin)
				}
				t.Logf("sequence=%d step=%d input=%s output=(%d,%d) err=%v reason=effective compared", sequence, step, input, gotLow, gotMin, gotErr)
			case 5:
				need := random.Int63n(maxProtect) + 1
				input = fmt.Sprintf("Reclaim(%d)", need)
				got, gotErr := calc.Reclaim(need)
				want := model.reclaim(need)
				assertModelError(t, sequence, step, input, gotErr, nil)
				compareReclaimResults(t, sequence, step, input, got, want)
			case 6:
				path := randomExistingPath(random, paths, segments)
				bytes := random.Int63n(maxProtect + 1)
				input = fmt.Sprintf("SetUsage(existing %q,%d)", path, bytes)
				assertModelError(t, sequence, step, input, calc.SetUsage(path, bytes), model.setUsage(path, bytes))
			}

			assertAllEffectiveMatch(t, calc, model, sequence, step, input)
			assertLeafUsagesMatch(t, calc, model, sequence, step, input)
		}
	}
}

func randomPath(random *rand.Rand, paths []string, segments []string) string {
	if len(paths) > 0 && random.Intn(3) == 0 {
		return paths[random.Intn(len(paths))]
	}
	depth := 1 + random.Intn(3)
	parts := make([]string, depth)
	for index := range parts {
		parts[index] = segments[random.Intn(len(segments))]
	}
	return "/" + strings.Join(parts, "/")
}

func randomExistingPath(random *rand.Rand, paths []string, segments []string) string {
	if len(paths) == 0 {
		return "/missing"
	}
	return paths[random.Intn(len(paths))]
}

func removePath(paths []string, target string) []string {
	filtered := paths[:0]
	for _, path := range paths {
		if path != target {
			filtered = append(filtered, path)
		}
	}
	return filtered
}

func assertModelError(t *testing.T, sequence, step int, input string, got, want error) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sequence=%d step=%d input=%s output_err=%v want_err=%v reason=rejection mismatch", sequence, step, input, got, want)
	}
	t.Logf("sequence=%d step=%d input=%s output_err=%v reason=error result matched", sequence, step, input, got)
}

func compareReclaimResults(t *testing.T, sequence, step int, input string, got ReclaimResult, want modelReclaimResult) {
	t.Helper()
	wantEvents := make([]ReclaimEvent, len(want.events))
	for index, event := range want.events {
		wantEvents[index] = ReclaimEvent{Path: event.path, Bytes: event.bytes, Pass: event.pass}
	}
	if got.Reclaimed != want.reclaimed || got.Insufficient != want.insufficient || !reflect.DeepEqual(got.Events, wantEvents) {
		t.Fatalf("sequence=%d step=%d input=%s output=%#v want=%#v reason=reclaim mismatch", sequence, step, input, got, want)
	}
	t.Logf("sequence=%d step=%d input=%s output=%#v reason=reclaim sequence and bytes matched", sequence, step, input, got)
}

func assertAllEffectiveMatch(t *testing.T, calc *Calculator, model *naiveModel, sequence, step int, input string) {
	t.Helper()
	want := model.effectiveMap()
	for path, value := range want {
		gotLow, gotMin, err := calc.Effective(path)
		if err != nil || gotLow != value.low || gotMin != value.min {
			t.Fatalf("sequence=%d step=%d after=%s path=%s output=(%d,%d),%v want=(%d,%d) reason=post-operation invariant", sequence, step, input, path, gotLow, gotMin, err, value.low, value.min)
		}
	}
}

func assertLeafUsagesMatch(t *testing.T, calc *Calculator, model *naiveModel, sequence, step int, input string) {
	t.Helper()
	for path, group := range model.groups {
		if path == "/" || len(model.children(path)) > 0 {
			continue
		}
		actual := calc.lookup(path)
		if actual == nil || actual.usage != group.usage {
			t.Fatalf("sequence=%d step=%d after=%s path=%s output leaf missing or usage mismatch reason=state mismatch", sequence, step, input, path)
		}
	}
}

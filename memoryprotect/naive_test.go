package memoryprotect

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

type operationKind int

const (
	opCreate operationKind = iota
	opRemove
	opSetProtect
	opSetUsage
	opEffective
	opReclaim
)

type operation struct {
	kind  operationKind
	path  string
	min   int64
	low   int64
	bytes int64
	need  int64
}

type operationResult struct {
	err          error
	effectiveLow int64
	effectiveMin int64
	reclaim      ReclaimResult
}

type naiveManager struct {
	nodes map[string]naiveGroup
}

type naiveGroup struct {
	parent string
	min    int64
	low    int64
	usage  int64
}

func newNaiveManager() *naiveManager {
	return &naiveManager{nodes: map[string]naiveGroup{"/": {}}}
}

func (n *naiveManager) apply(op operation) operationResult {
	switch op.kind {
	case opCreate:
		return operationResult{err: n.create(op.path, op.min, op.low)}
	case opRemove:
		return operationResult{err: n.remove(op.path)}
	case opSetProtect:
		return operationResult{err: n.setProtect(op.path, op.min, op.low)}
	case opSetUsage:
		return operationResult{err: n.setUsage(op.path, op.bytes)}
	case opEffective:
		low, min, err := n.effective(op.path)
		return operationResult{err: err, effectiveLow: low, effectiveMin: min}
	case opReclaim:
		result, err := n.reclaim(op.need)
		return operationResult{err: err, reclaim: result}
	}
	return operationResult{err: ErrInvalidArgument}
}

func (n *naiveManager) create(path string, min, low int64) error {
	if !validPath(path) || !validProtection(min, low) {
		return ErrInvalidArgument
	}
	if path == "/" {
		return ErrRootOperation
	}
	if _, ok := n.nodes[path]; ok {
		return ErrExists
	}
	parent := naiveParentPath(path)
	parentNode, ok := n.nodes[parent]
	if !ok {
		return ErrParentNotFound
	}
	if n.usage(parent).Sign() > 0 {
		return ErrParentHasUsage
	}
	n.nodes[path] = naiveGroup{parent: parent, min: min, low: low}
	_ = parentNode
	return nil
}

func (n *naiveManager) remove(path string) error {
	if !validPath(path) {
		return ErrInvalidArgument
	}
	if path == "/" {
		return ErrRootOperation
	}
	node, ok := n.nodes[path]
	if !ok {
		return ErrNotFound
	}
	if n.hasChildren(path) {
		return ErrHasChildren
	}
	_ = node
	delete(n.nodes, path)
	return nil
}

func (n *naiveManager) setProtect(path string, min, low int64) error {
	if !validPath(path) || !validProtection(min, low) {
		return ErrInvalidArgument
	}
	if path == "/" {
		return ErrRootOperation
	}
	node, ok := n.nodes[path]
	if !ok {
		return ErrNotFound
	}
	node.min = min
	node.low = low
	n.nodes[path] = node
	return nil
}

func (n *naiveManager) setUsage(path string, bytes int64) error {
	if !validPath(path) || bytes < 0 || bytes > maxValue {
		return ErrInvalidArgument
	}
	if path == "/" {
		return ErrRootOperation
	}
	node, ok := n.nodes[path]
	if !ok {
		return ErrNotFound
	}
	if n.hasChildren(path) {
		return ErrNotLeaf
	}
	node.usage = bytes
	n.nodes[path] = node
	return nil
}

func (n *naiveManager) effective(path string) (int64, int64, error) {
	if !validPath(path) {
		return 0, 0, ErrInvalidArgument
	}
	if path == "/" {
		return 0, 0, ErrRootOperation
	}
	if _, ok := n.nodes[path]; !ok {
		return 0, 0, ErrNotFound
	}
	values := n.calculateEffective()
	value := values[path]
	return value.low, value.min, nil
}

func (n *naiveManager) reclaim(need int64) (ReclaimResult, error) {
	if need < 1 || need > maxValue {
		return ReclaimResult{}, ErrInvalidArgument
	}

	effective := n.calculateEffective()
	var leaves []string
	for path := range n.nodes {
		if path != "/" && !n.hasChildren(path) {
			leaves = append(leaves, path)
		}
	}

	records := []ReclaimRecord{}
	remaining := big.NewInt(need)
	remaining = n.naiveReclaimPass(leaves, effective, 1, remaining, &records)
	if remaining.Sign() > 0 {
		n.naiveReclaimPass(leaves, effective, 2, remaining, &records)
	}

	reclaimed := big.NewInt(need).Sub(big.NewInt(need), remaining).Int64()
	return ReclaimResult{
		Records:      records,
		Reclaimed:    reclaimed,
		Insufficient: remaining.Sign() > 0,
	}, nil
}

func (n *naiveManager) children(path string) []string {
	var children []string
	for childPath, node := range n.nodes {
		if node.parent == path {
			children = append(children, childPath)
		}
	}
	sort.Strings(children)
	return children
}

func (n *naiveManager) hasChildren(path string) bool {
	return len(n.children(path)) > 0
}

func (n *naiveManager) usage(path string) *big.Int {
	children := n.children(path)
	if len(children) == 0 {
		return big.NewInt(n.nodes[path].usage)
	}
	total := new(big.Int)
	for _, child := range children {
		total.Add(total, n.usage(child))
	}
	return total
}

func (n *naiveManager) calculateEffective() map[string]effectiveValues {
	usages := make(map[string]*big.Int)
	var calculateUsage func(string) *big.Int
	calculateUsage = func(path string) *big.Int {
		children := n.children(path)
		if len(children) == 0 {
			usages[path] = big.NewInt(n.nodes[path].usage)
			return new(big.Int).Set(usages[path])
		}
		total := new(big.Int)
		for _, child := range children {
			total.Add(total, calculateUsage(child))
		}
		usages[path] = new(big.Int).Set(total)
		return total
	}
	calculateUsage("/")

	values := make(map[string]effectiveValues)
	var distribute func(string, *effectiveValues)
	distribute = func(parentPath string, parentValue *effectiveValues) {
		children := n.children(parentPath)
		lowCaps := make(map[string]*big.Int)
		minCaps := make(map[string]*big.Int)
		lowSum := new(big.Int)
		minSum := new(big.Int)
		for _, child := range children {
			node := n.nodes[child]
			lowCap := naiveCap(usages[child], node.low)
			minCap := naiveCap(usages[child], node.min)
			lowCaps[child] = lowCap
			minCaps[child] = minCap
			lowSum.Add(lowSum, lowCap)
			minSum.Add(minSum, minCap)
		}

		for _, child := range children {
			var effectiveLow *big.Int
			var rawMin *big.Int
			if parentPath == "/" {
				effectiveLow = new(big.Int).Set(lowCaps[child])
				rawMin = new(big.Int).Set(minCaps[child])
			} else {
				parentLow := big.NewInt(parentValue.low)
				parentMin := big.NewInt(parentValue.min)
				if lowSum.Cmp(parentLow) <= 0 {
					effectiveLow = new(big.Int).Set(lowCaps[child])
				} else {
					effectiveLow = naiveProportionalFloor(parentLow, lowCaps[child], lowSum)
				}
				if minSum.Cmp(parentMin) <= 0 {
					rawMin = new(big.Int).Set(minCaps[child])
				} else {
					rawMin = naiveProportionalFloor(parentMin, minCaps[child], minSum)
				}
			}
			if rawMin.Cmp(effectiveLow) > 0 {
				rawMin.Set(effectiveLow)
			}
			childValue := effectiveValues{
				low: effectiveLow.Int64(),
				min: rawMin.Int64(),
			}
			values[child] = childValue
			distribute(child, &childValue)
		}
	}
	distribute("/", nil)
	return values
}

func naiveParentPath(path string) string {
	index := strings.LastIndex(path, "/")
	if index == 0 {
		return "/"
	}
	return path[:index]
}

func naiveCap(usage *big.Int, protection int64) *big.Int {
	limit := big.NewInt(protection)
	if usage.Cmp(limit) < 0 {
		return new(big.Int).Set(usage)
	}
	return limit
}

func naiveProportionalFloor(parentEffective, childCap, siblingsSum *big.Int) *big.Int {
	numerator := new(big.Int).Mul(parentEffective, childCap)
	return new(big.Int).Quo(numerator, siblingsSum)
}

type naiveCandidate struct {
	path string
	over *big.Int
}

func (n *naiveManager) naiveReclaimPass(leaves []string, effective map[string]effectiveValues, pass int, remaining *big.Int, records *[]ReclaimRecord) *big.Int {
	candidates := []naiveCandidate{}
	for _, path := range leaves {
		protected := big.NewInt(effective[path].low)
		if pass == 2 {
			protected = big.NewInt(effective[path].min)
		}
		over := new(big.Int).Sub(big.NewInt(n.nodes[path].usage), protected)
		if over.Sign() > 0 {
			candidates = append(candidates, naiveCandidate{path: path, over: over})
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].over.Cmp(candidates[j].over) != 0 {
			return candidates[i].over.Cmp(candidates[j].over) > 0
		}
		return candidates[i].path < candidates[j].path
	})

	for _, candidate := range candidates {
		if remaining.Sign() == 0 {
			break
		}
		amount := new(big.Int)
		if remaining.Cmp(candidate.over) < 0 {
			amount.Set(remaining)
		} else {
			amount.Set(candidate.over)
		}
		node := n.nodes[candidate.path]
		node.usage = new(big.Int).Sub(big.NewInt(node.usage), amount).Int64()
		n.nodes[candidate.path] = node
		remaining.Sub(remaining, amount)
		*records = append(*records, ReclaimRecord{
			Path:  candidate.path,
			Bytes: amount.Int64(),
			Pass:  pass,
		})
	}
	return remaining
}

func TestRandomOperationsAgainstNaiveModel(t *testing.T) {
	for sequence := 0; sequence < 2000; sequence++ {
		rng := rand.New(rand.NewSource(int64(sequence + 1)))
		actual := NewManager()
		expected := newNaiveManager()
		paths := []string{}

		for step := 0; step < 30; step++ {
			op := generateOperation(rng, sequence, step, paths)
			got := applyActualResult(actual, op)
			want := expected.apply(op)
			if op.kind == opCreate && want.err == nil {
				paths = append(paths, op.path)
			}
			if op.kind == opRemove && want.err == nil {
				paths = removePath(paths, op.path)
			}
			t.Logf("input sequence=%d step=%d kind=%s path=%q min=%d low=%d bytes=%d need=%d",
				sequence, step, operationName(op.kind), op.path, op.min, op.low, op.bytes, op.need)
			t.Logf("output got=%+v want=%+v", got, want)
			t.Logf("judgment errors_equal=%t effective_equal=%t reclaim_equal=%t",
				errors.Is(got.err, want.err),
				got.effectiveLow == want.effectiveLow && got.effectiveMin == want.effectiveMin,
				reflect.DeepEqual(got.reclaim, want.reclaim))
			if !errors.Is(got.err, want.err) {
				t.Fatalf("error mismatch for %+v: got %v, want %v", op, got.err, want.err)
			}
			if got.effectiveLow != want.effectiveLow || got.effectiveMin != want.effectiveMin {
				t.Fatalf("effective mismatch for %+v: got (%d,%d), want (%d,%d)",
					op, got.effectiveLow, got.effectiveMin, want.effectiveLow, want.effectiveMin)
			}
			if !reflect.DeepEqual(got.reclaim, want.reclaim) {
				t.Fatalf("reclaim mismatch for %+v: got %+v, want %+v", op, got.reclaim, want.reclaim)
			}
		}
	}
}

func TestConcurrentOperationsAreSafe(t *testing.T) {
	m := NewManager()
	if err := m.Create("/a", 0, 0); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				path := fmt.Sprintf("/a/w%d", worker)
				_ = m.Create(path, 0, 0)
				_ = m.SetProtect(path, 0, maxValue)
				_ = m.SetUsage(path, maxValue)
				_, _, _ = m.Effective(path)
				_, _ = m.Reclaim(1)
				if i%4 == 0 {
					_ = m.Remove(path)
				}
			}
		}()
	}
	wg.Wait()
}

func applyActualResult(m *Manager, op operation) operationResult {
	switch op.kind {
	case opCreate:
		return operationResult{err: m.Create(op.path, op.min, op.low)}
	case opRemove:
		return operationResult{err: m.Remove(op.path)}
	case opSetProtect:
		return operationResult{err: m.SetProtect(op.path, op.min, op.low)}
	case opSetUsage:
		return operationResult{err: m.SetUsage(op.path, op.bytes)}
	case opEffective:
		low, min, err := m.Effective(op.path)
		return operationResult{err: err, effectiveLow: low, effectiveMin: min}
	case opReclaim:
		result, err := m.Reclaim(op.need)
		return operationResult{err: err, reclaim: result}
	}
	return operationResult{err: ErrInvalidArgument}
}

func generateOperation(rng *rand.Rand, sequence, step int, paths []string) operation {
	if step < 3 || len(paths) == 0 {
		name := randomName(rng)
		path := "/" + name
		if step >= 2 && len(paths) > 0 && rng.Intn(2) == 0 {
			path = randomExistingPath(rng, paths) + "/" + name
		}
		min := randomBoundedInt(rng, 5)
		low := min + randomBoundedInt(rng, 5)
		return operation{kind: opCreate, path: path, min: min, low: low}
	}

	switch operationKind(rng.Intn(6)) {
	case opCreate:
		name := randomName(rng)
		path := "/" + name
		if rng.Intn(2) == 0 {
			path = randomExistingPath(rng, paths) + "/" + name
		}
		if rng.Intn(20) == 0 {
			path = randomInvalidPath(rng)
		}
		min := randomBoundedInt(rng, 8)
		low := min + randomBoundedInt(rng, 8)
		if rng.Intn(20) == 0 {
			low = min - 1
		}
		return operation{kind: opCreate, path: path, min: min, low: low}
	case opRemove:
		return operation{kind: opRemove, path: randomExistingPath(rng, paths)}
	case opSetProtect:
		min := randomBoundedInt(rng, 8)
		low := min + randomBoundedInt(rng, 8)
		if rng.Intn(20) == 0 {
			low = min - 1
		}
		return operation{kind: opSetProtect, path: randomExistingPath(rng, paths), min: min, low: low}
	case opSetUsage:
		return operation{kind: opSetUsage, path: randomExistingPath(rng, paths), bytes: randomBoundedInt(rng, 8)}
	case opEffective:
		return operation{kind: opEffective, path: randomExistingPath(rng, paths)}
	default:
		return operation{kind: opReclaim, need: randomBoundedInt(rng, 9)}
	}
}

func operationName(kind operationKind) string {
	names := []string{"Create", "Remove", "SetProtect", "SetUsage", "Effective", "Reclaim"}
	return names[kind]
}

func assertResult(t *testing.T, got, want operationResult) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("result mismatch: got %+v, want %+v", got, want)
	}
}

func randomExistingPath(rng *rand.Rand, paths []string) string {
	return paths[rng.Intn(len(paths))]
}

func randomName(rng *rand.Rand) string {
	return fmt.Sprintf("g%d%d", rng.Intn(4), rng.Intn(1000))
}

func randomBoundedInt(rng *rand.Rand, digits int) int64 {
	if rng.Intn(25) == 0 {
		return maxValue
	}
	return rng.Int63n(int64(digits) * 100)
}

func randomInvalidPath(rng *rand.Rand) string {
	invalid := []string{"", "a", "/a/", "/a//b", "//"}
	return invalid[rng.Intn(len(invalid))]
}

func removePath(paths []string, path string) []string {
	result := make([]string, 0, len(paths))
	for _, existing := range paths {
		if existing != path {
			result = append(result, existing)
		}
	}
	return result
}

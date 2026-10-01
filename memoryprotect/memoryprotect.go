package memoryprotect

import (
	"errors"
	"math/big"
	"sort"
	"strings"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("memoryprotect: invalid argument")
	ErrRootOperation   = errors.New("memoryprotect: root cannot be operated on")
	ErrNotFound        = errors.New("memoryprotect: group not found")
	ErrExists          = errors.New("memoryprotect: group already exists")
	ErrParentNotFound  = errors.New("memoryprotect: parent group not found")
	ErrParentHasUsage  = errors.New("memoryprotect: parent group has usage")
	ErrNotLeaf         = errors.New("memoryprotect: group is not a leaf")
	ErrHasChildren     = errors.New("memoryprotect: group has children")
)

const maxValue = 1_000_000_000_000_000

type ReclaimRecord struct {
	Path  string
	Bytes int64
	Pass  int
}

type ReclaimResult struct {
	Records      []ReclaimRecord
	Reclaimed    int64
	Insufficient bool
}

type Manager struct {
	mu    sync.Mutex
	root  *group
	nodes map[string]*group
}

type group struct {
	path     string
	parent   *group
	children map[string]*group
	min      int64
	low      int64
	usage    int64
}

type effectiveValues struct {
	low int64
	min int64
}

func NewManager() *Manager {
	root := &group{path: "/", children: make(map[string]*group)}
	nodes := map[string]*group{"/": root}
	return &Manager{root: root, nodes: nodes}
}

func (m *Manager) Create(path string, min, low int64) error {
	if !validPath(path) || !validProtection(min, low) {
		return ErrInvalidArgument
	}
	if path == "/" {
		return ErrRootOperation
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.nodes[path]; ok {
		return ErrExists
	}
	parent, ok := m.nodes[parentPath(path)]
	if !ok {
		return ErrParentNotFound
	}
	if groupUsage(parent) > 0 {
		return ErrParentHasUsage
	}

	name := path[strings.LastIndex(path, "/")+1:]
	node := &group{
		path:     path,
		parent:   parent,
		children: make(map[string]*group),
		min:      min,
		low:      low,
	}
	m.nodes[path] = node
	parent.children[name] = node
	return nil
}

func (m *Manager) Remove(path string) error {
	if !validPath(path) {
		return ErrInvalidArgument
	}
	if path == "/" {
		return ErrRootOperation
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	node, ok := m.nodes[path]
	if !ok {
		return ErrNotFound
	}
	if len(node.children) > 0 {
		return ErrHasChildren
	}

	name := path[strings.LastIndex(path, "/")+1:]
	delete(node.parent.children, name)
	delete(m.nodes, path)
	return nil
}

func (m *Manager) SetProtect(path string, min, low int64) error {
	if !validPath(path) || !validProtection(min, low) {
		return ErrInvalidArgument
	}
	if path == "/" {
		return ErrRootOperation
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	node, ok := m.nodes[path]
	if !ok {
		return ErrNotFound
	}
	node.min = min
	node.low = low
	return nil
}

func (m *Manager) SetUsage(path string, bytes int64) error {
	if !validPath(path) || bytes < 0 || bytes > maxValue {
		return ErrInvalidArgument
	}
	if path == "/" {
		return ErrRootOperation
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	node, ok := m.nodes[path]
	if !ok {
		return ErrNotFound
	}
	if len(node.children) > 0 {
		return ErrNotLeaf
	}
	node.usage = bytes
	return nil
}

func (m *Manager) Effective(path string) (effectiveLow, effectiveMin int64, err error) {
	if !validPath(path) {
		return 0, 0, ErrInvalidArgument
	}
	if path == "/" {
		return 0, 0, ErrRootOperation
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.nodes[path]; !ok {
		return 0, 0, ErrNotFound
	}
	value := m.calculateEffective()[path]
	return value.low, value.min, nil
}

func (m *Manager) Reclaim(need int64) (ReclaimResult, error) {
	if need < 1 || need > maxValue {
		return ReclaimResult{}, ErrInvalidArgument
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	effective := m.calculateEffective()
	leaves := make([]*group, 0)
	var collectLeaves func(*group)
	collectLeaves = func(node *group) {
		if len(node.children) == 0 {
			leaves = append(leaves, node)
			return
		}
		for _, child := range sortedChildren(node) {
			collectLeaves(child)
		}
	}
	collectLeaves(m.root)

	records := make([]ReclaimRecord, 0)
	remaining := need
	remaining = m.reclaimPass(leaves, effective, 1, &remaining, &records)
	if remaining > 0 {
		m.reclaimPass(leaves, effective, 2, &remaining, &records)
	}

	reclaimed := need - remaining
	return ReclaimResult{
		Records:      records,
		Reclaimed:    reclaimed,
		Insufficient: remaining > 0,
	}, nil
}

func validPath(path string) bool {
	if path == "/" {
		return true
	}
	if path == "" || path[0] != '/' || path[len(path)-1] == '/' {
		return false
	}
	for _, segment := range strings.Split(path[1:], "/") {
		if segment == "" {
			return false
		}
	}
	return true
}

func validProtection(min, low int64) bool {
	return min >= 0 && low >= 0 && min <= low && low <= maxValue
}

func parentPath(path string) string {
	index := strings.LastIndex(path, "/")
	if index == 0 {
		return "/"
	}
	return path[:index]
}

func (m *Manager) calculateEffective() map[string]effectiveValues {
	usages := make(map[string]*big.Int, len(m.nodes))
	var calculateUsage func(*group) *big.Int
	calculateUsage = func(node *group) *big.Int {
		if len(node.children) == 0 {
			usages[node.path] = big.NewInt(node.usage)
			return usages[node.path]
		}
		total := new(big.Int)
		for _, child := range sortedChildren(node) {
			total.Add(total, calculateUsage(child))
		}
		usages[node.path] = total
		return total
	}
	calculateUsage(m.root)

	values := make(map[string]effectiveValues, len(m.nodes)-1)
	distribute(m.root, nil, usages, values)
	return values
}

func distribute(parent *group, parentValues *effectiveValues, usages map[string]*big.Int, values map[string]effectiveValues) {
	children := sortedChildren(parent)
	lowSum := new(big.Int)
	minSum := new(big.Int)
	lowCaps := make(map[string]int64, len(children))
	minCaps := make(map[string]int64, len(children))
	for _, child := range children {
		usage := usages[child.path]
		lowCap := capProtection(usage, child.low)
		minCap := capProtection(usage, child.min)
		lowCaps[child.path] = lowCap
		minCaps[child.path] = minCap
		lowSum.Add(lowSum, big.NewInt(lowCap))
		minSum.Add(minSum, big.NewInt(minCap))
	}

	for _, child := range children {
		lowCap := lowCaps[child.path]
		minCap := minCaps[child.path]
		var effectiveLow int64
		var rawMin int64
		if parent.path == "/" {
			effectiveLow = lowCap
			rawMin = minCap
		} else {
			if lowSum.Cmp(big.NewInt(parentValues.low)) <= 0 {
				effectiveLow = lowCap
			} else {
				effectiveLow = proportionalFloor(parentValues.low, lowCap, lowSum)
			}
			if minSum.Cmp(big.NewInt(parentValues.min)) <= 0 {
				rawMin = minCap
			} else {
				rawMin = proportionalFloor(parentValues.min, minCap, minSum)
			}
		}

		childValues := effectiveValues{
			low: effectiveLow,
			min: minInt64(rawMin, effectiveLow),
		}
		values[child.path] = childValues
		distribute(child, &childValues, usages, values)
	}
}

func sortedChildren(node *group) []*group {
	children := make([]*group, 0, len(node.children))
	for _, child := range node.children {
		children = append(children, child)
	}
	sort.Slice(children, func(i, j int) bool {
		return children[i].path < children[j].path
	})
	return children
}

func capProtection(usage *big.Int, protection int64) int64 {
	if usage.IsInt64() && usage.Int64() < protection {
		return usage.Int64()
	}
	return protection
}

func proportionalFloor(parentEffective, childCap int64, siblingsSum *big.Int) int64 {
	numerator := new(big.Int).Mul(big.NewInt(parentEffective), big.NewInt(childCap))
	return new(big.Int).Quo(numerator, siblingsSum).Int64()
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func groupUsage(node *group) int64 {
	if len(node.children) == 0 {
		return node.usage
	}
	var total int64
	for _, child := range sortedChildren(node) {
		total += groupUsage(child)
	}
	return total
}

type reclaimCandidate struct {
	path string
	over int64
	node *group
}

func (m *Manager) reclaimPass(leaves []*group, effective map[string]effectiveValues, pass int, remaining *int64, records *[]ReclaimRecord) int64 {
	candidates := make([]reclaimCandidate, 0)
	for _, node := range leaves {
		protected := effective[node.path].low
		if pass == 2 {
			protected = effective[node.path].min
		}
		over := node.usage - protected
		if over > 0 {
			candidates = append(candidates, reclaimCandidate{
				path: node.path,
				over: over,
				node: node,
			})
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].over != candidates[j].over {
			return candidates[i].over > candidates[j].over
		}
		return candidates[i].path < candidates[j].path
	})

	for _, candidate := range candidates {
		if *remaining == 0 {
			break
		}
		amount := minInt64(*remaining, candidate.over)
		candidate.node.usage -= amount
		*remaining -= amount
		*records = append(*records, ReclaimRecord{
			Path:  candidate.path,
			Bytes: amount,
			Pass:  pass,
		})
	}
	return *remaining
}

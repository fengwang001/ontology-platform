package cgroupmemory

import (
	"errors"
	"math/big"
	"strings"
	"sync"
)

const (
	maxProtect = 1_000_000_000_000_000
	maxNeed    = maxProtect
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrRootNotOperable = errors.New("root cannot be operated on")
	ErrNotFound        = errors.New("group not found")
	ErrAlreadyExists   = errors.New("group already exists")
	ErrParentNotFound  = errors.New("parent group not found")
	ErrParentHasUsage  = errors.New("parent group has usage")
	ErrNotLeaf         = errors.New("group is not a leaf")
	ErrHasChildren     = errors.New("group has children")
)

type Group struct {
	path     string
	parent   *Group
	children map[string]*Group
	min      int64
	low      int64
	usage    int64
}

type Calculator struct {
	mu   sync.RWMutex
	root *Group
}

type ReclaimEvent struct {
	Path  string
	Bytes int64
	Pass  int
}

type ReclaimResult struct {
	Events       []ReclaimEvent
	Reclaimed    int64
	Insufficient bool
}

func New() *Calculator {
	return &Calculator{root: &Group{path: "/", children: make(map[string]*Group)}}
}

func (c *Calculator) Create(path string, min, low int64) error {
	if !validPath(path) || !validProtect(min, low) {
		return ErrInvalidArgument
	}
	if path == "/" {
		return ErrRootNotOperable
	}

	parentPath, name := splitPath(path)

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.lookup(path) != nil {
		return ErrAlreadyExists
	}
	parent := c.lookup(parentPath)
	if parent == nil {
		return ErrParentNotFound
	}
	if parent != c.root && len(parent.children) == 0 && parent.usage > 0 {
		return ErrParentHasUsage
	}

	group := &Group{
		path:     path,
		parent:   parent,
		children: make(map[string]*Group),
		min:      min,
		low:      low,
	}
	parent.children[name] = group
	return nil
}

func (c *Calculator) Remove(path string) error {
	if !validPath(path) {
		return ErrInvalidArgument
	}
	if path == "/" {
		return ErrRootNotOperable
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	group := c.lookup(path)
	if group == nil {
		return ErrNotFound
	}
	if len(group.children) > 0 {
		return ErrHasChildren
	}

	_, name := splitPath(path)
	delete(group.parent.children, name)
	return nil
}

func (c *Calculator) SetProtect(path string, min, low int64) error {
	if !validPath(path) || !validProtect(min, low) {
		return ErrInvalidArgument
	}
	if path == "/" {
		return ErrRootNotOperable
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	group := c.lookup(path)
	if group == nil {
		return ErrNotFound
	}
	group.min = min
	group.low = low
	return nil
}

func (c *Calculator) SetUsage(path string, bytes int64) error {
	if !validPath(path) || bytes < 0 || bytes > maxProtect {
		return ErrInvalidArgument
	}
	if path == "/" {
		return ErrRootNotOperable
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	group := c.lookup(path)
	if group == nil {
		return ErrNotFound
	}
	if len(group.children) > 0 {
		return ErrNotLeaf
	}
	group.usage = bytes
	return nil
}

func (c *Calculator) Effective(path string) (effectiveLow, effectiveMin int64, err error) {
	if !validPath(path) {
		return 0, 0, ErrInvalidArgument
	}
	if path == "/" {
		return 0, 0, ErrRootNotOperable
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.lookup(path) == nil {
		return 0, 0, ErrNotFound
	}
	values := c.effectiveMap()[c.lookup(path)]
	return values.low, values.min, nil
}

func (c *Calculator) Reclaim(need int64) (ReclaimResult, error) {
	if need < 1 || need > maxNeed {
		return ReclaimResult{}, ErrInvalidArgument
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	return c.reclaim(need), nil
}

func validPath(path string) bool {
	if path == "/" {
		return true
	}
	if len(path) < 2 || path[0] != '/' || path[len(path)-1] == '/' {
		return false
	}
	segments := strings.Split(path[1:], "/")
	for _, segment := range segments {
		if segment == "" {
			return false
		}
	}
	return true
}

func validProtect(min, low int64) bool {
	return min >= 0 && low >= 0 && min <= low && min <= maxProtect && low <= maxProtect
}

func splitPath(path string) (string, string) {
	index := strings.LastIndexByte(path, '/')
	if index == 0 {
		return "/", path[1:]
	}
	return path[:index], path[index+1:]
}

func (c *Calculator) lookup(path string) *Group {
	if path == "/" {
		return c.root
	}
	current := c.root
	for _, name := range strings.Split(path[1:], "/") {
		next := current.children[name]
		if next == nil {
			return nil
		}
		current = next
	}
	return current
}

func (c *Calculator) groupUsage(group *Group) *big.Int {
	if len(group.children) == 0 {
		return big.NewInt(group.usage)
	}
	total := new(big.Int)
	for _, child := range group.children {
		total.Add(total, c.groupUsage(child))
	}
	return total
}

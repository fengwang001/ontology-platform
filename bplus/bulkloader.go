package bplus

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidCapacity = errors.New("bplus: leaf capacity must be at least 2")
	ErrInvalidDegree   = errors.New("bplus: internal page capacity must be at least 3")
	ErrInvalidPercent  = errors.New("bplus: fill percentage must be between 1 and 100")
	ErrEmptyKey        = errors.New("bplus: key must not be empty")
	ErrKeyOrder        = errors.New("bplus: keys must be strictly increasing")
	ErrFinished        = errors.New("bplus: loader has already been finished")
	ErrNotFinished     = errors.New("bplus: loader must be finished before get")
)

type OrderError struct {
	Index int
	Key   string
}

func (err OrderError) Error() string { return ErrKeyOrder.Error() + ": " + err.Key }

func (err OrderError) Is(target error) bool { return target == ErrKeyOrder }

type Page struct {
	Keys     []string
	Children []int
	Min      string
}

type GetResult struct {
	Found    bool
	Accesses int
}

type BulkLoader struct {
	mu               sync.RWMutex
	leafCapacity     int
	internalCapacity int
	leafTarget       int
	internalTarget   int
	leafMin          int
	internalMin      int
	levels           [][]Page
	maxKey           string
	finished         bool
}

func NewBulkLoader(leafCapacity, internalCapacity int, fillPercent int) (*BulkLoader, error) {
	if leafCapacity < 2 {
		return nil, ErrInvalidCapacity
	}
	if internalCapacity < 3 {
		return nil, ErrInvalidDegree
	}
	if fillPercent < 1 || fillPercent > 100 {
		return nil, ErrInvalidPercent
	}

	leafMin := leafCapacity / 2
	internalMin := (internalCapacity + 1) / 2
	leafTarget := max((leafCapacity*fillPercent+99)/100, leafMin)
	internalTarget := max((internalCapacity*fillPercent+99)/100, internalMin)

	return &BulkLoader{
		leafCapacity:     leafCapacity,
		internalCapacity: internalCapacity,
		leafTarget:       leafTarget,
		internalTarget:   internalTarget,
		leafMin:          leafMin,
		internalMin:      internalMin,
		levels:           [][]Page{{{}}},
	}, nil
}

func (loader *BulkLoader) Add(keys ...string) error {
	loader.mu.Lock()
	defer loader.mu.Unlock()

	if loader.finished {
		return ErrFinished
	}
	for _, key := range keys {
		if key == "" {
			return ErrEmptyKey
		}
	}

	existing := countKeys(loader.levels)
	previous := loader.maxKey
	for offset, key := range keys {
		if offset > 0 || existing > 0 {
			if key <= previous {
				return OrderError{Index: existing + offset, Key: key}
			}
		}
		previous = key
	}

	for _, key := range keys {
		loader.addKey(key)
	}
	return nil
}

func (loader *BulkLoader) Finish() error {
	loader.mu.Lock()
	defer loader.mu.Unlock()

	if loader.finished {
		return ErrFinished
	}

	loader.levels[0] = rebalanceLeafLast(loader.levels[0], loader.leafMin, loader.leafCapacity)

	for len(loader.levels[len(loader.levels)-1]) != 1 {
		lower := loader.levels[len(loader.levels)-1]
		loader.levels = append(loader.levels, buildInternal(lower, loader.internalTarget, loader.internalMin, loader.internalCapacity))
	}
	loader.finished = true
	return nil
}

func (loader *BulkLoader) Get(key string) (GetResult, error) {
	loader.mu.RLock()
	defer loader.mu.RUnlock()

	if !loader.finished {
		return GetResult{}, ErrNotFinished
	}

	result := GetResult{}
	node := 0
	for level := len(loader.levels) - 1; level > 0; level-- {
		page := loader.levels[level][node]
		separatorIndex := sort.Search(len(page.Keys), func(index int) bool {
			return page.Keys[index] > key
		})
		node = page.Children[separatorIndex]
		result.Accesses++
	}

	leaf := loader.levels[0][node]
	result.Accesses++
	result.Found = contains(leaf.Keys, key)
	return result, nil
}

func (loader *BulkLoader) Pages() [][]Page {
	loader.mu.RLock()
	defer loader.mu.RUnlock()
	return clonePages(loader.levels)
}

func (loader *BulkLoader) Height() int {
	loader.mu.RLock()
	defer loader.mu.RUnlock()
	if !loader.finished {
		return 0
	}
	return len(loader.levels)
}

func (loader *BulkLoader) addKey(key string) {
	leaves := loader.levels[0]
	current := len(leaves) - 1
	if len(leaves[current].Keys) >= loader.leafTarget {
		leaves = append(leaves, Page{Min: key})
		current++
		loader.levels[0] = leaves
	}
	leaves[current].Keys = append(leaves[current].Keys, key)
	if leaves[current].Min == "" {
		leaves[current].Min = key
	}
	loader.maxKey = key
}

func buildInternal(children []Page, target, minimum, capacity int) []Page {
	var pages []Page
	for start := 0; start < len(children); {
		end := min(start+target, len(children))
		pages = append(pages, makeInternalPage(children, start, end))
		pages[len(pages)-1].Min = children[start].Min
		start = end
	}
	return rebalanceInternalLast(pages, minimum, capacity)
}

func makeInternalPage(children []Page, start, end int) Page {
	page := Page{
		Keys:     make([]string, 0, max(end-start-1, 0)),
		Children: make([]int, 0, end-start),
		Min:      children[start].Min,
	}
	for index := start; index < end; index++ {
		page.Children = append(page.Children, index)
	}
	for index := start + 1; index < end; index++ {
		page.Keys = append(page.Keys, children[index].Min)
	}
	return page
}

func rebalanceLeafLast(pages []Page, minimum, capacity int) []Page {
	if len(pages) == 0 || len(pages[len(pages)-1].Keys) >= minimum {
		return pages
	}
	if len(pages) == 1 {
		if len(pages[0].Keys) == 0 {
			return pages
		}
		return pages
	}

	left := pages[len(pages)-2]
	right := pages[len(pages)-1]
	total := len(left.Keys) + len(right.Keys)
	keys := append(append([]string{}, left.Keys...), right.Keys...)

	if total <= capacity {
		pages[len(pages)-2] = Page{Keys: keys, Min: keys[0]}
		return pages[:len(pages)-1]
	}

	split := (total + 1) / 2
	pages[len(pages)-2] = Page{Keys: keys[:split], Min: keys[0]}
	pages[len(pages)-1] = Page{Keys: keys[split:], Min: keys[split]}
	return pages
}

func rebalanceInternalLast(pages []Page, minimum, capacity int) []Page {
	if len(pages) == 0 || len(pages[len(pages)-1].Children) >= minimum {
		return pages
	}
	if len(pages) == 1 {
		return pages
	}

	left := pages[len(pages)-2]
	right := pages[len(pages)-1]
	total := len(left.Children) + len(right.Children)
	children := append(append([]int{}, left.Children...), right.Children...)
	keys := append([]string{}, left.Keys...)
	keys = append(keys, right.Min)
	keys = append(keys, right.Keys...)

	if total <= capacity {
		pages[len(pages)-2] = Page{Keys: keys, Children: children, Min: left.Min}
		return pages[:len(pages)-1]
	}

	split := (total + 1) / 2
	pages[len(pages)-2] = Page{Keys: append([]string{}, keys[:split-1]...), Children: append([]int{}, children[:split]...), Min: left.Min}
	pages[len(pages)-1] = Page{Keys: append([]string{}, keys[split:]...), Children: append([]int{}, children[split:]...), Min: keys[split-1]}
	return pages
}

func clonePages(levels [][]Page) [][]Page {
	snapshot := make([][]Page, len(levels))
	for levelIndex, level := range levels {
		snapshot[levelIndex] = make([]Page, len(level))
		for pageIndex, page := range level {
			snapshot[levelIndex][pageIndex] = Page{
				Keys:     append([]string{}, page.Keys...),
				Children: append([]int{}, page.Children...),
				Min:      page.Min,
			}
		}
	}
	return snapshot
}

func countKeys(levels [][]Page) int {
	if len(levels) == 0 {
		return 0
	}
	count := 0
	for _, page := range levels[0] {
		count += len(page.Keys)
	}
	return count
}

func contains(keys []string, key string) bool {
	index := sort.SearchStrings(keys, key)
	return index < len(keys) && keys[index] == key
}

// Package ontology implements a concurrent multi-select faceted counter with
// hierarchical values and dimension dependencies.
package ontology

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// Distinguishable rejection reasons.
var (
	// ErrInvalidArgument: empty docID/dimension, illegal path, duplicate value
	// in the same dimension, dimension/value count over limit, or topN out of range.
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrDuplicateDocument: Add with an already existing docID.
	ErrDuplicateDocument = errors.New("duplicate document")
	// ErrDocumentNotFound: Replace/Delete with an unknown docID.
	ErrDocumentNotFound = errors.New("document not found")
	// ErrDuplicateLink: child already has a parent.
	ErrDuplicateLink = errors.New("duplicate dependency declaration")
	// ErrCyclicDependency: child equals parent, or the new link forms a cycle.
	ErrCyclicDependency = errors.New("cyclic dependency")
)

const (
	maxDimensionsPerDoc = 16
	maxValuesPerDim     = 32
	maxPathSegments     = 4
	minTopN             = 1
	maxTopN             = 50
)

// FacetCounter is a concurrent multi-select faceted counter.
type FacetCounter struct {
	mu sync.RWMutex
	// documents maps docID to its dimension -> node set (all path prefixes).
	documents map[string]map[string]map[string]struct{}
	// parentOf maps a dependent dimension to its (unique) parent dimension.
	parentOf map[string]string
}

// NewFacetCounter creates an empty FacetCounter.
func NewFacetCounter() *FacetCounter {
	return &FacetCounter{
		documents: make(map[string]map[string]map[string]struct{}),
		parentOf:  make(map[string]string),
	}
}

// Attrs maps a dimension name to its raw hierarchical values.
type Attrs map[string][]string

// FacetItem is a single counted value of a dimension.
type FacetItem struct {
	Value    string
	Count    int
	Selected bool
}

// FacetResult is the facet list of one effective dimension.
type FacetResult struct {
	Dimension string
	Items     []FacetItem
}

// FacetsResult is the output of Facets.
type FacetsResult struct {
	Total  int
	Facets []FacetResult
}

// validateAndExpand validates attrs and expands every value into the set of
// all its prefix nodes. It never mutates the input map or its slices.
func validateAndExpand(attrs Attrs) (map[string]map[string]struct{}, error) {
	dimNames := make([]string, 0, len(attrs))
	for dim := range attrs {
		dimNames = append(dimNames, dim)
	}
	sort.Strings(dimNames)
	if len(dimNames) > maxDimensionsPerDoc {
		return nil, ErrInvalidArgument
	}
	expanded := make(map[string]map[string]struct{}, len(dimNames))
	for _, dim := range dimNames {
		if dim == "" {
			return nil, ErrInvalidArgument
		}
		values := attrs[dim]
		if len(values) > maxValuesPerDim {
			return nil, ErrInvalidArgument
		}
		nodes := make(map[string]struct{})
		seen := make(map[string]struct{}, len(values))
		for _, value := range values {
			if _, dup := seen[value]; dup {
				return nil, ErrInvalidArgument
			}
			seen[value] = struct{}{}
			segments, ok := splitPath(value)
			if !ok {
				return nil, ErrInvalidArgument
			}
			prefix := ""
			for i, segment := range segments {
				if i > 0 {
					prefix += "/"
				}
				prefix += segment
				nodes[prefix] = struct{}{}
			}
		}
		if len(nodes) > 0 {
			expanded[dim] = nodes
		}
	}
	return expanded, nil
}

// splitPath validates a hierarchical value and returns its 1..4 non-empty
// segments. String-prefix-only sharing (e.g. "a" vs "ab") never matches.
func splitPath(value string) ([]string, bool) {
	if value == "" || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") ||
		strings.Contains(value, "//") {
		return nil, false
	}
	segments := strings.Split(value, "/")
	if len(segments) < 1 || len(segments) > maxPathSegments {
		return nil, false
	}
	for _, segment := range segments {
		if segment == "" {
			return nil, false
		}
	}
	return segments, true
}

func (fc *FacetCounter) Add(docID string, attrs Attrs) error {
	if docID == "" {
		return ErrInvalidArgument
	}
	expanded, err := validateAndExpand(attrs)
	if err != nil {
		return err
	}
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if _, exists := fc.documents[docID]; exists {
		return ErrDuplicateDocument
	}
	fc.documents[docID] = expanded
	return nil
}

func (fc *FacetCounter) Replace(docID string, attrs Attrs) error {
	if docID == "" {
		return ErrInvalidArgument
	}
	expanded, err := validateAndExpand(attrs)
	if err != nil {
		return err
	}
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if _, exists := fc.documents[docID]; !exists {
		return ErrDocumentNotFound
	}
	// Single map swap under the lock: a concurrent Facets never observes a mix
	// of old and new content.
	fc.documents[docID] = expanded
	return nil
}

func (fc *FacetCounter) Delete(docID string) error {
	if docID == "" {
		return ErrInvalidArgument
	}
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if _, exists := fc.documents[docID]; !exists {
		return ErrDocumentNotFound
	}
	delete(fc.documents, docID)
	return nil
}

func (fc *FacetCounter) Link(child, parent string) error {
	if child == "" || parent == "" {
		return ErrInvalidArgument
	}
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if _, ok := fc.parentOf[child]; ok {
		// Any re-declaration is rejected; the edge can never be changed.
		return ErrDuplicateLink
	}
	if child == parent {
		return ErrCyclicDependency
	}
	// Walk from parent; reaching child would close a cycle.
	for cursor := parent; ; {
		if cursor == child {
			return ErrCyclicDependency
		}
		next, ok := fc.parentOf[cursor]
		if !ok {
			break
		}
		cursor = next
	}
	fc.parentOf[child] = parent
	return nil
}

func (fc *FacetCounter) Facets(selected map[string][]string, topN int) (FacetsResult, error) {
	if topN < minTopN || topN > maxTopN {
		return FacetsResult{}, ErrInvalidArgument
	}
	// Validate selected and de-duplicate values per dimension (also determinizes
	// caller-supplied slice order).
	selectedSets := make(map[string]map[string]struct{}, len(selected))
	dimNames := make([]string, 0, len(selected))
	for dim := range selected {
		dimNames = append(dimNames, dim)
	}
	sort.Strings(dimNames)
	for _, dim := range dimNames {
		if dim == "" {
			return FacetsResult{}, ErrInvalidArgument
		}
		set := make(map[string]struct{}, len(selected[dim]))
		for _, value := range selected[dim] {
			if _, ok := splitPath(value); !ok {
				return FacetsResult{}, ErrInvalidArgument
			}
			set[value] = struct{}{}
		}
		selectedSets[dim] = set
	}

	fc.mu.RLock()
	defer fc.mu.RUnlock()

	// Collect every known dimension (documents, selections and dependency links).
	known := make(map[string]struct{})
	for _, doc := range fc.documents {
		for dim := range doc {
			known[dim] = struct{}{}
		}
	}
	for dim := range selectedSets {
		known[dim] = struct{}{}
	}
	for child, parent := range fc.parentOf {
		known[child] = struct{}{}
		known[parent] = struct{}{}
	}

	// Effective: no parent, or parent effective with a non-empty selected set.
	effective := make(map[string]bool)
	var isEffective func(string) bool
	isEffective = func(dim string) bool {
		if status, ok := effective[dim]; ok {
			return status
		}
		parent, hasParent := fc.parentOf[dim]
		if !hasParent {
			effective[dim] = true
			return true
		}
		status := isEffective(parent) && len(selectedSets[parent]) > 0
		effective[dim] = status
		return status
	}
	allDims := make([]string, 0, len(known))
	for dim := range known {
		if isEffective(dim) {
			allDims = append(allDims, dim)
		}
	}
	sort.Strings(allDims)

	// Constraining dimensions: effective and with a non-empty selected set.
	constraining := make([]string, 0, len(allDims))
	for _, dim := range allDims {
		if len(selectedSets[dim]) > 0 {
			constraining = append(constraining, dim)
		}
	}

	docIDs := make([]string, 0, len(fc.documents))
	for docID := range fc.documents {
		docIDs = append(docIDs, docID)
	}
	sort.Strings(docIDs)

	// M: documents satisfying every constraining dimension.
	matched := make([]string, 0, len(docIDs))
	for _, docID := range docIDs {
		doc := fc.documents[docID]
		ok := true
		for _, dim := range constraining {
			nodes := doc[dim]
			hit := false
			for value := range selectedSets[dim] {
				if _, has := nodes[value]; has {
					hit = true
					break
				}
			}
			if !hit {
				ok = false
				break
			}
		}
		if ok {
			matched = append(matched, docID)
		}
	}

	result := FacetsResult{Total: len(matched), Facets: []FacetResult{}}
	for _, dim := range allDims {
		// Constraints except dim itself.
		otherConstraints := make([]string, 0, len(constraining))
		for _, other := range constraining {
			if other != dim {
				otherConstraints = append(otherConstraints, other)
			}
		}
		// Count over ALL documents subject to the other dimensions'
		// constraints — not over M (which also applies d's own filter).
		counts := make(map[string]int)
		for _, docID := range docIDs {
			doc := fc.documents[docID]
			passes := true
			for _, other := range otherConstraints {
				nodes := doc[other]
				hit := false
				for value := range selectedSets[other] {
					if _, has := nodes[value]; has {
						hit = true
						break
					}
				}
				if !hit {
					passes = false
					break
				}
			}
			if !passes {
				continue
			}
			// Set semantics: one document counts a node at most once.
			for node := range doc[dim] {
				counts[node]++
			}
		}

		// Candidates: nodes with count > 0 plus selected values (may count 0).
		candidateSet := make(map[string]struct{})
		for node, count := range counts {
			if count > 0 {
				candidateSet[node] = struct{}{}
			}
		}
		for value := range selectedSets[dim] {
			candidateSet[value] = struct{}{}
		}
		if len(candidateSet) == 0 {
			continue
		}
		candidates := make([]string, 0, len(candidateSet))
		for value := range candidateSet {
			candidates = append(candidates, value)
		}
		sort.Slice(candidates, func(i, j int) bool {
			if counts[candidates[i]] != counts[candidates[j]] {
				return counts[candidates[i]] > counts[candidates[j]]
			}
			return candidates[i] < candidates[j]
		})
		if len(candidates) > topN {
			candidates = candidates[:topN]
		}
		items := make([]FacetItem, 0, len(candidates))
		for _, value := range candidates {
			_, isSelected := selectedSets[dim][value]
			items = append(items, FacetItem{
				Value:    value,
				Count:    counts[value],
				Selected: isSelected,
			})
		}
		result.Facets = append(result.Facets, FacetResult{
			Dimension: dim,
			Items:     items,
		})
	}
	return result, nil
}

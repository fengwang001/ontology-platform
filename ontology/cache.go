package ontology

import (
	"sync"
	"sync/atomic"
)

type ruleKey struct {
	path    string
	subject string
	action  string
}

type cacheKey struct {
	subject string
	action  string
	path    string
}

type cachedDecision struct {
	decision Decision
}

type DecisionCache struct {
	mu           sync.RWMutex
	rules        map[ruleKey]Effect
	entries      map[cacheKey]cachedDecision
	version      int64
	ruleLimit    int
	hits         atomic.Int64
	computations atomic.Int64
	invalidated  atomic.Int64
}

func NewDecisionCache(ruleLimit int) *DecisionCache {
	return &DecisionCache{
		rules:     make(map[ruleKey]Effect),
		entries:   make(map[cacheKey]cachedDecision),
		ruleLimit: max(ruleLimit, 0),
	}
}

func (cache *DecisionCache) Decide(subject, action, resourcePath string) (Decision, error) {
	if !validatePath(resourcePath) {
		return Decision{}, ErrInvalidPath
	}
	if subject == "" {
		return Decision{}, ErrEmptySubject
	}
	if action == "" {
		return Decision{}, ErrEmptyAction
	}

	key := cacheKey{subject: subject, action: action, path: resourcePath}

	cache.mu.RLock()
	if entry, ok := cache.entries[key]; ok {
		cache.hits.Add(1)
		decision := entry.decision
		cache.mu.RUnlock()
		return decision, nil
	}
	version := cache.version
	decision := cache.decideFromRules(subject, action, resourcePath)
	cache.mu.RUnlock()

	cache.computations.Add(1)
	return cache.backfill(key, version, decision)
}

func (cache *DecisionCache) backfill(key cacheKey, version int64, computed Decision) (Decision, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	if entry, ok := cache.entries[key]; ok {
		return entry.decision, nil
	}
	if cache.version != version {
		computed = cache.decideFromRules(key.subject, key.action, key.path)
	}
	cache.entries[key] = cachedDecision{decision: computed}
	return computed, nil
}

func (cache *DecisionCache) SetRule(path, subject, action string, effect Effect) error {
	if !validatePath(path) {
		return ErrInvalidPath
	}
	if subject == "" {
		return ErrEmptySubject
	}
	if action == "" {
		return ErrEmptyAction
	}

	key := ruleKey{path: path, subject: subject, action: action}

	cache.mu.Lock()
	defer cache.mu.Unlock()

	_, existed := cache.rules[key]
	if !existed && len(cache.rules) >= cache.ruleLimit {
		return ErrRuleLimitReached
	}
	if effect != Allow && effect != Deny {
		return ErrInvalidEffect
	}

	cache.rules[key] = effect
	cache.version++
	cache.invalidateChanged(path, subject, action)
	return nil
}

func (cache *DecisionCache) DeleteRule(path, subject, action string) error {
	if !validatePath(path) {
		return ErrInvalidPath
	}
	if subject == "" {
		return ErrEmptySubject
	}
	if action == "" {
		return ErrEmptyAction
	}

	key := ruleKey{path: path, subject: subject, action: action}

	cache.mu.Lock()
	defer cache.mu.Unlock()

	_, existed := cache.rules[key]
	if !existed {
		return ErrRuleNotFound
	}

	delete(cache.rules, key)
	cache.version++
	cache.invalidateChanged(path, subject, action)
	return nil
}

func (cache *DecisionCache) Stats() CacheStats {
	cache.mu.RLock()
	defer cache.mu.RUnlock()

	return CacheStats{
		Hits:         cache.hits.Load(),
		Computations: cache.computations.Load(),
		Invalidated:  cache.invalidated.Load(),
	}
}

func (cache *DecisionCache) decideFromRules(subject, action, resourcePath string) Decision {
	for _, ancestorPath := range ancestorPaths(resourcePath) {
		key := ruleKey{path: ancestorPath, subject: subject, action: action}
		if effect, ok := cache.rules[key]; ok {
			return Decision{Effect: effect, BasisPath: ancestorPath}
		}
	}
	return Decision{Effect: Deny, HasNoBasis: true}
}

func (cache *DecisionCache) entriesInSubtree(rootPath, subject, action string) map[cacheKey]cachedDecision {
	matches := make(map[cacheKey]cachedDecision)
	for key, entry := range cache.entries {
		if key.subject == subject && key.action == action && isDescendantOrSelf(key.path, rootPath) {
			matches[key] = entry
		}
	}
	return matches
}

func (cache *DecisionCache) invalidateChanged(rootPath, subject, action string) {
	for key, entry := range cache.entriesInSubtree(rootPath, subject, action) {
		before := entry.decision
		after := cache.decideFromRules(subject, action, key.path)
		if before != after {
			delete(cache.entries, key)
			cache.invalidated.Add(1)
		}
	}
}

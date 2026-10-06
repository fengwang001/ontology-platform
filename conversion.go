package overload

import (
	"fmt"
	"math"
	"sort"
)

type conversionPath struct {
	rank      Rank
	beforeUD  uint8
	afterUD   uint8
	chainedUD bool
}

type conversionTable struct {
	promotionAdj   map[TypeID]map[TypeID]struct{}
	promotion      map[TypeID]map[TypeID]uint8
	user           map[TypeID]map[TypeID]struct{}
	best           map[conversionPair]conversionPath
	chainedUserDef map[conversionPair]struct{}
}

type conversionPair struct {
	from TypeID
	to   TypeID
}

func newConversionTable() conversionTable {
	return conversionTable{
		promotionAdj:   map[TypeID]map[TypeID]struct{}{},
		promotion:      map[TypeID]map[TypeID]uint8{},
		user:           make(map[TypeID]map[TypeID]struct{}),
		best:           map[conversionPair]conversionPath{},
		chainedUserDef: map[conversionPair]struct{}{},
	}
}

func (t conversionTable) clone() conversionTable {
	return conversionTable{
		promotionAdj:   cloneTypeSetMap(t.promotionAdj),
		promotion:      cloneTypeUintMap(t.promotion),
		user:           cloneTypeSetMap(t.user),
		best:           cloneBestMap(t.best),
		chainedUserDef: clonePairSet(t.chainedUserDef),
	}
}

func (t *conversionTable) addPromotion(from, to TypeID) error {
	if from == to || t.reaches(to, from) {
		return &RegistrationError{Kind: ErrPromotionCycle, Msg: fmt.Sprintf("promotion %s -> %s closes a cycle", from, to)}
	}
	if t.promotionAdj[from] == nil {
		t.promotionAdj[from] = map[TypeID]struct{}{}
	}
	t.promotionAdj[from][to] = struct{}{}
	t.rebuild()
	return nil
}

func (t *conversionTable) addUserConversion(from, to TypeID) {
	if from == to {
		return
	}
	if t.user[from] == nil {
		t.user[from] = map[TypeID]struct{}{}
	}
	t.user[from][to] = struct{}{}
	t.rebuild()
}

func (t *conversionTable) rebuild() {
	t.promotion = shortestPromotions(t.promotionAdj)
	oneUser := oneUserPaths(t.promotionAdj, t.user)
	t.best = bestConversionPaths(t.promotion, oneUser)
	t.chainedUserDef = chainedPairs(t.user, t.promotionAdj)
}

func (t conversionTable) convert(from, to TypeID) conversionPath {
	if from == to {
		return conversionPath{rank: RankIdentity}
	}
	if path, ok := t.best[conversionPair{from: from, to: to}]; ok {
		return path
	}
	_, chained := t.chainedUserDef[conversionPair{from: from, to: to}]
	return conversionPath{chainedUD: chained}
}

func (t conversionTable) reaches(from, to TypeID) bool {
	if from == to {
		return true
	}
	_, ok := t.promotion[from][to]
	return ok
}

func shortestPromotions(adj map[TypeID]map[TypeID]struct{}) map[TypeID]map[TypeID]uint8 {
	vertices := map[TypeID]struct{}{}
	for from, destinations := range adj {
		vertices[from] = struct{}{}
		for to := range destinations {
			vertices[to] = struct{}{}
		}
	}
	ids := make([]TypeID, 0, len(vertices))
	for vertex := range vertices {
		ids = append(ids, vertex)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	index := map[TypeID]int{}
	for i, id := range ids {
		index[id] = i
	}
	const inf = math.MaxUint8
	dist := make([][]uint8, len(ids))
	for i := range dist {
		dist[i] = make([]uint8, len(ids))
		for j := range dist[i] {
			if i == j {
				dist[i][j] = 0
			} else {
				dist[i][j] = inf
			}
		}
	}
	for from, destinations := range adj {
		for to := range destinations {
			dist[index[from]][index[to]] = 1
		}
	}
	for k := range ids {
		for i := range ids {
			if dist[i][k] == inf {
				continue
			}
			for j := range ids {
				if dist[k][j] == inf {
					continue
				}
				candidate := dist[i][k] + dist[k][j]
				if candidate < dist[i][j] {
					dist[i][j] = candidate
				}
			}
		}
	}
	result := map[TypeID]map[TypeID]uint8{}
	for i, from := range ids {
		for j, to := range ids {
			if i != j && dist[i][j] != inf {
				if result[from] == nil {
					result[from] = map[TypeID]uint8{}
				}
				result[from][to] = dist[i][j]
			}
		}
	}
	return result
}

func oneUserPaths(promotionAdj map[TypeID]map[TypeID]struct{}, user map[TypeID]map[TypeID]struct{}) map[conversionPair]conversionPath {
	paths := map[conversionPair]conversionPath{}
	for userFrom, destinations := range user {
		userFrom := userFrom
		for userTo := range destinations {
			userTo := userTo
			consider := func(start TypeID, before uint8, finish TypeID, after uint8) {
				if start == finish {
					return
				}
				pair := conversionPair{from: start, to: finish}
				candidate := conversionPath{rank: RankUser, beforeUD: before, afterUD: after}
				if existing, ok := paths[pair]; !ok || lessPath(candidate, existing) {
					paths[pair] = candidate
				}
			}
			promotionSources := map[TypeID]uint8{userFrom: 0}
			for before, destinations := range promotionAdj {
				if _, ok := destinations[userFrom]; ok {
					promotionSources[before] = 1
				}
			}
			promotionTargets := map[TypeID]uint8{userTo: 0}
			for destination := range promotionAdj[userTo] {
				target := destination
				promotionTargets[target] = 1
			}
			for start, before := range promotionSources {
				for finish, after := range promotionTargets {
					consider(start, before, finish, after)
				}
			}
		}
	}
	return paths
}

func bestConversionPaths(promotion map[TypeID]map[TypeID]uint8, oneUser map[conversionPair]conversionPath) map[conversionPair]conversionPath {
	best := map[conversionPair]conversionPath{}
	for from, destinations := range promotion {
		for to, distance := range destinations {
			best[conversionPair{from: from, to: to}] = conversionPath{rank: RankPromotion, beforeUD: distance}
		}
	}
	for pair, path := range oneUser {
		if existing, ok := best[pair]; !ok || lessPath(path, existing) {
			best[pair] = path
		}
	}
	return best
}

func chainedPairs(user map[TypeID]map[TypeID]struct{}, promotionAdj map[TypeID]map[TypeID]struct{}) map[conversionPair]struct{} {
	result := map[conversionPair]struct{}{}
	pairs := make([]conversionPair, 0)
	for from, destinations := range user {
		for to := range destinations {
			pairs = append(pairs, conversionPair{from: from, to: to})
		}
	}
	for _, first := range pairs {
		for _, second := range pairs {
			_, middleOK := immediatePromotionDistance(promotionAdj, first.to, second.from)
			if !middleOK || first.from == second.to {
				continue
			}
			sources := immediateClosureInto(promotionAdj, first.from)
			targets := immediateClosureFrom(promotionAdj, second.to)
			for source := range sources {
				for target := range targets {
					if source != target {
						result[conversionPair{from: source, to: target}] = struct{}{}
					}
				}
			}
		}
	}
	return result
}

func immediatePromotionDistance(promotion map[TypeID]map[TypeID]struct{}, from, to TypeID) (uint8, bool) {
	if from == to {
		return 0, true
	}
	_, ok := promotion[from][to]
	return 1, ok
}

func immediateClosureFrom(promotion map[TypeID]map[TypeID]struct{}, source TypeID) map[TypeID]struct{} {
	result := map[TypeID]struct{}{source: {}}
	for target := range promotion[source] {
		result[target] = struct{}{}
	}
	return result
}

func immediateClosureInto(promotion map[TypeID]map[TypeID]struct{}, target TypeID) map[TypeID]struct{} {
	result := map[TypeID]struct{}{target: {}}
	for source, destinations := range promotion {
		if _, ok := destinations[target]; ok {
			result[source] = struct{}{}
		}
	}
	return result
}

func lessPath(left, right conversionPath) bool {
	if left.rank != right.rank {
		return left.rank < right.rank
	}
	if left.beforeUD != right.beforeUD {
		return left.beforeUD < right.beforeUD
	}
	return left.afterUD < right.afterUD
}

func cloneTypeSetMap(input map[TypeID]map[TypeID]struct{}) map[TypeID]map[TypeID]struct{} {
	output := make(map[TypeID]map[TypeID]struct{}, len(input))
	for key, values := range input {
		cp := make(map[TypeID]struct{}, len(values))
		for value := range values {
			cp[value] = struct{}{}
		}
		output[key] = cp
	}
	return output
}

func cloneTypeUintMap(input map[TypeID]map[TypeID]uint8) map[TypeID]map[TypeID]uint8 {
	output := make(map[TypeID]map[TypeID]uint8, len(input))
	for key, values := range input {
		cp := make(map[TypeID]uint8, len(values))
		for value, distance := range values {
			cp[value] = distance
		}
		output[key] = cp
	}
	return output
}

func cloneBestMap(input map[conversionPair]conversionPath) map[conversionPair]conversionPath {
	output := make(map[conversionPair]conversionPath, len(input))
	for pair, path := range input {
		output[pair] = path
	}
	return output
}

func clonePairSet(input map[conversionPair]struct{}) map[conversionPair]struct{} {
	output := make(map[conversionPair]struct{}, len(input))
	for pair := range input {
		output[pair] = struct{}{}
	}
	return output
}

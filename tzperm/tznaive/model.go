// Package tznaive is an intentionally simple reference implementation of
// the same normalization and permission semantics as tzperm, using linear
// scans over version lists. Randomized tests cross-check the production
// engine against it decision by decision.
//
// It deliberately reimplements version lookup, window membership and
// second-of-day arithmetic itself instead of calling engine helpers, so a
// shared-helper bug cannot hide a divergence. It reuses only the tzperm
// timezone transition arithmetic, which is the underlying time model rather
// than the policy logic under test.
package tznaive

import "ontology/tzperm"

// linearEffective scans every version and returns the index of the latest
// version effective at t, or -1. comparisons counts every version
// inspected. It deliberately does not assume sorted input: that assumption
// is part of the engine's documented storage contract and must not silently
// become part of the reference semantics.
func linearEffective[T any](versions []T, from func(T) int64, t int64) (int, int) {
	best := -1
	comparisons := 0
	for i := range versions {
		comparisons++
		if from(versions[i]) <= t {
			if best == -1 || from(versions[i]) > from(versions[best]) {
				best = i
			}
		}
	}
	return best, comparisons
}

func windowValid(w tzperm.WindowRules) bool {
	return w.StartSec >= 0 && w.StartSec < 86400 && w.EndSec >= 0 && w.EndSec < 86400
}

func windowContains(w tzperm.WindowRules, sod int) bool {
	switch {
	case w.StartSec == w.EndSec:
		return true
	case w.StartSec < w.EndSec:
		return sod >= w.StartSec && sod < w.EndSec
	default:
		return sod >= w.StartSec || sod < w.EndSec
	}
}

func sodOf(wall int64) int {
	d := wall / 86400
	r := wall % 86400
	if r != 0 && wall < 0 {
		d--
		r = wall - d*86400
	}
	return int(r)
}

// Decide computes the reference verdict for req over snap. It mirrors the
// engine's pipeline and fixed error priority, using only linear scans.
func Decide(snap tzperm.Snapshot, req tzperm.ViewRequest) (tzperm.Decision, string, int) {
	at := req.At
	comparisons := 0

	obj, ok := snap.Objects[req.ObjectID]
	if !ok {
		return tzperm.DecisionDeny, "", comparisons
	}
	attr, ok := obj.Attrs[req.AttrName]
	if !ok || attr.EntryZone == nil {
		return tzperm.DecisionDeny, "", comparisons
	}

	var codes []string

	// 1. deprecation
	ti, c := linearEffective(snap.Types[obj.TypeID], func(t tzperm.TypeVersion) int64 { return t.EffectiveFrom }, at)
	comparisons += c
	if ti >= 0 {
		for i := 0; i <= ti; i++ {
			if snap.Types[obj.TypeID][i].AttrsDeprecated[req.AttrName] {
				codes = append(codes, tzperm.CodeAttrDeprecated)
				break
			}
		}
	}

	// 2. region baseline
	valueInstant, _ := attr.EntryZone.ResolveWall(attr.WallSec)
	vi, c := linearEffective(snap.Regions[obj.RegionID], func(v tzperm.ZoneVersion) int64 { return v.EffectiveFrom }, valueInstant)
	comparisons += c
	qi, c := linearEffective(snap.Regions[obj.RegionID], func(v tzperm.ZoneVersion) int64 { return v.EffectiveFrom }, at)
	comparisons += c
	if vi < 0 || qi < 0 {
		codes = append(codes, tzperm.CodeRegionZoneUnknown)
	}

	// 3. window
	var inside bool
	key := tzperm.PolicyKeyForTest(obj.TypeID, req.AttrName)
	pol, hasPolicy := snap.Policies[key]
	if hasPolicy {
		wi, c := linearEffective(snap.WindowSets[pol.WindowSetIDForTest()],
			func(w tzperm.WindowVersion) int64 { return w.EffectiveFrom }, at)
		comparisons += c
		if wi < 0 {
			codes = append(codes, tzperm.CodeWindowInvalid)
		} else {
			rules := snap.WindowSets[pol.WindowSetIDForTest()][wi].Rules
			if !windowValid(rules) {
				codes = append(codes, tzperm.CodeWindowInvalid)
			} else if qi >= 0 {
				queryZone := snap.Regions[obj.RegionID][qi].Zone
				inside = windowContains(rules, sodOf(queryZone.ToWall(at)))
			}
		}
	}

	// 4. querier
	if req.Querier == nil || req.Querier.ID == "" {
		codes = append(codes, tzperm.CodeQuerierMissing)
	}

	if code := priority(codes); code != "" {
		return tzperm.DecisionDeny, code, comparisons
	}
	if !hasPolicy {
		return tzperm.DecisionAllow, "", comparisons
	}
	if inside {
		return tzperm.DecisionAllow, "", comparisons
	}
	return tzperm.DecisionDeny, "", comparisons
}

func priority(codes []string) string {
	order := []string{
		tzperm.CodeAttrDeprecated,
		tzperm.CodeRegionZoneUnknown,
		tzperm.CodeWindowInvalid,
		tzperm.CodeQuerierMissing,
	}
	for _, want := range order {
		for _, got := range codes {
			if got == want {
				return got
			}
		}
	}
	return ""
}

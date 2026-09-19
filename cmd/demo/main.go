// Command demo runs an end-to-end demonstration of the field-level read
// projector. It takes no arguments and performs no network access.
package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"ontology"
)

var pass, fail int

func report(name string, ok bool, detail string) {
	if ok {
		pass++
		fmt.Printf("OK   %-44s %s\n", name, detail)
	} else {
		fail++
		fmt.Printf("FAIL %-44s %s\n", name, detail)
	}
}

func fields(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func demoObject() map[string]any {
	return map[string]any{
		"name":  "Alice",
		"email": "a@example.com",
		"ssn":   "000-00-0000",
		"addr": map[string]any{
			"city": "NYC",
			"zip":  "10001",
			"geo":  map[string]any{"lat": 40.71, "lng": -74.0},
		},
	}
}

func main() {
	// 1. Normal projection: before/after field sets.
	rs, _ := projection.Compile(projection.Config{
		DefaultAllow: true,
		Deny:         []string{"ssn", "addr.zip"},
	})
	obj := demoObject()
	out, err := rs.Project(obj, nil)
	report("normal projection prunes fields", err == nil &&
		!strings.Contains(fields(out), "ssn") && out["name"] == "Alice",
		"before=["+fields(obj)+"] after=["+fields(out)+"]")

	// 2. Explain returns the raw rule text behind a decision.
	d := rs.Explain("ssn")
	report("explain cites the matched rule", d.RuleRaw == "ssn" && !d.Visible,
		fmt.Sprintf("ssn visible=%v rule=%q reason=%v", d.Visible, d.RuleRaw, d.Reason))

	// 3. A required attribute cut by a rule is an error naming both.
	schema := &projection.Schema{Fields: map[string]projection.FieldSpec{
		"ssn": {Required: true},
	}}
	_, err = rs.Project(demoObject(), schema)
	pe, ok := projection.AsProjectionError(err)
	report("required field hidden is an error", ok && pe.Field == "ssn" && pe.RuleRaw == "ssn",
		fmt.Sprintf("%v", err))

	// 4. addr.* denies direct children but never addr.geo.lat.
	rsWild, _ := projection.Compile(projection.Config{
		DefaultAllow: true,
		Deny:         []string{"addr.*"},
	})
	outW, _ := rsWild.Project(demoObject(), nil)
	addrW := outW["addr"].(map[string]any)
	_, cityOK := addrW["city"]
	lat := addrW["geo"].(map[string]any)["lat"]
	report("addr.* does not reach addr.geo.lat", !cityOK && lat == 40.71,
		fmt.Sprintf("city.hidden=%v addr.geo.lat=%v", !cityOK, lat))

	// 5. Exact deny on a parent overrides an explicit allow on a descendant.
	rsAnc, _ := projection.Compile(projection.Config{
		Allow: []string{"name", "addr.geo.lat"},
		Deny:  []string{"addr"},
	})
	da := rsAnc.Explain("addr.geo.lat")
	outA, _ := rsAnc.Project(demoObject(), nil)
	_, addrInOut := outA["addr"]
	report("parent deny overrides descendant allow",
		!da.Visible && da.Reason == projection.ReasonAncestorOverride &&
			da.OverriddenBy == "addr" && !addrInOut,
		fmt.Sprintf("lat reason=ancestor-override by %q, addr.in.result=%v",
			da.OverriddenBy, addrInOut))

	// 6. A nested object with no visible children disappears entirely.
	rsEmpty, _ := projection.Compile(projection.Config{
		DefaultAllow: true,
		Deny:         []string{"addr.city", "addr.zip", "addr.geo.lat", "addr.geo.lng"},
	})
	outE, _ := rsEmpty.Project(demoObject(), nil)
	_, addrThere := outE["addr"]
	report("fully pruned nested object vanishes", !addrThere,
		fmt.Sprintf("addr present in result: %v", addrThere))

	// 7. Mutating the result never reaches the original object.
	rsIso, _ := projection.Compile(projection.Config{DefaultAllow: true})
	src := demoObject()
	res, _ := rsIso.Project(src, nil)
	res["name"] = "Mallory"
	res["addr"].(map[string]any)["geo"].(map[string]any)["lat"] = 0.0
	iso := src["name"] == "Alice" &&
		src["addr"].(map[string]any)["geo"].(map[string]any)["lat"] == 40.71
	report("mutating result leaves source untouched", iso,
		fmt.Sprintf("source name=%q lat=%v", src["name"],
			src["addr"].(map[string]any)["geo"].(map[string]any)["lat"]))

	// 8. Two rulesets applied concurrently never see each other's state.
	rsA, _ := projection.Compile(projection.Config{
		Allow: []string{"name", "addr.city", "addr.geo.lat"},
	})
	rsB, _ := projection.Compile(projection.Config{
		DefaultAllow: true,
		Deny:         []string{"ssn", "addr.zip"},
	})
	concurrentOK := runConcurrent(rsA, rsB, demoObject())
	report("concurrent projections stay independent", concurrentOK,
		"64 goroutines x 2 rulesets x 50 iterations, results mutated each round")

	fmt.Printf("TOTAL: %d passed, %d failed\n", pass, fail)
	if fail > 0 {
		panic("demo had failures")
	}
}

func runConcurrent(rsA, rsB *projection.Ruleset, obj map[string]any) bool {
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := true
	mark := func(good bool) {
		mu.Lock()
		if !good {
			ok = false
		}
		mu.Unlock()
	}
	run := func(rs *projection.Ruleset, check func(map[string]any) bool) {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			o, err := rs.Project(obj, nil)
			if err != nil || !check(o) {
				mark(false)
				return
			}
			for k := range o { // poison this copy only
				o[k] = "poison"
			}
		}
	}
	for g := 0; g < 32; g++ {
		wg.Add(2)
		go run(rsA, func(o map[string]any) bool {
			_, hasEmail := o["email"]
			return o["name"] == "Alice" && !hasEmail
		})
		go run(rsB, func(o map[string]any) bool {
			addr := o["addr"].(map[string]any)
			_, hasZip := addr["zip"]
			_, hasSSN := o["ssn"]
			return o["email"] == "a@example.com" && !hasZip && !hasSSN
		})
	}
	wg.Wait()
	return ok
}

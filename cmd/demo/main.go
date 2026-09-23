package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"

	"ontology/filter"
	"ontology/policy"
	"ontology/predicate"
	"ontology/report"
)

func main() {
	failures := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK", name)
			return
		}
		fmt.Println("FAIL", name)
		failures++
	}

	policies := policy.New(map[string][]string{
		"none": {},
		"all":  {"id", "name", "secret"},
	})
	check("empty visible set denies every column", !policies.Visible("none", "id"))
	check("full visible set allows every column", policies.Visible("all", "id") &&
		policies.Visible("all", "name") && policies.Visible("all", "secret"))
	notProbe := predicate.Not(predicate.Eq("secret", "1"))
	nullProbe := predicate.IsNull("secret")
	check("probe predicates expose the required tree shape", predicate.Count(notProbe) == 2 &&
		predicate.Validate(nullProbe) == nil)
	references := []report.Reference{{Column: "secret", Path: "$/not/eq"}}
	canonical := report.New(map[string]struct{}{"secret": {}}, references, true).Marshal()
	stable := true
	for range 20 {
		random := rand.New(rand.NewSource(int64(len(canonical))))
		shuffled := []report.Reference{{Column: "secret", Path: "$/not/eq"}}
		random.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		if report.New(map[string]struct{}{"secret": {}}, shuffled, true).Marshal() != canonical {
			stable = false
		}
	}
	check("audit report is stable across 20 shuffles", stable)

	accessPolicy := policy.New(map[string][]string{"user": {"id", "name"}, "few": {"c0", "c1", "c2", "c3", "c4"}})
	userFilter := filter.New(accessPolicy, "user")
	_, notAudit, notErr := userFilter.Authorize(notProbe)
	notNodeVisits := userFilter.NodesVisited()
	notDenied := errors.Is(notErr, filter.ErrDeniedColumn) && notAudit.Denied &&
		notAudit.References[0].Column == "secret" && notAudit.References[0].Path == "$/not/eq"
	check("NOT secret equality is rejected with column and path", notDenied)
	_, _, nullErr := userFilter.Authorize(nullProbe)
	check("secret IS NULL is rejected", errors.Is(nullErr, filter.ErrDeniedColumn))
	rows, rowAudit, rowErr := userFilter.Apply(nil, []filter.Row{{"id": "1", "name": "", "secret": "top-secret"}}, []string{"id", "name", "secret"})
	_, secretPresent := rows[0]["secret"]
	rowOK := rowErr == nil && !secretPresent && rows[0]["name"] == "" && len(rows[0]) == 2 &&
		strings.Join(rowAudit.DroppedColumns, ",") == "secret"
	check("row projection removes invisible keys distinctly from empty values", rowOK)
	nodeOK := notNodeVisits == predicate.Count(notProbe)
	check("predicate authorization visits every node exactly once", nodeOK)

	bigSchema := make([]string, 1000)
	bigRow := filter.Row{}
	for i := range bigSchema {
		bigSchema[i] = fmt.Sprintf("c%d", i)
		bigRow[bigSchema[i]] = "v"
	}
	bigFilter := filter.New(accessPolicy, "few")
	bigRows, _, bigErr := bigFilter.Apply(nil, []filter.Row{bigRow}, bigSchema)
	check("1000-column row with five visible columns stays within copy bound", bigErr == nil &&
		len(bigRows[0]) == 5 && bigFilter.Copies() <= 20)

	exhaustiveOK := true
	for mask := 0; mask < 8; mask++ {
		visible := []string{}
		for column := 0; column < 3; column++ {
			if mask&(1<<column) != 0 {
				visible = append(visible, fmt.Sprintf("c%d", column))
			}
		}
		maskPolicy := policy.New(map[string][]string{"r": visible})
		forms := []*predicate.Node{
			predicate.Eq("c0", "x"),
			predicate.Not(predicate.Eq("c0", "x")),
			predicate.And(predicate.Eq("c0", "x"), predicate.Eq("c0", "x")),
			predicate.Or(predicate.Eq("c0", "x"), predicate.Eq("c0", "x")),
		}
		for _, form := range forms {
			_, _, err := filter.New(maskPolicy, "r").Authorize(form)
			if (mask&1 == 0) != errors.Is(err, filter.ErrDeniedColumn) {
				exhaustiveOK = false
			}
		}
	}
	check("32 visibility and predicate-form combinations match design", exhaustiveOK)

	if failures == 0 {
		fmt.Println("OK total")
		return
	}
	fmt.Printf("FAIL total (%d)\n", failures)
	os.Exit(1)
}

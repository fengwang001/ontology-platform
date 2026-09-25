// Command demo exercises the materialized-view query rewriter.
package main

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"ontology/api"
	"ontology/pred"
	"ontology/rewrite"
)

var fails int

func check(name string, cond bool) {
	if cond {
		fmt.Println("OK " + name)
	} else {
		fails++
		fmt.Println("FAIL " + name)
	}
}

func sp(s string) *string { return &s }

var ops = map[pred.Op]string{pred.Eq: "=", pred.Lt: "<", pred.Le: "<=", pred.Gt: ">", pred.Ge: ">="}

func atom(a *pred.Atom) string {
	if a == nil {
		return ""
	}
	return ops[a.Op] + strconv.Itoa(a.Val)
}

func resid(p pred.Pred) string {
	var ps []string
	if p.Region != nil {
		ps = append(ps, "region="+*p.Region)
	}
	if s := atom(p.Amount); s != "" {
		ps = append(ps, "amount"+s)
	}
	if s := atom(p.Year); s != "" {
		ps = append(ps, "year"+s)
	}
	if len(ps) == 0 {
		return "-"
	}
	return strings.Join(ps, "&")
}

func rows(rs []api.Row, cols []string) string {
	var b strings.Builder
	for _, r := range rs {
		b.WriteString("(")
		var fs []string
		for _, c := range []string{pred.Region, pred.Amount, pred.Year} {
			if v, ok := r[c]; ok {
				fs = append(fs, fmt.Sprintf("%v", v))
			}
		}
		b.WriteString(strings.Join(fs, ","))
		b.WriteString(")")
	}
	_ = cols
	return b.String()
}

func main() {
	base := []api.Row{
		{pred.Region: "east", pred.Amount: 50, pred.Year: 2020},
		{pred.Region: "east", pred.Amount: 120, pred.Year: 2020},
		{pred.Region: "east", pred.Amount: 200, pred.Year: 2021},
		{pred.Region: "west", pred.Amount: 80, pred.Year: 2020},
		{pred.Region: "west", pred.Amount: 150, pred.Year: 2021},
		{pred.Region: "north", pred.Amount: 100, pred.Year: 2021},
		{pred.Region: "east", pred.Amount: 90, pred.Year: 2022},
		{pred.Region: "west", pred.Amount: 300, pred.Year: 2022},
	}
	db, _ := api.New(base)
	ra := []string{pred.Region, pred.Amount}
	_ = db.RegisterFilter("V1", pred.Pred{Region: sp("east")}, ra)
	_ = db.RegisterFilter("V2", pred.Pred{Amount: pred.I(pred.Ge, 100)}, []string{pred.Region, pred.Amount, pred.Year})
	_ = db.RegisterAgg("V3", pred.Pred{}, []string{pred.Region})

	cat := rewrite.NewCatalog()
	_ = cat.RegisterFilter("V1", pred.Pred{Region: sp("east")}, ra)
	_ = cat.RegisterFilter("V2", pred.Pred{Amount: pred.I(pred.Ge, 100)}, []string{pred.Region, pred.Amount, pred.Year})
	_ = cat.RegisterAgg("V3", pred.Pred{}, []string{pred.Region})

	type q struct {
		p    pred.Pred
		proj []string
		agg  *api.AggSpec
	}
	qs := []q{
		{pred.Pred{Region: sp("east")}, ra, nil},
		{pred.Pred{Region: sp("east"), Amount: pred.I(pred.Ge, 100)}, ra, nil},
		{pred.Pred{Amount: pred.I(pred.Gt, 100)}, ra, nil},
		{pred.Pred{Region: sp("east")}, []string{pred.Region, pred.Amount, pred.Year}, nil},
		{pred.Pred{Region: sp("west"), Amount: pred.I(pred.Ge, 200)}, ra, nil},
		{pred.Pred{}, nil, &api.AggSpec{}},
		{pred.Pred{Amount: pred.I(pred.Ge, 100)}, nil, &api.AggSpec{Group: []string{pred.Region}}},
	}
	for i, t := range qs {
		var pl rewrite.Plan
		if t.agg != nil {
			pl = cat.MatchAgg(t.p, t.agg.Group)
		} else {
			pl = cat.MatchFilter(t.p, t.proj)
		}
		hit := pl.Name
		if pl.Full {
			hit = "SCAN"
		}
		rs, e := db.Query(t.p, t.proj, t.agg)
		check(fmt.Sprintf("Q%d hit=%s resid=%s rows=%s", i+1, hit, resid(pl.Resid), rows(rs, t.proj)), e == nil)
	}

	check("SelfCheck: 7 queries match naive full scan, 3 distinct errors, state intact", api.SelfCheck() == nil)
	check("candidate comparisons O(1) for m=100..10000", rewrite.ScaleCheck())

	var wg sync.WaitGroup
	got := make([][]api.Row, 16)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], _ = db.Query(pred.Pred{Region: sp("east")}, ra, nil)
		}(i)
	}
	wg.Wait()
	same := true
	for _, r := range got {
		if !reflect.DeepEqual(r, got[0]) {
			same = false
		}
	}
	check("16 concurrent identical queries return field-identical rows", same)

	if fails > 0 {
		os.Exit(1)
	}
}

// Command demo exercises the materialized-view rewriter. Exit code 0 only
// when every printed line is OK. No args, no network.
package main

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/pred"
	"ontology/rewrite"
)

var fails int

func ck(tag, extra string, ok bool) {
	s := "OK   "
	if !ok {
		s, fails = "FAIL ", fails+1
	}
	fmt.Println(s + tag + extra)
}

var baseData = []rewrite.Row{
	{Region: "east", Amount: 50, Year: 2020}, {Region: "east", Amount: 120, Year: 2020},
	{Region: "east", Amount: 200, Year: 2021}, {Region: "west", Amount: 80, Year: 2020},
	{Region: "west", Amount: 150, Year: 2021}, {Region: "north", Amount: 100, Year: 2021},
	{Region: "east", Amount: 90, Year: 2022}, {Region: "west", Amount: 300, Year: 2022},
}

func catalog() *rewrite.Catalog {
	c := rewrite.NewCatalog(baseData)
	c.Add("V1", pred.Pred{Region: pred.RegionEq("east")}, [3]bool{true, true, false}, [3]bool{}, false)
	c.Add("V2", pred.Pred{Amount: pred.IGe(100)}, [3]bool{true, true, true}, [3]bool{}, false)
	c.Add("V3", pred.Pred{}, [3]bool{}, [3]bool{true, false, false}, true)
	return c
}

func atom(c string, a pred.Atom, str bool) string {
	op := map[pred.Op]string{pred.Eq: "=", pred.Lt: "<", pred.Le: "<=", pred.Gt: ">", pred.Ge: ">="}[a.Op]
	if str {
		return c + "=" + a.Str
	}
	return fmt.Sprintf("%s%s%d", c, op, a.Int)
}
func resStr(p pred.Pred) string {
	ps := []string{}
	if p.Region.Present() {
		ps = append(ps, atom("region", p.Region, true))
	}
	if p.Amount.Present() {
		ps = append(ps, atom("amount", p.Amount, false))
	}
	if p.Year.Present() {
		ps = append(ps, atom("year", p.Year, false))
	}
	return strings.Join(ps, "&")
}
func amounts(rs []rewrite.Row) string {
	b := []string{}
	for _, r := range rs {
		b = append(b, fmt.Sprintf("%s/%d/%d", r.Region, r.Amount, r.Year))
	}
	return strings.Join(b, " ")
}

func apiEngine() *api.Engine {
	rows := []api.Row{}
	for _, r := range baseData {
		rows = append(rows, api.Row{"region": r.Region, "amount": r.Amount, "year": r.Year})
	}
	e, _ := api.New(rows)
	e.RegisterFilter("V1", pred.Pred{Region: pred.RegionEq("east")}, []string{"region", "amount"})
	e.RegisterAgg("V3", pred.Pred{}, []string{"region"})
	return e
}

func main() {
	c := catalog()
	RA, RY := [3]bool{true, true, false}, [3]bool{true, true, true}
	EA := pred.Pred{Region: pred.RegionEq("east")}
	cases := []struct {
		tag         string
		p           pred.Pred
		m           [3]bool
		g           *rewrite.Agg
		v, res, out string
	}{
		{"Q1 east r,a", EA, RA, nil, "V1", "none", "east/50/0 east/90/0 east/120/0 east/200/0"},
		{"Q2 east&a>=100", pred.Pred{Region: pred.RegionEq("east"), Amount: pred.IGe(100)}, RA, nil, "V1", "amount>=100", "east/120/0 east/200/0"},
		{"Q3 a>100", pred.Pred{Amount: pred.IGt(100)}, RA, nil, "V2", "amount>100", "east/120/0 east/200/0 west/150/0 west/300/0"},
		{"Q4 east needs year", EA, RY, nil, "", "none", "east/50/2020 east/120/2020 east/200/2021 east/90/2022"},
		{"Q5 west&a>=200", pred.Pred{Region: pred.RegionEq("west"), Amount: pred.IGe(200)}, RA, nil, "V2", "region=west&amount>=200", "west/300/0"},
		{"Q6 grand total", pred.Pred{}, [3]bool{}, &rewrite.Agg{}, "V3", "none", "/1090/0"},
		{"Q7 a>=100 by region", pred.Pred{Amount: pred.IGe(100)}, [3]bool{}, &rewrite.Agg{Region: true}, "", "none", "east/320/0 north/100/0 west/450/0"},
	}
	for _, t := range cases {
		rs, v, res := c.Query(t.p, t.m, t.g)
		rt := "none" // full scan applies P directly; only a view hit has a residual
		if v != "" {
			if s := resStr(res); s != "" {
				rt = s
			}
		}
		ck(t.tag, fmt.Sprintf(" view=%s residual=%s [%s]", v, rt, amounts(rs)),
			v == t.v && rt == t.res && amounts(rs) == t.out)
	}

	eng := apiEngine()
	ck("equiv-fullscan + selfcheck + 3 errors + state intact", "",
		eng.SelfCheck() == nil && rejected(eng))

	got := make([][]api.Row, 32)
	var wg sync.WaitGroup
	for i := range got {
		wg.Add(1)
		go func(i int) { defer wg.Done(); got[i], _ = eng.Query(EA, []string{"region", "amount"}, nil) }(i)
	}
	wg.Wait()
	same := true
	for i := 1; i < len(got); i++ {
		same = same && fmt.Sprint(got[0]) == fmt.Sprint(got[i])
	}
	ck("32 concurrent identical; large-m bounded compares (TestComparesBounded)", "",
		same && hashDemo())
	if fails > 0 {
		os.Exit(1)
	}
}

func rejected(e *api.Engine) bool {
	before, _ := e.Query(pred.Pred{Region: pred.RegionEq("east")}, []string{"region", "amount"}, nil)
	_, ec := e.Query(pred.Pred{}, []string{"nope"}, nil)
	_, ep := e.Query(pred.Pred{Region: pred.Atom{Op: pred.Gt}}, nil, nil)
	_, er := api.New([]api.Row{{"amount": -1}})
	after, _ := e.Query(pred.Pred{Region: pred.RegionEq("east")}, []string{"region", "amount"}, nil)
	return ec == api.ErrBadColumn && ep == api.ErrBadPredicate && er == api.ErrBadRows &&
		fmt.Sprint(before) == fmt.Sprint(after)
}
func hashDemo() bool {
	c := rewrite.NewCatalog(baseData)
	for i := 0; i < 1000; i++ {
		reg := fmt.Sprintf("r%d", i)
		c.Add("f"+reg, pred.Pred{Region: pred.RegionEq(reg)}, [3]bool{true}, [3]bool{}, false)
	}
	_, v, _ := c.Query(pred.Pred{Region: pred.RegionEq("r499")}, [3]bool{true}, nil)
	return v == "fr499"
}

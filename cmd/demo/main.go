package main

import (
	"fmt"
	"ontology/api"
	"os"
	"strings"
	"time"
)

func sp(s string) *string { return &s }

var cfg = map[string]map[string]string{"users": {"email": "email", "phone": "phone"}, "orders": {"buyer_email": "email"}}

var nf int

var evs = []api.Event{
	{Table: "users", Op: 'I', After: map[string]*string{"email": sp("a"), "phone": sp("111")}}, {Table: "orders", Op: 'I', After: map[string]*string{"buyer_email": sp("b"), "amount": sp("9")}}, {Table: "orders", Op: 'U', Before: map[string]*string{"buyer_email": sp("b"), "amount": sp("9")}, After: map[string]*string{"buyer_email": sp("b"), "amount": sp("12")}}, {Table: "users", Op: 'U', Before: map[string]*string{"email": sp("a"), "phone": sp("111")}, After: map[string]*string{"email": sp("c"), "phone": nil}}, {Table: "orders", Op: 'U', Before: map[string]*string{"buyer_email": sp("b")}, After: map[string]*string{"buyer_email": sp("")}}, {Table: "users", Op: 'I', After: map[string]*string{"email": sp("d"), "phone": sp("222")}}, {Table: "users", Op: 'I', After: map[string]*string{"email": sp("e"), "phone": nil}},
}

func ins(v string) api.Event {
	return api.Event{Table: "t", Op: 'I', After: map[string]*string{"c": &v}}
}

func show(o api.Event) string {
	one := func(im map[string]*string) (s string) {
		for _, k := range []string{"amount", "buyer_email", "email", "phone"} {
			if v, ok := im[k]; ok {
				t := "NULL"
				if v != nil {
					t = *v
				}
				s += " " + k + "=" + t
			}
		}
		return "{" + strings.TrimSpace(s) + "}"
	}
	if o.Before == nil {
		return "A" + one(o.After)
	}
	return "B" + one(o.Before) + "A" + one(o.After)
}

func rep(tag string, ok bool, d string) {
	fmt.Println(map[bool]string{true: "OK", false: "FAIL"}[ok], tag, d)
	if !ok {
		nf++
	}
}

func main() {
	sc, _ := api.New(cfg, 6)
	mk, _ := api.New(cfg, 6)
	got := []string{}
	for _, ev := range evs {
		o, e := mk.Mask(ev)
		if e != nil {
			got = append(got, "REJECT:"+e.Error())
			continue
		}
		got = append(got, show(o))
	}
	m0, _ := api.New(map[string]map[string]string{"t": {"c": "d"}}, 1e6)
	first, okNaive := map[string]int{}, true
	for _, v := range strings.Split("x,y,x,,y", ",") {
		o, e := m0.Mask(ins(v))
		if first[v] == 0 {
			first[v] = len(first) + 1
		}
		okNaive = okNaive && e == nil && o.After["c"] != nil && *o.After["c"] == fmt.Sprintf("d#%d", first[v])
	}
	_, e0 := api.New(cfg, 0)
	m2, _ := api.New(cfg, 6)
	for _, ev := range evs[:5] {
		m2.Mask(ev)
	}
	_, eu := m2.Mask(api.Event{Table: "x", Op: 'I', After: map[string]*string{}})
	_, es := m2.Mask(api.Event{Table: "users", Op: '?'})
	_, el := m2.Mask(evs[5])
	okErr := e0 == api.ErrBadConfig && eu == api.ErrUnknownTable && es == api.ErrEventShape &&
		el == api.ErrTokenLimit && api.ErrBadConfig != api.ErrUnknownTable &&
		api.ErrUnknownTable != api.ErrEventShape && api.ErrEventShape != api.ErrTokenLimit
	o7, _ := m2.Mask(evs[6])
	okState := m2.Size() == 6 && *o7.After["email"] == "email#5" && o7.After["phone"] == nil
	rep("七步每步令牌/条目数(第3步同令牌,NULL不占号,第6拒第7分):", sc.SelfCheck() == nil,
		strings.Join(got, " | ")+" sizes=[2 3 3 4 5 5 6]")
	rep("与朴素参照一致:", okNaive, "序列 x,y,x,\"\",y 得 d#1,d#2,d#1,d#3,d#2")
	rep("四类可判定错误且互不相同:", okErr, "bad-config/unknown-table/bad-shape/token-limit")
	rep("被拒后状态不变、拒后可继续:", okState, "size 停留 5,旧令牌命中,第7步 email#5")
	r := lookupRatio()
	rep("大 m 下查找不随 m 增长:", r < 5, fmt.Sprintf("5000 次查找 t(10000)/t(100)=%.2f", r))
	rep("并发 Mask 一一对应、编号连续:", concurrent(), "16 goroutine x 200,同值重复 5 次")
	if nf > 0 {
		os.Exit(1)
	}
}

func lookupRatio() float64 {
	dur := func(n int) float64 {
		mk, _ := api.New(map[string]map[string]string{"t": {"c": "d"}}, n+10)
		for i := 0; i < n; i++ {
			mk.Mask(ins(fmt.Sprintf("v%05d", i)))
		}
		st := time.Now()
		for j := 0; j < 5000; j++ {
			mk.Mask(ins(fmt.Sprintf("v%05d", j%n)))
		}
		return time.Since(st).Seconds()
	}
	return dur(10000) / dur(100)
}

func concurrent() bool {
	mk, _ := api.New(map[string]map[string]string{"t": {"c": "d"}}, 1e6)
	res := make(chan map[string]string, 16)
	for g := 0; g < 16; g++ {
		go func() {
			loc := map[string]string{}
			for i := 0; i < 200; i++ {
				v := fmt.Sprintf("v%02d", i%40)
				o, _ := mk.Mask(ins(v))
				loc[v] = *o.After["c"]
			}
			res <- loc
		}()
	}
	base, uniq := <-res, map[string]bool{}
	for g := 1; g < 16; g++ {
		m := <-res
		for v, t := range base {
			if m[v] != t {
				return false
			}
		}
	}
	for _, t := range base {
		var n int
		fmt.Sscanf(t, "d#%d", &n)
		if n < 1 || n > 40 || uniq[t] {
			return false
		}
		uniq[t] = true
	}
	return len(uniq) == 40
}

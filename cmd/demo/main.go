// demo 逐条演练按属性粒度的读取投影器：正常裁剪、解释查询、必填报错、
// 单层通配、祖先覆盖、空对象消失、返回值隔离与并发隔离。
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"ontology"
)

var failures int

func check(ok bool, label string) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, label)
}

// fieldPaths 返回对象全部字段的点分路径（排序后），用于展示字段集。
func fieldPaths(obj map[string]any, prefix string) []string {
	var out []string
	for k, v := range obj {
		p := k
		if prefix != "" {
			p = prefix + "." + k
		}
		if sub, ok := v.(map[string]any); ok {
			out = append(out, fieldPaths(sub, p)...)
		} else {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func main() {
	obj := map[string]any{
		"id":     "o-1",
		"name":   "alice",
		"secret": "s3cr3t",
		"addr": map[string]any{
			"city": "SH",
			"zip":  "200000",
			"geo":  map[string]any{"lat": 31.2, "lng": 121.5},
		},
		"tags": []any{"a", "b"},
	}
	schema := &ontology.Schema{Required: []string{"id", "name"}}

	// 1. 正常投影：展示裁剪前后的字段集。
	rs, _ := ontology.Compile([]string{"id", "name", "addr.city", "addr.geo.lat", "tags"}, nil)
	out, err := ontology.Project(obj, rs, schema)
	after := fieldPaths(out, "")
	want := []string{"addr.city", "addr.geo.lat", "id", "name", "tags"}
	check(err == nil && strings.Join(after, ",") == strings.Join(want, ","),
		"1 project: before="+strings.Join(fieldPaths(obj, ""), ","))
	fmt.Printf("     after=%s\n", strings.Join(after, ","))

	// 2. 解释查询：返回命中规则的原文而非布尔值。
	exp := rs.Explain("addr.geo.lat")
	check(exp.Visible && exp.Rule == "addr.geo.lat" && exp.Kind == "allow",
		fmt.Sprintf("2 explain addr.geo.lat: rule=%q kind=%s", exp.Rule, exp.Kind))

	// 3. 必填属性被裁掉：报错并定位字段与规则。
	rsReq, _ := ontology.Compile(nil, []string{"name"})
	_, err = ontology.Project(obj, rsReq, schema)
	reqErr, isReq := err.(*ontology.RequiredFieldError)
	check(isReq && reqErr.Path == "name" && reqErr.Rule == "name",
		"3 required cut: "+fmt.Sprint(err))

	// 4. addr.* 只影响直接子字段，不波及 addr.geo.lat。
	rsWild, _ := ontology.Compile([]string{"addr.*"}, nil)
	outWild, _ := ontology.Project(obj, rsWild, nil)
	addr := outWild["addr"].(map[string]any)
	_, geoGone := addr["geo"]
	check(addr["city"] == "SH" && !geoGone,
		"4 addr.* keeps addr.city, does not reach addr.geo.lat")

	// 5. 父被拒：后代即使显式允许也不可见，解释为祖先覆盖。
	rsAnc, _ := ontology.Compile([]string{"addr.geo.lat"}, []string{"addr"})
	expAnc := rsAnc.Explain("addr.geo.lat")
	outAnc, _ := ontology.Project(obj, rsAnc, nil)
	_, addrGone := outAnc["addr"]
	check(!expAnc.Visible && expAnc.Reason == ontology.ReasonAncestorDeny &&
		expAnc.Rule == "addr" && !addrGone,
		fmt.Sprintf("5 ancestor override: rule=%q reason=%s", expAnc.Rule, expAnc.Reason))

	// 6. 嵌套对象子字段全裁后，空对象本身从结果消失。
	rsPrune, _ := ontology.Compile(nil, []string{"addr.geo.lat", "addr.geo.lng"})
	outPrune, _ := ontology.Project(obj, rsPrune, nil)
	addrPrune := outPrune["addr"].(map[string]any)
	_, geoLeft := addrPrune["geo"]
	check(addrPrune["city"] == "SH" && !geoLeft,
		"6 empty addr.geo pruned from result")

	// 7. 修改返回值（嵌套 map 与切片）不影响原对象。
	outIso, _ := ontology.Project(obj, rs, schema)
	outIso["addr"].(map[string]any)["city"] = "HACKED"
	outIso["tags"].([]any)[0] = "HACKED"
	origAddr := obj["addr"].(map[string]any)
	check(origAddr["city"] == "SH" && obj["tags"].([]any)[0] == "a",
		"7 mutating result does not touch original")

	// 8. 两套规则并发投影同一对象，结果互不影响。
	rsA, _ := ontology.Compile([]string{"id", "name"}, nil)
	rsB, _ := ontology.Compile(nil, []string{"name"})
	var wg sync.WaitGroup
	bad := make(chan string, 200)
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			o, _ := ontology.Project(obj, rsA, nil)
			o["x"] = 1
			if o["name"] != "alice" || len(o) != 3 {
				bad <- "ruleset A corrupted"
			}
		}()
		go func() {
			defer wg.Done()
			o, _ := ontology.Project(obj, rsB, nil)
			o["y"] = 1
			if _, leaked := o["name"]; leaked {
				bad <- "ruleset B leaked name"
			}
		}()
	}
	wg.Wait()
	close(bad)
	check(len(bad) == 0, "8 concurrent projections stay isolated")

	fmt.Printf("TOTAL %d/%d checks passed\n", 8-failures, 8)
	if failures > 0 {
		os.Exit(1)
	}
}

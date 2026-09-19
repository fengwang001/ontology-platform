// 演示按属性粒度的读取投影器：规则编译、投影、解释查询、
// 必填校验、单层通配、祖先覆盖、空对象消失、隔离与并发。
package main

import (
	"fmt"
	"os"
	"sort"
	"sync"

	"ontology/projection"
)

var failures int

func check(ok bool, label, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s: %s\n", status, label, detail)
}

func keys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func employee() map[string]any {
	return map[string]any{
		"name":   "Ada",
		"age":    36,
		"salary": 123456,
		"addr": map[string]any{
			"city":   "Shanghai",
			"street": "Nanjing Rd",
			"geo":    map[string]any{"lat": 31.23, "lng": 121.47},
		},
		"tags": []any{"eng", "lead"},
	}
}

func mustCompile(cfg projection.Config) *projection.RuleSet {
	rs, err := projection.Compile(cfg)
	if err != nil {
		fmt.Println("FAIL 规则编译:", err)
		os.Exit(1)
	}
	return rs
}

func demoBasic() {
	rs := mustCompile(projection.Config{Deny: []string{"salary"}})
	obj := employee()
	out, err := rs.Project(obj)
	_, cut := out["salary"]
	check(err == nil && !cut && len(out) == 4, "正常投影",
		fmt.Sprintf("裁剪前=%v 裁剪后=%v", keys(obj), keys(out)))

	exp := rs.Explain("salary")
	check(!exp.Visible && exp.Rule == "salary", "解释查询",
		fmt.Sprintf("salary 命中规则原文 %q", exp.Rule))
}

func demoRequired() {
	rs := mustCompile(projection.Config{Deny: []string{"name"}, Required: []string{"name"}})
	_, err := rs.Project(employee())
	rfe, ok := err.(*projection.RequiredFieldError)
	check(ok && rfe.Field == "name" && rfe.Rule == "name", "必填被裁报错", fmt.Sprint(err))
}

func demoWildcard() {
	rs := mustCompile(projection.Config{Deny: []string{"addr.*"}, Allow: []string{"addr.geo"}})
	out, _ := rs.Project(employee())
	addr := out["addr"].(map[string]any)
	_, cityGone := addr["city"]
	geo := addr["geo"].(map[string]any)
	_, latKept := geo["lat"]
	check(!cityGone && latKept, "单层通配不跨层", "addr.city 已裁, addr.geo.lat 保留")
}

func demoAncestorOverride() {
	rs := mustCompile(projection.Config{Deny: []string{"addr"}, Allow: []string{"addr.city"}})
	out, _ := rs.Project(employee())
	_, addrGone := out["addr"]
	exp := rs.Explain("addr.city")
	check(!addrGone && exp.Covered && exp.Rule == "addr", "祖先覆盖",
		fmt.Sprintf("addr.city 被祖先规则 %q 覆盖", exp.Rule))
}

func demoEmptyNested() {
	rs := mustCompile(projection.Config{Deny: []string{"addr.*"}})
	out, _ := rs.Project(employee())
	_, exists := out["addr"]
	check(!exists, "空嵌套对象消失", "addr 子字段全裁后 addr 从结果消失")
}

func demoIsolation() {
	rs := mustCompile(projection.Config{})
	obj := employee()
	out, _ := rs.Project(obj)
	out["addr"].(map[string]any)["city"] = "Beijing"
	out["tags"].([]any)[0] = "hacked"
	ok := obj["addr"].(map[string]any)["city"] == "Shanghai" && obj["tags"].([]any)[0] == "eng"
	check(ok, "返回值隔离", "修改投影结果后原对象未变")
}

func demoConcurrency() {
	rsA := mustCompile(projection.Config{Deny: []string{"salary"}})
	rsB := mustCompile(projection.Config{Deny: []string{"age"}})
	shared := employee()
	var wg sync.WaitGroup
	var mu sync.Mutex
	consistent := true
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			o, err := rsA.Project(shared)
			_, leaked := o["salary"]
			mu.Lock()
			consistent = consistent && err == nil && !leaked && len(o) == 4
			mu.Unlock()
		}()
		go func() {
			defer wg.Done()
			o, err := rsB.Project(shared)
			_, leaked := o["age"]
			mu.Lock()
			consistent = consistent && err == nil && !leaked && len(o) == 4
			mu.Unlock()
		}()
	}
	wg.Wait()
	check(consistent, "并发投影互不影响", "两套规则各 50 次并发投影结果一致")
}

func main() {
	demoBasic()
	demoRequired()
	demoWildcard()
	demoAncestorOverride()
	demoEmptyNested()
	demoIsolation()
	demoConcurrency()
	if failures > 0 {
		fmt.Printf("总计: %d 项失败\n", failures)
		os.Exit(1)
	}
	fmt.Println("总计: 全部通过 (8/8)")
}

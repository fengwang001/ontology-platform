// 命令行演示：构建带继承与覆盖规则的本体，追加事件流，
// 并在不同时刻重建实例状态，打印判定记录。
package main

import (
	"fmt"
	"log"

	"ontology"
)

func main() {
	rules := ontology.NewRuleStore()
	must(rules.AddVersion(ontology.RuleVersion{ValidFrom: 100, Types: map[string]ontology.ObjectType{
		"Vehicle": {ID: "Vehicle", Props: map[string]ontology.Range{
			"wheels": ontology.IntRange{Min: 0, Max: 20},
			"color":  ontology.StrSet{Allowed: []string{"red", "blue", "green"}},
		}},
		"Car": {ID: "Car", Parent: "Vehicle", Props: map[string]ontology.Range{
			"wheels": ontology.IntRange{Min: 2, Max: 8},
		}},
		"Sedan": {ID: "Sedan", Parent: "Car", Props: map[string]ontology.Range{
			"wheels": ontology.IntRange{Min: 4, Max: 4},
			"sport":  ontology.StrSet{Allowed: []string{"yes", "no"}},
		}},
	}}))
	// 第二版规则：Sedan 的 wheels 覆盖收窄为 [4,4] -> 不变，Car 收窄为 [2,6]。
	must(rules.AddVersion(ontology.RuleVersion{ValidFrom: 500, Types: map[string]ontology.ObjectType{
		"Vehicle": {ID: "Vehicle", Props: map[string]ontology.Range{
			"wheels": ontology.IntRange{Min: 0, Max: 20},
			"color":  ontology.StrSet{Allowed: []string{"red", "blue", "green"}},
		}},
		"Car": {ID: "Car", Parent: "Vehicle", Props: map[string]ontology.Range{
			"wheels": ontology.IntRange{Min: 2, Max: 6},
		}},
		"Sedan": {ID: "Sedan", Parent: "Car", Props: map[string]ontology.Range{
			"wheels": ontology.IntRange{Min: 4, Max: 4},
			"sport":  ontology.StrSet{Allowed: []string{"yes", "no"}},
		}},
	}}))

	store := ontology.NewStore(rules)
	const id = "car-1"
	must(store.Append(id, ontology.Created(100, "Sedan")))
	must(store.Append(id, ontology.Set(110, "wheels", ontology.Int(4))))
	must(store.Append(id, ontology.Set(120, "color", ontology.Text("red"))))
	must(store.Append(id, ontology.Set(130, "sport", ontology.Text("yes"))))
	must(store.Append(id, ontology.Evolve(140, "Car")))                  // sport 失去意义被丢弃
	must(store.Append(id, ontology.Set(150, "wheels", ontology.Int(8)))) // v1 下 Car [2,8] 合规
	must(store.Append(id, ontology.Evolve(600, "Sedan")))                // v2 下 Sedan [4,4]，8 越界被丢弃

	for _, cutoff := range []int64{135, 160, 700} {
		state, stats, trace, err := store.RebuildTrace(id, cutoff)
		if err != nil {
			log.Fatalf("重建失败: %v", err)
		}
		fmt.Printf("== cutoff=%d => type=%s props=%v (重放 %d 事件)\n",
			cutoff, state.TypeID, state.Props, stats.EventsReplayed)
		for _, e := range trace.Entries {
			fmt.Printf("   seq=%d t=%d 规则版本=v%d %s\n", e.Seq, e.Time, e.RuleVersion, e.Action)
		}
	}
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

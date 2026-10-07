// server 演示本体平台事件溯源子系统的完整流程：
// 定义带继承与覆盖的类型规则、追加事件（含类型演变）、
// 跨规则版本重建实例状态，并输出审计日志。
package main

import (
	"fmt"
	"log"

	"ontology/ontology"
)

func main() {
	schema := ontology.NewSchemaRegistry(ontology.RuleVersion{
		EffectiveFrom: 0,
		Types: map[string]ontology.TypeDef{
			"Vehicle": {ID: "Vehicle", LocalProps: map[string]ontology.ValueRange{
				"speed": {Kind: ontology.IntRangeKind, Min: 0, Max: 200},
			}},
			"Car": {ID: "Car", Parent: "Vehicle", Overrides: map[string]ontology.ValueRange{
				"speed": {Kind: ontology.IntRangeKind, Min: 0, Max: 120},
			}, LocalProps: map[string]ontology.ValueRange{
				"seats": {Kind: ontology.IntRangeKind, Min: 1, Max: 9},
			}},
		},
	})
	store := ontology.NewEventStore()
	rb := ontology.NewRebuilder(store, schema, 8)

	const id = "vehicle-1"
	store.Append(ontology.Event{InstanceID: id, Time: 1, Kind: ontology.EventCreate, TypeID: "Vehicle"})
	store.Append(ontology.Event{InstanceID: id, Time: 2, Kind: ontology.EventSetProperty, Property: "speed", Value: ontology.IntVal(150)})
	store.Append(ontology.Event{InstanceID: id, Time: 3, Kind: ontology.EventEvolveType, TypeID: "Car"})
	store.Append(ontology.Event{InstanceID: id, Time: 4, Kind: ontology.EventSetProperty, Property: "seats", Value: ontology.IntVal(5)})
	store.Append(ontology.Event{InstanceID: id, Time: 5, Kind: ontology.EventEvolveType, TypeID: "Vehicle"})

	printState := func(label string, cutoff int64) {
		st, stats, err := rb.Rebuild(id, cutoff)
		if err != nil {
			fmt.Printf("%s: error: %v\n", label, err)
			return
		}
		fmt.Printf("%s: type=%s values=%v suppressed=%v (scanned=%d, checkpoint=%v)\n",
			label, st.TypeID, st.Values, st.Suppressed, stats.EventsScanned, stats.CheckpointUsed)
	}

	printState("t=2 (Vehicle, speed=150)      ", 2)
	printState("t=4 (Car, speed 越界被遮蔽)     ", 4)
	printState("t=5 (退回 Vehicle, speed 恢复)  ", 5)

	// 规则版本调整：t=10 起 Vehicle.speed 收紧到 [0,100]。
	if err := schema.AppendVersion(ontology.RuleVersion{
		EffectiveFrom: 10,
		Types: map[string]ontology.TypeDef{
			"Vehicle": {ID: "Vehicle", LocalProps: map[string]ontology.ValueRange{
				"speed": {Kind: ontology.IntRangeKind, Min: 0, Max: 100},
			}},
			"Car": {ID: "Car", Parent: "Vehicle"},
		},
	}); err != nil {
		log.Fatal(err)
	}
	printState("t=5 (新版本追加后,历史重建不变)", 5)

	audit, err := ontology.NewFileAuditLogger("audit.jsonl")
	if err != nil {
		log.Fatal(err)
	}
	defer audit.Close()
	st, stats, _ := rb.Rebuild(id, 5)
	audit.Log(ontology.AuditRecord{
		Kind: "rebuild", InstanceID: id, Cutoff: 5,
		SchemaVersion: schema.VersionAt(5).ID, EventCount: store.Len(),
		EventsScanned: stats.EventsScanned, CheckpointUsed: stats.CheckpointUsed,
		ResultType: st.TypeID, Conclusion: "ok",
	})
	fmt.Println("audit written to audit.jsonl")
}

package ontology

// 测试共用的三层类型层级：
//
//	A: score[0,100], tag{a,b,c}
//	B: 继承 A，覆盖 score->[0,10]，新增 bOnly{x,y}
//	C: 继承 B，覆盖 score->[0,5]，新增 cOnly(任意)
func testSchema() *SchemaRegistry {
	return NewSchemaRegistry(RuleVersion{
		EffectiveFrom: 0,
		Types: map[string]TypeDef{
			"A": {
				ID: "A",
				LocalProps: map[string]ValueRange{
					"score": {Kind: IntRangeKind, Min: 0, Max: 100},
					"tag":   {Kind: EnumRangeKind, Enum: []string{"a", "b", "c"}},
				},
			},
			"B": {
				ID:     "B",
				Parent: "A",
				Overrides: map[string]ValueRange{
					"score": {Kind: IntRangeKind, Min: 0, Max: 10},
				},
				LocalProps: map[string]ValueRange{
					"bOnly": {Kind: EnumRangeKind, Enum: []string{"x", "y"}},
				},
			},
			"C": {
				ID:     "C",
				Parent: "B",
				Overrides: map[string]ValueRange{
					"score": {Kind: IntRangeKind, Min: 0, Max: 5},
				},
				LocalProps: map[string]ValueRange{
					"cOnly": {Kind: AnyRange},
				},
			},
		},
	})
}

func create(id string, t int64, typeID string) Event {
	return Event{InstanceID: id, Time: t, Kind: EventCreate, TypeID: typeID}
}

func set(id string, t int64, prop string, v Value) Event {
	return Event{InstanceID: id, Time: t, Kind: EventSetProperty, Property: prop, Value: v}
}

func evolve(id string, t int64, target string) Event {
	return Event{InstanceID: id, Time: t, Kind: EventEvolveType, TypeID: target}
}

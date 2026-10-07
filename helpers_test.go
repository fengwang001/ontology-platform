package ontology

// 标准测试继承体系（三级链 + 一个旁支）：
//
//	Vehicle  wheels[0,20], color{red,blue,green}
//	├── Car      wheels[2,8]（收窄覆盖）
//	│   └── Sedan  wheels[4,4]（收窄覆盖）, sport{yes,no}（子类型新增属性）
//	└── Truck    （无覆盖）
func standardTypes() map[string]ObjectType {
	return map[string]ObjectType{
		"Vehicle": {ID: "Vehicle", Parent: "", Props: map[string]Range{
			"wheels": IntRange{Min: 0, Max: 20},
			"color":  StrSet{Allowed: []string{"red", "blue", "green"}},
		}},
		"Car": {ID: "Car", Parent: "Vehicle", Props: map[string]Range{
			"wheels": IntRange{Min: 2, Max: 8},
		}},
		"Sedan": {ID: "Sedan", Parent: "Car", Props: map[string]Range{
			"wheels": IntRange{Min: 4, Max: 4},
			"sport":  StrSet{Allowed: []string{"yes", "no"}},
		}},
		"Truck": {ID: "Truck", Parent: "Vehicle", Props: map[string]Range{}},
	}
}

// standardRules 返回单版本规则库，版本 0 自时刻 100 起生效。
func standardRules() *RuleStore {
	rs := NewRuleStore()
	if err := rs.AddVersion(RuleVersion{ValidFrom: 100, Types: standardTypes()}); err != nil {
		panic(err)
	}
	return rs
}

// mustAppend 追加事件，出错即 panic（测试辅助）。
func mustAppend(s *Store, id string, ev Event) {
	if err := s.Append(id, ev); err != nil {
		panic(err)
	}
}

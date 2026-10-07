package ontology_test

import (
	"fmt"
	"sort"

	"ontology/ontology"
)

type naiveModel struct {
	values map[string]map[string]ontology.Value
}

func newNaiveModel(schema ontology.Schema) *naiveModel {
	model := &naiveModel{values: map[string]map[string]ontology.Value{}}
	return model
}

func (m *naiveModel) create(id string) {
	m.values[id] = map[string]ontology.Value{}
}

func (m *naiveModel) write(property string, writes []ontology.PropertyWrite) bool {
	for _, write := range writes {
		if _, ok := m.values[write.Object]; !ok {
			return false
		}
	}
	for _, write := range writes {
		m.values[write.Object][property] = write.Value
	}
	return true
}

func (m *naiveModel) delete(id string) bool {
	if _, ok := m.values[id]; !ok {
		return false
	}
	delete(m.values, id)
	return true
}

func (m *naiveModel) get(id, property string) (ontology.Value, bool) {
	values, ok := m.values[id]
	if !ok {
		return ontology.Value{}, false
	}
	value, ok := values[property]
	if !ok {
		return ontology.Missing(), true
	}
	return value, true
}

func (m *naiveModel) query(schema ontology.Schema, indexName, property string, key ontology.IndexKey) []string {
	def := schema.Properties[property]
	ids := make([]string, 0)
	for id, values := range m.values {
		value, ok := values[property]
		if !ok {
			value = ontology.Missing()
		}
		for _, index := range def.Indexes {
			if index.Name != indexName {
				continue
			}
			actual := ontology.MissingKey()
			if value.Present {
				actual = index.Key(value)
				actual.Present = true
			}
			if actual == key {
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	return ids
}

func testSchema() ontology.Schema {
	key := func(value ontology.Value) ontology.IndexKey {
		return ontology.IndexKey{Present: true, Data: fmt.Sprint(value.Data)}
	}
	upper := func(value ontology.Value) ontology.IndexKey {
		text, _ := value.Data.(string)
		return ontology.IndexKey{Present: true, Data: text}
	}
	return ontology.Schema{Properties: map[string]ontology.PropertyDef{
		"name": {
			Name:    "name",
			Default: ontology.Explicit(""),
			Indexes: []ontology.IndexDef{
				{Name: "name-exact", Key: key},
				{Name: "name-alias", Key: upper},
			},
		},
		"age": {
			Name:    "age",
			Default: ontology.Explicit(0),
			Indexes: []ontology.IndexDef{{Name: "age-exact", Key: key}},
		},
		"broken": {
			Name:    "broken",
			Default: ontology.Explicit(""),
			Indexes: []ontology.IndexDef{{Name: "broken-index", Key: key, Fail: true}},
		},
	}}
}

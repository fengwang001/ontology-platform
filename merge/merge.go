package merge

import (
	"fmt"
	"sort"

	"ontology/source"
)

type Merged struct {
	value  map[string]string
	origin map[string]source.Level
	order  []source.Level
}

type merger struct {
	lookups int
}

func Merge(layers []source.Layer) *Merged {
	ordered := append([]source.Layer(nil), layers...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Level < ordered[j].Level
	})
	m := &merger{}
	result := &Merged{
		value:  map[string]string{},
		origin: map[string]source.Level{},
	}
	for _, layer := range ordered {
		for _, item := range layer.Items {
			m.lookups++
			result.value[item.Key] = item.Value
			result.origin[item.Key] = layer.Level
		}
		result.order = append(result.order, layer.Level)
	}
	result.setLookups(m.lookups)
	return result
}

func (r *Merged) Get(key string) (string, bool) {
	value, ok := r.value[key]
	return value, ok
}

func (r *Merged) Origin(key string) (source.Level, bool) {
	level, ok := r.origin[key]
	return level, ok
}

func (r *Merged) Keys() []string {
	keys := make([]string, 0, len(r.value))
	for key := range r.value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (r *Merged) Snapshot() map[string]string {
	out := make(map[string]string, len(r.value))
	for key, value := range r.value {
		out[key] = value
	}
	return out
}

func (r *Merged) String() string {
	return fmt.Sprintf("merged(%d keys)", len(r.value))
}

package ontology_test

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"ontology/ontology"
)

func TestRandomOperationsMatchNaiveFullScan(t *testing.T) {
	logger := &recordingLogger{}
	schema := testSchema()
	p := ontology.New(schema, logger)
	model := newNaiveModel(schema)
	rng := rand.New(rand.NewSource(1682))
	objects := make([]string, 0)
	nextObject := 0

	for step := 0; step < 240; step++ {
		action := rng.Intn(10)
		switch {
		case action < 3 || len(objects) == 0:
			id := fmt.Sprintf("o%03d", nextObject)
			nextObject++
			p.CreateObject(id)
			model.create(id)
			objects = append(objects, id)
		case action < 7:
			property := randomIndexedProperty(rng)
			chosen := randomObjects(rng, objects)
			writes := make([]ontology.PropertyWrite, 0, len(chosen))
			for _, id := range chosen {
				writes = append(writes, ontology.PropertyWrite{Object: id, Value: randomValue(rng)})
			}
			err := p.WriteProperty(context.Background(), property, writes)
			if err == nil {
				model.write(property, writes)
			}
			t.Logf("step=%d input=write property=%s writes=%v err=%v", step, property, writes, err)
			logLatest(t, logger)
		case action < 9:
			id := objects[rng.Intn(len(objects))]
			err := p.DeleteObject(context.Background(), id)
			if err == nil {
				model.delete(id)
				objects = removeObject(objects, id)
			}
			t.Logf("step=%d input=delete object=%s err=%v", step, id, err)
			logLatest(t, logger)
		default:
			id := objects[rng.Intn(len(objects))]
			property := randomIndexedProperty(rng)
			got, err := p.Get(id, property)
			want, exists := model.get(id, property)
			if err != nil || !exists || got != want {
				t.Fatalf("step=%d get %s/%s got=(%v,%v) want=(%v,%v)", step, id, property, got, err, want, exists)
			}
			t.Logf("step=%d input=get object=%s property=%s result=%v", step, id, property, got)
		}

		for _, property := range []string{"name", "age"} {
			for _, value := range []string{"", "alice", "bob", "0", "1", "42"} {
				key := ontology.IndexKey{Present: true, Data: value}
				indexName := property + "-exact"
				got, err := p.Query(indexName, property, key)
				if err != nil {
					t.Fatalf("step=%d query: %v", step, err)
				}
				sort.Strings(got)
				want := model.query(schema, indexName, property, key)
				if len(got) != len(want) || !reflect.DeepEqual(got, want) {
					t.Fatalf("step=%d query %s/%v got=%v want=%v", step, indexName, key, got, want)
				}
			}
			got, err := p.Query(property+"-exact", property, ontology.MissingKey())
			if err != nil {
				t.Fatal(err)
			}
			sort.Strings(got)
			want := model.query(schema, property+"-exact", property, ontology.MissingKey())
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("step=%d missing query got=%v want=%v", step, got, want)
			}
		}
	}
}

func randomIndexedProperty(rng *rand.Rand) string {
	properties := []string{"name", "age", "broken"}
	return properties[rng.Intn(len(properties))]
}

func randomValue(rng *rand.Rand) ontology.Value {
	if rng.Intn(5) == 0 {
		return ontology.Missing()
	}
	switch rng.Intn(4) {
	case 0:
		return ontology.Explicit("alice")
	case 1:
		return ontology.Explicit("bob")
	case 2:
		return ontology.Explicit(rng.Intn(3))
	default:
		return ontology.Explicit("")
	}
}

func randomObjects(rng *rand.Rand, objects []string) []string {
	count := 1 + rng.Intn(3)
	if count > len(objects) {
		count = len(objects)
	}
	chosen := make([]string, 0, count)
	for _, index := range rng.Perm(len(objects))[:count] {
		chosen = append(chosen, objects[index])
	}
	return append([]string(nil), chosen...)
}

func removeObject(objects []string, target string) []string {
	out := make([]string, 0, len(objects))
	for _, id := range objects {
		if id != target {
			out = append(out, id)
		}
	}
	return out
}

func logLatest(t *testing.T, logger *recordingLogger) {
	t.Helper()
	events := logger.snapshot()
	if len(events) == 0 {
		return
	}
	printEvent(t, events[len(events)-1])
}

package ontology_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"ontology/ontology"
)

func TestConcurrentWritesSamePropertyAreSerializable(t *testing.T) {
	p := ontology.New(testSchema(), nil)
	p.CreateObject("o1")
	const writers = 64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			value := fmt.Sprintf("v%02d", i)
			<-start
			if err := p.WriteProperty(context.Background(), "name", []ontology.PropertyWrite{{Object: "o1", Value: ontology.Explicit(value)}}); err != nil {
				t.Errorf("write %d: %v", i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	final, err := p.Get("o1", "name")
	if err != nil {
		t.Fatal(err)
	}
	assertQuery(t, p, "name-exact", "name", ontology.IndexKey{Present: true, Data: fmt.Sprint(final.Data)}, []string{"o1"})
	assertQuery(t, p, "name-alias", "name", ontology.IndexKey{Present: true, Data: fmt.Sprint(final.Data)}, []string{"o1"})
	if p.Clock() != writers {
		t.Fatalf("clock=%d want=%d", p.Clock(), writers)
	}
}

func TestConcurrentDifferentPropertiesDoNotCorruptIndexes(t *testing.T) {
	p := ontology.New(testSchema(), nil)
	p.CreateObject("o1")
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_ = p.WriteProperty(context.Background(), "name", []ontology.PropertyWrite{{Object: "o1", Value: ontology.Explicit("alice")}})
	}()
	go func() {
		defer wg.Done()
		<-start
		_ = p.WriteProperty(context.Background(), "age", []ontology.PropertyWrite{{Object: "o1", Value: ontology.Explicit(42)}})
	}()
	close(start)
	wg.Wait()

	assertQuery(t, p, "name-exact", "name", ontology.IndexKey{Present: true, Data: "alice"}, []string{"o1"})
	assertQuery(t, p, "age-exact", "age", ontology.IndexKey{Present: true, Data: "42"}, []string{"o1"})
	if p.Clock() != 2 {
		t.Fatalf("clock=%d", p.Clock())
	}
}

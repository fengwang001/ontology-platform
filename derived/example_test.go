package derived_test

import (
	"fmt"

	"ontology/derived"
)

func ExampleStore() {
	s := derived.NewStore(nil)
	_ = s.AddObject(derived.Object{ID: "a1", Type: "A",
		Properties: map[string]derived.Value{"name": "red"}})
	_ = s.AddObject(derived.Object{ID: "b1", Type: "B"})
	_ = s.AddDeclaration(derived.Declaration{
		Name: "by_source_name", DownstreamType: "B", LinkType: "owns",
		SourceType: "A", SourceProperty: "name", RequireUnique: true,
	})

	e, _ := s.Entry("by_source_name", "b1")
	fmt.Println(e.State) // no link yet

	s.AddLink("b1", "a1", "owns")
	e, _ = s.Entry("by_source_name", "b1")
	fmt.Println(e.State, e.Keys)

	s.SetProperty("a1", "name", "blue")
	fmt.Println(s.Lookup("by_source_name", "blue"))

	// Output:
	// NOT_INDEXABLE_NO_LINK
	// INDEXED [red]
	// [b1]
}

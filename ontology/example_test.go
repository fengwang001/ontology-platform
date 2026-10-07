package ontology_test

import (
	"fmt"

	"ontology/ontology"
)

func ExampleIterator() {
	s := ontology.NewStore()
	acl := ontology.NewACL(s)
	actor := ontology.Actor{ID: "alice"}

	s.PutLinkType(ontology.LinkType{ID: "knows", Cost: 1})
	for _, id := range []string{"a", "b", "c"} {
		s.PutObject(ontology.Object{ID: id})
		s.GrantObjectSee(id, actor.ID)
	}
	_ = s.AddLink(ontology.Link{Type: "knows", Source: "a", Target: "b"})
	s.GrantLinkTraverse(ontology.Link{Type: "knows", Source: "a", Target: "b"}, actor.ID)
	_ = s.AddLink(ontology.Link{Type: "knows", Source: "b", Target: "c"})
	s.GrantLinkTraverse(ontology.Link{Type: "knows", Source: "b", Target: "c"}, actor.ID)

	it := ontology.NewIterator(s, acl)
	tok := ""
	for {
		page, err := it.Traverse("a", 5, 10, tok, actor)
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		for _, o := range page.Objects {
			fmt.Println(o.ID)
		}
		if page.Done {
			fmt.Println("truncation:", page.Truncation)
			return
		}
		tok = page.NextToken
	}
	// Output:
	// a
	// b
	// c
	// truncation: complete
}

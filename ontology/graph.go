package ontology

import (
	"fmt"
	"sort"
)

type Object struct {
	ID     string
	TypeID string
}

type Link struct {
	ID       string
	TypeID   string
	SourceID string
	TargetID string
	Cost     int
}

type graphObject struct {
	object Object
}

type graphLink struct {
	link   Link
	source string
	target string
}

type Graph struct {
	objects map[string]graphObject
	links   map[string]graphLink
	out     map[string][]string
}

func NewGraph(objects []Object, links []Link) (*Graph, error) {
	g := &Graph{
		objects: make(map[string]graphObject, len(objects)),
		links:   make(map[string]graphLink, len(links)),
		out:     make(map[string][]string, len(objects)),
	}

	for _, object := range objects {
		if !validID(object.ID) || !validID(object.TypeID) {
			return nil, fmt.Errorf("ontology: invalid object %q with type %q", object.ID, object.TypeID)
		}
		if _, exists := g.objects[object.ID]; exists {
			return nil, fmt.Errorf("ontology: duplicate object ID %q", object.ID)
		}
		g.objects[object.ID] = graphObject{object: object}
	}

	for _, link := range links {
		if !validID(link.ID) || !validID(link.TypeID) {
			return nil, fmt.Errorf("ontology: invalid link %q with type %q", link.ID, link.TypeID)
		}
		if link.Cost < 0 {
			return nil, fmt.Errorf("ontology: link %q has negative cost %d", link.ID, link.Cost)
		}
		if _, exists := g.links[link.ID]; exists {
			return nil, fmt.Errorf("ontology: duplicate link ID %q", link.ID)
		}
		source, sourceExists := g.objects[link.SourceID]
		target, targetExists := g.objects[link.TargetID]
		if !sourceExists || !targetExists {
			return nil, fmt.Errorf("ontology: link %q references a missing endpoint", link.ID)
		}
		g.links[link.ID] = graphLink{link: link, source: source.object.TypeID, target: target.object.TypeID}
		g.out[link.SourceID] = append(g.out[link.SourceID], link.ID)
	}

	g.sortOutgoing()

	return g, nil
}

func (g *Graph) sortOutgoing() {
	for sourceID, outgoing := range g.out {
		sort.Slice(outgoing, func(i, j int) bool {
			left := g.links[outgoing[i]]
			right := g.links[outgoing[j]]
			if left.link.TargetID != right.link.TargetID {
				return left.link.TargetID < right.link.TargetID
			}
			if left.link.TypeID != right.link.TypeID {
				return left.link.TypeID < right.link.TypeID
			}
			return left.link.ID < right.link.ID
		})
		g.out[sourceID] = outgoing
	}
}

func validID(id string) bool {
	return id != ""
}

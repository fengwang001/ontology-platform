package traversal

import (
	"sort"
	"sync"
)

type Graph struct {
	mu      sync.RWMutex
	objects map[string]Object
	links   map[string]Link
}

type Snapshot struct {
	objects       map[string]Object
	links         map[string]Link
	outgoingLinks map[string][]string
}

func NewGraph() *Graph {
	return &Graph{
		objects: make(map[string]Object),
		links:   make(map[string]Link),
	}
}

func cloneObject(object Object) Object {
	object.Properties = cloneProperties(object.Properties)
	return object
}

func cloneLink(link Link) Link {
	link.Properties = cloneProperties(link.Properties)
	return link
}

func cloneProperties(properties map[string]string) map[string]string {
	if properties == nil {
		return nil
	}
	cloned := make(map[string]string, len(properties))
	for key, value := range properties {
		cloned[key] = value
	}
	return cloned
}

func (g *Graph) PutObject(object Object) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.objects[object.ID] = cloneObject(object)
}

func (g *Graph) DeleteObject(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.objects, id)
}

func (g *Graph) PutLink(link Link) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.links[link.ID] = cloneLink(link)
}

func (g *Graph) DeleteLink(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.links, id)
}

func (g *Graph) Snapshot() Snapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()
	snapshot := Snapshot{
		objects:       make(map[string]Object, len(g.objects)),
		links:         make(map[string]Link, len(g.links)),
		outgoingLinks: make(map[string][]string),
	}
	for id, object := range g.objects {
		snapshot.objects[id] = cloneObject(object)
	}
	for id, link := range g.links {
		snapshot.links[id] = cloneLink(link)
		if _, exists := g.objects[link.FromID]; exists {
			snapshot.outgoingLinks[link.FromID] = append(snapshot.outgoingLinks[link.FromID], id)
		}
	}
	for sourceID := range snapshot.outgoingLinks {
		linkIDs := snapshot.outgoingLinks[sourceID]
		sort.Slice(linkIDs, func(i, j int) bool { return linkIDs[i] < linkIDs[j] })
		snapshot.outgoingLinks[sourceID] = linkIDs
	}
	return snapshot
}

func (snapshot Snapshot) HasObject(id string) bool {
	_, exists := snapshot.objects[id]
	return exists
}

func (snapshot Snapshot) Object(id string) Object {
	return snapshot.objects[id]
}

func (snapshot Snapshot) Link(id string) Link {
	return snapshot.links[id]
}

func (snapshot Snapshot) OutgoingLinkIDs(sourceID string) []string {
	return append([]string(nil), snapshot.outgoingLinks[sourceID]...)
}

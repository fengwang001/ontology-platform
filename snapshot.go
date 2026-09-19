package ontology

import (
	"encoding/json"
	"os"
	"sort"
)

// snapshot is the on-disk representation of a Store. Links are stored as a
// sorted flat list so files are deterministic and diff-friendly.
type snapshot struct {
	ObjectTypes []string          `json:"objectTypes"`
	LinkTypes   []LinkType        `json:"linkTypes"`
	Objects     map[string]string `json:"objects"`
	Links       []snapshotLink    `json:"links"`
}

type snapshotLink struct {
	LinkType string `json:"linkType"`
	Source   string `json:"source"`
	Target   string `json:"target"`
}

// SaveToFile persists the whole store as JSON at path.
func (s *Store) SaveToFile(path string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := snapshot{Objects: s.st.objects}
	for name := range s.st.objectTypes {
		snap.ObjectTypes = append(snap.ObjectTypes, name)
	}
	sort.Strings(snap.ObjectTypes)
	for _, name := range sortedLinkTypeNames(s.st) {
		snap.LinkTypes = append(snap.LinkTypes, *s.st.linkTypes[name])
	}
	for _, lt := range sortedLinkTypeNames(s.st) {
		for _, src := range sortedKeysOf(s.st.fwd[lt]) {
			for _, dst := range sortedKeys(s.st.fwd[lt][src]) {
				snap.Links = append(snap.Links, snapshotLink{lt, src, dst})
			}
		}
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadFromFile rebuilds a store from a snapshot, re-validating every link
// through the normal create path.
func LoadFromFile(path string) (*Store, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	st := NewStore()
	for _, t := range snap.ObjectTypes {
		if err := st.RegisterObjectType(t); err != nil {
			return nil, err
		}
	}
	for _, lt := range snap.LinkTypes {
		if err := st.RegisterLinkType(lt); err != nil {
			return nil, err
		}
	}
	ids := make([]string, 0, len(snap.Objects))
	for id := range snap.Objects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := st.AddObject(snap.Objects[id], id); err != nil {
			return nil, err
		}
	}
	for _, l := range snap.Links {
		if err := st.CreateLink(l.LinkType, l.Source, l.Target); err != nil {
			return nil, err
		}
	}
	return st, nil
}

package ontology

import "sort"

// This file holds the naive full-rebuild reference model. It shares only the
// pure placement rule (decidePlacement) and ordering helpers with the
// incremental view; all state is recomputed from scratch on every call, with
// no incremental data structures. Randomized tests cross-check the
// incremental view against this model event by event.

// NaiveRebuild recomputes the view content for a link relation from the
// delivery log and the current type metadata, from scratch.
func (e *Engine) NaiveRebuild(linkTypeID string) []Group {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.snap()

	type objAcc struct {
		typeID string
		links  int
		write  *WriteRec
	}
	objs := make(map[string]*objAcc)
	pairs := make(map[[2]string]bool)
	acc := func(id, typeID string) *objAcc {
		o := objs[id]
		if o == nil {
			o = &objAcc{typeID: typeID}
			objs[id] = o
		}
		if typeID != "" {
			o.typeID = typeID
		}
		return o
	}
	for _, ev := range e.log {
		switch ev.Kind {
		case EvWrite:
			w := ev.Write
			prop, _, ok := s.groupingPropActive(w.TypeID)
			if ok && w.Prop != prop {
				continue
			}
			o := acc(w.ObjID, w.TypeID)
			if o.write == nil || o.write.WriteSeq < w.WriteSeq {
				ow := w
				o.write = &ow
			}
		case EvLink, EvUnlink:
			if ev.LinkType != linkTypeID {
				continue
			}
			pair := [2]string{ev.LeftID, ev.RightID}
			delta := 0
			if ev.Kind == EvLink {
				if pairs[pair] {
					continue
				}
				pairs[pair] = true
				delta = 1
			} else {
				if !pairs[pair] {
					continue
				}
				delete(pairs, pair)
				delta = -1
			}
			acc(ev.LeftID, ev.LeftType).links += delta
			acc(ev.RightID, ev.RightType).links += delta
		}
	}

	groups := make(map[string][]Item)
	for id, o := range objs {
		_ = id
		item, errCode := decidePlacement(s, linkTypeID, o.links, o.write)
		if errCode != nil || o.links == 0 || o.write == nil {
			continue
		}
		groups[item.GroupKey] = append(groups[item.GroupKey], item)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Group, 0, len(keys))
	for _, k := range keys {
		items := groups[k]
		sort.Slice(items, func(i, j int) bool { return lessItem(items[i], items[j]) })
		out = append(out, Group{Key: k, Items: items})
	}
	return out
}

// CompareGroups item-compares two query results and returns a description of
// the first mismatch, or "" when they are identical.
func CompareGroups(want, got []Group) string {
	if len(want) != len(got) {
		return "group count differs"
	}
	for i := range want {
		if want[i].Key != got[i].Key {
			return "group key differs at " + itoa(i)
		}
		if len(want[i].Items) != len(got[i].Items) {
			return "item count differs in group " + want[i].Key
		}
		for j := range want[i].Items {
			a, b := want[i].Items[j], got[i].Items[j]
			if a.ObjID != b.ObjID || a.TypeID != b.TypeID ||
				!a.Normalized.Equal(b.Normalized) || a.TZVersion != b.TZVersion {
				return "item differs in group " + want[i].Key + " at " + itoa(j)
			}
		}
	}
	return ""
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[p:])
}

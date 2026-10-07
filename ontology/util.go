package ontology

import "sort"

// sortLinks 使任何全量快照/可见性枚举具备确定性顺序，保证重放可逐条对照。
func sortLinks(links []Link) {
	sort.Slice(links, func(i, j int) bool {
		if links[i].TypeName != links[j].TypeName {
			return links[i].TypeName < links[j].TypeName
		}
		if links[i].SrcID != links[j].SrcID {
			return links[i].SrcID < links[j].SrcID
		}
		if links[i].TgtID != links[j].TgtID {
			return links[i].TgtID < links[j].TgtID
		}
		return links[i].ID < links[j].ID
	})
}

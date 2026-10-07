package lifecycle

import "time"

func nowTime() time.Time { return time.Now() }

func copyStringMap(m map[string]string) map[string]string {
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func copyLinkSet(m map[Link]bool) map[Link]bool {
	c := make(map[Link]bool, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func copyAttrOverlay(m map[string]map[string]AttrValue) map[string]map[string]AttrValue {
	c := make(map[string]map[string]AttrValue, len(m))
	for k, v := range m {
		inner := make(map[string]AttrValue, len(v))
		for ik, iv := range v {
			inner[ik] = iv
		}
		c[k] = inner
	}
	return c
}

func copyAdj(m map[string]map[string]map[string]bool) map[string]map[string]map[string]bool {
	c := make(map[string]map[string]map[string]bool, len(m))
	for from, tm := range m {
		inner := make(map[string]map[string]bool, len(tm))
		for typ, set := range tm {
			s2 := make(map[string]bool, len(set))
			for k, v := range set {
				s2[k] = v
			}
			inner[typ] = s2
		}
		c[from] = inner
	}
	return c
}

func mergeStringMap(dst, src map[string]string) map[string]string {
	if dst == nil {
		dst = map[string]string{}
	}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func mergeAttrOverlay(dst, src map[string]map[string]AttrValue) map[string]map[string]AttrValue {
	if dst == nil {
		dst = map[string]map[string]AttrValue{}
	}
	for id, ov := range src {
		if dst[id] == nil {
			dst[id] = map[string]AttrValue{}
		}
		for k, v := range ov {
			dst[id][k] = v
		}
	}
	return dst
}

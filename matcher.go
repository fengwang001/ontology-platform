package ontology

import "strings"

// entityMatches 判断实体是否以前缀开头，且前缀必须在路径段边界上对齐。
// 精确相等也算命中；"user" 不会匹配 "superuser"，但会匹配 "user" 与 "user/42"。
func entityMatches(entity, prefix string) bool {
	if prefix == "" {
		return true
	}
	if entity == prefix {
		return true
	}
	if !strings.HasPrefix(entity, prefix) {
		return false
	}
	// entity 比 prefix 长时，紧跟的字符必须是路径分隔符，避免退化成子串匹配。
	return strings.HasPrefix(entity[len(prefix):], "/")
}

// propertyMatches 判断属性名是否命中订阅集合；集合为空表示匹配全部。
func propertyMatches(property string, set map[string]struct{}) bool {
	if len(set) == 0 {
		return true
	}
	_, ok := set[property]
	return ok
}

func propertySet(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}

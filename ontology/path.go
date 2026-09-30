package ontology

import "strings"

func validatePath(path string) bool {
	if path == "/" {
		return true
	}
	if path == "" || !strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
		return false
	}

	segments := strings.Split(path[1:], "/")
	for _, segment := range segments {
		if segment == "" {
			return false
		}
	}
	return len(segments) > 0
}

func ancestorPaths(path string) []string {
	if !validatePath(path) {
		return nil
	}

	paths := []string{path}
	for index := strings.LastIndex(path, "/"); index >= 0; index = strings.LastIndex(path[:index], "/") {
		if index == 0 {
			paths = append(paths, "/")
			break
		}
		paths = append(paths, path[:index])
	}
	return paths
}

func isDescendantOrSelf(descendantPath, ancestorPath string) bool {
	if ancestorPath == "/" {
		return descendantPath == "/" || strings.HasPrefix(descendantPath, "/")
	}
	return descendantPath == ancestorPath || strings.HasPrefix(descendantPath, ancestorPath+"/")
}

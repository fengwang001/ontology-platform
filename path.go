package statusengine

import (
	pathpkg "path"
	"strings"
)

type normalizedPath string

func normalizePath(value string) (normalizedPath, error) {
	original := strings.ReplaceAll(value, "\\", "/")
	if strings.HasPrefix(original, "/") {
		return "", ErrInvalidPath
	}
	for _, part := range strings.Split(original, "/") {
		if part == ".." {
			return "", ErrInvalidPath
		}
	}
	cleaned := pathpkg.Clean(original)
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", ErrInvalidPath
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == "" || part == "." {
			return "", ErrInvalidPath
		}
	}
	return normalizedPath(cleaned), nil
}

func (p normalizedPath) Dir() normalizedPath {
	return normalizedPath(pathpkg.Dir(string(p)))
}

func isDescendantOrEqual(target, prefix normalizedPath) bool {
	return target == prefix || strings.HasPrefix(string(target), string(prefix)+"/")
}

func ancestorConflict(paths map[normalizedPath][]byte, target normalizedPath) bool {
	for existing := range paths {
		if isDescendantOrEqual(target, existing) || isDescendantOrEqual(existing, target) {
			return true
		}
	}
	return false
}

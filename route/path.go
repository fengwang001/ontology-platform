package route

import "errors"

var ErrInvalidPath = errors.New("invalid path")

func Normalize(path string) (string, error) {
	if path == "" || path[0] != '/' {
		return "", ErrInvalidPath
	}
	for i := 0; i < len(path); i++ {
		b := path[i]
		if b == '%' || b == '?' || b == '#' || b < 0x20 || b == 0x7f {
			return "", ErrInvalidPath
		}
	}

	var stack []string
	start := 0
	for i := 0; i <= len(path); i++ {
		if i != len(path) && path[i] != '/' {
			continue
		}
		segment := path[start:i]
		start = i + 1
		if segment == "" || segment == "." {
			continue
		}
		if segment == ".." {
			if len(stack) == 0 {
				return "", ErrInvalidPath
			}
			stack = stack[:len(stack)-1]
			continue
		}
		stack = append(stack, segment)
	}

	if len(stack) == 0 {
		return "/", nil
	}
	normalized := ""
	for _, segment := range stack {
		normalized += "/" + segment
	}
	return normalized, nil
}

package authz

import "errors"

var ErrInvalidScope = errors.New("invalid scope")

func ValidateScopes(scopes []string) error {
	for _, scope := range scopes {
		if scope == "" {
			return ErrInvalidScope
		}
	}
	return nil
}

func Allowed(required string, scopes []string) bool {
	if required == "" {
		return true
	}
	for _, scope := range scopes {
		if scope == required {
			return true
		}
	}
	return false
}

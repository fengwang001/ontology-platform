// Package authz implements scope validation and authorization checks
// for the gateway router.
package authz

// ValidScopes reports whether scopes contains no empty entries.
// Callers must reject requests with empty scopes before anything else.
func ValidScopes(scopes []string) bool {
	for _, s := range scopes {
		if s == "" {
			return false
		}
	}
	return true
}

// Allowed reports whether scopes satisfy the required scope.
// An empty required scope means the route is public.
func Allowed(required string, scopes []string) bool {
	if required == "" {
		return true
	}
	for _, s := range scopes {
		if s == required {
			return true
		}
	}
	return false
}

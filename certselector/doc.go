// Package certselector selects a hot-reloadable TLS certificate from a
// normalized client server name, client key-type preferences, and time.
//
// Exact SAN matches suppress wildcard matches and default fallback. Wildcards
// may only occupy the complete left-most label and match exactly one extra
// label. Successful selections record whether the source was exact, wildcard,
// or default fallback.
package certselector

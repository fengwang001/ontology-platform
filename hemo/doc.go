// Package hemo is the production scheduling and infection-isolation engine
// for a hemodialysis center.
//
// Start from NewSystem and Config; see docs/DESIGN.md for the design and
// complexity proof and docs/API.md for the operation reference. The
// independent reference implementation lives in package hemo/naive and the
// randomized differential harness in package hemo/fuzz.
package hemo

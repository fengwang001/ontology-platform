// Package routewatch monitors time-window compliance during delivery route
// execution. It projects arrivals and service events stop by stop, enforces
// soft/hard windows and the continuous-driving cap, propagates delays when
// actual arrivals are reported or stops are canceled, and maintains the
// externally published ETAs with debounce and lock-window rules.
//
// The main entry point is Monitor. Operations are concurrency-safe; rejected
// operations never mutate state or the logical clock. See DESIGN.md for the
// incremental-suffix argument and the differential-testing strategy.
package routewatch

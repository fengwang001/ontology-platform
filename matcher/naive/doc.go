// Package naive is an intentionally simple independent reference
// implementation used only by the differential tests. Slices and linear
// scans stand in for the production engine's heaps and linked lists, so
// agreement between the two on thousands of random operation streams gives
// strong evidence that the optimized engine is faithful to the rules.
package naive

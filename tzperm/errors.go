package tzperm

// Error codes reported in permission check outcomes. Messages are fixed
// generic strings: they never embed normalized time values, baseline zone
// identifiers or window boundary positions.
const (
	CodeAttrDeprecated    = "ATTR_DEPRECATED"
	CodeRegionZoneUnknown = "REGION_ZONE_UNDEFINED"
	CodeWindowInvalid     = "WINDOW_RULE_INVALID"
	CodeQuerierMissing    = "QUERIER_IDENTITY_MISSING"
)

// errorCodePriority fixes the reporting order when one request satisfies
// several error conditions at once. Only the highest-priority code is
// returned.
//
// Rationale:
//  1. schema deprecation is intrinsic to the object and must be enforced
//     before any subject-dependent evaluation;
//  2. an unresolvable normalization baseline makes every later step
//     undefined;
//  3. the window rule must be structurally valid before a subject is
//     required to authenticate against it;
//  4. missing querier identity is the most caller-specific condition.
var errorCodePriority = []string{
	CodeAttrDeprecated,
	CodeRegionZoneUnknown,
	CodeWindowInvalid,
	CodeQuerierMissing,
}

// highestErrorCode returns the highest-priority code among the inputs.
func highestErrorCode(codes ...string) string {
	for _, want := range errorCodePriority {
		for _, got := range codes {
			if got == want {
				return got
			}
		}
	}
	return ""
}

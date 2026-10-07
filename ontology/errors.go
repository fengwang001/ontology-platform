package ontology

import "fmt"

// ErrorClass classifies access-determination failures. Classes are reported
// in a fixed, unique priority order: a lower numeric value always wins when
// several conditions hold at the same time.
type ErrorClass int

const (
	// ClassNotFound (priority 1): the subject, instance or tag does not exist.
	ClassNotFound ErrorClass = iota + 1
	// ClassAllPathsBlocked (priority 2): every inheritance path of the tag
	// to the instance's object type passes through a blocking point.
	ClassAllPathsBlocked
	// ClassExplicitDeny (priority 3): an explicit deny overrode an allow.
	ClassExplicitDeny
	// ClassRoleCycle (priority 4): a cycle in the role hierarchy makes the
	// grant source undeterminable.
	ClassRoleCycle
)

func (c ErrorClass) String() string {
	switch c {
	case ClassNotFound:
		return "not-found"
	case ClassAllPathsBlocked:
		return "all-paths-blocked"
	case ClassExplicitDeny:
		return "explicit-deny"
	case ClassRoleCycle:
		return "role-hierarchy-cycle"
	}
	return "unknown"
}

// DecisionError is a classified determination failure. A denied decision
// never mutates tag inheritance state, the role hierarchy, or any clock.
type DecisionError struct {
	Class   ErrorClass
	Subject string
	Tag     string
	Detail  string
}

func (e *DecisionError) Error() string {
	return fmt.Sprintf("access decision failed [%s]: subject=%q tag=%q: %s",
		e.Class, e.Subject, e.Tag, e.Detail)
}

// ClassOf extracts the error class of err, or 0 if err is not a classified
// decision error.
func ClassOf(err error) ErrorClass {
	if de, ok := err.(*DecisionError); ok {
		return de.Class
	}
	return 0
}

func notFoundErr(subject, detail string) *DecisionError {
	return &DecisionError{Class: ClassNotFound, Subject: subject, Detail: detail}
}

func blockedErr(subject, tag, detail string) *DecisionError {
	return &DecisionError{Class: ClassAllPathsBlocked, Subject: subject, Tag: tag, Detail: detail}
}

func roleCycleErr(subject, detail string) *DecisionError {
	return &DecisionError{Class: ClassRoleCycle, Subject: subject, Detail: detail}
}

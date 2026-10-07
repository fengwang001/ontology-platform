package ontology

import "fmt"

const typePermissionKey = "__type__"

type policyState struct {
	allowed bool
	revoked bool
}

type permissionRecord struct {
	Type        string
	Subject     string
	PropertyID  string
	LineageID   string
	Operation   Operation
	Granted     bool
	Revoked     bool
	FromVersion int
	ToVersion   int
}

type typeState struct {
	name        string
	writePolicy WritePolicy
	current     int
	versions    []*TypeVersion

	attrPolicy map[string]map[string]map[string]policyState
	typePolicy map[string]map[string]map[string]policyState

	attrHistory []permissionRecord
	typeHistory []permissionRecord
}

type objectState struct {
	id       string
	typeName string
	version  int
	values   map[string]any
	exists   bool
}

func newTypeState(input CreateTypeInput) *typeState {
	version := newTypeVersion(1, input.Properties)
	return &typeState{
		name:        input.Name,
		writePolicy: input.WritePolicy,
		current:     1,
		versions:    []*TypeVersion{version},
		attrPolicy: map[string]map[string]map[string]policyState{
			"1": {},
		},
		typePolicy: map[string]map[string]map[string]policyState{
			"1": {},
		},
	}
}

func newTypeVersion(version int, properties []Property) *TypeVersion {
	copied := append([]Property(nil), properties...)
	byName := make(map[string]string, len(copied))
	for _, property := range copied {
		byName[property.Identifier] = property.LineageID
	}
	return &TypeVersion{Version: version, Properties: copied, byName: byName}
}

func (t *typeState) version(version int) (*TypeVersion, error) {
	if version < 1 || version > t.current {
		return nil, newError(KindInvalidInput, fmt.Sprintf("type %s has no version %d", t.name, version))
	}
	return t.versions[version-1], nil
}

func (t *typeState) lineageFor(version *TypeVersion, identifier string) (string, bool) {
	lineage, ok := version.byName[identifier]
	return lineage, ok
}

func (t *typeState) attrState(version int, subject string, lineage string, operation Operation) policyState {
	subjects, ok := t.attrPolicy[versionKey(version)]
	if !ok {
		return policyState{}
	}
	properties, ok := subjects[subject]
	if !ok {
		return policyState{}
	}
	return properties[attrPolicyKey(lineage, operation)]
}

func (t *typeState) operationState(version int, subject string, operation Operation) policyState {
	subjects, ok := t.typePolicy[versionKey(version)]
	if !ok {
		return policyState{}
	}
	return subjects[subject][string(operation)]
}

func attrPolicyKey(lineage string, operation Operation) string {
	return lineage + "\x00" + string(operation)
}

func clonePolicyMap(source map[string]map[string]map[string]policyState, versionKey string) map[string]map[string]map[string]policyState {
	cloned := make(map[string]map[string]map[string]policyState, len(source))
	versionCopy := map[string]map[string]policyState{}
	for subject, properties := range source[versionKey] {
		subjectCopy := make(map[string]policyState, len(properties))
		for key, state := range properties {
			subjectCopy[key] = state
		}
		versionCopy[subject] = subjectCopy
	}
	cloned[versionKey] = versionCopy
	return cloned
}

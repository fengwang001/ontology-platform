package ontology

import "strconv"

func (g *Gateway) requireType(name string) (*typeState, error) {
	t, ok := g.types[name]
	if !ok {
		return nil, newError(KindInvalidInput, "object type does not exist: "+name)
	}
	return t, nil
}

func (g *Gateway) requireObject(id string) (*objectState, error) {
	object, ok := g.objects[id]
	if !ok || !object.exists {
		return nil, newError(KindObjectNotFound, "object does not exist: "+id)
	}
	return object, nil
}

func ensureSubjectAttr(versionMap map[string]map[string]policyState, subject string) map[string]policyState {
	subjectMap, ok := versionMap[subject]
	if !ok {
		subjectMap = map[string]policyState{}
		versionMap[subject] = subjectMap
	}
	return subjectMap
}

func ensureSubjectType(versionMap map[string]map[string]policyState, subject string) map[string]policyState {
	return ensureSubjectAttr(versionMap, subject)
}

func versionKey(version int) string {
	return strconv.Itoa(version)
}

func normalizeOperationError(err error) error {
	if err != nil {
		return err
	}
	return newError(KindInvalidInput, "operation must be read or write")
}

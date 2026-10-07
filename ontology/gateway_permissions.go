package ontology

import "context"

func (g *Gateway) liveAttribute(typeName string, identifier string, operation Operation) (*typeState, *TypeVersion, string, error) {
	if !validOperation(operation) {
		return nil, nil, "", normalizeOperationError(nil)
	}
	t, err := g.requireType(typeName)
	if err != nil {
		return nil, nil, "", err
	}
	version := t.versions[t.current-1]
	lineage, ok := version.byName[identifier]
	if !ok {
		return nil, nil, "", newError(KindPropertyNotFound, "property identifier does not exist: "+identifier)
	}
	return t, version, lineage, nil
}

func (g *Gateway) GrantAttributePermission(_ context.Context, input GrantInput) (err error) {
	g.lock()
	defer func() {
		g.unlock()
		g.logs.append(DecisionLogEntry{Action: "grant_attribute_permission", Input: input, Error: describeError(err)})
	}()
	t, _, lineage, err := g.liveAttribute(input.Type, input.PropertyID, input.Operation)
	if err != nil {
		return err
	}
	if input.Subject == "" {
		return newError(KindInvalidInput, "subject is required")
	}
	key := versionKey(t.current)
	ensureSubjectAttr(t.attrPolicy[key], input.Subject)[attrPolicyKey(lineage, input.Operation)] = policyState{allowed: true, revoked: false}
	t.attrHistory = append(t.attrHistory, permissionRecord{Type: input.Type, Subject: input.Subject, PropertyID: input.PropertyID, LineageID: lineage, Operation: input.Operation, Granted: true, FromVersion: t.current, ToVersion: t.current})
	return nil
}

func (g *Gateway) RevokeAttributePermission(_ context.Context, input GrantInput) (err error) {
	g.lock()
	defer func() {
		g.unlock()
		g.logs.append(DecisionLogEntry{Action: "revoke_attribute_permission", Input: input, Error: describeError(err)})
	}()
	t, _, lineage, err := g.liveAttribute(input.Type, input.PropertyID, input.Operation)
	if err != nil {
		return err
	}
	if input.Subject == "" {
		return newError(KindInvalidInput, "subject is required")
	}
	key := versionKey(t.current)
	ensureSubjectAttr(t.attrPolicy[key], input.Subject)[attrPolicyKey(lineage, input.Operation)] = policyState{allowed: false, revoked: true}
	t.attrHistory = append(t.attrHistory, permissionRecord{Type: input.Type, Subject: input.Subject, PropertyID: input.PropertyID, LineageID: lineage, Operation: input.Operation, Revoked: true, FromVersion: t.current, ToVersion: t.current})
	return nil
}

func (g *Gateway) GrantTypePermission(_ context.Context, input TypeGrantInput) (err error) {
	g.lock()
	defer func() {
		g.unlock()
		g.logs.append(DecisionLogEntry{Action: "grant_type_permission", Input: input, Error: describeError(err)})
	}()
	t, err := g.requireType(input.Type)
	if err != nil || !validOperation(input.Operation) || input.Subject == "" {
		if err == nil {
			err = newError(KindInvalidInput, "valid subject and operation are required")
		}
		return err
	}
	key := versionKey(t.current)
	ensureSubjectType(t.typePolicy[key], input.Subject)[string(input.Operation)] = policyState{allowed: true, revoked: false}
	t.typeHistory = append(t.typeHistory, permissionRecord{Type: input.Type, Subject: input.Subject, PropertyID: typePermissionKey, Operation: input.Operation, Granted: true, FromVersion: t.current, ToVersion: t.current})
	return nil
}

func (g *Gateway) RevokeTypePermission(_ context.Context, input TypeGrantInput) (err error) {
	g.lock()
	defer func() {
		g.unlock()
		g.logs.append(DecisionLogEntry{Action: "revoke_type_permission", Input: input, Error: describeError(err)})
	}()
	t, err := g.requireType(input.Type)
	if err != nil || !validOperation(input.Operation) || input.Subject == "" {
		if err == nil {
			err = newError(KindInvalidInput, "valid subject and operation are required")
		}
		return err
	}
	key := versionKey(t.current)
	ensureSubjectType(t.typePolicy[key], input.Subject)[string(input.Operation)] = policyState{allowed: false, revoked: true}
	t.typeHistory = append(t.typeHistory, permissionRecord{Type: input.Type, Subject: input.Subject, PropertyID: typePermissionKey, Operation: input.Operation, Revoked: true, FromVersion: t.current, ToVersion: t.current})
	return nil
}

func (g *Gateway) AuditPermissionRecords(_ context.Context, query AuditQuery) (records []permissionRecord, err error) {
	g.lock()
	defer g.unlock()
	t, err := g.requireType(query.Type)
	if err != nil {
		return nil, err
	}
	records = append(records, t.attrHistory...)
	records = append(records, t.typeHistory...)
	return records, nil
}

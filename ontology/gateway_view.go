package ontology

import "context"

func (g *Gateway) PermissionView(_ context.Context, typeName string, subject string, versionNumber int) (view *PermissionView, err error) {
	g.lock()
	defer func() {
		g.unlock()
		g.logs.append(DecisionLogEntry{Action: "permission_view", Input: map[string]any{"type": typeName, "subject": subject, "version": versionNumber}, Output: view, Error: describeError(err)})
	}()
	t, err := g.requireType(typeName)
	if err != nil {
		return nil, err
	}
	version, err := t.version(versionNumber)
	if err != nil {
		return nil, err
	}
	attributes := map[string]AttributePermissionView{}
	for _, property := range version.Properties {
		readState := t.attrState(versionNumber, subject, property.LineageID, Read)
		writeState := t.attrState(versionNumber, subject, property.LineageID, Write)
		attributes[property.Identifier] = AttributePermissionView{
			LineageID:    property.LineageID,
			ReadAllowed:  readState.allowed,
			ReadRevoked:  readState.revoked,
			WriteAllowed: writeState.allowed,
			WriteRevoked: writeState.revoked,
		}
	}
	typeRead := t.operationState(versionNumber, subject, Read)
	typeWrite := t.operationState(versionNumber, subject, Write)
	return &PermissionView{
		Type:                      typeName,
		Subject:                   subject,
		Version:                   versionNumber,
		TypeRead:                  typeRead.allowed,
		TypeWrite:                 typeWrite.allowed,
		Attributes:                attributes,
		AccessedVersionRecords:    1,
		AccessedPermissionRecords: 0,
	}, nil
}

func (g *Gateway) DecisionLog() []DecisionLogEntry {
	return g.logs.Entries()
}

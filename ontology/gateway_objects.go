package ontology

import (
	"context"
	"sort"
)

func (g *Gateway) CreateObject(_ context.Context, input CreateObjectInput) (err error) {
	g.lock()
	defer func() {
		g.unlock()
		g.logs.append(DecisionLogEntry{Action: "create_object", Input: input, Error: describeError(err)})
	}()
	t, err := g.requireType(input.Type)
	if err != nil {
		return err
	}
	if input.ID == "" {
		return newError(KindInvalidInput, "object id is required")
	}
	if _, exists := g.objects[input.ID]; exists {
		return newError(KindInvalidInput, "object already exists")
	}
	version := t.versions[t.current-1]
	values := map[string]any{}
	for name, value := range input.Fields {
		lineage, ok := version.byName[name]
		if !ok {
			return newError(KindPropertyNotFound, "property identifier does not exist: "+name)
		}
		values[lineage] = value
	}
	g.objects[input.ID] = &objectState{id: input.ID, typeName: input.Type, version: t.current, values: values, exists: true}
	return nil
}

func (g *Gateway) WriteObject(_ context.Context, input WriteInput) (result *WriteResult, err error) {
	reason := map[string]any{"order": []string{"object_exists", "version_current", "write_permissions", "type_write_policy"}}
	g.lock()
	defer func() {
		g.unlock()
		g.logs.append(DecisionLogEntry{Action: "write_object", Input: input, Output: result, Reason: reason, Error: describeError(err)})
	}()
	object, err := g.requireObject(input.ObjectID)
	if err != nil {
		return nil, err
	}
	t := g.types[object.typeName]
	if input.SchemaVersion != t.current {
		return nil, newError(KindVersionStale, "schema version is not current")
	}
	version := t.versions[t.current-1]
	typeWrite := t.operationState(t.current, input.Actor, Write)
	fieldLineage := map[string]string{}
	var denied []string
	var revoked []string
	var allowed []string
	for field := range input.Fields {
		lineage, ok := version.byName[field]
		if !ok {
			return nil, newError(KindPropertyNotFound, "property identifier does not exist: "+field)
		}
		fieldLineage[field] = lineage
		if typeWrite.allowed && !typeWrite.revoked {
			allowed = append(allowed, field)
			continue
		}
		state := t.attrState(t.current, input.Actor, lineage, Write)
		if state.revoked {
			revoked = append(revoked, field)
			denied = append(denied, field)
			continue
		}
		if !state.allowed {
			denied = append(denied, field)
		}
		if state.allowed {
			allowed = append(allowed, field)
		}
	}
	sort.Strings(denied)
	sort.Strings(revoked)
	sort.Strings(allowed)
	reason["type_write"] = typeWrite
	reason["allowed"] = allowed
	reason["denied"] = denied
	reason["revoked"] = revoked
	reason["write_policy"] = t.writePolicy
	if t.writePolicy == RejectObject && len(denied) > 0 {
		if len(revoked) > 0 {
			return nil, newError(KindPermissionRevoked, "write permission revoked for: "+joinNames(revoked))
		}
		return nil, newError(KindAttributePermission, "write denied for: "+joinNames(denied))
	}
	written := map[string]any{}
	ignored := append([]string(nil), denied...)
	reason["ignored"] = ignored
	for field, value := range input.Fields {
		if contains(denied, field) {
			continue
		}
		lineage := fieldLineage[field]
		object.values[lineage] = value
		written[field] = value
	}
	object.version = t.current
	return &WriteResult{SchemaVersion: t.current, Written: written, Ignored: ignored}, nil
}

func (g *Gateway) ReadObject(_ context.Context, objectID string, actor string) (result *ReadResult, err error) {
	g.lock()
	defer func() {
		g.unlock()
		g.logs.append(DecisionLogEntry{Action: "read_object", Input: map[string]any{"object_id": objectID, "actor": actor}, Output: result, Error: describeError(err)})
	}()
	object, err := g.requireObject(objectID)
	if err != nil {
		return nil, err
	}
	t := g.types[object.typeName]
	typeRead := t.operationState(t.current, actor, Read)
	if typeRead.revoked {
		return nil, newError(KindTypePermissionDenied, "subject has no object-type read permission")
	}
	version := t.versions[t.current-1]
	projected := map[string]any{}
	var removed []string
	allowedCount := 0
	for _, property := range version.Properties {
		allowed := typeRead.allowed
		revoked := typeRead.revoked
		if !allowed {
			state := t.attrState(t.current, actor, property.LineageID, Read)
			allowed = state.allowed
			revoked = state.revoked
		}
		if !allowed || revoked {
			removed = append(removed, property.Identifier)
			continue
		}
		allowedCount++
		value, present := object.values[property.LineageID]
		if present {
			projected[property.Identifier] = value
		}
	}
	sort.Strings(removed)
	if !typeRead.allowed && allowedCount == 0 {
		return nil, newError(KindTypePermissionDenied, "subject has no object-type or readable attribute permission")
	}
	return &ReadResult{ObjectID: objectID, Type: object.typeName, SchemaVersion: t.current, PartialView: len(removed) > 0, Object: projected, Removed: removed}, nil
}

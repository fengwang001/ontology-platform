package ontology

import (
	"context"
)

func (g *Gateway) CreateObjectType(_ context.Context, input CreateTypeInput) (result *TypeVersion, err error) {
	g.lock()
	defer func() {
		g.unlock()
		g.logs.append(DecisionLogEntry{Action: "create_type", Input: input, Output: result, Error: describeError(err)})
	}()
	if input.Name == "" {
		return nil, newError(KindInvalidInput, "object type name is required")
	}
	if _, exists := g.types[input.Name]; exists {
		return nil, newError(KindInvalidInput, "object type already exists")
	}
	if err := validateWritePolicy(input.WritePolicy); err != nil {
		return nil, err
	}
	if err := validateProperties(input.Properties, nil); err != nil {
		return nil, err
	}
	t := newTypeState(input)
	g.types[input.Name] = t
	return t.versions[0], nil
}

func (g *Gateway) EvolveObjectType(_ context.Context, input Evolution) (result *TypeVersion, err error) {
	g.lock()
	defer func() {
		g.unlock()
		g.logs.append(DecisionLogEntry{Action: "evolve_type", Input: input, Output: result, Error: describeError(err)})
	}()
	t, err := g.requireType(input.Type)
	if err != nil {
		return nil, err
	}
	current := t.versions[t.current-1]
	nextProperties, err := evolveProperties(current, input)
	if err != nil {
		return nil, err
	}
	nextVersion := t.current + 1
	currentKey := versionKey(t.current)
	nextKey := versionKey(nextVersion)
	t.attrPolicy[nextKey] = clonePolicyMap(t.attrPolicy, currentKey)[currentKey]
	t.typePolicy[nextKey] = clonePolicyMap(t.typePolicy, currentKey)[currentKey]
	t.current = nextVersion
	version := newTypeVersion(nextVersion, nextProperties)
	t.versions = append(t.versions, version)
	return version, nil
}

func (g *Gateway) ConfigureWritePolicy(_ context.Context, input ConfigureWritePolicyInput) (err error) {
	g.lock()
	defer func() {
		g.unlock()
		g.logs.append(DecisionLogEntry{Action: "configure_write_policy", Input: input, Error: describeError(err)})
	}()
	t, err := g.requireType(input.Type)
	if err != nil {
		return err
	}
	if input.RejectObject == input.IgnoreField {
		return newError(KindWritePolicyConflict, "a type must declare exactly one write policy")
	}
	if input.RejectObject {
		t.writePolicy = RejectObject
	} else {
		t.writePolicy = IgnoreField
	}
	return nil
}

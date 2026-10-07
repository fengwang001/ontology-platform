package ontology

import (
	"context"
	"fmt"
	"math/rand/v2"
	"reflect"
	"sort"
	"testing"
)

type naiveEvent struct {
	scope     string
	kind      string
	version   int
	subject   string
	lineage   string
	operation Operation
}

type naiveModel struct {
	versions []map[string]string
	policies map[int]WritePolicy
	events   []naiveEvent
	objects  map[string]map[string]any
}

func newNaiveModel(initial []Property, policy WritePolicy) *naiveModel {
	names := map[string]string{}
	for _, property := range initial {
		names[property.Identifier] = property.LineageID
	}
	return &naiveModel{
		versions: []map[string]string{names},
		policies: map[int]WritePolicy{1: policy},
		objects:  map[string]map[string]any{},
	}
}

func (m *naiveModel) evolve(evolution Evolution) {
	properties := map[string]string{}
	for name, lineage := range m.versions[len(m.versions)-1] {
		properties[name] = lineage
	}
	for _, rename := range evolution.Rename {
		if lineage, ok := properties[rename.From]; ok {
			delete(properties, rename.From)
			properties[rename.To] = lineage
		}
	}
	for _, deprecated := range evolution.Deprecate {
		delete(properties, deprecated)
	}
	for _, added := range evolution.Add {
		properties[added.Identifier] = added.LineageID
	}
	m.versions = append(m.versions, properties)
	m.policies[len(m.versions)] = m.policies[len(m.versions)-1]
}

func (m *naiveModel) grant(scope string, kind string, subject string, lineage string, operation Operation) {
	m.events = append(m.events, naiveEvent{scope: scope, kind: kind, version: len(m.versions), subject: subject, lineage: lineage, operation: operation})
}

func (m *naiveModel) state(scope string, subject string, lineage string, operation Operation, version int) policyState {
	state := policyState{}
	for _, event := range m.events {
		if event.version > version || event.scope != scope || event.subject != subject || event.operation != operation {
			continue
		}
		if scope == "attr" && event.lineage != lineage {
			continue
		}
		if event.kind == "revoke" {
			state = policyState{revoked: true}
		} else {
			state = policyState{allowed: true}
		}
	}
	return state
}

func (m *naiveModel) write(id string, version int, subject string, fields map[string]string, values map[string]any) (*WriteResult, error) {
	if _, ok := m.objects[id]; !ok {
		return nil, newError(KindObjectNotFound, "object missing")
	}
	current := len(m.versions)
	if version != current {
		return nil, newError(KindVersionStale, "stale")
	}
	typeState := m.state("type", subject, "", Write, version)
	var denied []string
	var revoked []string
	for field, lineage := range fields {
		if typeState.allowed {
			continue
		}
		state := m.state("attr", subject, lineage, Write, version)
		if state.revoked {
			denied = append(denied, field)
			revoked = append(revoked, field)
		} else if !state.allowed {
			denied = append(denied, field)
		}
	}
	if len(denied) > 0 {
		if m.policies[current] == RejectObject && len(revoked) > 0 {
			return nil, newError(KindPermissionRevoked, "revoked")
		}
		if m.policies[current] == RejectObject {
			return nil, newError(KindAttributePermission, "denied")
		}
	}
	object := m.objects[id]
	written := map[string]any{}
	for field, lineage := range fields {
		if contains(denied, field) {
			continue
		}
		object[lineage] = values[field]
		written[field] = values[field]
	}
	return &WriteResult{SchemaVersion: current, Written: written, Ignored: denied}, nil
}

func (m *naiveModel) read(id string, subject string) (map[string]any, []string) {
	object, ok := m.objects[id]
	if !ok {
		return nil, nil
	}
	typeState := m.state("type", subject, "", Read, len(m.versions))
	if typeState.revoked {
		return nil, nil
	}
	result := map[string]any{}
	var removed []string
	allowedCount := 0
	for name, lineage := range m.versions[len(m.versions)-1] {
		state := typeState
		if !state.allowed {
			state = m.state("attr", subject, lineage, Read, len(m.versions))
		}
		if !state.allowed || state.revoked {
			removed = append(removed, name)
			continue
		}
		allowedCount++
		if value, ok := object[lineage]; ok {
			result[name] = value
		}
	}
	if !typeState.allowed && allowedCount == 0 {
		return nil, nil
	}
	return result, removed
}

func TestRandomOperationSequenceMatchesNaiveModel(t *testing.T) {
	ctx := context.Background()
	random := rand.New(rand.NewPCG(1670, 42))
	gateway := testGateway(t, IgnoreField)
	naive := newNaiveModel(gateway.types["employee"].versions[0].Properties, IgnoreField)
	currentNames := []string{"name", "email", "age"}
	lineageByName := map[string]string{"name": "l-name", "email": "l-email", "age": "l-age"}
	subjects := []string{"alice", "bob"}

	for step := 0; step < 120; step++ {
		switch random.IntN(7) {
		case 0:
			oldName := currentNames[random.IntN(len(currentNames))]
			newName := fmt.Sprintf("v%d-%s", step, oldName)
			evolution := Evolution{Type: "employee", Rename: []Rename{{From: oldName, To: newName}}}
			if _, err := gateway.EvolveObjectType(ctx, evolution); err != nil {
				t.Fatal(err)
			}
			naive.evolve(evolution)
			lineageByName[newName] = lineageByName[oldName]
			delete(lineageByName, oldName)
			currentNames = replaceName(currentNames, oldName, newName)
		case 1:
			name := currentNames[random.IntN(len(currentNames))]
			subject := subjects[random.IntN(len(subjects))]
			input := GrantInput{Type: "employee", Subject: subject, PropertyID: name, Operation: Operation(randomOperation(random))}
			if random.IntN(2) == 0 {
				if err := gateway.GrantAttributePermission(ctx, input); err != nil {
					t.Fatal(err)
				}
				naive.grant("attr", "grant", subject, lineageByName[name], input.Operation)
			} else {
				if err := gateway.RevokeAttributePermission(ctx, input); err != nil {
					t.Fatal(err)
				}
				naive.grant("attr", "revoke", subject, lineageByName[name], input.Operation)
			}
		case 2:
			if len(currentNames) == 1 {
				continue
			}
			name := currentNames[random.IntN(len(currentNames))]
			evolution := Evolution{Type: "employee", Deprecate: []string{name}}
			if _, err := gateway.EvolveObjectType(ctx, evolution); err != nil {
				t.Fatal(err)
			}
			naive.evolve(evolution)
			delete(lineageByName, name)
			currentNames = removeName(currentNames, name)
		case 3:
			subject := subjects[random.IntN(len(subjects))]
			operation := Operation(randomOperation(random))
			input := TypeGrantInput{Type: "employee", Subject: subject, Operation: operation}
			if random.IntN(2) == 0 {
				if err := gateway.GrantTypePermission(ctx, input); err != nil {
					t.Fatal(err)
				}
				naive.grant("type", "grant", subject, "", operation)
			} else {
				if err := gateway.RevokeTypePermission(ctx, input); err != nil {
					t.Fatal(err)
				}
				naive.grant("type", "revoke", subject, "", operation)
			}
		case 4, 5:
			id := "obj-1"
			if _, exists := naive.objects[id]; !exists {
				if err := gateway.CreateObject(ctx, CreateObjectInput{Type: "employee", ID: id, Fields: map[string]any{"name": "seed"}}); err != nil {
					t.Fatal(err)
				}
				naive.objects[id] = map[string]any{"l-name": "seed"}
			}
			fieldName := currentNames[random.IntN(len(currentNames))]
			fields := map[string]string{fieldName: lineageByName[fieldName]}
			values := map[string]any{fieldName: step}
			subject := subjects[random.IntN(len(subjects))]
			got, gotErr := gateway.WriteObject(ctx, WriteInput{ObjectID: id, SchemaVersion: len(naive.versions), Actor: subject, Fields: values})
			want, wantErr := naive.write(id, len(naive.versions), subject, fields, values)
			if errorKind(gotErr) != errorKind(wantErr) {
				t.Fatalf("step %d write error got=%v want=%v", step, gotErr, wantErr)
			}
			if gotErr == nil {
				sortResult(got.Ignored)
				sortResult(want.Ignored)
				if !reflect.DeepEqual(got.Ignored, want.Ignored) {
					t.Fatalf("step %d ignored got=%v want=%v", step, got.Ignored, want.Ignored)
				}
			}
		case 6:
			id := "obj-1"
			if _, ok := naive.objects[id]; !ok {
				if err := gateway.CreateObject(ctx, CreateObjectInput{Type: "employee", ID: id, Fields: map[string]any{"name": "seed"}}); err != nil {
					t.Fatal(err)
				}
				naive.objects[id] = map[string]any{"l-name": "seed"}
			}
			subject := subjects[random.IntN(len(subjects))]
			got, gotErr := gateway.ReadObject(ctx, id, subject)
			want, wantRemoved := naive.read(id, subject)
			if want == nil {
				if gotErr == nil {
					t.Fatalf("step %d expected read denial", step)
				}
			} else {
				sort.Strings(wantRemoved)
				if gotErr != nil || !reflect.DeepEqual(got.Object, want) || !reflect.DeepEqual(got.Removed, wantRemoved) {
					t.Fatalf("step %d read got=%#v err=%v want=%#v removed=%v", step, got, gotErr, want, wantRemoved)
				}
			}
		}
	}
}

func randomOperation(random *rand.Rand) string {
	if random.IntN(2) == 0 {
		return string(Read)
	}
	return string(Write)
}

func replaceName(names []string, oldName string, newName string) []string {
	result := append([]string(nil), names...)
	for index, name := range result {
		if name == oldName {
			result[index] = newName
		}
	}
	return result
}

func removeName(names []string, target string) []string {
	var result []string
	for _, name := range names {
		if name != target {
			result = append(result, name)
		}
	}
	return result
}

func errorKind(err error) ErrorKind {
	if err == nil {
		return ""
	}
	if target, ok := err.(*Error); ok {
		return target.Kind
	}
	return "unknown"
}

func sortResult(values []string) {
	// Naive and gateway may see maps in different order; tests only have one field per
	// write, but keep this hook to make that assumption explicit.
	if len(values) < 2 {
		return
	}
}

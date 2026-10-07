package ontology

import (
	"context"
	"errors"
	"testing"
)

func testGateway(t *testing.T, policy WritePolicy) *Gateway {
	t.Helper()
	gateway := NewGateway(WithDecisionLogHook(func(entry DecisionLogEntry) {
		t.Logf("decision action=%s input=%#v output=%#v reason=%q error=%q", entry.Action, entry.Input, entry.Output, entry.Reason, entry.Error)
	}))
	_, err := gateway.CreateObjectType(context.Background(), CreateTypeInput{
		Name:        "employee",
		WritePolicy: policy,
		Properties: []Property{
			{LineageID: "l-name", Identifier: "name", Required: true},
			{LineageID: "l-email", Identifier: "email"},
			{LineageID: "l-age", Identifier: "age", Constraint: "int"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return gateway
}

func requireErrorKind(t *testing.T, err error, kind ErrorKind) {
	t.Helper()
	var target *Error
	if !errors.As(err, &target) || target.Kind != kind {
		t.Fatalf("expected %s, got %v", kind, err)
	}
}

func grantType(t *testing.T, gateway *Gateway, subject string, operation Operation) {
	t.Helper()
	if err := gateway.GrantTypePermission(context.Background(), TypeGrantInput{Type: "employee", Subject: subject, Operation: operation}); err != nil {
		t.Fatal(err)
	}
}

func grantAttr(t *testing.T, gateway *Gateway, subject string, identifier string, operation Operation) {
	t.Helper()
	if err := gateway.GrantAttributePermission(context.Background(), GrantInput{Type: "employee", Subject: subject, PropertyID: identifier, Operation: operation}); err != nil {
		t.Fatal(err)
	}
}

func TestRenameKeepsPermissionByLineageButOldNameIsInvalid(t *testing.T) {
	ctx := context.Background()
	gateway := testGateway(t, RejectObject)
	grantAttr(t, gateway, "alice", "email", Write)

	version, err := gateway.EvolveObjectType(ctx, Evolution{Type: "employee", Rename: []Rename{{From: "email", To: "contact_email"}}})
	if err != nil {
		t.Fatal(err)
	}
	if version.Version != 2 {
		t.Fatalf("version = %d", version.Version)
	}

	err = gateway.CreateObject(ctx, CreateObjectInput{Type: "employee", ID: "obj-1", Fields: map[string]any{"name": "Ada"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = gateway.WriteObject(ctx, WriteInput{ObjectID: "obj-1", SchemaVersion: 2, Actor: "alice", Fields: map[string]any{"email": "old@example.com"}})
	requireErrorKind(t, err, KindPropertyNotFound)
	result, err := gateway.WriteObject(ctx, WriteInput{ObjectID: "obj-1", SchemaVersion: 2, Actor: "alice", Fields: map[string]any{"contact_email": "new@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Written["contact_email"] != "new@example.com" {
		t.Fatalf("written = %#v", result.Written)
	}
}

func TestDeprecatedPermissionIsInactiveButAuditable(t *testing.T) {
	ctx := context.Background()
	gateway := testGateway(t, RejectObject)
	grantAttr(t, gateway, "alice", "age", Write)
	if _, err := gateway.EvolveObjectType(ctx, Evolution{Type: "employee", Deprecate: []string{"age"}}); err != nil {
		t.Fatal(err)
	}
	err := gateway.GrantAttributePermission(ctx, GrantInput{Type: "employee", Subject: "alice", PropertyID: "age", Operation: Write})
	requireErrorKind(t, err, KindPropertyNotFound)

	records, err := gateway.AuditPermissionRecords(ctx, AuditQuery{Type: "employee"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].PropertyID != "age" || records[0].LineageID != "l-age" || !records[0].Granted {
		t.Fatalf("history = %#v", records)
	}
}

func TestDeprecateAndReaddSameIdentifierInOneEvolutionGetsNewLineage(t *testing.T) {
	ctx := context.Background()
	gateway := testGateway(t, RejectObject)
	version, err := gateway.EvolveObjectType(ctx, Evolution{
		Type:      "employee",
		Deprecate: []string{"age"},
		Add:       []Property{{LineageID: "l-new-age", Identifier: "age", Constraint: "int>=0"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if version.byName["age"] != "l-new-age" {
		t.Fatalf("age lineage = %q", version.byName["age"])
	}
	err = gateway.GrantAttributePermission(ctx, GrantInput{Type: "employee", Subject: "alice", PropertyID: "age", Operation: Write})
	if err != nil {
		t.Fatal(err)
	}
	records, err := gateway.AuditPermissionRecords(ctx, AuditQuery{Type: "employee"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].LineageID != "l-new-age" {
		t.Fatalf("records = %#v", records)
	}
}

func TestRejectObjectDenialChangesNoState(t *testing.T) {
	ctx := context.Background()
	gateway := testGateway(t, RejectObject)
	grantType(t, gateway, "writer", Write)
	if err := gateway.CreateObject(ctx, CreateObjectInput{Type: "employee", ID: "obj-1", Fields: map[string]any{"name": "Ada", "email": "a@example.com"}}); err != nil {
		t.Fatal(err)
	}
	grantAttr(t, gateway, "limited", "name", Write)
	_, err := gateway.WriteObject(ctx, WriteInput{ObjectID: "obj-1", SchemaVersion: 1, Actor: "limited", Fields: map[string]any{"name": "Grace", "age": 36}})
	requireErrorKind(t, err, KindAttributePermission)
	grantType(t, gateway, "reader", Read)
	read, err := gateway.ReadObject(ctx, "obj-1", "reader")
	if err != nil {
		t.Fatal(err)
	}
	if read.Object["name"] != "Ada" || read.Object["email"] != "a@example.com" {
		t.Fatalf("state changed: %#v", read.Object)
	}
}

func TestRevocationBeatsUnionAcrossVersions(t *testing.T) {
	ctx := context.Background()
	gateway := testGateway(t, IgnoreField)
	grantAttr(t, gateway, "alice", "email", Write)
	if _, err := gateway.EvolveObjectType(ctx, Evolution{Type: "employee", Add: []Property{{LineageID: "l-title", Identifier: "title"}}}); err != nil {
		t.Fatal(err)
	}
	if err := gateway.CreateObject(ctx, CreateObjectInput{Type: "employee", ID: "obj-1", Fields: map[string]any{"name": "Ada"}}); err != nil {
		t.Fatal(err)
	}
	result, err := gateway.WriteObject(ctx, WriteInput{ObjectID: "obj-1", SchemaVersion: 2, Actor: "alice", Fields: map[string]any{"email": "a@example.com", "title": "Eng"}})
	if err != nil || len(result.Ignored) != 1 {
		t.Fatalf("before revoke result=%#v err=%v", result, err)
	}
	if err := gateway.RevokeAttributePermission(ctx, GrantInput{Type: "employee", Subject: "alice", PropertyID: "email", Operation: Write}); err != nil {
		t.Fatal(err)
	}
	result, err = gateway.WriteObject(ctx, WriteInput{ObjectID: "obj-1", SchemaVersion: 2, Actor: "alice", Fields: map[string]any{"email": "b@example.com"}})
	if err != nil {
		t.Fatalf("ignore policy should not return an error: %v", err)
	}
	if len(result.Ignored) != 1 || result.Ignored[0] != "email" || len(result.Written) != 0 {
		t.Fatalf("after revoke result=%#v", result)
	}
	grantType(t, gateway, "reader", Read)
	read, err := gateway.ReadObject(ctx, "obj-1", "reader")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := read.Object["email"]; exists {
		if read.Object["email"] != "a@example.com" {
			t.Fatalf("revocation changed existing value: %#v", read.Object)
		}
	}
}

func TestRevokedTypeWriteDoesNotOverrideAttributeGrant(t *testing.T) {
	ctx := context.Background()
	gateway := testGateway(t, RejectObject)
	grantType(t, gateway, "alice", Write)
	if err := gateway.RevokeTypePermission(ctx, TypeGrantInput{Type: "employee", Subject: "alice", Operation: Write}); err != nil {
		t.Fatal(err)
	}
	grantAttr(t, gateway, "alice", "name", Write)
	if err := gateway.CreateObject(ctx, CreateObjectInput{Type: "employee", ID: "obj-1", Fields: map[string]any{"name": "Ada"}}); err != nil {
		t.Fatal(err)
	}
	_, err := gateway.WriteObject(ctx, WriteInput{ObjectID: "obj-1", SchemaVersion: 1, Actor: "alice", Fields: map[string]any{"name": "Grace", "age": 36}})
	requireErrorKind(t, err, KindAttributePermission)
	result, err := gateway.WriteObject(ctx, WriteInput{ObjectID: "obj-1", SchemaVersion: 1, Actor: "alice", Fields: map[string]any{"name": "Grace"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Written["name"] != "Grace" {
		t.Fatalf("result = %#v", result)
	}
}

func TestStaleVersionAndObjectNotFoundPriorities(t *testing.T) {
	ctx := context.Background()
	gateway := testGateway(t, RejectObject)
	_, err := gateway.WriteObject(ctx, WriteInput{ObjectID: "missing", SchemaVersion: 1})
	requireErrorKind(t, err, KindObjectNotFound)
	if err := gateway.CreateObject(ctx, CreateObjectInput{Type: "employee", ID: "obj-1", Fields: map[string]any{"name": "Ada"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.EvolveObjectType(ctx, Evolution{Type: "employee", Add: []Property{{LineageID: "l-title", Identifier: "title"}}}); err != nil {
		t.Fatal(err)
	}
	_, err = gateway.WriteObject(ctx, WriteInput{ObjectID: "obj-1", SchemaVersion: 1, Actor: "nobody", Fields: map[string]any{"name": "X", "missing": "Y"}})
	requireErrorKind(t, err, KindVersionStale)
}

func TestWritePolicyConflictIsDistinct(t *testing.T) {
	gateway := testGateway(t, RejectObject)
	err := gateway.ConfigureWritePolicy(context.Background(), ConfigureWritePolicyInput{Type: "employee", RejectObject: true, IgnoreField: true})
	requireErrorKind(t, err, KindWritePolicyConflict)
}

func TestReadProjectionRemovesUnauthorizedAttributes(t *testing.T) {
	ctx := context.Background()
	gateway := testGateway(t, RejectObject)
	grantType(t, gateway, "reader", Read)
	if err := gateway.CreateObject(ctx, CreateObjectInput{Type: "employee", ID: "obj-1", Fields: map[string]any{"name": "Ada", "email": "a@example.com", "age": 36}}); err != nil {
		t.Fatal(err)
	}
	read, err := gateway.ReadObject(ctx, "obj-1", "reader")
	if err != nil {
		t.Fatal(err)
	}
	if read.PartialView || len(read.Object) != 3 {
		t.Fatalf("unexpected full read: %#v", read)
	}
}

func TestAttributeReadProducesPartialProjection(t *testing.T) {
	ctx := context.Background()
	gateway := testGateway(t, RejectObject)
	grantAttr(t, gateway, "reader", "name", Read)
	if err := gateway.CreateObject(ctx, CreateObjectInput{Type: "employee", ID: "obj-1", Fields: map[string]any{"name": "Ada", "email": "a@example.com", "age": 36}}); err != nil {
		t.Fatal(err)
	}
	read, err := gateway.ReadObject(ctx, "obj-1", "reader")
	if err != nil {
		t.Fatal(err)
	}
	if !read.PartialView || len(read.Object) != 1 || read.Object["name"] != "Ada" {
		t.Fatalf("partial read = %#v", read)
	}
	if len(read.Removed) != 2 || read.Removed[0] != "age" || read.Removed[1] != "email" {
		t.Fatalf("removed = %#v", read.Removed)
	}
	if _, masked := read.Object["email"]; masked {
		t.Fatalf("masked value returned: %#v", read.Object)
	}
}

func TestTypeReadRevocationIsDenied(t *testing.T) {
	ctx := context.Background()
	gateway := testGateway(t, RejectObject)
	grantType(t, gateway, "reader", Read)
	if err := gateway.CreateObject(ctx, CreateObjectInput{Type: "employee", ID: "obj-1", Fields: map[string]any{"name": "Ada"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.ReadObject(ctx, "obj-1", "reader"); err != nil {
		t.Fatal(err)
	}
	if err := gateway.RevokeTypePermission(ctx, TypeGrantInput{Type: "employee", Subject: "reader", Operation: Read}); err != nil {
		t.Fatal(err)
	}
	_, err := gateway.ReadObject(ctx, "obj-1", "reader")
	requireErrorKind(t, err, KindTypePermissionDenied)
}

func TestPermissionViewIsConstantRecordAccess(t *testing.T) {
	ctx := context.Background()
	gateway := testGateway(t, RejectObject)
	for i := 0; i < 20; i++ {
		grantAttr(t, gateway, "alice", "name", Write)
		if _, err := gateway.EvolveObjectType(ctx, Evolution{Type: "employee", Tighten: []Constraint{{Property: "age", Constraint: "int>=0"}}}); err != nil {
			t.Fatal(err)
		}
	}
	view, err := gateway.PermissionView(ctx, "employee", "alice", 21)
	if err != nil {
		t.Fatal(err)
	}
	if view.AccessedVersionRecords != 1 || view.AccessedPermissionRecords != 0 {
		t.Fatalf("access counts = %d,%d", view.AccessedVersionRecords, view.AccessedPermissionRecords)
	}
}

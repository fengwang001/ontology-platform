package ontology

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func errKind(err error) ErrorKind {
	var ge *GatewayError
	if errors.As(err, &ge) {
		return ge.Kind
	}
	return ""
}

func newTestGateway(t *testing.T) (*Gateway, *bytes.Buffer) {
	t.Helper()
	var log bytes.Buffer
	return NewGateway(WriteLogger{W: &log}), &log
}

func bg() context.Context { return context.Background() }

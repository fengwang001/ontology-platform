// Package maperr maps typed errors to HTTP status codes and serializes
// them with full type fidelity (code + message + context + cause tree).
package maperr

import (
	"encoding/json"
	"errors"

	"ontology/aggregate"
	"ontology/errdef"
	"ontology/wrap"
)

// kindTag discriminates wire nodes.
type kindTag string

const (
	kindTyped kindTag = "typed"
	kindWrap  kindTag = "wrap"
	kindAgg   kindTag = "agg"
)

type wireNode struct {
	Kind     kindTag     `json:"kind"`
	Code     errdef.Code `json:"code"`
	Message  string      `json:"message"`
	Object   string      `json:"object,omitempty"`
	Property string      `json:"property,omitempty"`
	Cause    *wireNode   `json:"cause,omitempty"`
	Kids     []*wireNode `json:"kids,omitempty"`
}

type multiUnwrapper interface{ Unwrap() []error }

type rawMessager interface{ Message() string }

func contextOf(err error) (object, property string) {
	if cp, ok := err.(errdef.ContextProvider); ok {
		return cp.Object(), cp.Property()
	}
	return "", ""
}

func codeOf(err error) errdef.Code {
	if cp, ok := err.(errdef.CodeProvider); ok {
		return cp.ErrCode()
	}
	return errdef.CodeInternal
}

func isWrap(err error) bool {
	switch err.(type) {
	case *errdef.NotFoundError, *errdef.AlreadyExistsError, *errdef.ConflictError,
		*errdef.ValidationError, *errdef.PermissionDeniedError, *errdef.InternalError,
		*errdef.GenericError:
		return false
	}
	return true
}

func encode(err error) *wireNode {
	if err == nil {
		return nil
	}
	obj, prop := contextOf(err)
	n := &wireNode{Kind: kindTyped, Code: codeOf(err), Message: rawMessage(err), Object: obj, Property: prop}
	if kids, ok := err.(multiUnwrapper); ok {
		n.Kind = kindAgg
		for _, k := range kids.Unwrap() {
			n.Kids = append(n.Kids, encode(k))
		}
		return n
	}
	if isWrap(err) {
		n.Kind = kindWrap
	}
	if c := errors.Unwrap(err); c != nil {
		n.Cause = encode(c)
	}
	return n
}

func rawMessage(err error) string {
	if m, ok := err.(rawMessager); ok {
		return m.Message()
	}
	return err.Error()
}

func decode(n *wireNode) error {
	if n == nil {
		return nil
	}
	if n.Kind == kindAgg {
		kids := make([]error, 0, len(n.Kids))
		for _, k := range n.Kids {
			kids = append(kids, decode(k))
		}
		return aggregate.Rebuild(kids)
	}
	cause := decode(n.Cause)
	if n.Kind == kindWrap {
		return wrap.Rebuild(n.Code, n.Message, n.Object, n.Property, cause)
	}
	return errdef.Rebuild(n.Code, n.Message, n.Object, n.Property, cause)
}

// Marshal encodes err preserving code, context and the full cause tree.
func Marshal(err error) ([]byte, error) {
	return json.Marshal(encode(err))
}

// Unmarshal rebuilds concrete types by code; unknown/future codes become
// a code-bearing *GenericError instead of panicking.
func Unmarshal(data []byte) (error, error) {
	var n wireNode
	if err := json.Unmarshal(data, &n); err != nil {
		return nil, err
	}
	return decode(&n), nil
}

var httpByCode = map[errdef.Code]int{
	errdef.CodeNotFound:         404,
	errdef.CodeAlreadyExists:    409,
	errdef.CodeConflict:         409,
	errdef.CodeValidation:       400,
	errdef.CodePermissionDenied: 403,
	errdef.CodeInternal:         500,
}

// HTTPStatus maps an error to a status code. Aggregates take the first
// child's code; unknown codes degrade to 500.
func HTTPStatus(err error) int {
	if err == nil {
		return 200
	}
	if kids, ok := err.(multiUnwrapper); ok {
		if ks := kids.Unwrap(); len(ks) > 0 {
			return HTTPStatus(ks[0])
		}
	}
	if status, ok := httpByCode[codeOf(err)]; ok {
		return status
	}
	return 500
}

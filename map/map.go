// Package errmap maps typed errors to HTTP status codes and serializes them
// with type fidelity: round-tripped errors still satisfy errors.Is.
package errmap

import (
	"encoding/json"
	"errors"

	"ontology/aggregate"
	"ontology/errdef"
	"ontology/wrap"
)

// HTTPStatus maps an error to an HTTP status code by type. Unknown types and
// the base class degrade to 500. Aggregates use the first child's type.
func HTTPStatus(err error) int {
	if err == nil {
		return 200
	}
	var agg *aggregate.Aggregate
	if errors.As(err, &agg) {
		if first, ok := agg.At(0); ok {
			return HTTPStatus(first)
		}
		return 500
	}
	var typed *errdef.Error
	if !errors.As(err, &typed) {
		return 500
	}
	switch typed.Code() {
	case errdef.CodeNotFound:
		return 404
	case errdef.CodeAlreadyExists, errdef.CodeConflict:
		return 409
	case errdef.CodeValidation:
		return 400
	case errdef.CodePermissionDenied:
		return 403
	default:
		return 500
	}
}

// node is the wire representation. Aggregate uses Errors; a context wrapper
// sets Wrap=true; a typed error carries Code plus object/property context.
type node struct {
	Code      string  `json:"code,omitempty"`
	Message   string  `json:"message"`
	Object    string  `json:"object,omitempty"`
	Property  string  `json:"property,omitempty"`
	Wrap      bool    `json:"wrap,omitempty"`
	Aggregate bool    `json:"aggregate,omitempty"`
	Errors    []*node `json:"errors,omitempty"`
	Cause     *node   `json:"cause,omitempty"`
}

var knownCodes = map[errdef.Code]bool{
	errdef.CodeUnknown:          true,
	errdef.CodeNotFound:         true,
	errdef.CodeAlreadyExists:    true,
	errdef.CodeConflict:         true,
	errdef.CodeValidation:       true,
	errdef.CodePermissionDenied: true,
	errdef.CodeInternal:         true,
}

func encode(err error) *node {
	if err == nil {
		return nil
	}
	if agg, ok := err.(*aggregate.Aggregate); ok {
		n := &node{Aggregate: true, Message: agg.Error(),
			Errors: make([]*node, 0, agg.Len())}
		agg.Range(func(_ int, child error) bool {
			n.Errors = append(n.Errors, encode(child))
			return true
		})
		return n
	}
	if object, property, ok := wrap.ContextOf(err); ok {
		return &node{Wrap: true, Object: object, Property: property,
			Message: err.Error(), Cause: encode(wrap.Unwrap(err))}
	}
	var typed *errdef.Error
	if errors.As(err, &typed) {
		code := string(typed.Code())
		if typed.RawCode() != "" {
			code = typed.RawCode()
		}
		n := &node{Code: code, Message: typed.Message(),
			Object: typed.Object(), Property: typed.Property(),
			Cause: encode(typed.Cause())}
		return n
	}
	return &node{Message: err.Error()}
}

func decode(n *node) error {
	if n == nil {
		return nil
	}
	if n.Aggregate {
		children := make([]error, 0, len(n.Errors))
		for _, child := range n.Errors {
			if err := decode(child); err != nil {
				children = append(children, err)
			}
		}
		return aggregate.New(children...)
	}
	var inner error
	if n.Cause != nil {
		inner = decode(n.Cause)
	}
	if n.Wrap {
		return wrap.Wrap(inner, wrap.WithObject(n.Object),
			wrap.WithProperty(n.Property))
	}
	opts := []errdef.Option{errdef.WithObject(n.Object),
		errdef.WithProperty(n.Property)}
	if inner != nil {
		opts = append(opts, errdef.WithCause(inner))
	}
	code := errdef.Code(n.Code)
	if n.Code != "" && !knownCodes[code] {
		return errdef.NewUnknown(n.Code, n.Message, opts...) // future code: degrade, no panic
	}
	if n.Code == "" {
		code = errdef.CodeUnknown
	}
	return errdef.New(code, n.Message, opts...)
}

// Marshal serializes an error with code, context and cause preserved.
func Marshal(err error) ([]byte, error) {
	return json.Marshal(encode(err))
}

// Unmarshal rebuilds the typed error; errors.Is still works afterwards.
func Unmarshal(data []byte) (error, error) {
	var n node
	if err := json.Unmarshal(data, &n); err != nil {
		return nil, err
	}
	return decode(&n), nil
}

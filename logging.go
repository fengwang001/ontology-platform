package ontology

import (
	"fmt"
	"io"
)

func (r *Registry) logInstantiate(def string, args []Type, result Result, err error) {
	if r.logger == nil {
		return
	}
	decision := "hit"
	if err != nil {
		decision = "rejected:" + string(codeOf(err))
	} else if !result.Hit {
		decision = "created"
	}
	line := fmt.Sprintf("instantiate def=%s args=%v decision=%s output=%s\n", def, args, decision, formatResult(result, err))
	r.logMu.Lock()
	defer r.logMu.Unlock()
	_, _ = io.WriteString(r.logger, line)
}

func formatResult(result Result, err error) string {
	if err != nil {
		return "error=" + err.Error()
	}
	if result.Instance == nil {
		return "none"
	}
	return fmt.Sprintf("id=%d stale=%t", result.Instance.ID, result.Instance.Stale)
}

func codeOf(err error) ErrorCode {
	if typed, ok := err.(*Error); ok {
		return typed.Code
	}
	return ErrorCode("error")
}

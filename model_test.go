package incremental

import (
	"fmt"
	"strings"
)

type scriptChecker struct {
	signatureCalls      map[DeclID]int
	implementationCalls map[DeclID]int
}

func newScriptChecker() *scriptChecker {
	return &scriptChecker{
		signatureCalls:      map[DeclID]int{},
		implementationCalls: map[DeclID]int{},
	}
}

func (c *scriptChecker) CheckSignature(context *CheckContext, decl Declaration) Signature {
	c.signatureCalls[decl.ID]++
	output, refs, errText := parseScript(decl.SignatureSource)
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		read := context.Signature(ref)
		parts = append(parts, string(read.Value))
		if !read.Exists() && errText == "" {
			errText = read.ErrorText
		}
	}
	if output == "SAME" {
		return Signature{Value: "same"}
	}
	if errText != "" {
		return Signature{Value: output, ErrorText: errText}
	}
	return Signature{Value: output + "|" + strings.Join(parts, ",")}
}

func (c *scriptChecker) CheckImplementation(context *CheckContext, decl Declaration) Signature {
	c.implementationCalls[decl.ID]++
	output, refs, errText := parseScript(decl.ImplementationSource)
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		read := context.Signature(ref)
		parts = append(parts, string(read.Value))
		if !read.Exists() && errText == "" {
			errText = read.ErrorText
		}
	}
	if errText != "" {
		return Signature{Value: output, ErrorText: errText}
	}
	return Signature{Value: output + "|" + strings.Join(parts, ",")}
}

func parseScript(source string) (string, []DeclID, string) {
	parts := strings.Split(source, ";")
	output := strings.TrimSpace(parts[0])
	refs := []DeclID{}
	referenceText := ""
	if len(parts) > 1 {
		referenceText = parts[1]
	}
	for _, token := range strings.Fields(referenceText) {
		if token != "" {
			id := DeclID(token)
			duplicate := false
			for _, existing := range refs {
				if existing == id {
					duplicate = true
				}
			}
			if !duplicate {
				refs = append(refs, id)
			}
		}
	}
	if strings.HasPrefix(output, "ERR:") {
		return "", refs, strings.TrimSpace(strings.TrimPrefix(output, "ERR:"))
	}
	return output, refs, ""
}

func script(output string, refs ...DeclID) string {
	tokens := make([]string, len(refs))
	for i, ref := range refs {
		tokens[i] = string(ref)
	}
	return output + ";" + strings.Join(tokens, " ")
}

type logRecorder struct {
	events []string
}

func (r *logRecorder) Log(event string, args ...any) {
	r.events = append(r.events, fmt.Sprintf("%s %v", event, args))
}

func mustApply(t testingT, scheduler *Scheduler, edit Edit) EditOutcome {
	t.Helper()
	outcome, err := scheduler.Apply(edit)
	if err != nil {
		t.Fatalf("Apply(%v): %v", edit, err)
	}
	return outcome
}

type testingT interface {
	Helper()
	Fatalf(string, ...any)
}

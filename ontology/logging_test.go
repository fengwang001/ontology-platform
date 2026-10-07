package ontology

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// 日志需完整记录每次判定的输入、最终输出以及裁决依据（标签与角色）。
func TestDecisionLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	e := New(logger)
	must(t, e.AddObjectType("O"))
	must(t, e.AddTag("T"))
	must(t, e.AttachTag(Attachment{ObjectType: "O", Tag: "T"}))
	must(t, e.AddRole("parent"))
	must(t, e.AddRole("child", "parent"))
	must(t, e.AddSubject("s", "child"))
	must(t, e.AddInstance("i", "O"))
	must(t, e.AddGrant(Grant{Role: "parent", Tag: "T", Effect: Allow}))

	d := e.Decide("s", "i")
	if !d.Allowed {
		t.Fatalf("应允许: %+v", d)
	}
	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("判定未产生日志")
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("日志不是合法 JSON: %v", err)
	}
	// 输入与最终输出。
	for _, key := range []string{"subject", "instance", "allowed", "reason"} {
		if _, ok := rec[key]; !ok {
			t.Fatalf("日志缺少字段 %s: %v", key, rec)
		}
	}
	if rec["subject"] != "s" || rec["instance"] != "i" || rec["allowed"] != true {
		t.Fatalf("日志输入/输出不正确: %v", rec)
	}
	// 裁决依据：标签来源与角色授权证据。
	verdicts, ok := rec["verdicts"].([]any)
	if !ok || len(verdicts) != 1 {
		t.Fatalf("日志缺少裁决依据: %v", rec)
	}
	v := verdicts[0].(map[string]any)
	grants := v["Grants"].([]any)
	if len(grants) != 1 {
		t.Fatalf("日志缺少角色授权证据: %v", v)
	}
	g := grants[0].(map[string]any)
	if g["Role"] != "parent" || g["Inherited"] != true {
		t.Fatalf("授权证据应记录继承自上级角色: %v", g)
	}
	sources := v["Sources"].([]any)
	if len(sources) != 1 {
		t.Fatalf("日志缺少标签来源证据: %v", v)
	}
}

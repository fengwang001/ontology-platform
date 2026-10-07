package dlabel

import "testing"

// TestAuditLogCompleteness 验证每次调用都完整记录输入、最终输出与裁决依据，
// 且被拒绝的调用同样被记录。
func TestAuditLogCompleteness(t *testing.T) {
	log := NewMemoryAuditLog(0)
	p := NewPlatform([]string{"root"}, WithAuditLog(log), WithRetainedVersions(1000))
	must(t, p.RegisterObjectType("root", "O", map[string]ValueKind{"a": KindInt}))
	must(t, p.SetRule("root", Rule{ObjectType: "O", Tag: "hot", Body: AttrAtom("a", OpEq, IntValue(1))}))
	must(t, p.SetGrant("root", Grant{Subject: "u", Tag: "hot",
		Read: EffectAllow, Visibility: EffectAllow, Attrs: []string{"a"}}))
	must(t, p.CreateInstance("root", "O", "i", map[string]Value{"a": IntValue(1)}))

	// 成功读取：记录输入与输出。
	res, err := p.Begin("u").ReadAttributes("u", "O", "i", []string{"a"})
	must(t, err)
	if !res.Attrs["a"].Allowed {
		t.Fatal("expected allow")
	}

	// 被拒绝的可见性（无任何标签授权的主体）：仍然必须有日志。
	_, err = p.Begin("v").ReadAttributes("v", "O", "i", []string{"a"})
	if err == nil {
		t.Fatal("v must be invisible/denied")
	}

	// 被拒绝的写入：也必须有日志。
	must(t, p.SetGrant("root", Grant{Subject: "w", Tag: "hot",
		Read: EffectAllow, Write: EffectDeny, Visibility: EffectAllow, Attrs: []string{"a"}}))
	_ = p.WriteInstance("w", "O", "i", map[string]Value{"a": IntValue(2)})

	entries := log.Entries()
	if len(entries) == 0 {
		t.Fatal("no audit entries")
	}
	var sawReadOK, sawReadDenied, sawWriteDenied bool
	for _, e := range entries {
		if e.Call == "ReadAttributes" {
			if e.Inputs == "" {
				t.Fatal("read audit missing inputs")
			}
			if e.Subject == "u" && e.ErrCode == CodeOK {
				sawReadOK = true
				if e.Outputs == "" {
					t.Fatal("successful read audit missing outputs")
				}
				if len(e.Basis) == 0 {
					t.Fatal("successful read audit missing tag basis")
				}
				found := false
				for _, b := range e.Basis {
					if b.Tag == "hot" && b.Carried {
						found = true
					}
				}
				if !found {
					t.Fatal("basis does not record hot=true")
				}
				if len(e.AttrsRead) == 0 {
					t.Fatal("audit missing attrs-read evidence")
				}
			}
			if e.Subject == "v" && e.ErrCode == CodeNotVisible {
				sawReadDenied = true
				if e.Outputs != "" {
					t.Fatal("denied read must not record outputs")
				}
			}
		}
		if e.Call == "WriteInstance" && e.Subject == "w" && e.ErrCode == CodePermissionDenied {
			sawWriteDenied = true
			if e.Inputs == "" {
				t.Fatal("denied write audit missing inputs")
			}
		}
	}
	if !sawReadOK || !sawReadDenied || !sawWriteDenied {
		t.Fatalf("audit coverage incomplete: ok=%v readDenied=%v writeDenied=%v",
			sawReadOK, sawReadDenied, sawWriteDenied)
	}

	// 序号严格单调，保证日志顺序确定。
	for i := 1; i < len(entries); i++ {
		if entries[i].Seq <= entries[i-1].Seq {
			t.Fatal("audit sequence not strictly increasing")
		}
	}
}

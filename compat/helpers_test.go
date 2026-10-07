package compat

// buildChain 由创世 Schema、版本号序列与逐版本变更记录构造演化链，
// 物化每个版本的完整 Schema。changes[i] 是从 versions[i-1] 到 versions[i] 的变更。
func buildChain(genesis Schema, versions []Version, changes [][]Change) []Format {
	if len(versions) != len(changes)+1 {
		panic("versions must be one more than changes")
	}
	chain := make([]Format, len(versions))
	schema := cloneSchema(genesis)
	chain[0] = Format{Version: versions[0], Schema: schema}
	for i := 1; i < len(versions); i++ {
		schema = applyToSchema(schema, changes[i-1])
		chain[i] = Format{Version: versions[i], Schema: schema, Changes: changes[i-1]}
	}
	return chain
}

func cloneSchema(s Schema) Schema {
	out := make(Schema, len(s))
	for name, ot := range s {
		copied := make(ObjectType, len(ot))
		for p, prop := range ot {
			copied[p] = prop
		}
		out[name] = copied
	}
	return out
}

func applyToSchema(s Schema, changes []Change) Schema {
	out := cloneSchema(s)
	for _, c := range changes {
		ot, ok := out[c.ObjectType]
		if !ok {
			ot = ObjectType{}
			out[c.ObjectType] = ot
		}
		switch c.Kind {
		case ChangeAddProperty:
			ot[c.Property] = Property{Type: c.Type, Required: c.Required}
		case ChangeRemoveProperty:
			delete(ot, c.Property)
		case ChangeRetypeProperty:
			p := ot[c.Property]
			p.Type = c.Type
			ot[c.Property] = p
		case ChangeRequireProperty:
			p := ot[c.Property]
			p.Required = true
			ot[c.Property] = p
		case ChangeUnrequireProperty:
			p := ot[c.Property]
			p.Required = false
			ot[c.Property] = p
		}
	}
	return out
}

func v(major, minor, patch int) Version { return Version{major, minor, patch} }

func intType(min, max *float64) TypeSpec { return TypeSpec{Kind: KindInteger, Min: min, Max: max} }

func strType() TypeSpec { return TypeSpec{Kind: KindString} }

// readProfile 构造一个读取场景、无任何独立性声明的消费方。
func readProfile(at Version, accepts Range) Profile {
	return Profile{Name: "test-consumer", At: at, Accepts: accepts, Mode: ModeRead}
}

// assertVerdict 校验判定的等级与类别。
func assertVerdict(t interface {
	Helper()
	Errorf(string, ...any)
}, got Verdict, level Level, cat Category) {
	t.Helper()
	if got.Level != level || got.Category != cat {
		t.Errorf("verdict = (%s, %s), want (%s, %s); issues=%v notes=%v",
			got.Level, got.Category, level, cat, got.Issues, got.Notes)
	}
}

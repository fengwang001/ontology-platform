package lifecycle

import "testing"

func smokeSchema() *Schema {
	return &Schema{Types: map[string]*ObjectType{
		"doc": {
			Name:        "doc",
			States:      []string{"draft", "review", "done"},
			FinalStates: map[string]bool{"done": true},
			Transitions: map[string]*Transition{
				"submit": {Name: "submit", From: "draft", To: "review",
					Preconds: []Precondition{Attr("approved", true)},
					MaxCard:  []CardinalityRule{{LinkType: "tag", Max: 2}},
				},
				"close": {Name: "close", From: "review", To: "done"},
			},
		},
	}}
}

func TestSmokeFireOK(t *testing.T) {
	st := NewStore()
	st.AddInstance(&Instance{ID: "d1", Type: "doc", State: "draft",
		Attrs: map[string]AttrValue{"approved": true}})
	eng := NewEngine(smokeSchema(), st, DiscardLogger{})
	res, err := eng.Batch([]Op{Fire("d1", "submit")})
	if err != nil || !res.Committed {
		t.Fatalf("want commit, got %+v err=%v", res, err)
	}
	if got := st.GetInstance("d1").State; got != "review" {
		t.Fatalf("state=%s", got)
	}
}

func TestSmokePreconditionAndTerminal(t *testing.T) {
	st := NewStore()
	st.AddInstance(&Instance{ID: "d1", Type: "doc", State: "draft"})
	eng := NewEngine(smokeSchema(), st, DiscardLogger{})
	res, _ := eng.Batch([]Op{Fire("d1", "submit")})
	if res.Committed || res.Outcomes[0].Err.Code != CodePrecondition {
		t.Fatalf("want precondition, got %+v", res.Outcomes[0].Err)
	}
	st.AddInstance(&Instance{ID: "d2", Type: "doc", State: "review"})
	res, _ = eng.Batch([]Op{Fire("d2", "close")})
	if !res.Committed {
		t.Fatal("close should commit")
	}
	res, _ = eng.Batch([]Op{SetAttr("d2", "x", 1)})
	if res.Committed || res.Outcomes[0].Err.Code != CodeTerminal {
		t.Fatalf("want terminal, got %+v", res.Outcomes[0].Err)
	}
}

package dce_test

import (
	"bytes"
	"testing"

	"ontology/dce/analyzer"
	"ontology/dce/model"
	"ontology/dce/registry"
)

// graph 用便于书写的结构收集模块并求解。
type graph struct {
	mods map[string]*model.Module
	log  bytes.Buffer
}

func newGraph() *graph {
	return &graph{mods: map[string]*model.Module{}}
}

func (g *graph) add(m *model.Module) *graph {
	g.mods[m.ID] = m
	return g
}

func (g *graph) solve(t *testing.T, entries ...string) (*analyzer.Result, error) {
	t.Helper()
	reg := registry.New()
	for _, m := range g.mods {
		if err := reg.Register(m); err != nil {
			t.Fatalf("register %s: %v", m.ID, err)
		}
	}
	sess := reg.NewSession(entries)
	return sess.Solve(analyzer.WithLogger(analyzer.NewTextLogger(&g.log)))
}

// keptSet 把保留结果压成 "mod/decl=reason" 集合。
func keptSet(res *analyzer.Result) map[string]string {
	out := map[string]string{}
	for _, k := range res.Kept {
		out[k.Key.Module+"/"+k.Key.Decl] = k.Reason.String()
	}
	return out
}

func mod(id string, se model.SideEffect, decls []model.Declaration, imps []model.Import, exps []model.Export) *model.Module {
	return &model.Module{ID: id, SideEffect: se, Decls: decls, Imports: imps, Exports: exps}
}

func decl(name string, se bool, refs ...string) model.Declaration {
	return model.Declaration{Name: name, SideEffect: se, Refs: refs}
}

func localExp(name string) model.Export {
	return model.Export{Kind: model.ExportLocal, LocalName: name}
}

func namedExp(exportName, target, foreign string) model.Export {
	return model.Export{Kind: model.ExportReexport, ExportName: exportName, Target: target, ForeignName: foreign}
}

func starExp(target string) model.Export {
	return model.Export{Kind: model.ExportStarReexport, Target: target}
}

func imp(target string, bindings ...model.ImportBinding) model.Import {
	return model.Import{Target: target, Bindings: bindings}
}

func bind(local, imported string) model.ImportBinding {
	return model.ImportBinding{Local: local, Imported: imported}
}

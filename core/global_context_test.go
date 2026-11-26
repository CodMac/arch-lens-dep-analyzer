package core

import (
	"testing"

	"github.com/CodMac/arch-lens-dep-analyzer/model"
)

type fakeBuilder struct{}

func (fakeBuilder) BuildQualifiedName(parentQN, name string) string {
	if parentQN == "" {
		return name
	}
	return parentQN + "::" + name
}
func (fakeBuilder) IsPrimitive(string) bool { return false }

func def(qn, path string, kind model.ElementKind) *DefinitionEntry {
	isDecl := kind == model.FunctionDecl || kind == model.MethodDecl
	return &DefinitionEntry{
		Element:       &model.CodeElement{QualifiedName: qn, Name: qn, Kind: kind, Path: path},
		IsDeclaration: isDecl,
	}
}

// TestGlobalContextCrossFileSameQN 跨文件同 QN+Kind 应共存（C/C++ static/main/合并副本）。
func TestGlobalContextCrossFileSameQN(t *testing.T) {
	gc := NewBaseGlobalContext(nil, fakeBuilder{}, GlobalContextRules{})
	gc.AddDefinition(def("main()", "a.c", model.Function))
	gc.AddDefinition(def("main()", "b.c", model.Function))

	defs := gc.Definitions()
	if len(defs) != 2 {
		t.Fatalf("expected 2 definitions (cross-file coexist), got %d", len(defs))
	}
	paths := map[string]bool{}
	for _, d := range defs {
		paths[d.Element.Path] = true
	}
	if !paths["a.c"] || !paths["b.c"] {
		t.Fatalf("expected both a.c and b.c, got %v", paths)
	}
}

// TestGlobalContextSameFileSameQN 同文件同 QN+Kind 仍应去重（幂等）。
func TestGlobalContextSameFileSameQN(t *testing.T) {
	gc := NewBaseGlobalContext(nil, fakeBuilder{}, GlobalContextRules{})
	gc.AddDefinition(def("f()", "a.c", model.Function))
	gc.AddDefinition(def("f()", "a.c", model.Function))
	if got := len(gc.Definitions()); got != 1 {
		t.Fatalf("expected 1 definition (same file dedup), got %d", got)
	}
}

// TestGlobalContextDeclDefSameFile 同文件声明+定义（同 QN 不同 Kind）应共存。
func TestGlobalContextDeclDefSameFile(t *testing.T) {
	gc := NewBaseGlobalContext(nil, fakeBuilder{}, GlobalContextRules{})
	gc.AddDefinition(def("f()", "a.c", model.FunctionDecl))
	gc.AddDefinition(def("f()", "a.c", model.Function))
	if got := len(gc.Definitions()); got != 2 {
		t.Fatalf("expected decl+def coexist, got %d", got)
	}
	if e, ok := gc.FindByQualifiedName("f()"); !ok || e.Element.Kind != model.Function {
		t.Fatalf("FindByQualifiedName should prefer definition, got %+v", e)
	}
}

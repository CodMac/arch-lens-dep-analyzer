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

// testIndex 是 core 测试用的 DefinitionIndex：复刻「QN|Kind|Path 全量键 +
// 定义优先主索引 + 函数族重载索引」语义，供 BaseGlobalContext 容器测试使用。
type testIndex struct {
	qn       map[string]*DefinitionEntry
	primary  map[string]*DefinitionEntry
	overload map[string][]*DefinitionEntry
}

func newTestIndex() *testIndex {
	return &testIndex{
		qn:       make(map[string]*DefinitionEntry),
		primary:  make(map[string]*DefinitionEntry),
		overload: make(map[string][]*DefinitionEntry),
	}
}

func (i *testIndex) Add(d *DefinitionEntry) (*DefinitionEntry, bool) {
	key := d.Element.QualifiedName + "|" + string(d.Element.Kind) + "|" + d.Element.Path
	if existing, ok := i.qn[key]; ok {
		if existing.IsDeclaration && !d.IsDeclaration {
			i.qn[key] = d
			i.primary[d.Element.QualifiedName] = d
			return existing, true
		}
		return nil, false
	}
	i.qn[key] = d
	if existing, ok := i.primary[d.Element.QualifiedName]; !ok || (existing.IsDeclaration && !d.IsDeclaration) {
		i.primary[d.Element.QualifiedName] = d
	}
	switch d.Element.Kind {
	case model.Function, model.Method, model.FunctionDecl, model.MethodDecl:
		key := fakeBuilder{}.BuildQualifiedName(d.ParentQN, d.Element.Name)
		i.overload[key] = append(i.overload[key], d)
	}
	return nil, true
}

func (i *testIndex) FindByQualifiedName(qn string) (*DefinitionEntry, bool) {
	e, ok := i.primary[qn]
	return e, ok
}

func (i *testIndex) FindOverloadCandidates(noParamsQN string) ([]*DefinitionEntry, bool) {
	e, ok := i.overload[noParamsQN]
	return e, ok
}

// TestGlobalContextCrossFileSameQN 跨文件同 QN+Kind 应共存（C/C++ static/main/合并副本）。
func TestGlobalContextCrossFileSameQN(t *testing.T) {
	gc := NewBaseGlobalContext(nil, fakeBuilder{}, newTestIndex())
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
	gc := NewBaseGlobalContext(nil, fakeBuilder{}, newTestIndex())
	gc.AddDefinition(def("f()", "a.c", model.Function))
	gc.AddDefinition(def("f()", "a.c", model.Function))
	if got := len(gc.Definitions()); got != 1 {
		t.Fatalf("expected 1 definition (same file dedup), got %d", got)
	}
}

// TestGlobalContextDeclDefSameFile 同文件声明+定义（同 QN 不同 Kind）应共存，
// 且定义替换时 definitions 切片原地更新。
func TestGlobalContextDeclDefSameFile(t *testing.T) {
	gc := NewBaseGlobalContext(nil, fakeBuilder{}, newTestIndex())
	gc.AddDefinition(def("f()", "a.c", model.FunctionDecl))
	gc.AddDefinition(def("f()", "a.c", model.Function))
	if got := len(gc.Definitions()); got != 2 {
		t.Fatalf("expected decl+def coexist, got %d", got)
	}
	if e, ok := gc.FindByQualifiedName("f()"); !ok || e.Element.Kind != model.Function {
		t.Fatalf("FindByQualifiedName should prefer definition, got %+v", e)
	}
}

// TestGlobalContextDeclReplacedByDef 同 QN 同 Kind 的声明被定义替换时，
// definitions 切片应原地替换而非追加。
func TestGlobalContextDeclReplacedByDef(t *testing.T) {
	gc := NewBaseGlobalContext(nil, fakeBuilder{}, newTestIndex())
	decl := def("Fwd", "a.c", model.Struct)
	decl.IsDeclaration = true
	gc.AddDefinition(decl)
	gc.AddDefinition(def("Fwd", "a.c", model.Struct))

	defs := gc.Definitions()
	if len(defs) != 1 {
		t.Fatalf("expected in-place replace, got %d definitions", len(defs))
	}
	if defs[0].IsDeclaration {
		t.Fatal("definitions should hold the definition, not the declaration")
	}
}

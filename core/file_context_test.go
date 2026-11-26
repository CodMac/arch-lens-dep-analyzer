package core

import (
	"testing"

	"github.com/CodMac/arch-lens-dep-analyzer/model"
)

// TestFileContextQNIndexDefinitionPriority 同 QN 不同 Kind（定义 + 声明）时，
// FindByQualifiedName 必须定义优先，且不随后注册顺序变化。
func TestFileContextQNIndexDefinitionPriority(t *testing.T) {
	fc := NewBaseFileContext("a.cpp", nil, nil)
	// 先注册定义、后注册声明：单值 QN 索引会被后到的声明覆盖。
	fc.AddDefinition(def("S::m()", "a.cpp", model.Method))
	fc.AddDefinition(def("S::m()", "a.cpp", model.MethodDecl))

	entry, ok := fc.FindByQualifiedName("S::m()")
	if !ok {
		t.Fatal("FindByQualifiedName should find S::m()")
	}
	if entry.Element.Kind != model.Method {
		t.Fatalf("should prefer definition, got kind %s", entry.Element.Kind)
	}

	// 两个条目都应保留在文件定义列表中。
	if got := len(fc.Definitions()); got != 2 {
		t.Fatalf("expected decl+def coexist, got %d", got)
	}
}

// TestFileContextDeclDefReplaceSameKind 同 QN 同 Kind 的声明→定义应原地替换
// （C：`struct Fwd;` 后 `struct Fwd {};`）。
func TestFileContextDeclDefReplaceSameKind(t *testing.T) {
	fc := NewBaseFileContext("a.c", nil, nil)
	decl := def("Fwd", "a.c", model.Struct)
	decl.IsDeclaration = true
	fc.AddDefinition(decl)
	fc.AddDefinition(def("Fwd", "a.c", model.Struct))

	if got := len(fc.Definitions()); got != 1 {
		t.Fatalf("expected in-place replace, got %d definitions", got)
	}
	entry, ok := fc.FindByQualifiedName("Fwd")
	if !ok || entry.IsDeclaration {
		t.Fatalf("expected definition after replace, got %+v", entry)
	}
}

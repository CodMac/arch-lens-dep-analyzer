package core

import (
	"testing"

	"github.com/CodMac/arch-lens-dep-analyzer/model"
)

// TestFileContextQNIndexDefinitionPriority 同 QN 不同 Kind（定义 + 声明）时，
// FindByQualifiedName 必须定义优先，且不随后注册顺序变化。
func TestFileContextQNIndexDefinitionPriority(t *testing.T) {
	fc := NewBaseFileContext("a.cpp", nil, nil)
	fc.SetIndexPolicy(testFilePolicy{})
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
	fc.SetIndexPolicy(testFilePolicy{})
	decl := def("Fwd", "a.c", model.Struct)
	decl.Element.GetMores()[testIsDeclKey] = true
	fc.AddDefinition(decl)
	fc.AddDefinition(def("Fwd", "a.c", model.Struct))

	if got := len(fc.Definitions()); got != 1 {
		t.Fatalf("expected in-place replace, got %d definitions", got)
	}
	entry, ok := fc.FindByQualifiedName("Fwd")
	if !ok || isDeclEntry(entry) {
		t.Fatalf("expected definition after replace, got %+v", entry)
	}
}

// TestFileContextZeroValueUsable 零值 BaseFileContext 应可安全注册定义与导入。
func TestFileContextZeroValueUsable(t *testing.T) {
	var fc BaseFileContext
	fc.AddDefinition(def("f()", "a.c", model.Function))
	fc.AddImport("a.h", &ImportInfo{RawImportPath: "a.h"})

	if got := len(fc.Definitions()); got != 1 {
		t.Fatalf("expected 1 definition, got %d", got)
	}
	if _, ok := fc.FindByQualifiedName("f()"); !ok {
		t.Fatal("zero-value context should index definitions")
	}
}

// TestFileContextReturnsCopies 返回的切片/映射应为副本，篡改不影响内部索引。
func TestFileContextReturnsCopies(t *testing.T) {
	fc := NewBaseFileContext("a.c", nil, nil)
	fc.AddDefinition(def("f()", "a.c", model.Function))
	fc.AddImport("a.h", &ImportInfo{RawImportPath: "a.h"})

	defs := fc.Definitions()
	defs[0] = nil
	if fc.Definitions()[0] == nil {
		t.Fatal("Definitions should return a copy")
	}

	imports := fc.Imports()
	delete(imports, "a.h")
	if _, ok := fc.Imports()["a.h"]; !ok {
		t.Fatal("Imports should return a copy")
	}

	byName, _ := fc.FindByShortName("f()")
	byName[0] = nil
	if entries, _ := fc.FindByShortName("f()"); entries[0] == nil {
		t.Fatal("FindByShortName should return a copy")
	}
}

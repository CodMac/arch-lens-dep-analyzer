package context

import (
	"testing"

	"github.com/CodMac/arch-lens-dep-analyzer/core"
	"github.com/CodMac/arch-lens-dep-analyzer/model"
)

type testBuilder struct{}

func (testBuilder) BuildQualifiedName(parentQN, name string) string {
	if parentQN == "" {
		return name
	}
	return parentQN + "::" + name
}
func (testBuilder) IsPrimitive(string) bool { return false }

func entry(qn, name, path string, kind model.ElementKind, decl bool) *core.DefinitionEntry {
	return &core.DefinitionEntry{
		Element:       &model.CodeElement{QualifiedName: qn, Name: name, Kind: kind, Path: path},
		IsDeclaration: decl,
	}
}

// TestCppIndexCrossFileCoexist 跨文件同 QN+Kind（static/main/合并副本）应共存。
func TestCppIndexCrossFileCoexist(t *testing.T) {
	idx := NewDefinitionIndex(testBuilder{})
	if _, added := idx.Add(entry("main()", "main", "a.c", model.Function, false)); !added {
		t.Fatal("first main should be added")
	}
	if _, added := idx.Add(entry("main()", "main", "b.c", model.Function, false)); !added {
		t.Fatal("cross-file main should be added")
	}
}

// TestCppIndexDeclDefReplace 同 QN+Kind+Path 的声明被定义替换，主索引定义优先。
func TestCppIndexDeclDefReplace(t *testing.T) {
	idx := NewDefinitionIndex(testBuilder{})
	decl := entry("Fwd", "Fwd", "a.c", model.Struct, true)
	if _, added := idx.Add(decl); !added {
		t.Fatal("declaration should be added")
	}
	replaced, added := idx.Add(entry("Fwd", "Fwd", "a.c", model.Struct, false))
	if !added || replaced != decl {
		t.Fatalf("definition should replace declaration, got replaced=%v added=%v", replaced, added)
	}
	e, ok := idx.FindByQualifiedName("Fwd")
	if !ok || e.IsDeclaration {
		t.Fatalf("primary index should hold definition, got %+v", e)
	}
}

// TestCppIndexFindAllByQNDefinitionFirst 同 QN 多 Kind 时返回全部条目且定义优先。
func TestCppIndexFindAllByQNDefinitionFirst(t *testing.T) {
	idx := NewDefinitionIndex(testBuilder{})
	idx.Add(entry("S::m()", "m", "s.h", model.MethodDecl, true))
	idx.Add(entry("S::m()", "m", "s.cpp", model.Method, false))

	all := idx.FindAllByQualifiedName("S::m()")
	if len(all) != 2 {
		t.Fatalf("expected 2 entries for S::m(), got %d", len(all))
	}
	if all[0].Element.Kind != model.Method {
		t.Fatalf("definition should come first, got %s", all[0].Element.Kind)
	}
	if all[1].Element.Kind != model.MethodDecl {
		t.Fatalf("declaration should come last, got %s", all[1].Element.Kind)
	}
}

// TestCppIndexFindByShortNameAcrossFiles 全局短名索引跨文件返回全部同名定义。
func TestCppIndexFindByShortNameAcrossFiles(t *testing.T) {
	idx := NewDefinitionIndex(testBuilder{})
	idx.Add(entry("A::Dup", "Dup", "a.cpp", model.Struct, false))
	idx.Add(entry("B::Dup", "Dup", "b.cpp", model.Struct, false))
	idx.Add(entry("Other", "Other", "c.cpp", model.Struct, false))

	entries := idx.FindByShortName("Dup")
	if len(entries) != 2 {
		t.Fatalf("expected 2 Dup entries, got %d", len(entries))
	}
	if got := idx.FindByShortName("Other"); len(got) != 1 {
		t.Fatalf("expected 1 Other entry, got %d", len(got))
	}
}

// TestCppIndexOverloadKinds 自由函数与声明参与重载索引，普通类型不参与。
func TestCppIndexOverloadKinds(t *testing.T) {
	idx := NewDefinitionIndex(testBuilder{})
	f1 := entry("n::f(int)", "f", "a.cpp", model.Function, false)
	f1.ParentQN = "n"
	f2 := entry("n::f()", "f", "a.cpp", model.Function, false)
	f2.ParentQN = "n"
	s := entry("n::S", "S", "a.cpp", model.Struct, false)
	s.ParentQN = "n"
	idx.Add(f1)
	idx.Add(f2)
	idx.Add(s)

	entries, ok := idx.FindOverloadCandidates("n::f")
	if !ok || len(entries) != 2 {
		t.Fatalf("expected 2 overload candidates for n::f, got %d", len(entries))
	}
	if _, ok := idx.FindOverloadCandidates("n::S"); ok {
		t.Fatal("struct should not be indexed as overload candidate")
	}
}

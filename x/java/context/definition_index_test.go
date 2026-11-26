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
	return parentQN + "." + name
}
func (testBuilder) IsPrimitive(string) bool { return false }

func entry(qn, name, path string, kind model.ElementKind) *core.DefinitionEntry {
	return &core.DefinitionEntry{
		Element: &model.CodeElement{QualifiedName: qn, Name: name, Kind: kind, Path: path},
	}
}

// TestJavaIndexCrossFileCoexist 不同文件的同名类（不同包）应共存。
func TestJavaIndexCrossFileCoexist(t *testing.T) {
	idx := NewDefinitionIndex(testBuilder{})
	if _, added := idx.Add(entry("a.Foo", "Foo", "a/Foo.java", model.Class)); !added {
		t.Fatal("a.Foo should be added")
	}
	if _, added := idx.Add(entry("b.Foo", "Foo", "b/Foo.java", model.Class)); !added {
		t.Fatal("b.Foo should be added")
	}
}

// TestJavaIndexFindByShortName 全局短名索引返回不同包的同名类。
func TestJavaIndexFindByShortName(t *testing.T) {
	idx := NewDefinitionIndex(testBuilder{})
	idx.Add(entry("a.Foo", "Foo", "a/Foo.java", model.Class))
	idx.Add(entry("b.Foo", "Foo", "b/Foo.java", model.Class))

	entries := idx.FindByShortName("Foo")
	if len(entries) != 2 {
		t.Fatalf("expected 2 Foo entries, got %d", len(entries))
	}
	if all := idx.FindAllByQualifiedName("a.Foo"); len(all) != 1 {
		t.Fatalf("expected 1 entry for a.Foo, got %d", len(all))
	}
}

// TestJavaIndexOverloadOnlyMethod 仅 Method 参与重载索引。
func TestJavaIndexOverloadOnlyMethod(t *testing.T) {
	idx := NewDefinitionIndex(testBuilder{})
	m1 := entry("a.S.run()", "run", "a/S.java", model.Method)
	m1.ParentQN = "a.S"
	m2 := entry("a.S.run(int)", "run", "a/S.java", model.Method)
	m2.ParentQN = "a.S"
	c := entry("a.S", "S", "a/S.java", model.Class)
	c.ParentQN = "a"
	idx.Add(m1)
	idx.Add(m2)
	idx.Add(c)

	entries, ok := idx.FindOverloadCandidates("a.S.run")
	if !ok || len(entries) != 2 {
		t.Fatalf("expected 2 overload candidates for a.S.run, got %d", len(entries))
	}
	if _, ok := idx.FindOverloadCandidates("a.S"); ok {
		t.Fatal("class should not be indexed as overload candidate")
	}
}

package context

import (
	"testing"

	"github.com/CodMac/arch-lens-dep-analyzer/core"
	"github.com/CodMac/arch-lens-dep-analyzer/model"
	"github.com/CodMac/arch-lens-dep-analyzer/x/cpp/constants"
)

func fileCtx(path string, defs ...*model.CodeElement) core.FileContext {
	fc := core.NewBaseFileContext(path, nil, nil)
	for _, d := range defs {
		fc.AddDefinition(&core.DefinitionEntry{Element: d})
	}
	return fc
}

func typeElem(name, path, namespace string) *model.CodeElement {
	e := &model.CodeElement{
		Name:          name,
		QualifiedName: name,
		Kind:          model.Struct,
		Path:          path,
	}
	e.GetMores()[constants.NamespaceName] = namespace
	return e
}

// TestCppVisibleDefinitions C/C++ 可见性视图：本文件 + include 可达文件 + 全局作用域定义；
// 未被 include 的命名空间内定义不可见。
func TestCppVisibleDefinitions(t *testing.T) {
	gc := NewGlobalContext(nil, testBuilder{}).(*GlobalContext)

	a := fileCtx("a.h", typeElem("A", "a.h", ""))
	b := fileCtx("b.h", typeElem("B", "b.h", ""))
	c := fileCtx("c.cpp", typeElem("C", "c.cpp", ""))
	d := fileCtx("d.h", typeElem("D", "d.h", ""))
	e := fileCtx("e.h", typeElem("E", "e.h", "n"))

	b.AddImport("a.h", &core.ImportInfo{RawImportPath: "a.h"})
	c.AddImport("b.h", &core.ImportInfo{RawImportPath: "b.h"})

	for _, fc := range []core.FileContext{a, b, c, d, e} {
		gc.RegisterFileContext(fc)
	}

	visible := map[string]bool{}
	for _, def := range gc.VisibleDefinitions("c.cpp") {
		visible[def.Element.Name] = true
	}

	for _, want := range []string{"A", "B", "C", "D"} {
		if !visible[want] {
			t.Errorf("%s should be visible from c.cpp", want)
		}
	}
	if visible["E"] {
		t.Error("E (namespace n, not included) should not be visible from c.cpp")
	}
}

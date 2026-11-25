package test

import (
	"testing"

	"github.com/CodMac/arch-lens-dep-analyzer/model"
	"github.com/CodMac/arch-lens-dep-analyzer/x/cpp/constants"
)

// TestBinderCppConceptNotAlias concept 的 underlying 是约束表达式，不是类型别名：
// 不得进入别名表，也不得产出 underlying_with_qn；同名类型引用不得被 concept 污染。
func TestBinderCppConceptNotAlias(t *testing.T) {
	paths := ResolvePaths(t, "binder/cpp/concept", "*.cpp")
	gc, _ := RunExtraction(t, "x/cpp/testdata", paths)

	// concept 归 Type，标记 is_concept，且不绑定 underlying_with_qn。
	concept := AssertDef(t, gc, "cpt::Small", model.Type)
	AssertMores(t, gc, "cpt::Small", constants.TypeIsConcept, true)
	if concept != nil {
		if _, ok := concept.GetMores()[constants.TypeUnderlying+"_with_qn"]; ok {
			t.Errorf("concept cpt::Small should not have underlying_with_qn")
		}
	}

	// 同名 struct 的引用解析到自身，而不是被 concept 的约束表达式污染。
	AssertDef(t, gc, "Small", model.Struct)
	AssertMores(t, gc, "use()::s", constants.VariableTypeWithQN, "Small")
}

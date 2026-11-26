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
		if _, ok := concept.GetMores()[constants.TypeUnderlyingWithQN]; ok {
			t.Errorf("concept cpt::Small should not have underlying_with_qn")
		}
	}

	// 同名 struct 的引用解析到自身，而不是被 concept 的约束表达式污染。
	AssertDef(t, gc, "Small", model.Struct)
	AssertMores(t, gc, "use()::s", constants.VariableTypeWithQN, "Small")
}

// TestBinderCppAliasScope 别名展开按 owner 词法作用域/文件优先，而非全局扁平。
func TestBinderCppAliasScope(t *testing.T) {
	paths := ResolvePaths(t, "binder/cpp/alias_scope", "*.cpp")
	gc, _ := RunExtraction(t, "x/cpp/testdata", paths)

	// 嵌套类同名别名：内层 float 遮蔽外层 int。
	AssertMores(t, gc, "Outer::Inner::v", constants.FieldTypeWithQN, "float")

	// 跨文件同名 typedef：各文件按自身别名展开。
	AssertMores(t, gc, "A::v", constants.FieldTypeWithQN, "int")
	AssertMores(t, gc, "B::v", constants.FieldTypeWithQN, "float")

	// 模板别名底层退化为模板变量：保留别名自身 QN。
	AssertMores(t, gc, "Holder::v", constants.FieldTypeWithQN, "Vec")
}

// TestBinderCppSingleLetterStruct 单大写字母真实类型不被当作模板变量：
// typedef S value_type 的底层应解析为 struct S，而非退回别名自身。
func TestBinderCppSingleLetterStruct(t *testing.T) {
	paths := ResolvePaths(t, "binder/cpp/single_letter_struct", "*.cpp")
	gc, rels := RunExtraction(t, "x/cpp/testdata", paths)

	AssertMores(t, gc, "value_type", constants.TypeUnderlyingWithQN, "S")
	AssertMores(t, gc, "A::v", constants.FieldTypeWithQN, "S")

	FindRel(t, rels, func(r *model.DependencyRelation) bool {
		return r.Type == model.TypeArg && r.Source.QualifiedName == "A::v" &&
			r.Target.QualifiedName == "S"
	})
}

// TestBinderCppMultiWordPrimitive 多词基本类型识别为 PRIMITIVE，不落为外部类型。
func TestBinderCppMultiWordPrimitive(t *testing.T) {
	paths := ResolvePaths(t, "binder/cpp/primitive", "*.cpp")
	gc, rels := RunExtraction(t, "x/cpp/testdata", paths)

	for qn, want := range map[string]string{
		"P::a": "unsigned long long",
		"P::b": "long double",
		"P::c": "unsigned int",
		"P::d": "signed char",
		"P::e": "long long",
		"P::f": "int", // volatile 被剥离
	} {
		AssertMores(t, gc, qn, constants.FieldTypeWithQN, want)
	}

	// 参数类型目标必须是 PRIMITIVE（而非外部 CLASS）。
	for _, want := range []string{"unsigned long long", "long double"} {
		name := want
		FindRel(t, rels, func(r *model.DependencyRelation) bool {
			return r.Type == model.Parameter && r.Target.Kind == model.Primitive &&
				r.Target.QualifiedName == name
		})
	}
}

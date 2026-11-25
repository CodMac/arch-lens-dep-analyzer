package test

import (
	"testing"

	"github.com/CodMac/arch-lens-dep-analyzer/core"
	"github.com/CodMac/arch-lens-dep-analyzer/model"
)

// TestLinkerCppTopology Linker 产出的文件/命名空间拓扑：
// File → 顶层元素、Namespace → SubNamespace、Namespace → File，
// 类外定义作为文件根，类内成员不作为文件子节点。
func TestLinkerCppTopology(t *testing.T) {
	paths := ResolvePaths(t, "collector/base", "*.c", "*.h", "*.cpp")
	ec := RunPipeline(t, "x/cpp/testdata", paths, core.LevelRaw)

	cases := []struct {
		name string
		src  string
		tgt  string
	}{
		{"Namespace→SubNamespace", "app", "app::core"},
		{"Namespace→File", "app", "collector/base/basic.cpp"},
		{"File→顶层类型", "collector/base/basic.cpp", "app::Box"},
		{"File→类外定义", "collector/base/basic.cpp", "app::core::Base::helper()"},
	}
	for _, c := range cases {
		c := c
		FindRel(t, ec.Relations, func(r *model.DependencyRelation) bool {
			return r.Type == model.Contain && r.Source.QualifiedName == c.src &&
				r.Target.QualifiedName == c.tgt
		})
	}

	// 类内成员（字段/方法声明）不是文件子节点。
	for _, target := range []string{"app::core::Base::m_val", "app::Box::run(int)"} {
		target := target
		if HasRel(ec.Relations, func(r *model.DependencyRelation) bool {
			return r.Type == model.Contain && r.Source.Kind == model.File &&
				r.Source.QualifiedName == "collector/base/basic.cpp" &&
				r.Target.QualifiedName == target
		}) {
			t.Errorf("File should not directly contain class member %s", target)
		}
	}
}

// TestLinkerCppImplLinkDirection ImplLink 方向为定义 → 声明，且只声明不建链。
func TestLinkerCppImplLinkDirection(t *testing.T) {
	paths := ResolvePaths(t, "collector/base", "*.c", "*.h", "*.cpp")
	ec := RunPipeline(t, "x/cpp/testdata", paths, core.LevelRaw)

	FindRel(t, ec.Relations, func(r *model.DependencyRelation) bool {
		return r.Type == model.ImplLink && r.Source.QualifiedName == "add(int,int)" &&
			r.Source.Kind == model.Function && r.Target.Kind == model.FunctionDecl
	})
}

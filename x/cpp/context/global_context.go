package context

import (
	"path/filepath"
	"strings"

	"github.com/CodMac/arch-lens-dep-analyzer/core"
	"github.com/CodMac/arch-lens-dep-analyzer/model"
	"github.com/CodMac/arch-lens-dep-analyzer/x/cpp/constants"
)

// GlobalContext 是 C/C++ 的全局符号上下文。
//
// C/C++ 没有「包声明」：NAMESPACE 元素由 Collector 在遍历 namespace_definition 时创建，
// 故 RegisterFileContext 直接委托 Base，无需额外注册步骤。
type GlobalContext struct {
	*core.BaseGlobalContext
}

// NewGlobalContext 创建 C/C++ 全局上下文。
func NewGlobalContext(resolver core.SymbolResolver, builder core.SymbolBuilder) core.GlobalContext {
	return &GlobalContext{
		BaseGlobalContext: core.NewBaseGlobalContext(resolver, builder, NewDefinitionIndex(builder)),
	}
}

// VisibleDefinitions 返回 fromFile 可见的定义（实现 core.FileVisibility）。
//
// C/C++ 可见性近似规则：
//   - 本文件定义；
//   - 沿 include 边可达的文件定义（include 路径按「精确匹配 → 路径后缀匹配」解析）；
//   - 全局作用域（无 namespace）定义对所有文件可见。
//
// 宏展开、条件编译与 include 目录搜索不在建模范围内，故这是近似视图。
func (gc *GlobalContext) VisibleDefinitions(fromFile string) []*core.DefinitionEntry {
	files := gc.FileContexts()
	seen := make(map[string]bool)
	queue := []string{fromFile}
	var out []*core.DefinitionEntry

	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		if seen[path] {
			continue
		}
		seen[path] = true

		fc, ok := files[path]
		if !ok {
			continue
		}
		out = append(out, fc.Definitions()...)
		for _, imps := range fc.Imports() {
			for _, imp := range imps {
				if imp == nil {
					continue
				}
				for _, included := range resolveInclude(files, imp.RawImportPath) {
					if !seen[included] {
						queue = append(queue, included)
					}
				}
			}
		}
	}

	// 全局作用域定义对所有文件可见（跨翻译单元符号在模型中视为全局）。
	for _, def := range gc.Definitions() {
		if seen[def.Element.Path] {
			continue
		}
		if isGlobalNamespace(def.Element) {
			out = append(out, def)
		}
	}
	return out
}

// resolveInclude 把 include 原始路径解析为项目内文件路径：
// 先精确匹配，再按路径后缀匹配（`base.h` 命中 `collector/base.h`）。
// 后缀匹配可能命中多个同名头文件，全部返回由调用方按集合处理。
func resolveInclude(files map[string]core.FileContext, raw string) []string {
	raw = filepath.ToSlash(raw)
	if raw == "" {
		return nil
	}
	if _, ok := files[raw]; ok {
		return []string{raw}
	}
	var matches []string
	suffix := "/" + raw
	for path := range files {
		if strings.HasSuffix(path, suffix) {
			matches = append(matches, path)
		}
	}
	return matches
}

// isGlobalNamespace 判断元素是否位于全局作用域（无 namespace）。
func isGlobalNamespace(elem *model.CodeElement) bool {
	if elem == nil || elem.Extra == nil {
		return true
	}
	ns, _ := elem.Extra.Mores[constants.NamespaceName].(string)
	return ns == ""
}

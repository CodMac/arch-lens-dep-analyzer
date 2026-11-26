package context

import (
	"github.com/CodMac/arch-lens-dep-analyzer/core"
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

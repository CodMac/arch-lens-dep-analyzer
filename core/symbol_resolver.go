package core

import (
	"github.com/CodMac/arch-lens-dep-analyzer/core/ast"
	"github.com/CodMac/arch-lens-dep-analyzer/model"
)

// SymbolResolver 负责语言特有的符号解析。
// 其核心是 ResolveAction：将 AST 上的动作节点解析为目标符号。
// 该接口同时承载语言实现内部各组件（enricher/desugar/chain）共享的成员解析能力。
type SymbolResolver interface {
	// BuildQualifiedName 拼接父符号 QN 与子符号名称为完整 QN。
	BuildQualifiedName(parentQN, name string) string

	// IsPrimitive 判断类型名是否为语言基本类型。
	IsPrimitive(typeName string) bool

	// ResolveType 解析结构体符号（Package/Class/Interface/...）。
	// 如果上下文未找到，返回指定 kind 的外部实体。
	ResolveType(gc GlobalContext, fc FileContext, symbol string, kind model.ElementKind) *model.CodeElement

	// ResolveFunc 解析方法符号（Method）。
	ResolveFunc(gc GlobalContext, fc FileContext, node ast.Node, receiver, symbol string) *model.CodeElement

	// ResolveVar 解析变量符号（Field、Variable）。
	ResolveVar(gc GlobalContext, fc FileContext, node ast.Node, receiver, symbol string) *model.CodeElement

	// ResolveAction 统一的动作解析入口：根据动作节点、上下文与关系类型解析目标符号。
	ResolveAction(gc GlobalContext, fc FileContext, node ast.Node, relType model.DependencyType) *model.CodeElement
}

// ScopedTypeResolver 是语言可选能力：按使用位置解析类型。
//
// ResolveType 只有类型文本、没有使用位置，无法处理「同名类型在不同 namespace/类
// 作用域内指向不同实体」（C++ 的 B::Foo vs A::Foo）。调用方若持有类型出现处的
// AST 节点，应优先使用本接口；未实现时退回 SymbolResolver.ResolveType。
type ScopedTypeResolver interface {
	ResolveTypeAt(gc GlobalContext, fc FileContext, node ast.Node, symbol string, kind model.ElementKind) *model.CodeElement
}

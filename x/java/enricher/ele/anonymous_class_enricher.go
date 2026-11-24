package ele

import (
	"github.com/CodMac/arch-lens-dep-analyzer/core"
	coreast "github.com/CodMac/arch-lens-dep-analyzer/core/ast"
	"github.com/CodMac/arch-lens-dep-analyzer/model"
	"github.com/CodMac/arch-lens-dep-analyzer/x/java/constants"
	"github.com/CodMac/arch-lens-dep-analyzer/x/java/helper"
)

// AnonymousClassEnricher 为匿名内部类生成父类型元数据。
type AnonymousClassEnricher struct {
	fCtx core.FileContext
}

// EnrichMetadata 提取 new 关键字后的父类型并构造 Signature。
func (c *AnonymousClassEnricher) EnrichMetadata(elem *model.CodeElement, node coreast.Node) {
	extra := elem.Extra

	// 在 identifyElement 中, AnonymousClass 锚定的 node 是 "object_creation_expression"
	if node.Kind() != "object_creation_expression" {
		return
	}

	// 提取 new 关键字后的类型，例如 new Runnable() { ... } 中的 Runnable
	typeNode := node.ChildByFieldName("type")
	if typeNode != nil {
		typeName := helper.GetNodeContent(typeNode, *c.fCtx.SourceBytes())
		extra.Mores[constants.AnonymousClassType] = typeName
		elem.Signature = "anonymous extends/implements " + typeName
	}
}

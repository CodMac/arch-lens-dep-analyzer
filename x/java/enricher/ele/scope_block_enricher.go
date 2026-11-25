package ele

import (
	"github.com/CodMac/arch-lens-dep-analyzer/core"
	coreast "github.com/CodMac/arch-lens-dep-analyzer/core/ast"
	"github.com/CodMac/arch-lens-dep-analyzer/model"
	"github.com/CodMac/arch-lens-dep-analyzer/x/java/constants"
	"github.com/CodMac/arch-lens-dep-analyzer/x/java/helper"
)

// ScopeBlockEnricher 为代码块（实例初始化块/静态初始化块/普通块）生成元数据。
type ScopeBlockEnricher struct {
	fCtx core.FileContext
}

// EnrichMetadata 填充块级 Mores（是否静态、复杂度）与 Signature。
func (c *ScopeBlockEnricher) EnrichMetadata(elem *model.CodeElement, node coreast.Node) {
	extra := elem.Extra

	isStatic := node.Kind() == "static_initializer"
	extra.Mores[constants.BlockIsStatic] = isStatic
	extra.Mores[constants.MethodComplexity] = helper.CalculateComplexity(node)

	elem.Signature = "{...}"
	if isStatic {
		elem.Signature = "static {...}"
	}
}

package ele

import (
	"github.com/CodMac/arch-lens-dep-analyzer/core"
	coreast "github.com/CodMac/arch-lens-dep-analyzer/core/ast"
	"github.com/CodMac/arch-lens-dep-analyzer/model"
	"github.com/CodMac/arch-lens-dep-analyzer/x/java/constants"
	"github.com/CodMac/arch-lens-dep-analyzer/x/java/helper"
)

// FileEnricher 为文件元素生成行数指标。
type FileEnricher struct {
	fCtx core.FileContext
}

// EnrichMetadata 填充文件级 Mores（LOC / RawLOC）。
func (e *FileEnricher) EnrichMetadata(elem *model.CodeElement, node coreast.Node) {
	srcs := e.fCtx.SourceBytes()

	elem.Extra.Mores[constants.FileRawLOC] = helper.CalculateRawLOC(*srcs)
	elem.Extra.Mores[constants.FileLOC] = helper.CalculateLOC(*srcs)
}

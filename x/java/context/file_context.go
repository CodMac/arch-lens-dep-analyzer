// Package context 提供 Java 语言专属的 FileContext / GlobalContext 实现。
//
// 通用索引/去重/锁机制复用 core.Base*；本包只承载 Java 的语义差异：
//   - 单包语义（实现本包 PackageAwareFileContext）
//   - 包名按点号逐级注册为 PACKAGE 元素
//   - 重载索引只收 Method（Java 无自由函数）
package context

import (
	"github.com/CodMac/arch-lens-dep-analyzer/core"
	coreast "github.com/CodMac/arch-lens-dep-analyzer/core/ast"
)

// PackageAwareFileContext 是 Java 的可选能力接口：单包语义。
// 语言专属能力接口归语言包所有，core 不含语言概念。
type PackageAwareFileContext interface {
	core.FileContext

	// PackageName 包名（如 com.example.base）。
	PackageName() string

	// SetPackageName 设置包名。
	SetPackageName(name string)
}

// FileContext 是 Java 的文件上下文：在通用实现之上增加单包语义。
type FileContext struct {
	*core.BaseFileContext

	packageName string
}

// NewFileContext 创建 Java 文件上下文。
func NewFileContext(filePath, packageName string, root coreast.Node, sourceBytes *[]byte) *FileContext {
	return &FileContext{
		BaseFileContext: core.NewBaseFileContext(filePath, root, sourceBytes),
		packageName:     packageName,
	}
}

// PackageName 返回包名（如 com.example.base）。
func (fc *FileContext) PackageName() string { return fc.packageName }

// SetPackageName 设置包名。
func (fc *FileContext) SetPackageName(name string) { fc.packageName = name }

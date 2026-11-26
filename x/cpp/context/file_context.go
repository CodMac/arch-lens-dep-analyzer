// Package context 提供 C/C++ 语言专属的 FileContext / GlobalContext 实现。
//
// 通用索引/去重/锁机制复用 core.Base*；本包只承载 C/C++ 的语义差异：
//   - 文件可含多个 namespace（实现 NamespaceAware）
//   - 命名空间按 :: 逐级注册为 NAMESPACE 元素
//   - 重载索引收 Function 族（C 自由函数 + 声明）
package context

import (
	"github.com/CodMac/arch-lens-dep-analyzer/core"
	coreast "github.com/CodMac/arch-lens-dep-analyzer/core/ast"
	"github.com/CodMac/arch-lens-dep-analyzer/model"
	"github.com/CodMac/arch-lens-dep-analyzer/x/cpp/constants"
	"github.com/CodMac/arch-lens-dep-analyzer/x/cpp/preproc"
)

// IsDeclaration 判断元素是否为「声明」而非定义（C/C++ 前向声明、函数原型、extern 变量）。
// 声明/定义是 C/C++ 概念，标记存放在元素元数据中，core 不含该字段。
func IsDeclaration(elem *model.CodeElement) bool {
	if elem == nil || elem.Extra == nil {
		return false
	}
	v, _ := elem.Extra.Mores[constants.IsDeclaration].(bool)
	return v
}

// fileIndexPolicy 实现 core.FileIndexPolicy：同键声明被定义替换，查找定义优先。
type fileIndexPolicy struct{}

func (fileIndexPolicy) ShouldReplace(old, new *core.DefinitionEntry) bool {
	return IsDeclaration(old.Element) && !IsDeclaration(new.Element)
}

func (fileIndexPolicy) IsDefinition(entry *core.DefinitionEntry) bool {
	return !IsDeclaration(entry.Element)
}

// NamespaceAware 是 C/C++ 专属的可选能力接口：
// 一文件可含多个命名空间，其余语言无需实现。
type NamespaceAware interface {
	core.FileContext

	// Namespaces 命名空间列表（按出现顺序，含嵌套前缀）。
	Namespaces() []string

	// SetNamespaces 设置命名空间列表。
	SetNamespaces(ns []string)
}

// UsingAware 是 C/C++ 专属的可选能力接口：
// 记录 using 声明（using ns::f;）与命名空间别名（namespace na = ns;），
// 供 Resolver 在非限定名解析时优先命中被引入的符号。
type UsingAware interface {
	core.FileContext

	// UsingAlias 返回 using 声明引入的短名对应的目标限定名。
	UsingAlias(name string) (string, bool)

	// AddUsingAlias 记录一条 using 声明。
	AddUsingAlias(name, target string)

	// NamespaceAlias 返回命名空间别名对应的目标命名空间名。
	NamespaceAlias(alias string) (string, bool)

	// AddNamespaceAlias 记录一条命名空间别名。
	AddNamespaceAlias(alias, target string)
}

// ActionRoots 汇总动作提取需要遍历的全部语法树根节点。
//
// 条件编译投影只保留每组一个分支，投影树无法覆盖 #elif/#else 与嵌套分支；
// 原始树（保留 preproc_* 节点）与分支回补树可补足。三棵树共享同一套字节坐标
// （投影等长），因此 Extractor 可按 (RelType, StartByte, EndByte) 去重。
type ActionRoots struct {
	// Primary 主树（投影树；退化时为原始树）。
	Primary coreast.Node
	// Raw 原始树（全分支），退化或不可用时为 nil。
	Raw coreast.Node
	// Alts 分支回补树（仅当原始树有 ERROR 时存在）。
	Alts []coreast.Node
	// Dead #if 0 死区区间（按 Start 升序），死区内的动作不产关系。
	Dead []preproc.Region
}

// FileContext 是 C/C++ 的文件上下文：在通用实现之上增加命名空间列表。
type FileContext struct {
	*core.BaseFileContext

	namespaces  []string
	actionRoots ActionRoots

	usingAliases     map[string]string
	namespaceAliases map[string]string
}

// NewFileContext 创建 C/C++ 文件上下文。
func NewFileContext(filePath string, root coreast.Node, sourceBytes *[]byte) *FileContext {
	fc := &FileContext{
		BaseFileContext: core.NewBaseFileContext(filePath, root, sourceBytes),
	}
	fc.SetIndexPolicy(fileIndexPolicy{})
	return fc
}

// Namespaces 返回文件内出现的命名空间（按出现顺序，含嵌套前缀）。
func (fc *FileContext) Namespaces() []string { return fc.namespaces }

// SetNamespaces 设置命名空间列表。
func (fc *FileContext) SetNamespaces(ns []string) { fc.namespaces = ns }

// UsingAlias 返回 using 声明引入的短名对应的目标限定名。
func (fc *FileContext) UsingAlias(name string) (string, bool) {
	t, ok := fc.usingAliases[name]
	return t, ok
}

// AddUsingAlias 记录一条 using 声明。
func (fc *FileContext) AddUsingAlias(name, target string) {
	if fc.usingAliases == nil {
		fc.usingAliases = make(map[string]string)
	}
	fc.usingAliases[name] = target
}

// NamespaceAlias 返回命名空间别名对应的目标命名空间名。
func (fc *FileContext) NamespaceAlias(alias string) (string, bool) {
	t, ok := fc.namespaceAliases[alias]
	return t, ok
}

// AddNamespaceAlias 记录一条命名空间别名。
func (fc *FileContext) AddNamespaceAlias(alias, target string) {
	if fc.namespaceAliases == nil {
		fc.namespaceAliases = make(map[string]string)
	}
	fc.namespaceAliases[alias] = target
}

// SetActionRoots 设置动作提取需要遍历的语法树根节点。
func (fc *FileContext) SetActionRoots(r ActionRoots) { fc.actionRoots = r }

// ActionRoots 返回动作提取需要遍历的语法树根节点。
func (fc *FileContext) ActionRoots() ActionRoots { return fc.actionRoots }

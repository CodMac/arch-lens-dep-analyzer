package core

import (
	"sync"

	coreast "github.com/CodMac/arch-lens-dep-analyzer/core/ast"
	"github.com/CodMac/arch-lens-dep-analyzer/model"
)

// DefinitionEntry 描述一个代码元素定义及其父级归属。
type DefinitionEntry struct {
	Element  *model.CodeElement
	ParentQN string

	// Node 保留该定义锚定的 AST 节点（元数据填充/作用域修正/语法糖处理用）。
	// 仅语言实现内部使用，引擎不负责清理（FileContext 随文件生命周期释放）。
	Node coreast.Node
}

// GlobalContext 描述整个分析会话的全局符号上下文。
//
// 分层设计：core 只保留语言无关契约，通用索引/锁机制由 BaseGlobalContext 提供；
// 语言专属语义（包/命名空间注册、重载候选 kind）由 x/{lang}/context 实现。
// 注意：实现内部自带锁，调用方不得持锁再调用（避免嵌套锁死锁）。
type GlobalContext interface {
	// RegisterFileContext 注册一个文件上下文（Collector 阶段产出）。
	// 语言实现可覆盖此方法，在委托 Base 前完成包/命名空间注册等语言专属逻辑。
	RegisterFileContext(fc FileContext)

	// FileContexts 返回全部文件上下文（只读快照）。
	FileContexts() map[string]FileContext

	// FileContext 按路径返回单个文件上下文。
	FileContext(relPath string) (FileContext, bool)

	// Definitions 返回全部定义（只读快照）。
	Definitions() []*DefinitionEntry

	// AddDefinition 添加一个定义（幂等：已存在的 QN+Kind+Path 忽略）。
	AddDefinition(def *DefinitionEntry)

	// FindByQualifiedName 按限定名查找定义。
	// 声明/定义分离语言（C/C++）中同一 QN 可能存在声明+定义两个元素，
	// 此方法返回「定义优先」：优先非声明条目（由语言 DefinitionIndex 判定）。
	FindByQualifiedName(qn string) (*DefinitionEntry, bool)

	// FindAllByQualifiedName 按限定名返回全部定义（定义优先排序）。
	// 同 QN 可对应不同 Kind（类型/函数/变量、声明/定义），调用方按 Kind 自行挑选。
	FindAllByQualifiedName(qn string) []*DefinitionEntry

	// FindByShortName 按短名返回全部定义（跨文件）。
	// 用于替代语言侧对 Definitions() 的全量扫描（O(defs)）。
	FindByShortName(name string) []*DefinitionEntry

	// FindOverloadCandidates 按不含参数的 QN 查找可重载候选（Method/Function 族）。
	// 参与 kind 由语言 DefinitionIndex 实现决定。
	FindOverloadCandidates(noParamsQN string) ([]*DefinitionEntry, bool)

	// BuildQualifiedName 委托 Builder 拼接 QN。
	BuildQualifiedName(parentQN, name string) string

	// Resolver 返回语言符号解析器。
	Resolver() SymbolResolver

	// Builder 返回语言符号构建器。
	Builder() SymbolBuilder
}

// FileVisibility 是语言可选能力：按文件返回可见定义视图。
//
// 语言实现可基于 include/import/namespace/package 私有等规则裁剪；
// 未实现该接口的语言，调用方应退回 Definitions() 全量视图。
type FileVisibility interface {
	// VisibleDefinitions 返回 fromFile 可见的全部定义。
	VisibleDefinitions(fromFile string) []*DefinitionEntry
}

// DefinitionIndex 是语言实现提供的全局定义索引。
//
// 索引主键、声明/定义替换、重载候选归并均属语言语义，core 只保留契约：
//   - 全量索引键：Java 为 QN+Kind+Path；C/C++ 同为 QN+Kind+Path（声明/定义与
//     跨文件同名需共存），但语义由各自实现负责；
//   - 主索引：裸 QN → 「定义优先」条目；
//   - 重载索引：去参 QN → 候选（Java 仅 Method，C/C++ 含 Function 族）。
//
// 实现内部不加锁：调用方（BaseGlobalContext）在同一把锁下读写。
type DefinitionIndex interface {
	// Add 索引一个定义。
	//   added=false 表示同键已存在（重复定义，忽略）；
	//   replaced 非 nil 表示该定义原地替换了旧条目（如声明 → 定义）。
	Add(def *DefinitionEntry) (replaced *DefinitionEntry, added bool)

	// FindByQualifiedName 按限定名查找定义（定义优先）。
	FindByQualifiedName(qn string) (*DefinitionEntry, bool)

	// FindAllByQualifiedName 按限定名返回全部定义（定义优先排序）。
	FindAllByQualifiedName(qn string) []*DefinitionEntry

	// FindByShortName 按短名返回全部定义。
	FindByShortName(name string) []*DefinitionEntry

	// FindOverloadCandidates 按不含参数的 QN 查找可重载候选。
	FindOverloadCandidates(noParamsQN string) ([]*DefinitionEntry, bool)
}

// BaseGlobalContext 是 GlobalContext 的通用实现：只保留容器、锁与生命周期，
// 索引结构与策略由语言实现注入的 DefinitionIndex 承担。
type BaseGlobalContext struct {
	resolver SymbolResolver
	builder  SymbolBuilder
	index    DefinitionIndex

	mu           sync.RWMutex
	fileContexts map[string]FileContext
	definitions  []*DefinitionEntry
}

// NewBaseGlobalContext 创建全局上下文 Base 实现。
func NewBaseGlobalContext(resolver SymbolResolver, builder SymbolBuilder, index DefinitionIndex) *BaseGlobalContext {
	return &BaseGlobalContext{
		resolver:     resolver,
		builder:      builder,
		index:        index,
		fileContexts: make(map[string]FileContext),
		definitions:  make([]*DefinitionEntry, 0),
	}
}

// RegisterFileContext 登记文件内全部定义（同一把写锁下批量完成）。
func (gc *BaseGlobalContext) RegisterFileContext(fc FileContext) {
	gc.RegisterFileContextWith(fc, nil)
}

// RegisterFileContextWith 是 RegisterFileContext 的模板方法：
// 在同一把写锁内先执行 before（语言专属注册，如 Java 的包层级），再登记文件与定义。
// before 内必须使用 AddDefinitionLocked（已持写锁，不可再调加锁方法）。
func (gc *BaseGlobalContext) RegisterFileContextWith(fc FileContext, before func()) {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	if before != nil {
		before()
	}
	gc.fileContexts[fc.FilePath()] = fc
	for _, def := range fc.Definitions() {
		gc.addDefinitionLocked(def)
	}
}

// FileContexts 返回全部文件上下文（只读快照）。
func (gc *BaseGlobalContext) FileContexts() map[string]FileContext {
	gc.mu.RLock()
	defer gc.mu.RUnlock()
	out := make(map[string]FileContext, len(gc.fileContexts))
	for k, v := range gc.fileContexts {
		out[k] = v
	}
	return out
}

// FileContext 按路径返回单个文件上下文。
func (gc *BaseGlobalContext) FileContext(relPath string) (FileContext, bool) {
	gc.mu.RLock()
	defer gc.mu.RUnlock()
	fc, ok := gc.fileContexts[relPath]
	return fc, ok
}

// Definitions 返回全部定义（只读快照）。
func (gc *BaseGlobalContext) Definitions() []*DefinitionEntry {
	gc.mu.RLock()
	defer gc.mu.RUnlock()
	return append([]*DefinitionEntry(nil), gc.definitions...)
}

// AddDefinition 添加一个定义（幂等：已存在的 QN+Kind+Path 忽略）。
func (gc *BaseGlobalContext) AddDefinition(def *DefinitionEntry) {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	gc.addDefinitionLocked(def)
}

// AddDefinitionLocked 在调用方已持写锁时登记定义。
// 供语言实现的自定义注册流程（如 RegisterFileContextWith 的 before）复用，
// 避免重复实现索引同步逻辑。
func (gc *BaseGlobalContext) AddDefinitionLocked(def *DefinitionEntry) {
	gc.addDefinitionLocked(def)
}

// addDefinitionLocked 调用语言索引登记定义，并维护 definitions 切片。
// 索引负责去重与声明/定义替换判定，Base 只按结果同步切片顺序。
func (gc *BaseGlobalContext) addDefinitionLocked(def *DefinitionEntry) {
	replaced, added := gc.index.Add(def)
	if !added {
		return
	}
	if replaced != nil {
		for i, d := range gc.definitions {
			if d == replaced {
				gc.definitions[i] = def
				return
			}
		}
	}
	gc.definitions = append(gc.definitions, def)
}

// FindByQualifiedName 按限定名查找定义（定义优先，由语言索引决定）。
func (gc *BaseGlobalContext) FindByQualifiedName(qn string) (*DefinitionEntry, bool) {
	gc.mu.RLock()
	defer gc.mu.RUnlock()
	return gc.index.FindByQualifiedName(qn)
}

// FindByQualifiedNameLocked 在调用方已持锁（读或写）时按限定名查找定义。
// 供 RegisterFileContextWith 的 before 等持锁流程复用。
func (gc *BaseGlobalContext) FindByQualifiedNameLocked(qn string) (*DefinitionEntry, bool) {
	return gc.index.FindByQualifiedName(qn)
}

// FindAllByQualifiedName 按限定名返回全部定义（定义优先排序，由语言索引决定）。
func (gc *BaseGlobalContext) FindAllByQualifiedName(qn string) []*DefinitionEntry {
	gc.mu.RLock()
	defer gc.mu.RUnlock()
	return gc.index.FindAllByQualifiedName(qn)
}

// FindByShortName 按短名返回全部定义（跨文件，由语言索引决定）。
func (gc *BaseGlobalContext) FindByShortName(name string) []*DefinitionEntry {
	gc.mu.RLock()
	defer gc.mu.RUnlock()
	return gc.index.FindByShortName(name)
}

// FindOverloadCandidates 按不含参数的 QN 查找可重载候选（候选 kind 由语言索引决定）。
// 返回副本，避免调用方篡改索引内部切片。
func (gc *BaseGlobalContext) FindOverloadCandidates(noParamsQN string) ([]*DefinitionEntry, bool) {
	gc.mu.RLock()
	defer gc.mu.RUnlock()
	entries, ok := gc.index.FindOverloadCandidates(noParamsQN)
	if !ok {
		return nil, false
	}
	return append([]*DefinitionEntry(nil), entries...), true
}

// BuildQualifiedName 委托 Builder 拼接 QN。
func (gc *BaseGlobalContext) BuildQualifiedName(parentQN, name string) string {
	return gc.builder.BuildQualifiedName(parentQN, name)
}

// Resolver 返回语言符号解析器。
func (gc *BaseGlobalContext) Resolver() SymbolResolver { return gc.resolver }

// Builder 返回语言符号构建器。
func (gc *BaseGlobalContext) Builder() SymbolBuilder { return gc.builder }

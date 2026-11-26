package context

import (
	"github.com/CodMac/arch-lens-dep-analyzer/core"
	"github.com/CodMac/arch-lens-dep-analyzer/model"
)

// DefinitionIndex 是 Java 的全局定义索引。
//
// 键与策略编码 Java 语义：
//   - 全量索引键 = QN + "|" + Kind + "|" + Path：同名类可在不同包/文件共存。
//   - 主索引 = 裸 QN：Java 无声明/定义分离，条目均为定义。
//   - 重载索引 = 去参 QN：仅 Method 参与重载消解。
//
// 实现内部不加锁，由 BaseGlobalContext 在同一把锁下调用。
type DefinitionIndex struct {
	builder core.SymbolBuilder

	qnIndex       map[string]*core.DefinitionEntry
	primaryIndex  map[string]*core.DefinitionEntry
	overloadIndex map[string][]*core.DefinitionEntry

	// qnAllIndex 同 QN 的全部条目，按注册顺序保存。
	qnAllIndex map[string][]*core.DefinitionEntry
	// shortNameIndex 短名 → 全部条目（跨文件）。
	shortNameIndex map[string][]*core.DefinitionEntry
}

// NewDefinitionIndex 创建 Java 全局定义索引。
func NewDefinitionIndex(builder core.SymbolBuilder) *DefinitionIndex {
	return &DefinitionIndex{
		builder:        builder,
		qnIndex:        make(map[string]*core.DefinitionEntry),
		primaryIndex:   make(map[string]*core.DefinitionEntry),
		overloadIndex:  make(map[string][]*core.DefinitionEntry),
		qnAllIndex:     make(map[string][]*core.DefinitionEntry),
		shortNameIndex: make(map[string][]*core.DefinitionEntry),
	}
}

// Add 索引一个定义。
func (idx *DefinitionIndex) Add(def *core.DefinitionEntry) (*core.DefinitionEntry, bool) {
	key := qnKey(def.Element.QualifiedName, def.Element.Kind, def.Element.Path)
	if _, exists := idx.qnIndex[key]; exists {
		return nil, false
	}
	idx.qnIndex[key] = def
	idx.qnAllIndex[def.Element.QualifiedName] = append(idx.qnAllIndex[def.Element.QualifiedName], def)
	idx.shortNameIndex[def.Element.Name] = append(idx.shortNameIndex[def.Element.Name], def)

	if _, exists := idx.primaryIndex[def.Element.QualifiedName]; !exists {
		idx.primaryIndex[def.Element.QualifiedName] = def
	}

	if def.Element.Kind == model.Method {
		key := idx.builder.BuildQualifiedName(def.ParentQN, def.Element.Name)
		idx.overloadIndex[key] = append(idx.overloadIndex[key], def)
	}
	return nil, true
}

// FindByQualifiedName 按限定名查找定义。
func (idx *DefinitionIndex) FindByQualifiedName(qn string) (*core.DefinitionEntry, bool) {
	e, ok := idx.primaryIndex[qn]
	return e, ok
}

// FindAllByQualifiedName 按限定名返回全部定义。
func (idx *DefinitionIndex) FindAllByQualifiedName(qn string) []*core.DefinitionEntry {
	entries := idx.qnAllIndex[qn]
	if len(entries) == 0 {
		return nil
	}
	return append([]*core.DefinitionEntry(nil), entries...)
}

// FindByShortName 按短名返回全部定义（跨文件）。
func (idx *DefinitionIndex) FindByShortName(name string) []*core.DefinitionEntry {
	entries := idx.shortNameIndex[name]
	if len(entries) == 0 {
		return nil
	}
	return append([]*core.DefinitionEntry(nil), entries...)
}

// FindOverloadCandidates 按不含参数的 QN 查找可重载候选。
func (idx *DefinitionIndex) FindOverloadCandidates(noParamsQN string) ([]*core.DefinitionEntry, bool) {
	entries, ok := idx.overloadIndex[noParamsQN]
	return entries, ok
}

// qnKey 构造全量索引键：QN + "|" + Kind + "|" + Path。
func qnKey(qn string, kind model.ElementKind, path string) string {
	return qn + "|" + string(kind) + "|" + path
}

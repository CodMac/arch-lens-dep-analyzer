package context

import (
	"github.com/CodMac/arch-lens-dep-analyzer/core"
	"github.com/CodMac/arch-lens-dep-analyzer/model"
)

// DefinitionIndex 是 C/C++ 的全局定义索引。
//
// 键与策略编码 C/C++ 语义：
//   - 全量索引键 = QN + "|" + Kind + "|" + Path：声明/定义 QN 相同但 Kind 不同
//     （FUNCTION_DECL vs FUNCTION），需共存；`static` 函数、多个示例文件的 `main`、
//     合并副本会在不同文件出现相同 QN+Kind，也需共存（否则整文件元素丢失）。
//   - 主索引 = 裸 QN：定义优先，纯声明（无对应定义）也需可查找。
//   - 重载索引 = 去参 QN：自由函数与声明同样参与重载消解。
//
// 实现内部不加锁，由 BaseGlobalContext 在同一把锁下调用。
type DefinitionIndex struct {
	builder core.SymbolBuilder

	qnIndex       map[string]*core.DefinitionEntry
	primaryIndex  map[string]*core.DefinitionEntry
	overloadIndex map[string][]*core.DefinitionEntry

	// qnAllIndex 同 QN 的全部条目（含不同 Kind），按注册顺序保存。
	qnAllIndex map[string][]*core.DefinitionEntry
	// shortNameIndex 短名 → 全部条目（跨文件）。
	shortNameIndex map[string][]*core.DefinitionEntry
}

// NewDefinitionIndex 创建 C/C++ 全局定义索引。
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
	if existing, exists := idx.qnIndex[key]; exists {
		// 声明与定义同 QN 同 Kind（C 的 struct Fwd; struct Fwd{};）：定义覆盖声明。
		if existing.IsDeclaration && !def.IsDeclaration {
			idx.replace(existing, def)
			idx.qnIndex[key] = def
			idx.primaryIndex[def.Element.QualifiedName] = def
			return existing, true
		}
		return nil, false
	}

	idx.qnIndex[key] = def
	idx.qnAllIndex[def.Element.QualifiedName] = append(idx.qnAllIndex[def.Element.QualifiedName], def)
	idx.shortNameIndex[def.Element.Name] = append(idx.shortNameIndex[def.Element.Name], def)

	// 主索引：定义优先。声明不覆盖已有定义，但纯声明符号仍需可查找；
	// 若先注册的是声明、后注册定义，则定义覆盖声明。
	if existing, exists := idx.primaryIndex[def.Element.QualifiedName]; !exists {
		idx.primaryIndex[def.Element.QualifiedName] = def
	} else if existing.IsDeclaration && !def.IsDeclaration {
		idx.primaryIndex[def.Element.QualifiedName] = def
	}

	if isOverloadKind(def.Element.Kind) {
		idx.appendOverload(def)
	}
	return nil, true
}

// FindByQualifiedName 按限定名查找定义（定义优先）。
func (idx *DefinitionIndex) FindByQualifiedName(qn string) (*core.DefinitionEntry, bool) {
	e, ok := idx.primaryIndex[qn]
	return e, ok
}

// FindAllByQualifiedName 按限定名返回全部定义（定义优先排序）。
func (idx *DefinitionIndex) FindAllByQualifiedName(qn string) []*core.DefinitionEntry {
	entries := idx.qnAllIndex[qn]
	if len(entries) == 0 {
		return nil
	}
	out := make([]*core.DefinitionEntry, 0, len(entries))
	for _, e := range entries {
		if !e.IsDeclaration {
			out = append(out, e)
		}
	}
	for _, e := range entries {
		if e.IsDeclaration {
			out = append(out, e)
		}
	}
	return out
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

// replace 用定义替换已登记的声明（保持注册顺序与索引一致）。
func (idx *DefinitionIndex) replace(old, def *core.DefinitionEntry) {
	replaceEntry(idx.qnAllIndex[def.Element.QualifiedName], old, def)
	replaceEntry(idx.shortNameIndex[def.Element.Name], old, def)
	if isOverloadKind(def.Element.Kind) {
		key := idx.builder.BuildQualifiedName(def.ParentQN, def.Element.Name)
		entries := idx.overloadIndex[key]
		for i, d := range entries {
			if d == old {
				entries[i] = def
				break
			}
		}
	}
}

func (idx *DefinitionIndex) appendOverload(def *core.DefinitionEntry) {
	key := idx.builder.BuildQualifiedName(def.ParentQN, def.Element.Name)
	idx.overloadIndex[key] = append(idx.overloadIndex[key], def)
}

// qnKey 构造全量索引键：QN + "|" + Kind + "|" + Path。
func qnKey(qn string, kind model.ElementKind, path string) string {
	return qn + "|" + string(kind) + "|" + path
}

// replaceEntry 在切片中把 old 原地替换为 def。
func replaceEntry(entries []*core.DefinitionEntry, old, def *core.DefinitionEntry) {
	for i, e := range entries {
		if e == old {
			entries[i] = def
			return
		}
	}
}

// isOverloadKind 判断元素 kind 是否参与重载索引。
func isOverloadKind(kind model.ElementKind) bool {
	switch kind {
	case model.Method, model.Function, model.MethodDecl, model.FunctionDecl:
		return true
	}
	return false
}

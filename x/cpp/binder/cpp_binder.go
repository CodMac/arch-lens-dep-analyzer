// Package binder 实现 C/C++ 语言 core.Binder 接口：
// 将元素上收集到的 raw 类型字符串（返回值/参数/变量/字段类型）
// 解析为 QualifiedName，写入 WithQN 后缀的 Mores 键。
//
// C/C++ 与 Java 的关键差异：
//   - QN 分隔符为 ::，短名作用域上溯沿 ns/类嵌套链
//   - typedef/using 别名链需先展开再解析
//   - 无 import 语句，跨文件类型靠全局 QN 与 include 表
package binder

import (
	"strings"

	"github.com/CodMac/arch-lens-dep-analyzer/core"
	"github.com/CodMac/arch-lens-dep-analyzer/model"
	"github.com/CodMac/arch-lens-dep-analyzer/x/cpp/constants"
	cppctx "github.com/CodMac/arch-lens-dep-analyzer/x/cpp/context"
	"github.com/CodMac/arch-lens-dep-analyzer/x/cpp/helper"
)

// Binder 实现 core.Binder。
type Binder struct {
	resolver core.SymbolResolver
}

// New 创建 C/C++ Binder。
func New(resolver core.SymbolResolver) *Binder {
	return &Binder{resolver: resolver}
}

// BindSymbols 遍历全局上下文中的全部定义，完成 raw 类型到 QN 的绑定。
// GlobalContext 内部自加锁，此处通过快照遍历，不持有任何锁。
func (b *Binder) BindSymbols(gc core.GlobalContext) error {
	for _, fCtx := range gc.FileContexts() {
		for _, entry := range fCtx.Definitions() {
			elem := entry.Element
			if elem.Extra == nil {
				continue
			}
			mores := elem.Extra.Mores

			// 1. 返回值
			if raw, ok := mores[constants.FunctionReturnType].(string); ok && raw != "" {
				mores[constants.FunctionReturnTypeWithQN] = b.resolveToQN(gc, fCtx, elem.QualifiedName, raw)
			}

			// 2. 参数列表 ["type name", ...]
			if params, ok := mores[constants.FunctionParameters].([]string); ok {
				qnParams := make([]string, len(params))
				copy(qnParams, params)
				for i, p := range params {
					if p == "..." {
						qnParams[i] = p
						continue
					}
					typePart, namePart := splitParam(p)
					if typePart == "" {
						continue
					}
					qn := b.resolveToQN(gc, fCtx, elem.QualifiedName, typePart)
					if namePart != "" {
						qnParams[i] = qn + " " + namePart
					} else {
						qnParams[i] = qn
					}
				}
				mores[constants.FunctionParametersWithQN] = qnParams
			}

			// 3. 变量类型
			if raw, ok := mores[constants.VariableRawType].(string); ok && raw != "" {
				mores[constants.VariableTypeWithQN] = b.resolveToQN(gc, fCtx, elem.QualifiedName, raw)
			}

			// 4. 字段类型
			if raw, ok := mores[constants.FieldRawType].(string); ok && raw != "" {
				mores[constants.FieldTypeWithQN] = b.resolveToQN(gc, fCtx, elem.QualifiedName, raw)
			}

			// 5. typedef/using 底层类型（concept 的 underlying 是约束表达式，不是类型，跳过）
			if isConcept(mores) {
				continue
			}
			if raw, ok := mores[constants.TypeUnderlying].(string); ok && raw != "" {
				mores[constants.TypeUnderlyingWithQN] = b.resolveToQN(gc, fCtx, elem.QualifiedName, raw)
			}
		}
	}
	return nil
}

// resolveToQN 将 raw 类型字符串解析为 QN；基本类型原样返回。
// ownerQN 用于短名的词法作用域上溯。
func (b *Binder) resolveToQN(gc core.GlobalContext, fCtx core.FileContext, ownerQN, rawType string) string {
	rawType = helper.NormalizeTypeText(rawType)
	if rawType == "" {
		return ""
	}

	base := helper.BaseTypeName(rawType)
	if base == "" {
		return rawType
	}
	if b.resolver.IsPrimitive(base) {
		return base
	}

	// typedef/using 别名链展开（最多 8 层，防环）。
	// 单大写字母（T/B/E…）也可能是真实类型或别名，必须先展开/查找，
	// 只有确实解析不到时才按模板类型变量处理。
	origBase := base
	base = b.expandAlias(gc, fCtx, ownerQN, base, 8)
	if base == "" {
		return rawType
	}
	// using 声明引入的类型（using ns::Type; Type x;）：按目标限定名解析。
	if !strings.Contains(base, helper.QNSep) {
		if ua, ok := fCtx.(cppctx.UsingAware); ok {
			if target, found := ua.UsingAlias(base); found && target != "" {
				base = target
			}
		}
	}
	if b.resolver.IsPrimitive(base) {
		return base
	}
	// 模板别名底层退化为模板变量（template<class T> using Vec = T*;）：
	// 展开结果 "T" 无意义，保留别名自身 QN。
	if helper.IsTypeVariable(base) && base != origBase {
		if elem := b.findAliasElem(gc, fCtx, ownerQN, origBase); elem != nil {
			return elem.QualifiedName
		}
	}

	// 短名 + owner 作用域：沿词法嵌套链上溯（ns::Class::member -> ns::Class -> ns）。
	if !strings.Contains(base, helper.QNSep) && ownerQN != "" {
		for scope := helper.PkgOf(ownerQN); scope != ""; {
			scoped := helper.BuildQN(scope, base)
			if entry, ok := gc.FindByQualifiedName(scoped); ok {
				return entry.Element.QualifiedName
			}
			next := helper.PkgOf(scope)
			if next == scope {
				break
			}
			scope = next
		}
	}

	ele := b.resolver.ResolveType(gc, fCtx, base, model.Class)
	if ele == nil {
		return base
	}
	return ele.QualifiedName
}

// expandAlias 展开 typedef/using 别名链。
// 查找顺序：限定名 → owner 词法作用域上溯 → 文件内短名 → 全局唯一短名。
// 全局存在多个同名别名时视为歧义、不展开（交给 ResolveType 按文件作用域解析）。
func (b *Binder) expandAlias(gc core.GlobalContext, fCtx core.FileContext, ownerQN, name string, depth int) string {
	if depth <= 0 {
		return name
	}
	elem := b.findAliasElem(gc, fCtx, ownerQN, name)
	if elem == nil || elem.Extra == nil {
		return name
	}
	// concept 的 underlying 是约束表达式，不是可展开的类型别名。
	if isConcept(elem.Extra.Mores) {
		return name
	}
	underlying, ok := elem.Extra.Mores[constants.TypeUnderlying].(string)
	if !ok || underlying == "" {
		return name
	}
	// 匿名 struct/union/class 承载实体（typedef struct {...} Point;）：
	// 别名本身即目标类型，底层文本不是可解析的类型名，不展开。
	if isAnonymousTypeText(underlying) {
		return elem.QualifiedName
	}
	next := helper.BaseTypeName(underlying)
	if next == "" || next == name {
		return name
	}
	return b.expandAlias(gc, fCtx, elem.QualifiedName, next, depth-1)
}

// findAliasElem 按作用域优先级查找 typedef/using 元素：
// 限定名精确 → owner 词法作用域上溯 → 文件内短名 → 全局唯一短名。
// 全局同名候选多于一个时返回 nil（歧义）。
func (b *Binder) findAliasElem(gc core.GlobalContext, fCtx core.FileContext, ownerQN, name string) *model.CodeElement {
	if strings.Contains(name, helper.QNSep) {
		if entry, ok := gc.FindByQualifiedName(name); ok && entry.Element.Kind == model.Type {
			return entry.Element
		}
		return nil
	}
	// owner 词法作用域上溯：Outer::Inner::v 的 alias_t 先查 Outer::Inner::alias_t。
	for scope := helper.PkgOf(ownerQN); scope != ""; {
		if entry, ok := gc.FindByQualifiedName(helper.BuildQN(scope, name)); ok && entry.Element.Kind == model.Type {
			return entry.Element
		}
		next := helper.PkgOf(scope)
		if next == scope {
			break
		}
		scope = next
	}
	// 文件内短名优先（跨文件同名别名各按本文件解析）。
	if entries, ok := fCtx.FindByShortName(name); ok {
		for _, e := range entries {
			if e.Element.Kind == model.Type {
				return e.Element
			}
		}
	}
	// 全局短名：仅唯一候选时使用，避免跨文件同名别名互相污染。
	// 单大写字母可能是模板类型变量，不做跨文件全局查找（本文件内已查过）。
	if helper.IsTypeVariable(name) {
		return nil
	}
	var found *model.CodeElement
	for _, def := range gc.Definitions() {
		if def.Element.Kind != model.Type || def.Element.Name != name {
			continue
		}
		if found != nil && found.QualifiedName != def.Element.QualifiedName {
			return nil
		}
		found = def.Element
	}
	return found
}

// splitParam 将 "int x" / "const char* s" / "std::vector<int> v" 拆为类型与名称。
// 名称恒为最后一个 token（若最后一个 token 是合法标识符且前面还有内容）。
func splitParam(p string) (typePart, namePart string) {
	p = helper.NormalizeTypeText(p)
	if p == "" {
		return "", ""
	}
	// 函数指针参数：int (*cb)(int) —— 名称在 (*name) 内。
	if i := strings.Index(p, "(*"); i >= 0 {
		rest := p[i+2:]
		if j := strings.Index(rest, ")"); j > 0 {
			return p[:i] + "(*)", rest[:j]
		}
	}
	idx := strings.LastIndexAny(p, " \t")
	if idx < 0 {
		return p, ""
	}
	candidate := strings.TrimSpace(p[idx+1:])
	if candidate == "" || !isIdentifier(candidate) {
		return p, ""
	}
	return strings.TrimSpace(p[:idx]), candidate
}

// isAnonymousTypeText 判断类型文本是否为匿名 struct/union/class/enum 定义
// （typedef struct { ... } Point; 的底层文本）。
func isAnonymousTypeText(s string) bool {
	s = strings.TrimSpace(s)
	for _, kw := range []string{"struct", "union", "class", "enum"} {
		if !strings.HasPrefix(s, kw) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(s, kw))
		return strings.HasPrefix(rest, "{") || strings.HasPrefix(rest, ":")
	}
	return false
}

// isConcept 判断元素是否为 C++20 concept（其 underlying 是约束表达式）。
func isConcept(mores map[string]any) bool {
	v, ok := mores[constants.TypeIsConcept].(bool)
	return ok && v
}

// isIdentifier 判断是否为合法 C/C++ 标识符。
func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

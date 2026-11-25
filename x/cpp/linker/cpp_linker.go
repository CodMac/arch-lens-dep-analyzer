// Package linker 实现 C/C++ 语言 core.Linker 接口：
//   - Contain 拓扑（Namespace 层级 / File → 顶层元素）
//   - ImplLink 声明-实现链接（函数级签名配对，定义 → 声明）
package linker

import (
	"strings"

	"github.com/CodMac/arch-lens-dep-analyzer/core"
	"github.com/CodMac/arch-lens-dep-analyzer/model"
	"github.com/CodMac/arch-lens-dep-analyzer/x/cpp/constants"
	cppctx "github.com/CodMac/arch-lens-dep-analyzer/x/cpp/context"
	"github.com/CodMac/arch-lens-dep-analyzer/x/cpp/helper"
)

// Linker 实现 core.Linker。
type Linker struct{}

// New 创建 C/C++ Linker。
func New() *Linker { return &Linker{} }

// LinkHierarchy 构建 C/C++ 拓扑树并补齐 ImplLink。
func (l *Linker) LinkHierarchy(gc core.GlobalContext) []*model.DependencyRelation {
	relMap := make(map[string]*model.DependencyRelation)

	// --- 1. File → 顶层元素 ---
	for _, fCtx := range gc.FileContexts() {
		filePath := fCtx.FilePath()
		for _, entry := range fCtx.Definitions() {
			if entry.Element.Kind == model.File {
				continue
			}
			if isTopLevel(entry, fCtx) {
				l.addRel(relMap, model.File, filePath, entry.Element.Kind, entry.Element.QualifiedName)
			}
		}
	}

	// --- 2. Namespace 层级（C++） ---
	l.linkNamespaces(gc, relMap)

	// --- 3. ImplLink（定义 → 声明） ---
	l.linkImpl(gc, relMap)

	result := make([]*model.DependencyRelation, 0, len(relMap))
	for _, rel := range relMap {
		result = append(result, rel)
	}
	return result
}

// isTopLevel 判定元素是否直接挂在文件节点下。
// C++：无 namespace 前缀（ParentQN 为空）的元素；C：ParentQN 为空。
func isTopLevel(entry *core.DefinitionEntry, fCtx core.FileContext) bool {
	if entry.ParentQN == "" {
		return true
	}
	// 父级是命名空间元素（非类/结构体）→ 也属文件顶层拓扑。
	if p, ok := fCtx.FindByQualifiedName(entry.ParentQN); ok {
		return p.Element.Kind == model.Namespace
	}
	return false
}

// linkNamespaces 构建 Namespace → SubNamespace、Namespace → 元素、Namespace → File 关系。
func (l *Linker) linkNamespaces(gc core.GlobalContext, relMap map[string]*model.DependencyRelation) {
	for _, def := range gc.Definitions() {
		if def.Element.Kind != model.Namespace {
			continue
		}
		qn := def.Element.QualifiedName
		parent := helper.PkgOf(qn)
		if parent == "" {
			continue
		}
		// 父级必须是真实存在的 namespace 元素；否则（QN 异常导致的伪父级）
		// 会产出指向不存在元素的悬空 CONTAIN。
		if p, ok := gc.FindByQualifiedName(parent); !ok || p.Element.Kind != model.Namespace {
			continue
		}
		l.addRel(relMap, model.Namespace, parent, model.Namespace, qn)
	}

	// Namespace → 直接子元素由 Extractor 的 Contain 产出（按 ParentQN），此处不重复生成。

	// Namespace → File（文件内出现该 namespace 即建边）。
	for _, fCtx := range gc.FileContexts() {
		nfc, ok := fCtx.(cppctx.NamespaceAware)
		if !ok {
			continue
		}
		for _, ns := range nfc.Namespaces() {
			l.addRel(relMap, model.Namespace, ns, model.File, fCtx.FilePath())
		}
	}
}

// linkImpl 按函数签名配对声明与定义，产出 ImplLink（定义 → 声明）。
// 签名 = 完整 QN（含 ns::Class）+ 参数类型列表；static 函数无声明不参与；
// 只声明无定义不建链接。
func (l *Linker) linkImpl(gc core.GlobalContext, relMap map[string]*model.DependencyRelation) {
	type sigInfo struct {
		defs  []*model.CodeElement
		decls []*model.CodeElement
	}
	index := make(map[string]*sigInfo)

	for _, def := range gc.Definitions() {
		elem := def.Element
		if !isFunctionKind(elem.Kind) {
			continue
		}
		// static 自由函数是内部链接、无跨文件声明，不参与配对；
		// static 成员函数是外部链接（声明在类内、定义在类外），应正常配对。
		if isStaticFreeFunc(elem) {
			continue
		}
		sig := implSignature(elem)
		if sig == "" {
			continue
		}
		info := index[sig]
		if info == nil {
			info = &sigInfo{}
			index[sig] = info
		}
		if isDeclKind(elem.Kind) {
			info.decls = append(info.decls, elem)
		} else {
			info.defs = append(info.defs, elem)
		}
	}

	for sig, info := range index {
		if len(info.defs) == 0 || len(info.decls) == 0 {
			continue
		}
		// 同一函数可能在多个头文件重复声明，全部建链，避免只连第一个声明而丢失其余。
		for _, def := range info.defs {
			for _, decl := range info.decls {
				key := string(model.ImplLink) + ":" + def.QualifiedName + "->" + decl.QualifiedName + "@" + decl.Path
				if _, exists := relMap[key]; exists {
					continue
				}
				relMap[key] = &model.DependencyRelation{
					Type:   model.ImplLink,
					Source: def,
					Target: decl,
					Mores:  map[string]interface{}{constants.RelImplLinkSignature: sig},
				}
			}
		}
	}
}

// implSignature 构造 ImplLink 配对签名：函数名 QN（去尾部参数列表）+ 规范化参数类型列表。
// 参数类型以 Binder 的 parameters_with_qn 为基准（已展开 typedef/using、剥离 struct tag、
// 数组/指针退化），再从原始文本补回 const/指针/引用限定，避免 `f(S)` 与 `f(const S&)`
// 因 WithQN 丢限定符而误并。
func implSignature(elem *model.CodeElement) string {
	if elem.Extra == nil {
		return ""
	}
	raw, _ := elem.Extra.Mores[constants.FunctionParameters].([]string)
	withQN, _ := elem.Extra.Mores[constants.FunctionParametersWithQN].([]string)
	types := make([]string, 0, len(raw))
	for i, p := range raw {
		q := ""
		if i < len(withQN) {
			q = withQN[i]
		}
		types = append(types, canonicalParamType(p, q))
	}
	return stripParamList(elem.QualifiedName) + "(" + strings.Join(types, ",") + ")"
}

// canonicalParamType 生成参数类型的规范化文本：
// 基类型取 WithQN（别名展开 / tag 剥离 / 指针引用数组剥离后的 QN），
// 限定符（const/volatile/指针/引用/数组）从原始文本补回，数组按指针退化。
func canonicalParamType(raw, withQN string) string {
	rawType := paramTypeOnly(raw)
	base := ""
	if withQN != "" {
		base = paramTypeOnly(withQN)
	}
	if base == "" || strings.ContainsAny(base, "*&[]()") {
		base = helper.BaseTypeName(rawType)
	}
	if base == "" {
		return rawType
	}
	ptrs := strings.Count(rawType, "*") + strings.Count(rawType, "[")
	refs := strings.Count(rawType, "&")
	var b strings.Builder
	if hasWord(rawType, "const") {
		b.WriteString("const ")
	}
	b.WriteString(base)
	b.WriteString(strings.Repeat("*", ptrs))
	b.WriteString(strings.Repeat("&", refs))
	if hasWord(rawType, "volatile") {
		b.WriteString(" volatile")
	}
	return b.String()
}

// hasWord 判断类型文本中是否含指定关键字 token。
func hasWord(s, word string) bool {
	for _, f := range strings.Fields(s) {
		if f == word {
			return true
		}
	}
	return false
}

// stripParamList 去掉 QN 尾部的参数列表：`ns::C::f(int)` → `ns::C::f`，
// `C::operator()()` → `C::operator()`。非函数 QN 原样返回。
func stripParamList(qn string) string {
	if !strings.HasSuffix(qn, ")") {
		return qn
	}
	depth := 0
	for i := len(qn) - 1; i >= 0; i-- {
		switch qn[i] {
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				return qn[:i]
			}
		}
	}
	return qn
}

// paramTypeOnly 从 "int x" 提取 "int"。
func paramTypeOnly(p string) string {
	p = helper.NormalizeTypeText(p)
	if i := strings.LastIndexAny(p, " \t"); i > 0 {
		candidate := strings.TrimSpace(p[i+1:])
		if candidate != "" && !strings.ContainsAny(candidate, "*&[]<>,()") {
			return strings.TrimSpace(p[:i])
		}
	}
	return p
}

// isStaticFreeFunc 判定是否为 static 自由函数（内部链接，不参与 ImplLink 配对）。
func isStaticFreeFunc(elem *model.CodeElement) bool {
	if elem.Kind != model.Function && elem.Kind != model.FunctionDecl {
		return false
	}
	return isStaticFunc(elem)
}

// isStaticFunc 判定函数是否为 static。
func isStaticFunc(elem *model.CodeElement) bool {
	if elem.Extra == nil {
		return false
	}
	for _, m := range elem.Extra.Modifiers {
		if m == "static" {
			return true
		}
	}
	return false
}

// addRel 生成唯一 Key 并加入 Map（已存在则跳过）。
func (l *Linker) addRel(m map[string]*model.DependencyRelation, srcKind model.ElementKind, srcQN string, tgtKind model.ElementKind, tgtQN string) {
	key := string(srcKind) + ":" + srcQN + "->" + tgtQN
	if _, exists := m[key]; exists {
		return
	}
	m[key] = &model.DependencyRelation{
		Type: model.Contain,
		Source: &model.CodeElement{
			Kind: srcKind, QualifiedName: srcQN, IsFormSource: true,
		},
		Target: &model.CodeElement{
			Kind: tgtKind, QualifiedName: tgtQN, IsFormSource: true,
		},
	}
}

func isFunctionKind(k model.ElementKind) bool {
	switch k {
	case model.Function, model.Method, model.FunctionDecl, model.MethodDecl:
		return true
	}
	return false
}

func isDeclKind(k model.ElementKind) bool {
	return k == model.FunctionDecl || k == model.MethodDecl
}

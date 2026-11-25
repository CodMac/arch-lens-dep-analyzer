// Package collector 实现 C/C++ 语言 core.Collector 接口：
// 将单个源文件解析为 FileContext（含全部代码元素定义与索引）。
//
// C/C++ 与 Java 的关键差异：
//   - QN 分隔符为 ::（namespace/类/方法层级）
//   - 声明与定义分离（FUNCTION_DECL/METHOD_DECL 与 FUNCTION/METHOD 成对）
//   - 条件编译在 Parser 层做「投影」：未选中分支按字节置空格（等长），
//     AST 中只保留一条语法完整的分支；条件标签由投影区间回填 cpp.condition
//   - 匿名 struct 不产独立元素，字段挂承载实体
package collector

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/CodMac/arch-lens-dep-analyzer/core"
	coreast "github.com/CodMac/arch-lens-dep-analyzer/core/ast"
	"github.com/CodMac/arch-lens-dep-analyzer/model"
	"github.com/CodMac/arch-lens-dep-analyzer/x/cpp/constants"
	cppctx "github.com/CodMac/arch-lens-dep-analyzer/x/cpp/context"
	"github.com/CodMac/arch-lens-dep-analyzer/x/cpp/helper"
	"github.com/CodMac/arch-lens-dep-analyzer/x/cpp/preproc"
)

// Collector 实现 core.Collector。
type Collector struct {
	resolver core.SymbolResolver

	// condRegions 条件编译分支区间：文件路径 -> 被选中的分支区间（按 Start 升序）。
	// 由 Parser 的预处理投影产出：AST 中已不存在 preproc_ifdef 节点，
	// 条件标签只能从投影结果回填。引擎按文件并行调用 CollectDefinitions，故需加锁。
	mu          sync.Mutex
	condRegions map[string][]preproc.Region
	// deadRegions #if 0 死区区间：文件路径 -> 死区（按 Start 升序）。
	// 原始树保留死区代码，需据此跳过其元素。
	deadRegions map[string][]preproc.Region
}

// New 创建 C/C++ Collector。
func New(resolver core.SymbolResolver) *Collector {
	return &Collector{
		resolver:    resolver,
		condRegions: make(map[string][]preproc.Region),
		deadRegions: make(map[string][]preproc.Region),
	}
}

// identified 描述一个待注册的元素及其声明器节点（QN 计算用）。
type identified struct {
	elem     *model.CodeElement
	declNode coreast.Node // 声明器节点（函数/变量/字段），可为 nil
	sig      string       // 函数参数签名 "(int,char*)"，非函数为空
	isDecl   bool         // 是否为「声明」（非定义）：声明与定义共享同一 QN，不参与重复计数
	parentQN string       // 计算后的父级 QN（assignQN 填充）
}

// =============================================================================
// core.Collector 接口实现
// =============================================================================

// CollectDefinitions 从源文件收集符号定义并产出 FileContext。
func (c *Collector) CollectDefinitions(src coreast.SourceFile) (core.FileContext, error) {
	if src == nil || src.Root() == nil {
		return nil, fmt.Errorf("empty source file")
	}
	root := src.Root()
	content := src.Content()
	path := src.Path()

	fCtx := cppctx.NewFileContext(path, root, &content)

	// Step 0: 文件自身元素
	c.initFileElem(fCtx)

	// 条件编译区间：由 Parser 的投影结果提供（AST 中已无 preproc_* 节点）。
	//
	// 未选中分支的代码已被置空，但其中的 #include/#define/#undef/#pragma 仍被保留
	// （见 preproc.KeepIncludes），这些行仍会产出元素，因此 Dropped 区间也要参与
	// 条件标签回填，否则保留下来的宏/导入会丢失 cpp.condition。
	if ps, ok := src.(interface{ Preproc() *preproc.Result }); ok {
		if proj := ps.Preproc(); proj != nil {
			regions := make([]preproc.Region, 0, len(proj.Regions)+len(proj.Dropped))
			regions = append(regions, proj.Regions...)
			regions = append(regions, proj.Dropped...)
			if len(regions) > 0 {
				c.setCondRegions(path, regions)
			}
			if len(proj.Dead) > 0 {
				c.setDeadRegions(path, proj.Dead)
			}
		}
	}

	// Step 1: 顶层声明（#include）
	c.processTopLevelDeclarations(fCtx)

	// Step 2: 递归收集定义
	nameOccurrence := make(map[string]int)
	c.collectBasicDefinitions(root, fCtx, "", "", nameOccurrence)

	// Step 2b: 原始树补元素。投影树只保留每组一个分支，嵌套/其余分支的符号
	// 不在其中；原始树保留 preproc_* 节点与全部分支体，可补齐。
	// AddDefinition 按 QN+Kind 幂等，重复符号自动忽略。
	// 条件标签无需切换：首遍的 Regions/Dropped 区间已覆盖全部分支。
	var rawRoot coreast.Node
	if rp, ok := src.(interface{ RawRoot() coreast.Node }); ok {
		rawRoot = rp.RawRoot()
		if rawRoot != nil {
			c.collectBasicDefinitions(rawRoot, fCtx, "", "", make(map[string]int))
		}
	}

	// Step 2c: 分支回补。仅当原始树不可用（有 ERROR）时才有补充树。
	if ap, ok := src.(interface{ AltRoots() []coreast.Node }); ok {
		for _, altRoot := range ap.AltRoots() {
			if altRoot != nil {
				c.collectBasicDefinitions(altRoot, fCtx, "", "", make(map[string]int))
			}
		}
	}

	// Step 3: 元数据增强
	c.enrichMetadata(fCtx)

	// Step 4: 汇总命名空间列表（C++ 多 namespace）
	c.collectNamespaces(fCtx)

	// Step 5: 展示层字段（NamespaceQN / EnclosingQN / TypeQN）
	c.fillDisplayFields(fCtx)

	// Step 6: 暴露动作提取需要的全部语法树根节点。
	// Extractor 阶段拿不到 SourceFile，只能经由 FileContext 取树。
	roots := cppctx.ActionRoots{Primary: fCtx.Root(), Raw: rawRoot}
	if ap, ok := src.(interface{ AltRoots() []coreast.Node }); ok {
		roots.Alts = ap.AltRoots()
	}
	if ps, ok := src.(interface{ Preproc() *preproc.Result }); ok {
		if proj := ps.Preproc(); proj != nil {
			roots.Dead = proj.Dead
		}
	}
	fCtx.SetActionRoots(roots)

	c.releaseCondStack(fCtx.FilePath())
	return fCtx, nil
}

// fillDisplayFields 填充展示层三字段：
//   - NamespaceQN：元素所属最近外层 namespace 全 QN（无则空）
//   - EnclosingQN：元素直接词法外层（即 ParentQN）
//   - TypeQN：元素所属最外层具名类型（类型元素取自身所在的最外层类型）
//
// 必须在全部定义注册、ParentQN 最终确定、namespace 列表汇总后执行。
func (c *Collector) fillDisplayFields(fCtx core.FileContext) {
	nsSet := make(map[string]bool)
	if nfc, ok := fCtx.(cppctx.NamespaceAware); ok {
		for _, ns := range nfc.Namespaces() {
			nsSet[ns] = true
		}
	}
	for _, entry := range fCtx.Definitions() {
		elem := entry.Element
		if elem.Kind == model.File {
			continue
		}
		displayQN := normalizeDisplayQN(fCtx, elem.QualifiedName, nsSet)
		elem.EnclosingQN = displayEnclosingQN(fCtx, entry)
		elem.NamespaceQN = nearestNamespace(fCtx, entry, nsSet, displayQN)
		elem.TypeQN = outermostType(fCtx, entry, nsSet, elem.NamespaceQN, displayQN)
	}
}

// displayEnclosingQN 返回元素的直接词法外层 QN。
// 外层是 namespace 时剥离重开消歧后缀（ns$2 -> ns）；外层是类型时保留原始 QN。
func displayEnclosingQN(fCtx core.FileContext, entry *core.DefinitionEntry) string {
	if entry.ParentQN == "" {
		return ""
	}
	if parent, ok := fCtx.FindByQualifiedName(entry.ParentQN); ok && parent.Element.Kind == model.Namespace {
		return stripNamespaceDisambig(entry.ParentQN)
	}
	return entry.ParentQN
}

// nearestNamespace 返回元素所属最近外层 namespace 全 QN。
// namespace 元素自身取其父 namespace；其余元素取 QN 的最长 namespace 前缀。
func nearestNamespace(fCtx core.FileContext, entry *core.DefinitionEntry, nsSet map[string]bool, displayQN string) string {
	elem := entry.Element
	if elem.Kind == model.Namespace {
		return helper.PkgOf(displayQN)
	}
	parts := helper.SplitQN(displayQN)
	for i := len(parts) - 1; i > 0; i-- {
		cand := strings.Join(parts[:i], helper.QNSep)
		if isNamespaceQN(fCtx, cand, nsSet) {
			return cand
		}
	}
	// 跨文件类外定义（源文件不写 namespace 块，仅限定定义）：本文件查不到
	// namespace 定义，沿父链回退推导。
	if p := entry.ParentQN; p != "" {
		if parent, ok := fCtx.FindByQualifiedName(p); ok {
			// 父级在本文件可见：递归取其 namespace（父级可能是函数/块/类型）。
			return nearestNamespace(fCtx, parent, nsSet,
				normalizeDisplayQN(fCtx, parent.Element.QualifiedName, nsSet))
		}
		return namespaceFromQNStruct(p)
	}
	return ""
}

// namespaceFromQNStruct 在父级定义不可见时按 QN 结构推导 namespace：
// 函数签名段（含括号）上溯到其宿主，多段 QN 视为「namespace::类型」取前缀；
// 单段无法区分全局类型与 namespace，按类型处理返回空。
// 例：ool_no_block::Remote::method(int) -> ool_no_block；GlobalCls -> ""。
func namespaceFromQNStruct(qn string) string {
	if qn == "" {
		return ""
	}
	if strings.Contains(qn, "(") {
		return namespaceFromQNStruct(helper.PkgOf(qn))
	}
	if strings.Contains(qn, helper.QNSep) {
		return helper.PkgOf(qn)
	}
	return ""
}

// outermostType 返回元素所属最外层具名类型 QN（展示层 L2 宿主）。
// 类型元素（Class/Struct/Enum）取自身命名空间前缀之后的第一段；
// 成员/方法/局部元素沿父链上溯到类型，再取该类型所在的最外层类型。
func outermostType(fCtx core.FileContext, entry *core.DefinitionEntry, nsSet map[string]bool, nsQN, displayQN string) string {
	if isCppTypeKind(entry.Element.Kind) {
		return topTypeSegment(displayQN, nsQN)
	}
	parentQN := entry.ParentQN
	for parentQN != "" {
		parent, ok := fCtx.FindByQualifiedName(parentQN)
		if !ok {
			// 跨文件类外定义（impl_a.cpp 的 impl::Service::member）：父级类型定义
			// 在头文件，本文件上下文查不到，按 QN 结构回退推导。
			return typeFromQN(displayQN, nsQN)
		}
		if isCppTypeKind(parent.Element.Kind) {
			parentDisplayQN := normalizeDisplayQN(fCtx, parent.Element.QualifiedName, nsSet)
			return topTypeSegment(parentDisplayQN, nsQN)
		}
		parentQN = parent.ParentQN
	}
	return ""
}

// normalizeDisplayQN 将 QN 中 namespace 段的重开消歧后缀（$N）剥离，
// 仅用于展示层字段；元素自身的 QualifiedName 保持原样。
func normalizeDisplayQN(fCtx core.FileContext, qn string, nsSet map[string]bool) string {
	parts := helper.SplitQN(qn)
	raw := make([]string, 0, len(parts))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		raw = append(raw, p)
		seg := p
		if isNamespaceQN(fCtx, strings.Join(raw, helper.QNSep), nsSet) {
			seg = stripNamespaceDisambig(p)
		}
		out = append(out, seg)
	}
	return strings.Join(out, helper.QNSep)
}

// isNamespaceQN 判定 QN 是否指向 namespace 元素。
func isNamespaceQN(fCtx core.FileContext, qn string, nsSet map[string]bool) bool {
	if nsSet[qn] {
		return true
	}
	if e, ok := fCtx.FindByQualifiedName(qn); ok && e.Element.Kind == model.Namespace {
		return true
	}
	return false
}

// stripNamespaceDisambig 剥离 QN 末段的数字消歧后缀：ns$2 -> ns。
// 匿名 namespace（$anon）与 lambda/block（$1 但非 namespace）不受影响。
func stripNamespaceDisambig(seg string) string {
	i := strings.LastIndex(seg, "$")
	if i <= 0 || i == len(seg)-1 {
		return seg
	}
	for _, c := range seg[i+1:] {
		if c < '0' || c > '9' {
			return seg
		}
	}
	return seg[:i]
}

// typeFromQN 在父级定义不可见时，按 QN 结构推导最外层类型：
// 去掉 namespace 前缀后，若首段是类型名（非函数签名）且后续还有段，则首段即宿主类型。
// 例：ns=impl, qn=impl::Service::member(int) -> impl::Service；
//
//	ns=impl, qn=impl::split_a(int)::x -> ""（首段是函数，不是类型）。
func typeFromQN(qn, nsQN string) string {
	rest := qn
	if nsQN != "" {
		prefix := nsQN + helper.QNSep
		if strings.HasPrefix(qn, prefix) {
			rest = qn[len(prefix):]
		}
	}
	parts := helper.SplitQN(rest)
	if len(parts) < 2 || strings.Contains(parts[0], "(") {
		return ""
	}
	if nsQN != "" {
		return nsQN + helper.QNSep + parts[0]
	}
	return parts[0]
}

// topTypeSegment 去掉 namespace 前缀后取第一段类型 QN。
// 例：ns=a, qn=a::Outer::Inner -> a::Outer；qn=a::Outer -> a::Outer。
func topTypeSegment(qn, nsQN string) string {
	rest := qn
	if nsQN != "" {
		prefix := nsQN + helper.QNSep
		if strings.HasPrefix(qn, prefix) {
			rest = qn[len(prefix):]
		}
	}
	parts := helper.SplitQN(rest)
	if len(parts) == 0 {
		return ""
	}
	first := parts[0]
	// 首段是函数签名（含括号）说明宿主是函数/局部作用域，无具名宿主类型。
	// 例：freeFnWithLocal()::LocalInFree -> ""。
	if strings.Contains(first, "(") {
		return ""
	}
	if nsQN != "" {
		return nsQN + helper.QNSep + first
	}
	return first
}

// isCppTypeKind 判定是否为具名类型（可作展示层 L2 宿主）。
func isCppTypeKind(k model.ElementKind) bool {
	switch k {
	case model.Class, model.Struct, model.Enum:
		return true
	}
	return false
}

// =============================================================================
// 核心生命周期
// =============================================================================

func (c *Collector) initFileElem(fCtx core.FileContext) {
	filePath := fCtx.FilePath()
	fileElem := &model.CodeElement{
		Kind:          model.File,
		Name:          filepath.Base(filePath),
		QualifiedName: filePath,
		Path:          filePath,
		IsFormSource:  true,
	}
	fCtx.AddDefinition(&core.DefinitionEntry{Element: fileElem, ParentQN: "", Node: fCtx.Root()})
}

// processTopLevelDeclarations 收集 #include 声明（可出现在条件编译分支内）。
func (c *Collector) processTopLevelDeclarations(fCtx core.FileContext) {
	c.walkIncludes(fCtx.Root(), fCtx)
}

func (c *Collector) walkIncludes(node coreast.Node, fCtx core.FileContext) {
	if node == nil {
		return
	}
	if node.Kind() == "preproc_include" {
		if !c.inDeadZone(fCtx.FilePath(), node) {
			c.handleInclude(node, fCtx)
		}
		return
	}
	for i := 0; i < node.ChildCount(); i++ {
		c.walkIncludes(node.Child(i), fCtx)
	}
}

// handleInclude 将 #include 登记为文件级导入。
// 项目内 "foo.h" → File 元素；系统 <stdio.h> → 外部符号（cpp.include.is_system=true）。
func (c *Collector) handleInclude(node coreast.Node, fCtx core.FileContext) {
	pathNode := node.ChildByFieldName("path")
	if pathNode == nil {
		return
	}
	raw := helper.GetNodeContent(pathNode, *fCtx.SourceBytes())
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "<")
	raw = strings.TrimSuffix(raw, ">")
	raw = strings.TrimPrefix(raw, "\"")
	raw = strings.TrimSuffix(raw, "\"")
	if raw == "" {
		return
	}
	isSystem := pathNode.Kind() == "system_lib_string"
	alias := raw
	if i := strings.LastIndexAny(raw, "/\\"); i >= 0 {
		alias = raw[i+1:]
	}
	info := &core.ImportInfo{
		Kind:          model.File,
		Alias:         alias,
		RawImportPath: raw,
		Location:      helper.ExtractLocation(node, fCtx.FilePath()),
	}
	info.SetAttr(constants.ImportAttrIsSystem, isSystem)
	fCtx.AddImport(alias, info)
}

// collectBasicDefinitions 递归收集定义。
// currentQN 为当前作用域 QN；tmplParams 为模板参数原文。
func (c *Collector) collectBasicDefinitions(node coreast.Node, fCtx core.FileContext, currentQN, tmplParams string, occurrences map[string]int) {
	if node == nil {
		return
	}
	src := *fCtx.SourceBytes()

	// friend 声明（friend void f(); / friend class X;）不是成员定义/声明，
	// 不产元素；其内部 declaration 也不应挂到当前类下。
	if node.Kind() == "friend_declaration" {
		return
	}

	// #if 0 死区：原始树会保留死区代码，跳过其元素。
	if node.IsNamed() && c.inDeadZone(fCtx.FilePath(), node) {
		return
	}

	// 模板声明：记录参数文本，传递给内部类型/函数。
	// 模板参数列表只作为文本记录，其内部 parameter_declaration（如 `Small T`）
	// 不是变量元素，不能下钻收集。
	if node.Kind() == "template_declaration" {
		params := ""
		if p := node.ChildByFieldName("parameters"); p != nil {
			params = helper.GetNodeContent(p, src)
		}
		childOcc := make(map[string]int)
		for i := 0; i < node.ChildCount(); i++ {
			ch := node.Child(i)
			if ch.Kind() == "template_parameter_list" {
				continue
			}
			c.collectBasicDefinitions(ch, fCtx, currentQN, params, childOcc)
		}
		return
	}

	if node.IsNamed() {
		items, kind := c.identifyElements(node, fCtx, tmplParams)
		if len(items) > 0 {
			cond := c.activeCondition(fCtx.FilePath(), node)
			for i := range items {
				it := &items[i]
				c.assignQN(it, currentQN, occurrences, src)
				it.elem.Path = fCtx.FilePath()
				it.elem.Location = helper.ExtractLocation(node, fCtx.FilePath())
				if cond != "" {
					it.elem.GetMores()[constants.Condition] = cond
				}
				fCtx.AddDefinition(&core.DefinitionEntry{
					Element:       it.elem,
					ParentQN:      it.parentQN,
					IsDeclaration: it.isDecl,
					Node:          node,
				})
			}

			if helper.IsScopeContainer(kind) {
				// 纯声明（函数/方法原型）没有函数体，其参数不是变量：
				// 不下钻参数列表，避免为原型产出 `f(int)::param` 这类幽灵 Variable。
				// 定义（Function/Method）的形参仍在作用域内，照常收集。
				if kind == model.FunctionDecl || kind == model.MethodDecl {
					return
				}
				scopeQN := items[0].elem.QualifiedName
				childOcc := make(map[string]int)
				for i := 0; i < node.ChildCount(); i++ {
					c.collectBasicDefinitions(node.Child(i), fCtx, scopeQN, tmplParams, childOcc)
				}
				return
			}

			// 匿名 struct/union 承载实体（决策 #2）：字段挂 typedef 名 / 变量名。
			// 多声明器（typedef struct {...} A, *B; / struct {...} s1, s2;）时每个承载实体各挂一份。
			if carriers := anonymousStructCarriers(node, items, src); len(carriers) > 0 {
				for _, carrier := range carriers {
					childOcc := make(map[string]int)
					for i := 0; i < node.ChildCount(); i++ {
						c.collectBasicDefinitions(node.Child(i), fCtx, carrier, tmplParams, childOcc)
					}
				}
				return
			}
		}
	}

	for i := 0; i < node.ChildCount(); i++ {
		c.collectBasicDefinitions(node.Child(i), fCtx, currentQN, tmplParams, occurrences)
	}
}

// anonymousStructCarriers 判定节点是否为「匿名 struct/union 的承载声明」，
// 若是则返回全部承载实体的 QN（typedef 名 / 变量名 / 字段名），供其成员字段挂载。
// 覆盖形态：typedef struct {...} Point; / struct {...} obj; / struct {...} inner;
// 多声明器形态（typedef struct {...} A, *B;）返回全部承载实体，字段各挂一份（允许冗余）。
func anonymousStructCarriers(node coreast.Node, items []identified, src []byte) []string {
	if len(items) == 0 {
		return nil
	}
	switch node.Kind() {
	case "type_definition", "declaration", "field_declaration":
	default:
		return nil
	}
	t := node.ChildByFieldName("type")
	if t == nil {
		return nil
	}
	if t.Kind() != "struct_specifier" && t.Kind() != "union_specifier" {
		return nil
	}
	if t.ChildByFieldName("name") != nil {
		return nil
	}
	// 只取承载实体（变量/字段/类型别名），函数声明器不是匿名类型的承载者。
	out := make([]string, 0, len(items))
	for _, it := range items {
		switch it.elem.Kind {
		case model.Variable, model.Field, model.Type:
			if it.elem.QualifiedName != "" {
				out = append(out, it.elem.QualifiedName)
			}
		}
	}
	return out
}

// resolveRelativeQN 将类外定义的相对限定名按当前命名空间补全前缀。
// 例：namespace app 内的 core::Base::helper -> app::core::Base::helper；
// 已含当前前缀（app::core::Base::helper）时不重复补全。
func resolveRelativeQN(full, currentQN string) string {
	if currentQN == "" {
		return full
	}
	if strings.HasPrefix(full, currentQN+helper.QNSep) {
		return full
	}
	return helper.BuildQN(currentQN, full)
}

// =============================================================================
// 条件编译栈
// =============================================================================

func (c *Collector) setCondRegions(path string, regions []preproc.Region) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.condRegions[path] = regions
}

func (c *Collector) setDeadRegions(path string, regions []preproc.Region) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadRegions[path] = regions
}

func (c *Collector) releaseCondStack(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.condRegions, path)
	delete(c.deadRegions, path)
}

// inDeadZone 判断节点是否**完全**落在 #if 0 死区内。
//
// 必须要求完全包含：tree-sitter 的错误恢复会在死区边界产出跨区 ERROR 节点
// （起点在死区内、终点在区外，可跨数百行），只看起点会把整段正常代码误判为死区。
func (c *Collector) inDeadZone(path string, node coreast.Node) bool {
	c.mu.Lock()
	regions := c.deadRegions[path]
	c.mu.Unlock()
	if len(regions) == 0 {
		return false
	}
	start, end := node.StartByte(), node.EndByte()
	for _, r := range regions {
		if start < r.Start {
			break
		}
		if start >= r.Start && end <= r.End {
			return true
		}
	}
	return false
}

// activeCondition 返回节点所在的最内层条件编译分支标签。
//
// 投影后 AST 中不再有 preproc_ifdef 节点，条件标签来自 Parser 记录的
// 「被选中分支区间」：取覆盖该节点起始字节、Depth 最大的区间。
func (c *Collector) activeCondition(path string, node coreast.Node) string {
	c.mu.Lock()
	regions := c.condRegions[path]
	c.mu.Unlock()
	if len(regions) == 0 {
		return ""
	}
	pos := node.StartByte()
	best := ""
	bestDepth := -1
	for _, r := range regions {
		if r.Start <= pos && pos < r.End && r.Depth > bestDepth {
			best, bestDepth = r.Label, r.Depth
		}
	}
	return best
}

// =============================================================================
// 元素识别
// =============================================================================

func (c *Collector) identifyElements(node coreast.Node, fCtx core.FileContext, tmplParams string) ([]identified, model.ElementKind) {
	src := *fCtx.SourceBytes()
	switch node.Kind() {
	case "function_definition":
		decl := node.ChildByFieldName("declarator")
		name, _ := declaratorFullName(decl, src)
		kind := model.Function
		if isMethodContext(node, decl, src) {
			kind = model.Method
		}
		elem := newElem(kind, name, tmplParams)
		// `= default` / `= delete`：无函数体，但仍是类内定义（编译器生成/删除），
		// 记 is_declaration 便于区分，kind 保持定义语义。
		if node.ChildByFieldName("body") == nil {
			elem.GetMores()[constants.IsDeclaration] = true
		}
		return []identified{{elem: elem, declNode: decl, sig: paramSignature(decl, src)}}, kind

	case "declaration":
		return c.identifyDeclaration(node, fCtx, tmplParams)

	case "field_declaration":
		decl := node.ChildByFieldName("declarator")
		if isFunc, _ := classifyDeclarator(decl, src); isFunc {
			name, _ := declaratorFullName(decl, src)
			elem := newElem(model.MethodDecl, name, tmplParams)
			elem.GetMores()[constants.FunctionIsDeclaration] = true
			return []identified{{elem: elem, declNode: decl, sig: paramSignature(decl, src), isDecl: true}}, model.MethodDecl
		}
		var out []identified
		for _, d := range collectDeclarators(node) {
			name, _ := declaratorFullName(d, src)
			if name == "" {
				continue
			}
			out = append(out, identified{elem: newElem(model.Field, name, tmplParams), declNode: d})
		}
		if len(out) == 0 {
			return nil, ""
		}
		return out, model.Field

	case "struct_specifier", "union_specifier":
		if node.ChildByFieldName("name") == nil {
			// 匿名 struct：不产独立元素，字段挂承载实体（决策 #2）。
			return nil, ""
		}
		if isTypeReference(node) {
			// 局部/字段声明中的类型引用（struct Node n;）不是定义。
			return nil, ""
		}
		name := normalizeDeclaredTypeName(node.ChildByFieldName("name"), src)
		return []identified{{
			elem:   newElem(model.Struct, name, tmplParams),
			isDecl: node.ChildByFieldName("body") == nil,
		}}, model.Struct

	case "class_specifier":
		if node.ChildByFieldName("name") == nil {
			return nil, ""
		}
		if isTypeReference(node) {
			return nil, ""
		}
		name := normalizeDeclaredTypeName(node.ChildByFieldName("name"), src)
		return []identified{{
			elem:   newElem(model.Class, name, tmplParams),
			isDecl: node.ChildByFieldName("body") == nil,
		}}, model.Class

	case "enum_specifier":
		if node.ChildByFieldName("name") == nil {
			return nil, ""
		}
		if isTypeReference(node) {
			return nil, ""
		}
		name := normalizeDeclaredTypeName(node.ChildByFieldName("name"), src)
		// 匿名 enum（`enum : int { A, B };`）：tree-sitter-cpp 会给出一个文本为空的
		// type_identifier（非 nil），归一化后为空串。空名不能作为 QN 段，否则产出
		// `ns::` 与 `ns::::A`。按匿名处理：不产 Enum 元素，枚举常量挂当前容器。
		if name == "" {
			return nil, ""
		}
		return []identified{{
			elem:   newElem(model.Enum, name, tmplParams),
			isDecl: node.ChildByFieldName("body") == nil,
		}}, model.Enum

	case "enumerator":
		nameNode := node.ChildByFieldName("name")
		if nameNode == nil {
			return nil, ""
		}
		name := helper.GetNodeContent(nameNode, src)
		return []identified{{elem: newElem(model.EnumConstant, name, tmplParams)}}, model.EnumConstant

	case "type_definition":
		// typedef 可含多个声明器：typedef int TI1, TI2; / typedef struct {...} A, *B;
		var out []identified
		for _, d := range collectDeclarators(node) {
			name, _ := declaratorFullName(d, src)
			if name == "" {
				continue
			}
			out = append(out, identified{elem: newElem(model.Type, name, tmplParams), declNode: d})
		}
		if len(out) == 0 {
			return nil, ""
		}
		return out, model.Type

	case "alias_declaration":
		nameNode := node.ChildByFieldName("name")
		if nameNode == nil {
			return nil, ""
		}
		name := helper.GetNodeContent(nameNode, src)
		return []identified{{elem: newElem(model.Type, name, tmplParams)}}, model.Type

	case "namespace_definition":
		nameNode := node.ChildByFieldName("name")
		name := "$anon"
		if nameNode != nil {
			name = helper.GetNodeContent(nameNode, src)
		}
		return []identified{{elem: newElem(model.Namespace, name, tmplParams)}}, model.Namespace

	case "preproc_def", "preproc_function_def":
		nameNode := node.ChildByFieldName("name")
		if nameNode == nil {
			return nil, ""
		}
		name := helper.GetNodeContent(nameNode, src)
		elem := newElem(model.Macro, name, tmplParams)
		elem.GetMores()[constants.MacroName] = name
		if v := node.ChildByFieldName("value"); v != nil {
			elem.GetMores()[constants.MacroBody] = helper.GetNodeContent(v, src)
		}
		elem.GetMores()[constants.MacroIsFunctionLike] = node.Kind() == "preproc_function_def"
		return []identified{{elem: elem}}, model.Macro

	case "parameter_declaration":
		name := paramName(node, src)
		if name == "" {
			return nil, ""
		}
		return []identified{{elem: newElem(model.Variable, name, tmplParams)}}, model.Variable

	case "for_range_loop":
		// range-for 的循环变量不在 declaration 节点内（type/declarator 直接挂 for_range_loop）。
		decl := node.ChildByFieldName("declarator")
		name, _ := declaratorFullName(decl, src)
		if name == "" {
			return nil, ""
		}
		return []identified{{elem: newElem(model.Variable, name, tmplParams), declNode: decl}}, model.Variable

	case "lambda_expression":
		return []identified{{elem: newElem(model.Lambda, "lambda", tmplParams)}}, model.Lambda

	case "compound_statement":
		if !isBlockScope(node) {
			return nil, ""
		}
		return []identified{{elem: newElem(model.ScopeBlock, "block", tmplParams)}}, model.ScopeBlock
	}
	return nil, ""
}

// identifyDeclaration 处理 declaration：函数原型 → *Decl；其余声明器 → Variable。
// 混合形态（int mixf(int), mixv;）逐个声明器独立判定，不因命中首个函数而丢弃其余变量。
func (c *Collector) identifyDeclaration(node coreast.Node, fCtx core.FileContext, tmplParams string) ([]identified, model.ElementKind) {
	src := *fCtx.SourceBytes()
	decls := collectDeclarators(node)
	if len(decls) == 0 {
		return nil, ""
	}

	_, extern, _ := scanStorage(node, src)
	var out []identified
	kind := model.ElementKind("")
	for _, d := range decls {
		// 结构化绑定（auto [a, b] = ...）：每个绑定名各产一个变量元素。
		if bindings := structuredBindingNames(d, src); len(bindings) > 0 {
			for _, b := range bindings {
				out = append(out, identified{elem: newElem(model.Variable, b, tmplParams), declNode: d})
			}
			if kind == "" {
				kind = model.Variable
			}
			continue
		}
		name, _ := declaratorFullName(d, src)
		if name == "" {
			continue
		}
		if isFunc, _ := classifyDeclarator(d, src); isFunc {
			k := model.FunctionDecl
			if isMethodContext(node, d, src) {
				k = model.MethodDecl
			}
			elem := newElem(k, name, tmplParams)
			elem.GetMores()[constants.FunctionIsDeclaration] = true
			out = append(out, identified{elem: elem, declNode: d, sig: paramSignature(d, src), isDecl: true})
			if kind == "" {
				kind = k
			}
			continue
		}
		out = append(out, identified{elem: newElem(model.Variable, name, tmplParams), declNode: d, isDecl: extern})
		if kind == "" {
			kind = model.Variable
		}
	}
	if len(out) == 0 {
		return nil, ""
	}
	return out, kind
}

// structuredBindingNames 返回结构化绑定声明器中的各绑定名。
// 形态：init_declarator -> structured_binding_declarator [ identifier, identifier, ... ]
func structuredBindingNames(decl coreast.Node, src []byte) []string {
	d := decl
	if d != nil && d.Kind() == "init_declarator" {
		d = innerDeclarator(d)
	}
	if d == nil || d.Kind() != "structured_binding_declarator" {
		return nil
	}
	var out []string
	for i := 0; i < d.NamedChildCount(); i++ {
		ch := d.NamedChild(i)
		if ch.Kind() == "identifier" {
			out = append(out, strings.TrimSpace(ch.Utf8Text(src)))
		}
	}
	return out
}

// newElem 构造一个源码元素（Location/Path 由调用方填充）。
func newElem(kind model.ElementKind, name, tmplParams string) *model.CodeElement {
	elem := &model.CodeElement{
		Kind:         kind,
		Name:         name,
		IsFormSource: true,
	}
	if tmplParams != "" {
		elem.GetMores()[constants.TemplateParams] = tmplParams
	}
	return elem
}

// =============================================================================
// QN 计算
// =============================================================================

// assignQN 计算元素 QualifiedName（含声明器限定名与函数签名）。
func (c *Collector) assignQN(it *identified, currentQN string, occurrences map[string]int, src []byte) {
	elem := it.elem

	// 限定声明器（out-of-line 定义/类外变量）：相对限定名按当前命名空间补全前缀。
	if it.declNode != nil {
		if full, ok := declaratorFullName(it.declNode, src); ok && strings.Contains(full, helper.QNSep) {
			resolved := resolveRelativeQN(full, currentQN)
			elem.QualifiedName = resolved + it.sig
			it.parentQN = helper.PkgOf(resolved)
			// Name 只取最后一段（类外定义 Widget::~Widget 的 Name 应为 ~Widget），
			// 否则构造/析构/运算符等按名称判定的语义会失效。
			if parts := helper.SplitQN(resolved); len(parts) > 0 {
				elem.Name = parts[len(parts)-1]
			}
			return
		}
	}
	// 宏没有作用域语义：QN 固定为裸宏名（qn-format.md §8.1），
	// 即使定义在 namespace/函数体内也不加前缀，ParentQN 留空。
	if elem.Kind == model.Macro {
		it.parentQN = ""
		occurrences[elem.Name]++
		if n := occurrences[elem.Name]; n > 1 {
			elem.QualifiedName = fmt.Sprintf("%s$%d", elem.Name, n)
		} else {
			elem.QualifiedName = elem.Name
		}
		return
	}

	it.parentQN = currentQN

	// 函数/方法的消歧序号必须插在参数列表之前（apply$2(int,int)），
	// 否则 QN 不再以参数列表结尾，会被误判为缺参数列表。
	// 因此计数键用「名+签名」，序号只加在名字上。
	name, suffix := elem.Name, ""
	if isFunctionKind(elem.Kind) {
		suffix = it.sig
	}

	// 声明与定义共享同一 QN：声明不参与同名重复计数，
	// 否则同文件的 `int f(int); int f(int){}` 会被消歧成 f(int) / f(int)$2。
	if it.isDecl {
		elem.QualifiedName = c.resolver.BuildQualifiedName(currentQN, name+suffix)
		return
	}

	switch elem.Kind {
	case model.Lambda, model.ScopeBlock:
		// Lambda/ScopeBlock 的 `$N` 是「同容器第 N 个 lambda/block」的序号，
		// 与普通同名元素（如名为 `block` 的变量）无关，故用独立计数键，
		// 避免变量 `block` 把作用域块挤成 `block$2`。
		key := "#" + elem.Name
		occurrences[key]++
		name = fmt.Sprintf("%s$%d", elem.Name, occurrences[key])
	default:
		// 消歧键含 Kind：QN+Kind 才是元素身份，跨 Kind 同名（C 的 struct tag 与
		// typedef 别名 `typedef struct X {...} X;`）不应触发 `$N`，否则 STRUCT 被
		// 挤成 `X$2`，下游 EXTEND/成员解析错指 typedef。
		key := name + suffix + "|" + string(elem.Kind)
		occurrences[key]++
		if occurrences[key] > 1 {
			name = fmt.Sprintf("%s$%d", name, occurrences[key])
		}
	}
	elem.QualifiedName = c.resolver.BuildQualifiedName(currentQN, name+suffix)
}

func isFunctionKind(k model.ElementKind) bool {
	switch k {
	case model.Function, model.Method, model.FunctionDecl, model.MethodDecl:
		return true
	}
	return false
}

// =============================================================================
// 声明器工具
// =============================================================================

// collectDeclarators 返回声明节点中的全部声明器（跳过类型/存储类/限定符）。
func collectDeclarators(node coreast.Node) []coreast.Node {
	typeNode := node.ChildByFieldName("type")
	var out []coreast.Node
	for i := 0; i < node.NamedChildCount(); i++ {
		ch := node.NamedChild(i)
		if ch == nil {
			continue
		}
		if typeNode != nil && ch.Equal(typeNode) {
			continue
		}
		switch ch.Kind() {
		case "storage_class_specifier", "type_qualifier", "attribute_declaration",
			"attribute_specifier", "virtual_specifier", "alignas_qualifier", "ms_declspec_modifier":
			continue
		}
		out = append(out, ch)
	}
	return out
}

// classifyDeclarator 判定声明器是否为函数形态。
// 函数指针变量（int (*fp)(int)）的 function_declarator 内层为 parenthesized_declarator
// 且括号内无函数声明器，不视为函数；
// 返回函数指针/数组指针的函数（int (*rfp(int))(int) / int (*rap(void))[5]）括号内有
// 函数声明器，仍视为函数。
func classifyDeclarator(decl coreast.Node, src []byte) (bool, string) {
	if decl == nil {
		return false, ""
	}
	switch decl.Kind() {
	case "function_declarator":
		inner := decl.ChildByFieldName("declarator")
		if inner != nil && inner.Kind() == "parenthesized_declarator" {
			if findFunctionDeclaratorInParens(inner) == nil {
				return false, ""
			}
		}
		name, _ := declaratorFullName(inner, src)
		return true, name
	case "pointer_declarator", "reference_declarator", "init_declarator", "array_declarator":
		return classifyDeclarator(innerDeclarator(decl), src)
	case "parenthesized_declarator":
		if findFunctionDeclaratorInParens(decl) == nil {
			return false, ""
		}
		name, _ := declaratorFullName(decl, src)
		return true, name
	case "operator_cast":
		// 转换运算符（operator bool / operator int）本身即函数形态。
		name, _ := declaratorFullName(decl, src)
		return true, name
	}
	return false, ""
}

// innerDeclarator 返回声明器的内层声明器。
// tree-sitter-cpp 中 pointer_declarator/init_declarator/array_declarator 用 declarator 字段，
// 但 reference_declarator（`D& f()` / `operator=`）没有该字段，内层声明器直接作为命名子节点。
func innerDeclarator(decl coreast.Node) coreast.Node {
	if decl == nil {
		return nil
	}
	if d := decl.ChildByFieldName("declarator"); d != nil {
		return d
	}
	for i := 0; i < decl.NamedChildCount(); i++ {
		ch := decl.NamedChild(i)
		switch ch.Kind() {
		case "function_declarator", "pointer_declarator", "reference_declarator",
			"array_declarator", "parenthesized_declarator", "qualified_identifier",
			"identifier", "type_identifier", "field_identifier", "operator_name",
			"destructor_name", "template_function", "attributed_declarator":
			return ch
		}
	}
	return nil
}

// declaratorFullName 提取声明器的名称（剥指针/引用/数组/函数外层）。
// 第二个返回值为 true 表示名称为限定名（含 ::）。
func declaratorFullName(decl coreast.Node, src []byte) (string, bool) {
	if decl == nil {
		return "", false
	}
	switch decl.Kind() {
	case "pointer_declarator", "reference_declarator", "init_declarator",
		"array_declarator", "attributed_declarator":
		return declaratorFullName(innerDeclarator(decl), src)
	case "function_declarator":
		return declaratorFullName(innerDeclarator(decl), src)
	case "parenthesized_declarator":
		for i := 0; i < decl.NamedChildCount(); i++ {
			if n, ok := declaratorFullName(decl.NamedChild(i), src); n != "" {
				return n, ok
			}
		}
		return "", false
	case "structured_binding_declarator":
		// 结构化绑定（auto [a, b] = ...）：无单一名称，由 identifyDeclaration 展开。
		return "", false
	case "qualified_identifier":
		return normalizeQualified(decl.Utf8Text(src)), true
	case "template_function":
		// 函数模板显式特化（template <> void f<int>(...)）的名称节点：
		// 取 name 字段（f），剥离模板实参。
		return declaratorFullName(decl.ChildByFieldName("name"), src)
	case "operator_cast":
		// 转换运算符（operator bool / operator int）：名称由 operator + 目标类型组成。
		// 目标类型若是模板（operator ParamGenerator<T>），按 QN 契约剥离实参。
		t := ""
		if tn := decl.ChildByFieldName("type"); tn != nil {
			t = normalizeParamType(helper.GetNodeContent(tn, src))
		}
		return normalizeTypeText("operator " + t), false
	case "destructor_name", "operator_name", "field_identifier", "identifier", "type_identifier":
		return strings.TrimSpace(decl.Utf8Text(src)), false
	}
	return "", false
}

// normalizeDeclaredTypeName 归一化类型定义的名称节点：剥离模板实参。
// 模板特化（template <> struct Box<int>）的 name 是 template_type，其文本含实参；
// QN 契约要求剥离（Box<int> → Box），与类外定义 Box<int>::run 的归一化一致。
func normalizeDeclaredTypeName(nameNode coreast.Node, src []byte) string {
	if nameNode == nil {
		return ""
	}
	if nameNode.Kind() == "template_type" {
		if n := nameNode.ChildByFieldName("name"); n != nil {
			return helper.GetNodeContent(n, src)
		}
	}
	return helper.StripTemplateArgs(helper.GetNodeContent(nameNode, src))
}

// normalizeQualified 归一化限定名文本：去掉空白与各段的模板实参。
// 例：app::Box<int>::run -> app::Box::run
//
// 必须先整体剥离模板实参再按 :: 切分：模板实参内部可能含 ::（如
// Matcher<const std::string&>），先切分会把实参劈成两段，剥离失效。
func normalizeQualified(s string) string {
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "\t", "")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	s = helper.StripTemplateArgsKeepSuffix(s)
	// 全局作用域限定符 `::Foo` 不是 QN 的一部分（QN 用相对/绝对命名空间前缀表达），
	// 生成代码（protobuf .pb.cc）大量使用 `::ns::Type`，前导 `::` 会产出空首段。
	s = strings.TrimPrefix(s, helper.QNSep)
	return s
}

// isMethodContext 判定函数是否属于类/结构体方法。
func isMethodContext(node, decl coreast.Node, src []byte) bool {
	if decl != nil {
		if full, ok := declaratorFullName(decl, src); ok && strings.Contains(full, helper.QNSep) {
			return true
		}
	}
	for curr := node.Parent(); curr != nil; curr = curr.Parent() {
		switch curr.Kind() {
		case "field_declaration_list":
			p := curr.Parent()
			if p != nil && (p.Kind() == "class_specifier" || p.Kind() == "struct_specifier" || p.Kind() == "union_specifier") {
				return true
			}
		case "function_definition", "translation_unit":
			return false
		}
	}
	return false
}

// isTypeReference 判定 struct/class/enum 说明符是否为「类型引用」而非定义。
// 无 body 且父节点为声明/字段/参数/typedef 时是引用（struct Node n; / struct Fwd; 除外）。
func isTypeReference(node coreast.Node) bool {
	if node.ChildByFieldName("body") != nil {
		return false
	}
	p := node.Parent()
	if p == nil {
		return false
	}
	switch p.Kind() {
	case "translation_unit", "declaration_list":
		// 顶层/命名空间内的前向声明：保留为元素（记 is_declaration）。
		return false
	}
	return true
}

// isBlockScope 判定 compound_statement 是否应作为独立作用域块。
// 函数体/Lambda 体的块由函数/Lambda 自身承载，不重复建块。
func isBlockScope(node coreast.Node) bool {
	p := node.Parent()
	if p == nil {
		return false
	}
	switch p.Kind() {
	case "function_definition", "lambda_expression", "field_declaration_list",
		"declaration_list", "translation_unit":
		return false
	}
	return true
}

// paramSignature 返回函数参数类型签名 "(int,char*)"。
func paramSignature(decl coreast.Node, src []byte) string {
	fd := findFunctionDeclarator(decl)
	if fd == nil {
		return "()"
	}
	pNode := fd.ChildByFieldName("parameters")
	if pNode == nil {
		return "()"
	}
	var types []string
	for i := 0; i < pNode.NamedChildCount(); i++ {
		t := paramTypeText(pNode.NamedChild(i), src, true)
		if t == "" {
			continue
		}
		types = append(types, t)
	}
	// f(void) 等价于无参。
	if len(types) == 1 && types[0] == "void" {
		types = nil
	}
	return "(" + strings.Join(types, ",") + ")"
}

// findFunctionDeclarator 沿声明器链找到「被声明实体自身」的 function_declarator。
// 返回函数指针的函数（int (*rfp(int))(int)）外层 function_declarator 描述的是返回类型，
// 需穿透 parenthesized_declarator 找到内层 rfp 的 function_declarator。
func findFunctionDeclarator(decl coreast.Node) coreast.Node {
	for decl != nil {
		switch decl.Kind() {
		case "function_declarator":
			inner := decl.ChildByFieldName("declarator")
			if inner != nil && inner.Kind() == "parenthesized_declarator" {
				if fd := findFunctionDeclaratorInParens(inner); fd != nil {
					return fd
				}
			}
			return decl
		case "pointer_declarator", "reference_declarator", "init_declarator", "array_declarator":
			decl = innerDeclarator(decl)
		case "parenthesized_declarator":
			return findFunctionDeclaratorInParens(decl)
		default:
			return nil
		}
	}
	return nil
}

// findFunctionDeclaratorInParens 在括号声明器内查找函数声明器（穿透指针/引用/数组）。
func findFunctionDeclaratorInParens(paren coreast.Node) coreast.Node {
	if paren == nil {
		return nil
	}
	for i := 0; i < paren.NamedChildCount(); i++ {
		if fd := findFunctionDeclarator(paren.NamedChild(i)); fd != nil {
			return fd
		}
	}
	return nil
}

// paramTypeText 提取参数的类型文本（含 const/指针/引用/数组语义）。
// stripTpl 为 true 时剥离模板实参（用于 QN 签名）；为 false 时保留原文（用于展示）。
func paramTypeText(p coreast.Node, src []byte, stripTpl bool) string {
	if p == nil {
		return ""
	}
	// 注释是具名节点，会出现在参数列表的 NamedChild 中，但不是参数。
	if p.Kind() == "comment" {
		return ""
	}
	if p.Kind() == "variadic_parameter" {
		return "..."
	}
	norm := normalizeTypeText
	if stripTpl {
		norm = normalizeParamType
	}
	// 参数包展开（template <typename... T> void f(T... v)）：
	// variadic_parameter_declaration 的 type 字段是包元素类型，声明器带 `...`。
	// 签名按「包元素类型 + ...」记（T...），与 C 的 `...` 区分。
	if p.Kind() == "variadic_parameter_declaration" {
		t := ""
		if tn := p.ChildByFieldName("type"); tn != nil {
			t = helper.GetNodeContent(tn, src)
		}
		if decl := p.ChildByFieldName("declarator"); decl != nil {
			t += declaratorTypeSuffix(decl)
		}
		return norm(t + "...")
	}
	// optional_parameter_declaration 是带默认实参的形参（int b = 2），
	// 与 parameter_declaration 结构一致，但默认值不能进入 QN 签名。
	if p.Kind() != "parameter_declaration" && p.Kind() != "optional_parameter_declaration" {
		return norm(helper.GetNodeContent(p, src))
	}
	var parts []string
	for i := 0; i < p.ChildCount(); i++ {
		ch := p.Child(i)
		if ch.Kind() == "type_qualifier" {
			parts = append(parts, helper.GetNodeContent(ch, src))
		}
	}
	if t := p.ChildByFieldName("type"); t != nil {
		parts = append(parts, helper.GetNodeContent(t, src))
	}
	decl := p.ChildByFieldName("declarator")
	if decl != nil {
		if findFunctionDeclarator(decl) != nil {
			parts = append(parts, "(*)")
		} else {
			parts = append(parts, declaratorTypeSuffix(decl))
		}
	}
	return norm(strings.Join(parts, " "))
}

// declaratorTypeSuffix 返回声明器上的指针/引用/数组标记。
func declaratorTypeSuffix(decl coreast.Node) string {
	var sb strings.Builder
	for decl != nil {
		switch decl.Kind() {
		case "pointer_declarator", "abstract_pointer_declarator":
			sb.WriteString("*")
			decl = decl.ChildByFieldName("declarator")
		case "reference_declarator", "abstract_reference_declarator":
			// 右值引用（int&&）与左值引用（int&）在 QN 中必须区分。
			if countChildToken(decl, "&&") > 0 {
				sb.WriteString("&&")
			} else {
				sb.WriteString("&")
			}
			decl = innerDeclarator(decl)
		case "array_declarator", "abstract_array_declarator":
			sb.WriteString("[]")
			decl = innerDeclarator(decl)
		default:
			return sb.String()
		}
	}
	return sb.String()
}

// hasVariadicToken 判断参数列表中是否出现 `...`。
// tree-sitter-cpp 把可变参数的 `...` 作为匿名 token 子节点（不是 variadic_parameter）。
func hasVariadicToken(params coreast.Node) bool {
	if params == nil {
		return false
	}
	for i := 0; i < params.ChildCount(); i++ {
		if params.Child(i).Kind() == "..." {
			return true
		}
	}
	return false
}

// countChildToken 统计声明器直接子节点中指定运算符 token 的出现次数。
func countChildToken(decl coreast.Node, token string) int {
	n := 0
	for i := 0; i < decl.ChildCount(); i++ {
		if decl.Child(i).Kind() == token {
			n++
		}
	}
	return n
}

// normalizeTypeText 归一化类型文本：去掉 * & 前空格。
// 用于记录「原文类型」的 Mores（raw type / underlying / 基类），保留模板实参。
func normalizeTypeText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " *", "*")
	s = strings.ReplaceAll(s, " &", "&")
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, " [", "[")
	return s
}

// normalizeParamType 归一化**参数签名**中的类型文本：
//   - 剥离模板实参并保留其后的指针/引用/数组后缀（Blob<Dtype>* → Blob*）；
//   - 压缩空白、去掉 * & 与 [ 前的空格（const char * → const char*）。
//
// 关键字间的空格（const char、unsigned int）按规范保留。
func normalizeParamType(s string) string {
	return normalizeTypeText(helper.StripTemplateArgsKeepSuffix(s))
}

// paramName 提取参数名。
func paramName(p coreast.Node, src []byte) string {
	if p == nil || (p.Kind() != "parameter_declaration" && p.Kind() != "optional_parameter_declaration") {
		return ""
	}
	name, _ := declaratorFullName(p.ChildByFieldName("declarator"), src)
	return name
}

// =============================================================================
// 元数据增强
// =============================================================================

func (c *Collector) enrichMetadata(fCtx core.FileContext) {
	src := *fCtx.SourceBytes()
	for _, entry := range fCtx.Definitions() {
		elem, node := entry.Element, entry.Node
		if node == nil {
			continue
		}
		mores := elem.GetMores()

		// 通用：命名空间 / 可见性
		// （条件编译分支在 collectBasicDefinitions 注册时记录，此时条件栈已全部弹出）
		if elem.Kind != model.File {
			mores[constants.NamespaceName] = helper.PkgOf(elem.QualifiedName)
		}
		if vis := visibilityOf(node, src); vis != "" {
			mores[constants.Visibility] = vis
		}

		switch elem.Kind {
		case model.File:
			mores[constants.FileRawLOC] = len(strings.Split(string(src), "\n"))
			mores[constants.FileLOC] = countEffectiveLOC(src)
		case model.Function, model.Method, model.FunctionDecl, model.MethodDecl:
			c.enrichFunction(elem, node, src)
		case model.Variable:
			c.enrichVariable(elem, node, src)
			c.linkLambdaInitializer(elem, entry, fCtx, src)
		case model.Field:
			c.enrichField(elem, node, src)
		case model.Struct:
			c.enrichStruct(elem, node, src)
		case model.Class:
			c.enrichClass(elem, node, src)
		case model.Enum:
			c.enrichEnum(elem, node, src)
		case model.Type:
			c.enrichType(elem, node, src)
		}
	}
}

func (c *Collector) enrichFunction(elem *model.CodeElement, node coreast.Node, src []byte) {
	mores := elem.GetMores()
	decl := node.ChildByFieldName("declarator")

	// 返回类型
	if t := node.ChildByFieldName("type"); t != nil {
		mores[constants.FunctionReturnType] = normalizeTypeText(helper.GetNodeContent(t, src))
	}

	// 参数列表
	fd := findFunctionDeclarator(decl)
	if fd != nil {
		if pNode := fd.ChildByFieldName("parameters"); pNode != nil {
			var params []string
			variadic := hasVariadicToken(pNode)
			for i := 0; i < pNode.NamedChildCount(); i++ {
				p := pNode.NamedChild(i)
				if p.Kind() == "variadic_parameter" {
					// C grammar 用 variadic_parameter 节点，C++ grammar 用匿名 `...` token。
					variadic = true
					params = append(params, "...")
					continue
				}
				// 展示用参数列表保留原文（含模板实参），与 QN 签名口径分离。
				t := paramTypeText(p, src, false)
				if t == "" {
					continue
				}
				if n := paramName(p, src); n != "" {
					params = append(params, t+" "+n)
				} else {
					params = append(params, t)
				}
			}
			if variadic && (len(params) == 0 || params[len(params)-1] != "...") {
				params = append(params, "...")
			}
			if !(len(params) == 1 && params[0] == "void") {
				mores[constants.FunctionParameters] = params
			}
			if variadic {
				mores[constants.FunctionIsVariadic] = true
			}
		}
	}

	// 修饰符：storage_class_specifier / type_qualifier / virtual
	var mods []string
	isVirtual := false
	scanModifiers(node, src, &mods, &isVirtual)
	if fd != nil {
		scanModifiers(fd, src, &mods, &isVirtual)
	}
	if len(mods) > 0 {
		if elem.Extra == nil {
			elem.Extra = &model.Extra{}
		}
		elem.Extra.Modifiers = mods
	}
	if isVirtual {
		mores[constants.FunctionIsVirtual] = true
	}

	// 纯虚函数：field_declaration 的 default_value 为 0
	if node.Kind() == "field_declaration" {
		if dv := node.ChildByFieldName("default_value"); dv != nil {
			if strings.TrimSpace(dv.Utf8Text(src)) == "0" {
				mores[constants.FunctionIsPureVirtual] = true
			}
		}
	}

	// 名称语义
	name := elem.Name
	if strings.HasPrefix(name, "operator") {
		mores[constants.FunctionIsOperator] = true
	}
	if strings.HasPrefix(name, "~") {
		mores[constants.FunctionIsDestructor] = true
	}
	if node.ChildByFieldName("type") == nil && !strings.HasPrefix(name, "~") && !strings.HasPrefix(name, "operator") {
		mores[constants.FunctionIsConstructor] = true
	}
}

func scanModifiers(node coreast.Node, src []byte, mods *[]string, isVirtual *bool) {
	if node == nil {
		return
	}
	for i := 0; i < node.ChildCount(); i++ {
		ch := node.Child(i)
		switch ch.Kind() {
		case "storage_class_specifier", "type_qualifier", "virtual_specifier":
			*mods = append(*mods, helper.GetNodeContent(ch, src))
		case "virtual":
			*isVirtual = true
			*mods = append(*mods, "virtual")
		}
	}
}

func (c *Collector) enrichVariable(elem *model.CodeElement, node coreast.Node, src []byte) {
	mores := elem.GetMores()
	if t := node.ChildByFieldName("type"); t != nil {
		mores[constants.VariableRawType] = normalizeTypeText(helper.GetNodeContent(t, src))
	}
	static, extern, isConst := scanStorage(node, src)
	if static {
		mores[constants.VariableIsStatic] = true
	}
	if extern {
		mores[constants.VariableIsExtern] = true
		mores[constants.IsDeclaration] = true
	}
	if isConst {
		mores[constants.VariableIsConst] = true
	}
	if isGlobalDeclaration(node) {
		mores[constants.VariableIsGlobal] = true
	}
}

func (c *Collector) enrichField(elem *model.CodeElement, node coreast.Node, src []byte) {
	mores := elem.GetMores()
	if t := node.ChildByFieldName("type"); t != nil {
		mores[constants.FieldRawType] = normalizeTypeText(helper.GetNodeContent(t, src))
	}
	static, _, isConst := scanStorage(node, src)
	if static {
		mores[constants.FieldIsStatic] = true
	}
	if isConst {
		mores[constants.FieldIsConst] = true
	}
}

func (c *Collector) enrichStruct(elem *model.CodeElement, node coreast.Node, src []byte) {
	mores := elem.GetMores()
	if node.Kind() == "union_specifier" {
		mores[constants.StructIsUnion] = true
	}
	if node.ChildByFieldName("body") == nil {
		mores[constants.StructIsDeclaration] = true
		mores[constants.IsDeclaration] = true
	}
	// struct 同样可继承（struct D : public B），基类记录规则与 class 一致。
	if supers := collectBaseClasses(node, src); len(supers) > 0 {
		mores[constants.ClassSuperClass] = supers
	}
}

func (c *Collector) enrichClass(elem *model.CodeElement, node coreast.Node, src []byte) {
	mores := elem.GetMores()
	if node.ChildByFieldName("body") == nil {
		mores[constants.ClassIsDeclaration] = true
		mores[constants.IsDeclaration] = true
	}
	if supers := collectBaseClasses(node, src); len(supers) > 0 {
		mores[constants.ClassSuperClass] = supers
	}
}

// collectBaseClasses 提取 base_class_clause 中的基类名原文。
// 基类名保留原文（可能相对当前命名空间），由 Extractor 结合全局上下文解析。
func collectBaseClasses(node coreast.Node, src []byte) []string {
	var supers []string
	for i := 0; i < node.NamedChildCount(); i++ {
		ch := node.NamedChild(i)
		if ch.Kind() != "base_class_clause" {
			continue
		}
		for j := 0; j < ch.NamedChildCount(); j++ {
			b := ch.NamedChild(j)
			switch b.Kind() {
			case "access_specifier", "virtual":
				continue
			case "qualified_identifier", "type_identifier", "template_type":
				supers = append(supers, normalizeTypeText(helper.GetNodeContent(b, src)))
			}
		}
	}
	return supers
}

// linkLambdaInitializer 记录「变量初始化为 lambda」的关联 Lambda QN，
// 使 `auto f = [](){...}; f();` 的调用能解析到 Lambda 元素。
func (c *Collector) linkLambdaInitializer(elem *model.CodeElement, entry *core.DefinitionEntry, fCtx core.FileContext, src []byte) {
	node := entry.Node
	if node == nil || node.Kind() != "declaration" {
		return
	}
	// 定位该变量对应的声明器，取其 value 是否为 lambda_expression。
	for _, d := range collectDeclarators(node) {
		name, _ := declaratorFullName(d, src)
		if name != elem.Name {
			continue
		}
		if d.Kind() != "init_declarator" {
			return
		}
		v := d.ChildByFieldName("value")
		if v == nil || v.Kind() != "lambda_expression" {
			return
		}
		// 在已收集定义中找位置匹配的 Lambda 元素。
		for _, def := range fCtx.Definitions() {
			if def.Element.Kind != model.Lambda || def.Node == nil {
				continue
			}
			if def.Node.StartByte() == v.StartByte() && def.Node.EndByte() == v.EndByte() {
				elem.GetMores()[constants.VariableLambdaQN] = def.Element.QualifiedName
				return
			}
		}
		return
	}
}

// enrichType 记录 typedef/using 别名的底层类型（供 Binder 解析别名链）。
func (c *Collector) enrichType(elem *model.CodeElement, node coreast.Node, src []byte) {
	mores := elem.GetMores()
	switch node.Kind() {
	case "type_definition":
		if t := node.ChildByFieldName("type"); t != nil {
			underlying := normalizeTypeText(helper.GetNodeContent(t, src))
			// 多声明器时各别名的指针/数组修饰不同：typedef int TI, *PTI;
			for _, d := range collectDeclarators(node) {
				name, _ := declaratorFullName(d, src)
				if name != elem.Name {
					continue
				}
				underlying += declaratorTypeSuffix(d)
				break
			}
			mores[constants.TypeUnderlying] = underlying
		}
	case "alias_declaration":
		if t := node.ChildByFieldName("type"); t != nil {
			raw := helper.GetNodeContent(t, src)
			// type_descriptor 的文本含内部类型，直接使用即可。
			mores[constants.TypeUnderlying] = normalizeTypeText(raw)
		}
	}
}

func (c *Collector) enrichEnum(elem *model.CodeElement, node coreast.Node, src []byte) {
	text := node.Utf8Text(src)
	if strings.Contains(text, "enum class") || strings.Contains(text, "enum struct") {
		elem.GetMores()[constants.EnumIsScoped] = true
	}
	// 前向声明（enum Tagged;）无 body，无枚举常量。
	if node.ChildByFieldName("body") == nil {
		elem.GetMores()[constants.IsDeclaration] = true
	}
}

// countEffectiveLOC 计算有效代码行数（排除空行与纯注释行）。
// 简化处理：不处理块注释内的代码行，与 Java 版语义对齐（Java 版移除块注释后再统计）。
func countEffectiveLOC(src []byte) int {
	text := string(src)
	// 移除块注释。
	for {
		start := strings.Index(text, "/*")
		if start < 0 {
			break
		}
		end := strings.Index(text[start+2:], "*/")
		if end < 0 {
			text = text[:start]
			break
		}
		text = text[:start] + text[start+2+end+2:]
	}
	loc := 0
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "//") {
			loc++
		}
	}
	return loc
}

// scanStorage 提取 static/extern/const 标记。
func scanStorage(node coreast.Node, src []byte) (static, extern, isConst bool) {
	if node == nil {
		return
	}
	for i := 0; i < node.ChildCount(); i++ {
		ch := node.Child(i)
		switch ch.Kind() {
		case "storage_class_specifier":
			switch strings.TrimSpace(ch.Utf8Text(src)) {
			case "static":
				static = true
			case "extern":
				extern = true
			}
		case "type_qualifier":
			if strings.TrimSpace(ch.Utf8Text(src)) == "const" {
				isConst = true
			}
		}
	}
	return
}

// isGlobalDeclaration 判定 declaration 是否为全局变量（顶层或 namespace 内）。
func isGlobalDeclaration(node coreast.Node) bool {
	curr := node.Parent()
	for curr != nil {
		switch curr.Kind() {
		case "translation_unit":
			return true
		case "declaration_list":
			p := curr.Parent()
			if p != nil && p.Kind() == "namespace_definition" {
				return true
			}
			return false
		case "linkage_specification":
			curr = curr.Parent()
			continue
		default:
			return false
		}
	}
	return false
}

// visibilityOf 返回成员所在的访问级别（最近的前置 access_specifier）。
func visibilityOf(node coreast.Node, src []byte) string {
	p := node.Parent()
	if p == nil || p.Kind() != "field_declaration_list" {
		return ""
	}
	vis := ""
	for i := 0; i < p.ChildCount(); i++ {
		ch := p.Child(i)
		if ch.Kind() == "access_specifier" {
			vis = strings.TrimSpace(ch.Utf8Text(src))
			continue
		}
		if ch.Equal(node) {
			return vis
		}
	}
	return vis
}

// collectNamespaces 汇总文件内出现的命名空间（含嵌套前缀）。
func (c *Collector) collectNamespaces(fCtx core.FileContext) {
	seen := make(map[string]bool)
	var out []string
	for _, entry := range fCtx.Definitions() {
		if entry.Element.Kind != model.Namespace {
			continue
		}
		parts := helper.SplitQN(entry.Element.QualifiedName)
		var cur []string
		for _, p := range parts {
			cur = append(cur, p)
			full := strings.Join(cur, helper.QNSep)
			if !seen[full] {
				seen[full] = true
				out = append(out, full)
			}
		}
	}
	if len(out) > 0 {
		if nfc, ok := fCtx.(cppctx.NamespaceAware); ok {
			nfc.SetNamespaces(out)
		}
	}
}

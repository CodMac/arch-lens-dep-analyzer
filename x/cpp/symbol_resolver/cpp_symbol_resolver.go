// Package symbol_resolver 实现 C/C++ 语言的 core.SymbolResolver 接口。
// 职责：统一动作解析入口（Call/Create/Cast/Assign/Use/Throw），
// 以及类型/变量/函数的基础解析（供 binder/extractor 共享）。
//
// 解析采用**两段式**（与 Java 的「Context → Segmenter → ChainResolver」三段式相对）：
//
//	段 1 归一化：把任意动作 AST 节点折叠为统一的 expr 描述
//	             （form + recv + name + typeText + argc），
//	             吸收 field_expression / qualified_identifier / call_expression /
//	             new_expression / cast_expression / template_function 等形态差异。
//	段 2 查找：只依赖 expr，沿「词法作用域链 + 类型继承链」查找目标符号；
//	             需要接收者类型时递归 typeOf(recv)，实现链式调用的类型传播。
//
// 之所以不需要 Java 的三段式：C/C++ 的 AST 已把 receiver 与成员名分开
// （call_expression.function 为 field_expression，其 argument 即 receiver），
// 且无 super / Xxx.class / 方法引用 / 泛型实例化等语法。
package symbol_resolver

import (
	"strings"

	"github.com/CodMac/arch-lens-dep-analyzer/core"
	coreast "github.com/CodMac/arch-lens-dep-analyzer/core/ast"
	"github.com/CodMac/arch-lens-dep-analyzer/model"
	"github.com/CodMac/arch-lens-dep-analyzer/x/cpp/constants"
	cppctx "github.com/CodMac/arch-lens-dep-analyzer/x/cpp/context"
	"github.com/CodMac/arch-lens-dep-analyzer/x/cpp/helper"
)

// SymbolResolver 实现 core.SymbolResolver。
type SymbolResolver struct{}

// New 创建 C/C++ SymbolResolver。
func New() *SymbolResolver { return &SymbolResolver{} }

// =============================================================================
// core.SymbolResolver 接口实现
// =============================================================================

// BuildQualifiedName 拼接父符号 QN 与子符号名称为完整 QN（:: 分隔）。
func (r *SymbolResolver) BuildQualifiedName(parentQN, name string) string {
	return helper.BuildQN(parentQN, name)
}

// IsPrimitive 判断类型名是否为 C/C++ 基本类型。
func (r *SymbolResolver) IsPrimitive(typeName string) bool {
	return helper.IsPrimitive(helper.BaseTypeName(typeName))
}

// ResolveType 解析类型符号；未找到返回外部实体。
// 无使用位置，无法区分不同作用域的同名类型；持有节点时请用 ResolveTypeAt。
func (r *SymbolResolver) ResolveType(gc core.GlobalContext, fc core.FileContext, symbol string, kind model.ElementKind) *model.CodeElement {
	return r.ResolveTypeAt(gc, fc, nil, symbol, kind)
}

// ResolveTypeAt 按使用位置解析类型（实现 core.ScopedTypeResolver）。
// node 为类型出现处的 AST 节点：非限定名先沿词法作用域链查找，
// 使 namespace B 内的 Foo 命中 B::Foo 而非其它 namespace 的同名类型。
func (r *SymbolResolver) ResolveTypeAt(gc core.GlobalContext, fc core.FileContext, node coreast.Node, symbol string, kind model.ElementKind) *model.CodeElement {
	raw := helper.NormalizeTypeText(symbol)
	if raw == "" || raw == "?" {
		return nil
	}
	base := helper.BaseTypeName(raw)
	if base == "" {
		return nil
	}
	if r.IsPrimitive(base) {
		return &model.CodeElement{Name: base, QualifiedName: base, Kind: model.Primitive, IsFormExternal: true}
	}

	// 单大写字母（T/B/E…）既可能是真实类型名，也可能是模板类型变量。
	// 先查**本文件内**的真实类型：命中则是真实类型；未命中按模板变量处理。
	// 不做跨文件全局查找，避免模板参数 T 被其它文件的 struct T 污染。
	if helper.IsTypeVariable(base) {
		if entries, ok := fc.FindByShortName(base); ok {
			for _, e := range entries {
				if isTypeKind(e.Element.Kind) {
					return e.Element
				}
			}
		}
		return nil
	}

	if elem := r.lookupTypeAt(gc, fc, node, base); elem != nil {
		return elem
	}

	// 外部兜底：按 :: 分配命名空间归属。
	return helper.NewExternalElement(kind, base, base)
}

// ResolveVar 解析变量/字段/枚举常量。
func (r *SymbolResolver) ResolveVar(gc core.GlobalContext, fc core.FileContext, node coreast.Node, receiver, symbol string) *model.CodeElement {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return nil
	}
	e := &expr{form: formIdent, node: node, name: symbol}
	if receiver != "" {
		e.form = formMember
		e.recv = &expr{form: formText, node: node, text: receiver}
	}
	return r.resolveValue(gc, fc, e)
}

// ResolveFunc 解析函数/方法。
func (r *SymbolResolver) ResolveFunc(gc core.GlobalContext, fc core.FileContext, node coreast.Node, receiver, symbol string) *model.CodeElement {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return nil
	}
	e := &expr{form: formIdent, node: node, name: symbol, argc: r.argCount(node)}
	if receiver != "" {
		e.form = formMember
		e.recv = &expr{form: formText, node: node, text: receiver}
	}
	return r.resolveFunc(gc, fc, e)
}

// ResolveAction 统一动作解析入口：段 1 归一化 → 段 2 查找。
func (r *SymbolResolver) ResolveAction(gc core.GlobalContext, fc core.FileContext, node coreast.Node, relType model.DependencyType) *model.CodeElement {
	if node == nil {
		return nil
	}
	src := *fc.SourceBytes()
	e := r.normalize(node, src)
	if e == nil {
		return nil
	}

	switch relType {
	case model.Call:
		// static_cast<T>(x) 等显式转换：语义为 CAST 而非 CALL。
		if e.form == formCast && e.typeText != "" {
			return r.ResolveTypeAt(gc, fc, e.node, e.typeText, model.Class)
		}
		// extractor 传入的是 call_expression 的 function 字段（非整个调用节点），
		// 此时归一化结果不含实参个数，需从祖先调用节点补齐（重载挑选依赖它）。
		if e.form != formCall {
			e.argc = r.argCount(node)
		}
		if elem := r.resolveExpr(gc, fc, e, wantFunc); elem != nil {
			return elem
		}
		// 可调用对象（operator() 重载 / 函数指针变量）不建模为外部函数调用。
		if r.lookupLocalVariable(gc, fc, e.node, e.name) != nil {
			return nil
		}
		// 构造器调用（T(...) 临时对象）：解析到真实的构造器元素（带参数列表）。
		// 无显式构造器（隐式构造器 / typedef）时返回 nil：不产指向不存在元素的 CALL。
		if t := r.lookupType(gc, fc, e.name); t != nil {
			return r.lookupConstructor(gc, t, e.argc)
		}
		// 兜底：外部函数（保持原有 QN 形态：接收者原文::函数名）。
		qn := e.name
		if txt := exprText(e.recv, src); txt != "" {
			qn = helper.BuildQN(txt, e.name)
		}
		return helper.NewExternalElement(model.Function, e.name, qn)

	case model.Create, model.Cast:
		return r.resolveExpr(gc, fc, e, wantType)

	case model.Assign, model.Use:
		return r.resolveExpr(gc, fc, e, wantValue)

	case model.Throw:
		// throw new T() → 类型；throw f() → 函数；throw var → 变量声明类型。
		switch e.form {
		case formNew:
			return r.resolveExpr(gc, fc, e, wantType)
		case formCall:
			// throw T(...)：被调用者是类型名时取类型（临时对象）。
			if t := r.lookupTypeAt(gc, fc, e.node, e.name); t != nil {
				return t
			}
			// 兜底：按被调用者全名解析为类型（std::runtime_error(...) 等外部类型）。
			// 不能退回 resolveExpr(wantFunc)：那会把异常类型误判为函数/方法。
			raw := e.name
			if txt := exprText(e.recv, src); txt != "" {
				raw = helper.BuildQN(txt, e.name)
			}
			return r.externalTypeOf(raw)
		default:
			if t := r.typeOf(gc, fc, e); t != nil {
				return t
			}
			// 变量声明类型不在源码中（外部类型）时仍产出外部类型元素。
			if v := r.resolveValue(gc, fc, e); v != nil && v.Extra != nil {
				for _, key := range []string{constants.VariableTypeWithQN, constants.FieldTypeWithQN} {
					if raw, ok := v.Extra.Mores[key].(string); ok && raw != "" {
						return r.ResolveTypeAt(gc, fc, e.node, raw, model.Class)
					}
				}
			}
			return nil
		}
	}
	return nil
}

// =============================================================================
// 段 1：表达式归一化
// =============================================================================

// exprForm 是动作表达式归一化后的形态。
type exprForm int

const (
	formIdent  exprForm = iota // 裸标识符：name
	formMember                 // 成员访问：recv.name / recv->name
	formScoped                 // 限定访问：scope::name
	formCall                   // 调用：callee(...)
	formNew                    // new T(...)
	formCast                   // (T)x / static_cast<T>(x)
	formText                   // 纯文本接收者（公开 SPI 传入 receiver 字符串时）
)

// expr 是动作节点的归一化描述（段 1 产出）。
// 段 2 只依赖本结构，不再关心原始 AST 形态。
type expr struct {
	form     exprForm
	node     coreast.Node // 表达式节点（可为 nil）
	recv     *expr        // 接收者表达式（formMember / formScoped / formCall）
	name     string       // 成员名 / 函数名 / 标识符
	typeText string       // formNew / formCast 的类型文本
	text     string       // formText 的接收者原文
	argc     int          // formCall 的实参个数
}

// normalize 将任意动作节点折叠为统一的 expr（段 1）。
func (r *SymbolResolver) normalize(node coreast.Node, src []byte) *expr {
	if node == nil {
		return nil
	}
	switch node.Kind() {
	case "identifier", "field_identifier", "type_identifier", "namespace_identifier",
		"operator_name", "destructor_name", "primitive_type", "sized_type_specifier":
		return &expr{form: formIdent, node: node, name: strings.TrimSpace(node.Utf8Text(src))}

	case "field_expression":
		field := node.ChildByFieldName("field")
		if field == nil {
			return nil
		}
		return &expr{
			form: formMember,
			node: node,
			recv: r.normalize(node.ChildByFieldName("argument"), src),
			name: helper.GetNodeContent(field, src),
		}

	case "qualified_identifier":
		nameNode := node.ChildByFieldName("name")
		if nameNode == nil {
			return nil
		}
		return &expr{
			form: formScoped,
			node: node,
			recv: r.normalize(node.ChildByFieldName("scope"), src),
			name: strings.TrimSpace(nameNode.Utf8Text(src)),
		}

	case "call_expression":
		fnNode := node.ChildByFieldName("function")
		callee := r.normalize(fnNode, src)
		if callee == nil {
			return nil
		}
		// 立即调用的 lambda：保留 lambda 节点本身，供段 2 直接命中 Lambda 元素。
		if fnNode != nil && fnNode.Kind() == "lambda_expression" {
			callee.form = formCall
			callee.node = fnNode
			callee.name = "lambda"
			if args := node.ChildByFieldName("arguments"); args != nil {
				callee.argc = args.NamedChildCount()
			}
			return callee
		}
		// static_cast<T>(x) 等：保持转换语义，不当作普通调用。
		if callee.form == formCast {
			return callee
		}
		callee.form = formCall
		callee.node = node
		if args := node.ChildByFieldName("arguments"); args != nil {
			callee.argc = args.NamedChildCount()
		}
		return callee

	case "new_expression":
		return &expr{form: formNew, node: node, typeText: typeNodeName(node.ChildByFieldName("type"), src)}

	case "cast_expression":
		return &expr{form: formCast, node: node, typeText: typeNodeName(node.ChildByFieldName("type"), src)}

	case "template_function", "template_method":
		nameNode := node.ChildByFieldName("name")
		name := ""
		if nameNode != nil {
			name = strings.TrimSpace(nameNode.Utf8Text(src))
		}
		if isCastKeyword(name) {
			return &expr{form: formCast, node: node, typeText: castTemplateArg(node, src)}
		}
		if nameNode != nil {
			return r.normalize(nameNode, src)
		}
		return nil

	case "pointer_expression", "parenthesized_expression":
		arg := node.ChildByFieldName("argument")
		if arg == nil {
			for i := 0; i < node.NamedChildCount(); i++ {
				arg = node.NamedChild(i)
				break
			}
		}
		return r.normalize(arg, src)

	case "subscript_expression":
		return r.normalize(node.ChildByFieldName("argument"), src)
	}

	// 未知形态：退化为裸标识符（名称取原文）。
	return &expr{form: formIdent, node: node, name: strings.TrimSpace(node.Utf8Text(src))}
}

// exprText 返回表达式的源码文本（外部兜底 QN 用）。
func exprText(e *expr, src []byte) string {
	if e == nil {
		return ""
	}
	if e.form == formText {
		return e.text
	}
	if e.node != nil {
		return helper.GetNodeContent(e.node, src)
	}
	return e.text
}

// =============================================================================
// 段 2：查找
// =============================================================================

// wantKind 是段 2 的期望解析类别。
type wantKind int

const (
	wantValue wantKind = iota // 变量/字段/枚举常量
	wantFunc                  // 函数/方法
	wantType                  // 类型
)

// resolveExpr 按期望类别解析归一化表达式（段 2 入口）。
func (r *SymbolResolver) resolveExpr(gc core.GlobalContext, fc core.FileContext, e *expr, want wantKind) *model.CodeElement {
	if e == nil {
		return nil
	}
	switch e.form {
	case formNew, formCast:
		if e.typeText == "" {
			return nil
		}
		return r.ResolveTypeAt(gc, fc, e.node, e.typeText, model.Class)

	case formCall:
		if want == wantType {
			return r.typeOf(gc, fc, e)
		}
		return r.resolveFunc(gc, fc, e)

	case formIdent, formMember, formScoped, formText:
		switch want {
		case wantFunc:
			return r.resolveFunc(gc, fc, e)
		case wantType:
			if t := r.typeOf(gc, fc, e); t != nil {
				return t
			}
			// 未命中源码类型：按原文产外部类型（Create/Cast 语义）。
			raw := e.name
			if e.node != nil {
				raw = helper.GetNodeContent(e.node, *fc.SourceBytes())
			}
			return r.ResolveTypeAt(gc, fc, e.node, raw, model.Class)
		default:
			return r.resolveValue(gc, fc, e)
		}
	}
	return nil
}

// resolveFunc 解析函数/方法：接收者类型 → 成员方法；否则作用域链 → 自由函数。
func (r *SymbolResolver) resolveFunc(gc core.GlobalContext, fc core.FileContext, e *expr) *model.CodeElement {
	if e == nil || e.name == "" {
		return nil
	}
	// 立即调用的 lambda（[](int v){...}(3)）：直接命中该 Lambda 元素。
	if e.node != nil && e.node.Kind() == "lambda_expression" {
		if elem := lambdaElementOf(fc, e.node); elem != nil {
			return elem
		}
	}
	if e.recv != nil {
		if recvType := r.typeOf(gc, fc, e.recv); recvType != nil {
			if elem := r.lookupMemberFunc(gc, fc, recvType, e.name, e.argc); elem != nil {
				return elem
			}
			// 命名空间接收者：成员可能位于 inline namespace 内，保留作用域回退。
			if recvType.Kind == model.Namespace {
				return r.lookupScopedFunc(gc, fc, e.node, e.name, e.argc)
			}
			// 外部接收者类型：产外部方法。
			return helper.NewExternalElement(model.Method, e.name, helper.BuildQN(recvType.QualifiedName, e.name))
		}
		// 接收者存在但类型未知：不能忽略接收者按非限定名查找
		// （否则 zz.freeFn() 会误命中全局 freeFn）。
		return nil
	}
	if elem := r.lookupScopedFunc(gc, fc, e.node, e.name, e.argc); elem != nil {
		return elem
	}
	// 变量持有 lambda（auto f = [](){...}; f();）：解析到 Lambda 元素。
	if v := r.lookupScopedSymbol(gc, fc, e.node, e.name); v != nil && v.Extra != nil {
		if lambdaQN, ok := v.Extra.Mores[constants.VariableLambdaQN].(string); ok && lambdaQN != "" {
			if entry, found := gc.FindByQualifiedName(lambdaQN); found {
				return entry.Element
			}
		}
		// 可调用对象（obj(args)）：解析到其类型的 operator() 重载。
		if owner := r.declaredTypeOf(gc, fc, v); owner != nil {
			if elem := r.lookupMemberFunc(gc, fc, owner, "operator()", e.argc); elem != nil {
				return elem
			}
		}
	}
	return nil
}

// resolveValue 解析变量/字段/枚举常量：接收者类型 → 成员；否则作用域链。
func (r *SymbolResolver) resolveValue(gc core.GlobalContext, fc core.FileContext, e *expr) *model.CodeElement {
	if e == nil || e.name == "" {
		return nil
	}
	if e.recv != nil {
		if recvType := r.typeOf(gc, fc, e.recv); recvType != nil {
			if elem := r.lookupMember(gc, fc, recvType, e.name, model.Field); elem != nil {
				return elem
			}
			// 命名空间接收者：成员可能位于 inline namespace 内，保留作用域回退。
			if recvType.Kind == model.Namespace {
				return r.lookupScopedSymbol(gc, fc, e.node, e.name)
			}
			return nil
		}
		// 接收者存在但类型未知：不按非限定名查找（zz.baseField 不应命中 Base::baseField）。
		return nil
	}
	return r.lookupScopedSymbol(gc, fc, e.node, e.name)
}

// typeOf 返回表达式的静态类型（链式接收者传播的递归入口）。
func (r *SymbolResolver) typeOf(gc core.GlobalContext, fc core.FileContext, e *expr) *model.CodeElement {
	if e == nil {
		return nil
	}
	switch e.form {
	case formNew, formCast:
		if e.typeText == "" {
			return nil
		}
		return r.lookupType(gc, fc, helper.BaseTypeName(e.typeText))

	case formCall:
		return r.returnTypeOf(gc, fc, r.resolveFunc(gc, fc, e))

	case formText:
		return r.textType(gc, fc, e)

	case formScoped:
		// 命名空间别名接收者（na::sub）：展开别名后按限定名查找。
		if e.recv != nil && e.recv.form == formIdent {
			if target := namespaceAliasTarget(fc, e.recv.name); target != "" {
				if t := r.lookupType(gc, fc, helper.BuildQN(target, e.name)); t != nil {
					return t
				}
			}
		}
		// A::B 先按完整限定名当类型（namespace/嵌套类），再按成员变量。
		if e.node != nil {
			if t := r.lookupType(gc, fc, helper.BaseTypeName(helper.GetNodeContent(e.node, *fc.SourceBytes()))); t != nil {
				return t
			}
		}
		if t := r.declaredTypeOf(gc, fc, r.resolveValue(gc, fc, e)); t != nil {
			return t
		}
		return r.lookupType(gc, fc, helper.BaseTypeName(e.name))

	case formIdent, formMember:
		// this / *this：静态类型为宿主类（含类外定义的成员函数）。
		if e.form == formIdent && e.name == "this" {
			return r.thisType(gc, fc, e.node)
		}
		// 命名空间别名（namespace na = ns; na::f()）解析为目标命名空间。
		if e.form == formIdent {
			if target := namespaceAliasTarget(fc, e.name); target != "" {
				if t := r.lookupType(gc, fc, target); t != nil {
					return t
				}
			}
		}
		// 裸标识符/成员作接收者时通常是变量（obj.method()），变量优先于同名类型。
		if t := r.declaredTypeOf(gc, fc, r.resolveValue(gc, fc, e)); t != nil {
			return t
		}
		return r.lookupTypeAt(gc, fc, e.node, helper.BaseTypeName(e.name))
	}
	return nil
}

// thisType 返回 this 的静态类型：宿主类/结构体。
// 类内成员函数直接取区间包含的类；类外定义（void Derived::f() {...}）按
// 作用域链上溯到最近的类型元素。
func (r *SymbolResolver) thisType(gc core.GlobalContext, fc core.FileContext, node coreast.Node) *model.CodeElement {
	if t := enclosingType(fc, node); t != nil {
		return t
	}
	for _, scope := range r.scopeChain(fc, node) {
		if scope == "" {
			break
		}
		if t := r.lookupType(gc, fc, scope); t != nil {
			return t
		}
	}
	return nil
}

// textType 解析纯文本接收者的类型（公开 SPI 传入 receiver 字符串时）。
func (r *SymbolResolver) textType(gc core.GlobalContext, fc core.FileContext, e *expr) *model.CodeElement {
	recv := strings.TrimSpace(e.text)
	if recv == "" {
		return nil
	}
	if recv == "this" || recv == "*this" {
		return r.thisType(gc, fc, e.node)
	}
	if t := r.lookupType(gc, fc, helper.BaseTypeName(recv)); t != nil {
		return t
	}
	return r.declaredTypeOf(gc, fc, r.lookupScopedSymbol(gc, fc, e.node, recv))
}

// returnTypeOf 取函数元素的返回类型元素（优先 QN 版本，退回原始文本）。
// 源码内类型优先；外部类型（std::string 等）也返回外部类型元素，
// 使 `make().size()` 能按接收者类型解析为 std::string::size 而非 make()::size。
func (r *SymbolResolver) returnTypeOf(gc core.GlobalContext, fc core.FileContext, fn *model.CodeElement) *model.CodeElement {
	if fn == nil || fn.Extra == nil {
		return nil
	}
	for _, key := range []string{constants.FunctionReturnTypeWithQN, constants.FunctionReturnType} {
		if t, ok := fn.Extra.Mores[key].(string); ok && t != "" {
			base := helper.BaseTypeName(t)
			if elem := r.lookupType(gc, fc, base); elem != nil {
				return elem
			}
			if base == "" || r.IsPrimitive(base) || helper.IsTypeVariable(base) {
				continue
			}
			return helper.NewExternalElement(model.Class, base, base)
		}
	}
	return nil
}

// declaredTypeKeys 是变量/字段声明类型的 Mores 键（优先 QN 版本，退回原始文本）。
var declaredTypeKeys = []string{
	constants.VariableTypeWithQN, constants.FieldTypeWithQN,
	constants.VariableRawType, constants.FieldRawType,
}

// declaredTypeText 取变量/字段元素的声明类型原文。
func declaredTypeText(v *model.CodeElement) string {
	if v == nil || v.Extra == nil {
		return ""
	}
	for _, key := range declaredTypeKeys {
		if t, ok := v.Extra.Mores[key].(string); ok && t != "" {
			return t
		}
	}
	return ""
}

// declaredTypeOf 取变量/字段元素的声明类型元素。
// 源码内类型优先；外部类型（std::string 等）也返回外部类型元素，
// 使 `s.size()` 能按接收者类型解析为 std::string::size 而非退化为 s::size。
func (r *SymbolResolver) declaredTypeOf(gc core.GlobalContext, fc core.FileContext, v *model.CodeElement) *model.CodeElement {
	raw := declaredTypeText(v)
	if raw == "" {
		return nil
	}
	base := helper.BaseTypeName(raw)
	if t := r.lookupType(gc, fc, base); t != nil {
		return t
	}
	if base == "" || r.IsPrimitive(base) || helper.IsTypeVariable(base) {
		return nil
	}
	return helper.NewExternalElement(model.Class, base, base)
}

// =============================================================================
// 类型/成员查找
// =============================================================================

// usingTarget 返回 using 声明引入的短名对应的目标限定名。
func usingTarget(fc core.FileContext, name string) string {
	if ua, ok := fc.(cppctx.UsingAware); ok {
		if t, found := ua.UsingAlias(name); found {
			return t
		}
	}
	return ""
}

// namespaceAliasTarget 返回命名空间别名对应的目标命名空间名。
func namespaceAliasTarget(fc core.FileContext, name string) string {
	if ua, ok := fc.(cppctx.UsingAware); ok {
		if t, found := ua.NamespaceAlias(name); found {
			return t
		}
	}
	return ""
}

// lookupTypeAt 按使用位置查找类型：非限定名先沿词法作用域链查找，
// 再退回全局 lookupType（限定名/文件短名/全局短名）。
func (r *SymbolResolver) lookupTypeAt(gc core.GlobalContext, fc core.FileContext, node coreast.Node, name string) *model.CodeElement {
	if node != nil && !strings.Contains(name, helper.QNSep) {
		for _, scope := range r.scopeChain(fc, node) {
			// using 声明在声明所在作用域引入符号，优先于全局作用域（空 scope）。
			if scope == "" {
				if target := usingTarget(fc, name); target != "" && target != name {
					if t := r.lookupTypeAt(gc, fc, node, target); t != nil {
						return t
					}
				}
			}
			qn := helper.BuildQN(scope, name)
			for _, entry := range gc.FindAllByQualifiedName(qn) {
				if isTypeKind(entry.Element.Kind) {
					return entry.Element
				}
			}
		}
	}
	return r.lookupType(gc, fc, name)
}

// lookupType 按限定名/短名查找类型元素。
func (r *SymbolResolver) lookupType(gc core.GlobalContext, fc core.FileContext, name string) *model.CodeElement {
	for _, entry := range gc.FindAllByQualifiedName(name) {
		if isTypeKind(entry.Element.Kind) {
			return entry.Element
		}
	}
	// 相对限定名：namespace app 内的 core::Base 实为 app::core::Base。
	if strings.Contains(name, helper.QNSep) {
		var namespaces []string
		if nfc, ok := fc.(cppctx.NamespaceAware); ok {
			namespaces = nfc.Namespaces()
		}
		for _, ns := range namespaces {
			if cand := helper.BuildQN(ns, name); cand != name {
				for _, entry := range gc.FindAllByQualifiedName(cand) {
					if isTypeKind(entry.Element.Kind) {
						return entry.Element
					}
				}
			}
		}
	}
	// 文件内短名优先（C 无 ns 时尤其重要）。
	if entries, ok := fc.FindByShortName(name); ok {
		for _, e := range entries {
			if isTypeKind(e.Element.Kind) {
				return e.Element
			}
		}
	}
	// 全局短名（跨文件）。
	for _, def := range gc.FindByShortName(name) {
		if isTypeKind(def.Element.Kind) {
			return def.Element
		}
	}
	return nil
}

// lookupMember 在类型（含基类）中查找字段/枚举常量。
func (r *SymbolResolver) lookupMember(gc core.GlobalContext, fc core.FileContext, owner *model.CodeElement, name string, want model.ElementKind) *model.CodeElement {
	for _, o := range r.typeChain(gc, fc, owner, 0) {
		qn := helper.BuildQN(o.QualifiedName, name)
		for _, entry := range gc.FindAllByQualifiedName(qn) {
			switch entry.Element.Kind {
			case model.Field, model.Variable, model.EnumConstant:
				return entry.Element
			}
		}
	}
	return nil
}

// lookupMemberFunc 在类型（含基类）中查找方法。
func (r *SymbolResolver) lookupMemberFunc(gc core.GlobalContext, fc core.FileContext, owner *model.CodeElement, name string, argCount int) *model.CodeElement {
	for _, o := range r.typeChain(gc, fc, owner, 0) {
		qn := helper.BuildQN(o.QualifiedName, name)
		if entries, ok := gc.FindOverloadCandidates(qn); ok {
			if e := pickOverload(entries, argCount); e != nil {
				return e.Element
			}
		}
	}
	return nil
}

// lookupConstructor 在类型中查找与实参个数匹配的构造器元素。
// 仅 Class/Struct 有构造器语义；typedef（TYPE）等返回 nil。
func (r *SymbolResolver) lookupConstructor(gc core.GlobalContext, t *model.CodeElement, argc int) *model.CodeElement {
	if t == nil || (t.Kind != model.Class && t.Kind != model.Struct) {
		return nil
	}
	qn := helper.BuildQN(t.QualifiedName, t.Name)
	entries, ok := gc.FindOverloadCandidates(qn)
	if !ok {
		return nil
	}
	if e := pickOverload(entries, argc); e != nil {
		return e.Element
	}
	return nil
}

// externalTypeOf 按类型原文产外部类型元素（保留 :: 限定与原始形态）。
func (r *SymbolResolver) externalTypeOf(raw string) *model.CodeElement {
	raw = helper.NormalizeTypeText(raw)
	base := helper.BaseTypeName(raw)
	if base == "" || base == "?" {
		return nil
	}
	if r.IsPrimitive(base) {
		return &model.CodeElement{Name: base, QualifiedName: base, Kind: model.Primitive, IsFormExternal: true}
	}
	if helper.IsTypeVariable(base) {
		return nil
	}
	return helper.NewExternalElement(model.Class, base, raw)
}

// typeChain 返回类型自身及其基类链（广度优先，去环）。
func (r *SymbolResolver) typeChain(gc core.GlobalContext, fc core.FileContext, owner *model.CodeElement, depth int) []*model.CodeElement {
	if owner == nil || depth > 8 {
		return nil
	}
	out := []*model.CodeElement{owner}
	if owner.Extra == nil {
		return out
	}
	supers, _ := owner.Extra.Mores[constants.ClassSuperClass].([]string)
	for _, s := range supers {
		base := r.lookupType(gc, fc, helper.BaseTypeName(s))
		if base == nil || base.IsFormExternal || base.QualifiedName == owner.QualifiedName {
			continue
		}
		out = append(out, r.typeChain(gc, fc, base, depth+1)...)
	}
	return out
}

// lambdaElementOf 返回与给定 lambda_expression 节点位置一致的 Lambda 元素。
func lambdaElementOf(fc core.FileContext, node coreast.Node) *model.CodeElement {
	if node == nil {
		return nil
	}
	for _, entry := range fc.Definitions() {
		if entry.Element.Kind != model.Lambda || entry.Node == nil {
			continue
		}
		if entry.Node.Equal(node) {
			return entry.Element
		}
	}
	return nil
}

// enclosingType 返回节点所属的类/结构体元素。
func enclosingType(fc core.FileContext, node coreast.Node) *model.CodeElement {
	if node == nil {
		return nil
	}
	start, end := node.StartByte(), node.EndByte()
	var best *model.CodeElement
	bestSpan := -1
	for _, entry := range fc.Definitions() {
		k := entry.Element.Kind
		if k != model.Class && k != model.Struct {
			continue
		}
		if entry.Node == nil {
			continue
		}
		s, e := entry.Node.StartByte(), entry.Node.EndByte()
		if s <= start && end <= e {
			if span := e - s; bestSpan < 0 || span < bestSpan {
				bestSpan, best = span, entry.Element
			}
		}
	}
	return best
}

// =============================================================================
// 作用域链查找
// =============================================================================

// scopeChain 返回节点的词法作用域 QN 链（最内层 → 最外层 → ""）。
func (r *SymbolResolver) scopeChain(fc core.FileContext, node coreast.Node) []string {
	var chain []string
	// 控制流语句的条件/初始化/更新表达式不在语句体块内，但语句内声明的变量
	// （if/while/switch 条件声明、for 初始化变量）挂在语句体块下。此处把该块
	// 显式加入作用域链，避免退化为文件短名而误命中同名变量。
	for anc := node.Parent(); anc != nil; anc = anc.Parent() {
		if !isControlFlowKind(anc.Kind()) || !nodeInStatementHead(node, anc) {
			continue
		}
		if body := primaryBodyNode(anc); body != nil {
			if qn := blockQNByRange(fc, body); qn != "" {
				chain = append(chain, qn)
			}
		}
	}
	owner := enclosingScope(fc, node)
	if owner != nil {
		chain = append(chain, owner.QualifiedName)
		for scope := helper.PkgOf(owner.QualifiedName); scope != ""; {
			chain = append(chain, scope)
			next := helper.PkgOf(scope)
			if next == scope {
				break
			}
			scope = next
		}
	}
	chain = append(chain, "")
	return chain
}

// enclosingScope 返回节点所属的最内层作用域元素（函数/方法/类/Lambda/块）。
func enclosingScope(fc core.FileContext, node coreast.Node) *model.CodeElement {
	if node == nil {
		return nil
	}
	start, end := node.StartByte(), node.EndByte()
	var best *model.CodeElement
	bestSpan := -1
	for _, entry := range fc.Definitions() {
		if !helper.IsScopeContainer(entry.Element.Kind) {
			continue
		}
		if entry.Node == nil {
			continue
		}
		s, e := entry.Node.StartByte(), entry.Node.EndByte()
		if s <= start && end <= e {
			if span := e - s; bestSpan < 0 || span < bestSpan {
				bestSpan, best = span, entry.Element
			}
		}
	}
	return best
}

// lookupLocalVariable 仅沿词法作用域链查找局部变量（不跨文件、不查全局短名）。
// 用于判定「被调用者其实是局部可调用对象」，避免误产外部函数。
func (r *SymbolResolver) lookupLocalVariable(gc core.GlobalContext, fc core.FileContext, node coreast.Node, name string) *model.CodeElement {
	for _, scope := range r.scopeChain(fc, node) {
		qn := helper.BuildQN(scope, name)
		for _, entry := range gc.FindAllByQualifiedName(qn) {
			if entry.Element.Kind == model.Variable {
				return entry.Element
			}
		}
	}
	return nil
}

// lookupScopedSymbol 沿作用域链查找变量/字段/枚举常量。
func (r *SymbolResolver) lookupScopedSymbol(gc core.GlobalContext, fc core.FileContext, node coreast.Node, name string) *model.CodeElement {
	for _, scope := range r.scopeChain(fc, node) {
		// using 声明在声明所在作用域引入符号，优先于全局作用域（空 scope）。
		if scope == "" {
			if target := usingTarget(fc, name); target != "" && target != name {
				if elem := r.lookupScopedSymbol(gc, fc, node, target); elem != nil {
					return elem
				}
			}
		}
		qn := helper.BuildQN(scope, name)
		for _, entry := range gc.FindAllByQualifiedName(qn) {
			switch entry.Element.Kind {
			case model.Variable, model.Field, model.EnumConstant:
				return entry.Element
			}
		}
	}
	// 文件内短名（同文件全局变量/字段）。
	if entries, ok := fc.FindByShortName(name); ok {
		for _, e := range entries {
			switch e.Element.Kind {
			case model.Variable, model.Field, model.EnumConstant:
				return e.Element
			}
		}
	}
	// 全局短名（跨文件全局变量/枚举常量）。
	// 必须排除局部变量与类字段：它们不具全局可见性，按短名跨文件命中会
	// 把 `std::runtime_error(...)` 的 `std` 误解析为别的文件里的局部变量。
	for _, def := range gc.FindByShortName(name) {
		switch def.Element.Kind {
		case model.EnumConstant:
			return def.Element
		case model.Variable:
			if isGlobalVariable(def.Element) {
				return def.Element
			}
		}
	}
	return nil
}

// isGlobalVariable 判断变量是否为文件/全局作用域变量。
func isGlobalVariable(e *model.CodeElement) bool {
	if e == nil || e.Extra == nil {
		return false
	}
	isGlobal, _ := e.Extra.Mores[constants.VariableIsGlobal].(bool)
	return isGlobal
}

// lookupScopedFunc 沿作用域链查找函数/方法。
func (r *SymbolResolver) lookupScopedFunc(gc core.GlobalContext, fc core.FileContext, node coreast.Node, name string, argCount int) *model.CodeElement {
	for _, scope := range r.scopeChain(fc, node) {
		// using 声明在声明所在作用域引入符号，优先于全局作用域（空 scope）。
		if scope == "" {
			if target := usingTarget(fc, name); target != "" && target != name {
				if elem := r.lookupScopedFunc(gc, fc, node, target, argCount); elem != nil {
					return elem
				}
			}
		}
		qn := helper.BuildQN(scope, name)
		if entries, ok := gc.FindOverloadCandidates(qn); ok {
			if e := pickOverload(entries, argCount); e != nil {
				return e.Element
			}
		}
	}
	// 文件内短名（static 函数）。
	if entries, ok := fc.FindByShortName(name); ok {
		for _, e := range entries {
			if isFunctionKind(e.Element.Kind) {
				return e.Element
			}
		}
	}
	// 全局短名。
	for _, def := range gc.FindByShortName(name) {
		if isFunctionKind(def.Element.Kind) {
			return def.Element
		}
	}
	return nil
}

// pickOverload 按实参个数挑选重载（定义优先）。
func pickOverload(entries []*core.DefinitionEntry, argCount int) *core.DefinitionEntry {
	if len(entries) == 0 {
		return nil
	}
	var fallback *core.DefinitionEntry
	for _, e := range entries {
		params, _ := e.Element.Extra.Mores[constants.FunctionParameters].([]string)
		if len(params) == argCount {
			if !isDeclKind(e.Element.Kind) {
				return e
			}
			if fallback == nil {
				fallback = e
			}
		}
	}
	if fallback != nil {
		return fallback
	}
	// 无参数信息（或数量不匹配）：定义优先，否则首个。
	for _, e := range entries {
		if !isDeclKind(e.Element.Kind) {
			return e
		}
	}
	return entries[0]
}

// argCount 返回调用节点的实参个数。
func (r *SymbolResolver) argCount(node coreast.Node) int {
	call := findAncestorOrSelf(node, "call_expression", "new_expression")
	if call == nil {
		return 0
	}
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return 0
	}
	return args.NamedChildCount()
}

// =============================================================================
// 节点工具
// =============================================================================

// isCastKeyword 判断是否为 C++ 显式转换关键字。
func isCastKeyword(name string) bool {
	switch name {
	case "static_cast", "dynamic_cast", "const_cast", "reinterpret_cast":
		return true
	}
	return false
}

// castTemplateArg 取 template_function 的模板实参类型文本。
func castTemplateArg(node coreast.Node, src []byte) string {
	if node == nil {
		return ""
	}
	if node.Kind() == "template_function" {
		if args := node.ChildByFieldName("arguments"); args != nil {
			for i := 0; i < args.NamedChildCount(); i++ {
				if name := typeNodeName(args.NamedChild(i), src); name != "" {
					return name
				}
			}
		}
	}
	return ""
}

// typeNodeName 提取类型节点的名称文本（template_type 取模板名）。
func typeNodeName(node coreast.Node, src []byte) string {
	if node == nil {
		return ""
	}
	switch node.Kind() {
	case "type_descriptor":
		if t := node.ChildByFieldName("type"); t != nil {
			return typeNodeName(t, src)
		}
	case "template_type":
		if n := node.ChildByFieldName("name"); n != nil {
			return helper.GetNodeContent(n, src)
		}
	case "qualified_identifier", "type_identifier", "primitive_type", "sized_type_specifier":
		return helper.GetNodeContent(node, src)
	}
	return helper.GetNodeContent(node, src)
}

// findAncestorOrSelf 向上查找指定 kind 的节点。
func findAncestorOrSelf(node coreast.Node, kinds ...string) coreast.Node {
	for curr := node; curr != nil; curr = curr.Parent() {
		for _, k := range kinds {
			if curr.Kind() == k {
				return curr
			}
		}
		if strings.HasSuffix(curr.Kind(), "_statement") {
			return nil
		}
	}
	return nil
}

func isTypeKind(k model.ElementKind) bool {
	switch k {
	case model.Class, model.Struct, model.Enum, model.Type, model.Namespace, model.Interface:
		return true
	}
	return false
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

// isControlFlowKind 判定语句是否引入控制流声明作用域（与 Collector 保持一致）。
func isControlFlowKind(kind string) bool {
	switch kind {
	case "if_statement", "for_statement", "while_statement", "do_statement",
		"switch_statement", "try_statement", "catch_clause", "for_range_loop":
		return true
	}
	return false
}

// primaryBodyNode 返回语句的主执行体节点（if 取 consequence，其余取 body）。
func primaryBodyNode(node coreast.Node) coreast.Node {
	if node.Kind() == "if_statement" {
		if b := node.ChildByFieldName("consequence"); b != nil {
			return b
		}
	} else if b := node.ChildByFieldName("body"); b != nil {
		return b
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		if ch := node.NamedChild(i); ch != nil && ch.Kind() == "compound_statement" {
			return ch
		}
	}
	return nil
}

// nodeInStatementHead 判定 node 是否位于语句的「头部」（条件/初始化/更新/参数），
// 而非主执行体、else 体或 catch 体。只有头部节点才需要补该语句的声明作用域块。
func nodeInStatementHead(node, stmt coreast.Node) bool {
	body := primaryBodyNode(stmt)
	for n := node; n != nil && !n.Equal(stmt); n = n.Parent() {
		if body != nil && n.Equal(body) {
			return false
		}
		switch n.Kind() {
		case "else_clause", "catch_clause":
			return false
		}
	}
	return true
}

// blockQNByRange 按字节区间查找 body 对应的 ScopeBlock QN。
// 用区间而非节点身份，兼容主树/原始树/回补树的不同节点实例。
func blockQNByRange(fc core.FileContext, body coreast.Node) string {
	s, e := body.StartByte(), body.EndByte()
	for _, entry := range fc.Definitions() {
		if entry.Element.Kind != model.ScopeBlock || entry.Node == nil {
			continue
		}
		if entry.Node.StartByte() == s && entry.Node.EndByte() == e {
			return entry.Element.QualifiedName
		}
	}
	return ""
}

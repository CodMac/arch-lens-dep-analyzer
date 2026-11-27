// Package constants 集中定义 C/C++ 语言的 Mores key 常量，避免业务代码散落魔法字符串。
package constants

// =============================================================================
// C/C++ Collector 采集的 Element.Extra.Mores
// =============================================================================

const (
	// FunctionReturnType 函数/方法的返回类型原始文本 -> string
	FunctionReturnType = "cpp.function.return_type"
	// FunctionReturnTypeWithQN 函数/方法的返回类型 QN -> string
	FunctionReturnTypeWithQN = "cpp.function.return_type_with_qn"
	// FunctionParameters 函数/方法的参数列表 (原始类型文本 + 名称) -> []string
	FunctionParameters = "cpp.function.parameters"
	// FunctionParametersWithQN 函数/方法的参数列表 (类型 QN + 名称) -> []string
	FunctionParametersWithQN = "cpp.function.parameters_with_qn"
	// FunctionIsDeclaration 是否为函数声明（无函数体）-> bool
	FunctionIsDeclaration = "cpp.function.is_declaration"
	// FunctionIsVariadic 是否为可变参数函数 (... ) -> bool
	FunctionIsVariadic = "cpp.function.is_variadic"
	// FunctionIsOperator 是否为运算符重载 -> bool
	FunctionIsOperator = "cpp.function.is_operator"
	// FunctionIsConstructor 是否为构造函数 -> bool
	FunctionIsConstructor = "cpp.function.is_constructor"
	// FunctionIsDestructor 是否为析构函数 -> bool
	FunctionIsDestructor = "cpp.function.is_destructor"
	// FunctionIsVirtual 是否为虚函数 -> bool
	FunctionIsVirtual = "cpp.function.is_virtual"
	// FunctionIsPureVirtual 是否为纯虚函数 -> bool
	FunctionIsPureVirtual = "cpp.function.is_pure_virtual"
	// FunctionIsExplicit 是否为 explicit 转换构造/转换运算符 -> bool
	FunctionIsExplicit = "cpp.function.is_explicit"
	// FunctionIsNoexcept 是否声明为 noexcept -> bool
	FunctionIsNoexcept = "cpp.function.is_noexcept"
	// FunctionIsDefaulted 是否为 `= default`（编译器默认生成）-> bool
	FunctionIsDefaulted = "cpp.function.is_defaulted"
	// FunctionIsDeleted 是否为 `= delete`（显式删除）-> bool
	FunctionIsDeleted = "cpp.function.is_deleted"

	// VariableRawType 局部变量/全局变量的类型文本 -> string
	VariableRawType = "cpp.variable.raw_type"
	// VariableTypeWithQN 局部变量/全局变量的类型 QN -> string
	VariableTypeWithQN = "cpp.variable.type_with_qn"
	// VariableIsStatic 变量是否为 static 存储类 -> bool
	VariableIsStatic = "cpp.variable.is_static"
	// VariableIsExtern 变量是否为 extern 存储类 -> bool
	VariableIsExtern = "cpp.variable.is_extern"
	// VariableIsConst 变量是否为 const -> bool
	VariableIsConst = "cpp.variable.is_const"
	// VariableIsGlobal 变量是否为全局变量（顶层声明）-> bool
	VariableIsGlobal = "cpp.variable.is_global"
	// VariableLambdaQN 变量初始化为 lambda 时，对应 Lambda 元素的 QN -> string
	VariableLambdaQN = "cpp.variable.lambda_qn"

	// FieldRawType 成员字段的类型文本 -> string
	FieldRawType = "cpp.field.raw_type"
	// FieldTypeWithQN 成员字段的类型 QN -> string
	FieldTypeWithQN = "cpp.field.type_with_qn"
	// FieldIsStatic 字段是否为 static -> bool
	FieldIsStatic = "cpp.field.is_static"
	// FieldIsConst 字段是否为 const -> bool
	FieldIsConst = "cpp.field.is_const"

	// StructIsUnion 是否为 union -> bool
	StructIsUnion = "cpp.struct.is_union"
	// StructIsDeclaration 是否为前向声明 (struct Foo;) -> bool
	StructIsDeclaration = "cpp.struct.is_declaration"

	// ClassSuperClass C++ 类的父类列表（含继承方式 virtual/private）-> []string
	ClassSuperClass = "cpp.class.superclass"
	// ClassIsDeclaration 是否为类的前向声明 -> bool
	ClassIsDeclaration = "cpp.class.is_declaration"
	// ClassFriendTypes 友元类型声明（friend class X;）的类型文本列表 -> []string
	ClassFriendTypes = "cpp.class.friend_types"
	// ClassFriendFunctions 友元函数声明（friend void f(int);）的 "名(签名)" 列表 -> []string
	ClassFriendFunctions = "cpp.class.friend_functions"

	// EnumIsScoped 是否为 C++ enum class -> bool
	EnumIsScoped = "cpp.enum.is_scoped"

	// TypeUnderlying typedef/using 别名指向的底层类型文本 -> string
	TypeUnderlying = "cpp.type.underlying"
	// TypeUnderlyingWithQN typedef/using 底层类型绑定后的 QN -> string
	TypeUnderlyingWithQN = "cpp.type.underlying_with_qn"
	// TypeIsConcept 是否为 C++20 concept（underlying 是约束表达式而非类型，不参与别名展开）-> bool
	TypeIsConcept = "cpp.type.is_concept"

	// MacroName 宏名 -> string
	MacroName = "cpp.macro.name"
	// MacroBody 宏体原文 -> string
	MacroBody = "cpp.macro.body"
	// MacroIsFunctionLike 是否为函数式宏 #define F(x) -> bool
	MacroIsFunctionLike = "cpp.macro.is_function_like"

	// NamespaceName 元素所属命名空间（全局/顶层为空）-> string
	NamespaceName = "cpp.namespace"
	// TemplateParams 模板参数声明原文 -> string
	TemplateParams = "cpp.template.params"
	// Condition 元素所在条件编译分支（如 #ifdef X / #ifndef Y / #else / #elif Z）-> string
	Condition = "cpp.condition"
	// Visibility 成员可见性 (public/private/protected) -> string
	Visibility = "cpp.visibility"
	// IsDeclaration 通用声明标记（前向声明/extern 声明）-> bool
	IsDeclaration = "cpp.is_declaration"

	// NamespaceIsInline inline namespace（C++11）-> bool
	NamespaceIsInline = "cpp.namespace.is_inline"
	// Linkage 元素所在链接规范（extern "C" -> "C"）-> string
	Linkage = "cpp.linkage"
	// ModuleName 文件所属 C++20 模块名（`export module foo;`）-> string
	ModuleName = "cpp.module.name"
	// ModuleIsExport 模块声明是否带 export -> bool
	ModuleIsExport = "cpp.module.is_export"

	// FileLOC 文件有效行数 -> int
	FileLOC = "cpp.file.metrics.loc"
	// FileRawLOC 文件行数 -> int
	FileRawLOC = "cpp.file.metrics.loc_raw"
)

// =============================================================================
// 关系 (Rel) Mores key
// =============================================================================

const (
	// RelRawText 关系原始文本（如 include 路径、函数调用文本）-> string
	RelRawText = "cpp.rel.raw_text"
	// RelAstKind 触发关系的 AST 节点类型 -> string
	RelAstKind = "cpp.rel.ast_kind"
	// RelAssignOperator 赋值运算符 -> string
	RelAssignOperator = "cpp.rel.assign.operator"
	// RelAssignIsInitializer 是否为变量初始化 -> bool
	RelAssignIsInitializer = "cpp.rel.assign.is_initializer"
	// RelIncludeIsSystem 是否为系统/三方头文件 (#include <...>) -> bool
	RelIncludeIsSystem = "cpp.rel.include.is_system"
	// RelImplLinkSignature ImplLink 配对签名 -> string
	RelImplLinkSignature = "cpp.rel.impl_link.signature"
	// RelImportIsModule 是否为 C++20 模块导入（import foo; / import <x>;）-> bool
	RelImportIsModule = "cpp.rel.import.is_module"
	// RelImportIsExport 是否为再导出导入（export import foo;）-> bool
	RelImportIsExport = "cpp.rel.import.is_export"
)

// =============================================================================
// ImportInfo.Attrs 语言专属键
// =============================================================================

const (
	// ImportAttrIsSystem 是否为系统/三方头文件 (#include <...> / import <...>) -> bool
	ImportAttrIsSystem = "cpp.include.is_system"
	// ImportAttrIsModule 是否为 C++20 模块导入（import foo;）-> bool
	ImportAttrIsModule = "cpp.import.is_module"
	// ImportAttrIsExport 是否为再导出导入（export import foo;）-> bool
	ImportAttrIsExport = "cpp.import.is_export"
)

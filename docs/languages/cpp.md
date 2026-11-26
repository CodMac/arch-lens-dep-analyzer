# C/C++ 语言实现边界

> 适用范围：`x/cpp/` · 与 [qn-format.md](../qn-format.md)（QN 契约）、[language-extension.md](../language-extension.md)（SPI 契约）互为补充
>
> 本文档回答「C/C++ 支持到什么程度」：元素/关系覆盖清单、声明-实现配对模型、条件编译策略、实现边界与已知限制。
> 行为契约由 `x/cpp/testdata/e2e/baseline.golden.jsonl`（4404 行 = 1545 元素 + 2859 关系，LevelBalanced）锚定。

## 1. 总体状态

C 与 C++ **共用同一套实现**（`x/cpp/`），仅 Parser 层按扩展名选 grammar。全部 9 个 SPI（Parser / ParserFactory / Collector / Binder / Extractor / Linker / NoiseFilter / SymbolResolver / SymbolBuilder）已实现并经 `x/cpp/init.go` 同时注册为 `core.LangC` 与 `core.LangCPP`。

### 1.1 grammar 选型

`tree-sitter-cpp` 是 `tree-sitter-c` 的**严格超集**（C 独有规则为空集），因此：

| 扩展名 | grammar | 说明 |
|---|---|---|
| `.c` | tree-sitter-c | 纯 C 优先，AST 与 cpp grammar 对纯 C 代码逐节点一致 |
| `.h` | tree-sitter-cpp | 超集，兼容纯 C 头文件与 C++ 头文件 |
| `.cpp` / `.hpp` / `.cc` / `.cxx` | tree-sitter-cpp | C++ |
| 其它 | tree-sitter-cpp | 兜底 |

> CLI 侧 `-lang c` 扫描 `.c`/`.h`，`-lang cpp` 扫描 `.cpp`/`.hpp`/`.cc`/`.cxx`/`.h`（见 `cmd/analyzer/main.go` 的 `langExtensions`）。

### 1.2 处理流水线

| 阶段 | 入口 | 职责 | 详见 |
|---|---|---|---|
| Parser | `x/cpp/cpp_parser.go` + `x/cpp/preproc` | 条件编译双树（投影树 + 原始树）、宏归一化（等长置空）、分支回补、死区标注 | §5 |
| Collector | `x/cpp/collector` | Step 0–6：文件元素 → include/模块导入 → 递归定义（QN 分配 / 匿名承载 / 条件标注 / 控制流声明作用域 / friend）→ 原始树补元素 → 分支回补 → 元数据增强（含 concept / linkage / inline namespace）→ namespace 汇总 → 展示字段 → 动作树根 | §2、§5.2–5.3 |
| Binder | `x/cpp/binder` | raw 类型文本 → QN；typedef/using 别名链按 owner 作用域/文件优先递归展开；多词基本类型归一化 | §6.3 |
| Extractor | `x/cpp/extractor` | 结构关系 + 动作关系 + Capture 派生；多树按字节区间去重 | §3、§5.5 |
| Linker | `x/cpp/linker` | Contain 拓扑 + ImplLink 签名配对 | §4 |
| SymbolResolver | `x/cpp/symbol_resolver` | 两段式：归一化为 `expr` → 作用域链/继承链查找 + 链式类型传播 | §6.2 |
| NoiseFilter | `x/cpp/noise_filter` | 复用 `core.DefaultNoiseFilter` 三态；ImplLink 恒保留 | §8 |

## 2. 元素覆盖（CodeElement）

| 元素 | QN 格式 | 说明 |
|---|---|---|
| File | `相对路径`（正斜杠） | 头文件与源文件各自是独立 File 元素 |
| Namespace | `ns1` / `ns1::ns2` | 嵌套 namespace 逐级拆分，每级独立元素；匿名 namespace 记 `$anon`；**可重开**，同文件同名重开归一为同一 QN（不参与 `$N`） |
| Struct / Class | `ns::类型名` | `struct`/`union` 均产 STRUCT（union 记 `cpp.struct.is_union`）；类内嵌套类型 `Outer::Inner` |
| Enum / EnumConstant | `ns::枚举名` / `ns::枚举名::常量` | `enum class` 记 `cpp.enum.is_scoped=true` |
| Function（定义） | `[ns::]函数名(参数类型列表)` | 有函数体 |
| FunctionDecl（声明） | 同上 | 头文件原型 / 无函数体 declaration |
| Method（定义） | `ns::类名::方法名(参数类型列表)` | 类内定义或类外 `ns::Class::method` 定义 |
| MethodDecl（声明） | 同上 | 类内成员声明 |
| Field | `类QN::字段名` | 含 static / const 字段 |
| Type（typedef / using / concept） | `[ns::]别名` | typedef/using 的底层类型记 `cpp.type.underlying`；与同名 struct tag 以同 QN 不同 Kind 共存。C++20 `concept X = 约束;` 同样归 Type，`cpp.type.underlying` 记约束表达式原文，`cpp.is_declaration=false` + `cpp.type.is_concept=true`（不参与别名展开） |
| Variable | `所在作用域QN::变量名` | 局部变量挂函数/块/Lambda；全局变量挂文件（`cpp.variable.is_global=true`）；控制流语句头部的声明挂语句体块（§2.2.1） |
| Macro | `宏名` | `#define`；函数式宏记 `cpp.macro.is_function_like=true`，宏体原文入 `cpp.macro.body` |
| Lambda | `容器QN::lambda$N` | 形参/局部变量挂其下 |
| ScopeBlock | `容器QN::block$N` | 非函数体/Lambda 体的 `compound_statement`（if/for/switch/catch 体等）；控制流语句头部的声明挂对应语句体块（§2.2.1） |

### 2.1 参数签名规则

参数类型列表是 QN 的一部分（区分重载），规则：

- 仅保留**类型**，不含参数名：`add(int a, int b)` → `add(int,int)`。
- 保留指针/引用/const 语义：`const char* s` → `const char*`；`int& x` → `int&`。
- 数组退化为 `[]`：`int arr[10]` → `int[]`。
- 函数指针参数记 `(*)`：`int (*cb)(int)` → `int(*)`。
- 可变参数记 `...`。
- `f(void)` 等价无参 → `f()`。
- 模板实参**从 QN 中剥离**（`Box<int>::run` → `Box::run`），模板参数原文记 `cpp.template.params`。

### 2.2 匿名 struct / union 的承载规则

匿名 struct/union **不产独立元素**，其字段挂到承载实体下：

| 形态 | 承载实体 | 示例 |
|---|---|---|
| `typedef struct { int x; } Point;` | typedef 名 | `Point::x` |
| `struct { int a; } obj;` | 变量名 | `obj::a` |
| `struct Outer { struct { int a; } pair; };` | 外层字段名 | `Outer::pair::a` |

### 2.2.1 控制流语句作用域

C/C++ 允许在控制流语句头部声明变量，其作用域是**该语句本身**（含语句体），而不是外层函数体。Collector 为这类语句创建/复用**语句体块**（`ScopeBlock`，`容器QN::block$N`），头部声明挂到该块下：

| 形态 | 头部声明归属 | 示例 |
|---|---|---|
| `if (int x = f(); x)` / `if (auto p = g())` | 对应 `if` 体块 | `f()::block$1::x` |
| `for (int i = 0; ...)` / `for (auto& e : xs)` | 对应 `for` 体块（range-for 变量同） | `f()::block$2::i` |
| `while (T* p = next())` / `switch (int k = ...)` | 对应语句体块 | `f()::block$3::p` |
| `try { } catch (const E& e) { }` | 对应 `catch_clause` 体块 | `f()::block$4::e` |

- 块编号由容器内 `#block` 计数确定性推导，与同名普通变量解耦（§2.3.1）。
- 语句体本身若已产 `ScopeBlock`（`compound_statement`），头部声明复用同一块；`else`/`catch` 等 body-like 子节点沿用外层计数，避免块号重排。
- Resolver 的 `scopeChain` 对「位于语句头部（条件/初始化/更新表达式）的节点」补入对应语句体块，使头部表达式中的变量读取解析到该块声明，且不会跨函数误绑同名变量。

### 2.2.2 friend 声明

`friend` 声明**不产独立元素**，而是：

- 在宿主类元素的 Mores 中记录：`cpp.class.friend_types`（`friend class X;` 的类型文本列表）、`cpp.class.friend_functions`（`friend void f(int);` 的 `名(签名)` 列表）。
- 非限定的友元函数声明（`friend void f(Outer&);`）额外注册为**命名空间级 `FunctionDecl`**，使其可被调用解析与 ImplLink 配对。
- Extractor 产出 `FRIEND` 关系：Source = 宿主类，Target = 友元类型 / 友元函数（按命名空间上溯 + 文件内短名解析）。

### 2.3 声明 / 定义双元素

`FunctionDecl`/`MethodDecl` 与 `Function`/`Method` **成对共存**，QN 相同、Kind 不同：

- 全量索引键为 `QN + "|" + Kind + "|" + Path`，两者不互相吞并。
- `FindByQualifiedName` **定义优先**：先注册声明、后注册定义时定义覆盖声明；纯声明（无定义）仍可命中。

### 2.3.1 同名共存与 `$N` 消歧

`$N` 后缀只在「同一文件、同一容器、同一 Kind」内对重名计数，键为 `名字 + 签名 + "|" + Kind`（`assignQN`）。据此：

| 场景 | 是否产生 `$N` | 说明 |
|---|---|---|
| 跨文件同名（`static` 函数、多个示例 `main`、合并副本） | 否 | 靠索引键的 Path 维度共存（见 §2.3.2） |
| 不同 Kind 同名（`typedef struct X {...} X;` 的 STRUCT 与 TYPE） | 否 | QN+Kind 才是元素身份；tag 与 typedef 以同 QN 不同 Kind 共存 |
| 同文件 namespace 重开 | 否 | namespace 恒用裸名（`assignQN` 对 `model.Namespace` 直接返回），子元素前缀不被 `$2` 传染 |
| 同文件同容器同 Kind 重名（条件编译多分支定义、非法重定义） | 是 | 真实多分支定义，语义正确 |
| Lambda / ScopeBlock | 是（独立计数） | 计数键为 `#lambda` / `#block`，与同名普通变量解耦 |

### 2.3.2 全局索引与可见性

C/C++ 的全局索引由 `x/cpp/context.NewDefinitionIndex` 提供（`core.DefinitionIndex` 实现），维护三个索引：

| 索引 | 键 | 用途 |
|---|---|---|
| `qnIndex` | `QN\|Kind\|Path` | 全量去重/共存；声明与定义同键时定义覆盖声明 |
| `primaryIndex` | 裸 `QN` | `FindByQualifiedName` 的 O(1) 查找，定义优先 |
| `overloadIndex` | 不含参数的 QN | `FindOverloadCandidates`，参与 Kind 为 Function/Method/FunctionDecl/MethodDecl |

已知边界：`primaryIndex` / `overloadIndex` 仍以**裸 QN** 为键。当同一 QN 在多个翻译单元出现（`static` 函数、`inline`、匿名 namespace 的 `$anon` 实体）时，`FindByQualifiedName` 只返回其中一个，无法表达 per-TU 可见性。这是索引层的独立设计问题，**不应**用 `$N` 解决（加后缀反而破坏跨文件合法共存）。

per-TU 可见性由 `core.FileVisibility` 提供：`x/cpp/context` 的 `VisibleDefinitions(fromFile)` 返回「本文件 + include 可达文件（精确/后缀匹配）+ 全局作用域定义」的近似视图。宏展开与 include 目录搜索不在建模范围；Resolver 尚未接入（见 §7.5 R-1/R-3）。

### 2.4 展示层三字段（NamespaceQN / EnclosingQN / TypeQN）

Collector 末尾（Step 5，全部定义注册与 namespace 汇总之后）统一填充，仅用于展示，不参与符号解析：

| 字段 | 取值规则 | 示例 |
|---|---|---|
| `NamespaceQN` | 元素所属**最近外层 namespace** 全 QN；无 namespace 为空 | `ns::Outer::Inner::field` → `ns` |
| `EnclosingQN` | 元素**直接词法外层**（即 `ParentQN`） | `ns::Outer::Inner::field` → `ns::Outer::Inner` |
| `TypeQN` | 元素所属**最外层具名宿主类型**（Class/Struct/Enum）；类型元素取自身所在最外层类型 | `ns::Outer::Inner::field` → `ns::Outer` |

关键规则：

- **File 元素三字段恒空**。
- **类型元素**（Class/Struct/Enum）的 `TypeQN` 取自身所在的最外层类型：`ns::L1::L2::L3` → `ns::L1`。
- **函数/方法**的 `TypeQN` 为空（函数不是类型）；自由函数内的局部类型 `TypeQN` 也为空。
- **重开 namespace** 同名即同一实体，不参与 `$N` 消歧：两次 `namespace ns_reopen {}` 都产出 `ns_reopen`，其下子元素前缀保持 `ns_reopen::Second`，因此 `ns_reopen::T` 引用可直接解析、跨重开块的声明/定义可配对。
- **跨文件类外定义**（源文件不写 `namespace {}` 块，仅限定定义）沿父链回退推导：`ool_no_block::Remote::method(int)` → `NamespaceQN=ool_no_block`、`TypeQN=ool_no_block::Remote`。
- **匿名 namespace**（`$anon`）与具名 namespace 同等对待，可作为 `NamespaceQN`。

已知边界：

| # | 边界 | 说明 |
|---|---|---|
| E1 | typedef 承载的匿名 struct | `typedef struct { int x; } Point;` 的字段 `Point::x` 的 `TypeQN` 为空（typedef 不是具名类型 kind） |
| E2 | 变量承载的匿名 struct | `struct { int x; } obj;` 的字段 `obj::x` 的 `TypeQN` 为空 |
| E3 | 跨文件嵌套类型 | 类外定义且本文件不可见父类型链时，`TypeQN` 只能取 QN 结构可判定的层级 |
| E4 | 单段限定定义的宿主歧义 | `GlobalCls::method(int)` 这类单段前缀无法区分「全局类型」与「namespace」，按类型处理（`NamespaceQN` 留空） |
| E5 | 同 QN 跨 Kind 的 TypeQN 查找 | `typedef struct X {...} X;` 的字段 `X::f` 的 `TypeQN=X`，但裸 QN 主索引可能返回 TYPE 而非 STRUCT；消费方判定「是否具名类型」时须按 QN 汇总全部 Kind（展示层不变量测试即如此） |

## 3. 关系覆盖（DependencyRelation）

### 3.1 结构关系

| 关系 | 说明 |
|---|---|
| Import | 文件级 `#include`。`"foo.h"`（项目内）按路径后缀匹配 File 元素；`<stdio.h>`（系统）产外部 File 符号。`cpp.rel.include.is_system` 标记。C++20 `import foo;` / `import <x>;` / `import "x.h";` 同样合成 Import，`cpp.rel.import.is_module=true`；`export import` 另记 `cpp.rel.import.is_export=true` |
| Contain | 层级拓扑：File → 顶层元素、Namespace → 子 Namespace、Namespace → File、类/结构体/枚举 → 成员、函数 → 局部变量/块/Lambda |
| Extend | C++ 继承（`base_class_clause`），基类名按当前命名空间补全后解析 |
| Parameter | 函数/方法形参类型（`cpp.rel.raw_text`） |
| Return | 函数/方法返回类型（`void` 不产） |
| TypeArg | 变量/字段类型 + 模板实参（`Foo<Bar>` 的 `Bar` 各产一条） |
| Friend | 友元声明（Source = 宿主类，Target = 友元类型/函数），见 §2.2.2 |
| ImplLink | 声明-实现链接（定义 → 声明），见 §4 |

### 3.2 动作关系（带 Location）

| 关系 | 触发点 | 说明 |
|---|---|---|
| Call | `call_expression` | 自由函数、`obj.method()`、`ptr->method()`、`Class::staticMethod()`、`ns::func()`、Lambda 变量调用 |
| Create | `new_expression` | C++ `new T(...)`；同时产构造器 CALL |
| Cast | `cast_expression` / `static_cast` 等 | C 风格 `(T)x` 与 `static_cast<T>(x)`；`(void)x` 抑制形态不产 |
| Assign | `assignment_expression` / `init_declarator` / `field_initializer` | 含变量初始化与构造器成员初始化列表 `: a(x)` |
| Use | `identifier` / `field_expression` / `qualified_identifier` | 变量/字段/枚举常量读取 |
| Throw | `throw_statement` | 异常类型（`throw new T` / `throw T(...)` / `throw var`） |
| Capture | 由 Lambda 的 USE/ASSIGN 推导 | 目标为 Lambda 作用域外的局部变量 |

### 3.3 Source 定位

动作关系的 Source 取**包含该节点的最内层可执行元素**（区间最小者）：

- Function / Method / Lambda / ScopeBlock / Field 均可作 Source。
- 局部 Variable **不作 Source**（其初始化表达式归属所在函数）；仅全局 Variable 可作 Source（初始化表达式归属变量自身）。

## 4. 声明-实现链接（ImplLink）

### 4.1 配对模型：签名配对，不靠文件名

真实工程存在多种头↔源形态，故**不按同名 basename 推断**，而是按函数签名配对：

| 形态 | 处理 |
|---|---|
| 1 头 ↔ 1 源（`util.h` ↔ `util.cpp`） | 签名配对自然命中 |
| 1 头 ↔ 多源（`big.h` 声明 5 个函数，分散在多个源文件定义） | 签名配对跨文件命中 |
| `static` 自由函数（内部链接） | **不参与配对** |
| `static` 成员函数（类内声明 + 类外定义） | **参与配对**（外部链接） |
| 同一函数在多个头文件声明 | 定义与**全部**声明分别建链 |
| 只声明无定义（`extern int extFunc()`） | 声明照常产出，**不建 ImplLink** |

### 4.2 签名格式

```
<函数名 QN（不含参数列表）>(<规范化参数类型列表>)
```

例：`add(int,int)`、`app::Box::twice(int)`、`ns::C::operator=(const Widget&)`。

- 函数名 QN 含 namespace / 类前缀，可区分重载与同名；尾部参数列表先剥离，避免与参数列表重复。
- 参数类型以 Binder 的 `cpp.function.parameters_with_qn` 为基（已展开 typedef/using、剥离 `struct/class/union/enum` tag、数组/指针退化），再从原始文本补回 `const`/指针/引用限定，保证 `f(S)` 与 `f(const S&)` 不误并。
- 基本类型等价拼写归一：`unsigned`→`unsigned int`、`signed`→`int`、`long int`→`long`、`unsigned long int`→`unsigned long`、`short int`→`short`。
- 签名写入关系 Mores `cpp.rel.impl_link.signature`。

### 4.3 方向

`ImplLink: Source = 定义（Function/Method）→ Target = 声明（FunctionDecl/MethodDecl）`。

## 5. 条件编译策略

tree-sitter **不做宏展开**，条件编译各分支在 AST 中互斥，且常把语法结构劈裂（`#if` 落在函数体/初始化列表中间）。引擎采用「双树解析 + 分支回补 + 分支标注 + 多树去重」策略：解析与回补在 Parser 层完成（`x/cpp/preproc`），标注与元素合并由 Collector 完成，关系去重由 Extractor 完成。

### 5.0 双树模型（核心）

同一份源码字节解析两棵树，二者共享同一套字节坐标：

| 树 | 来源 | 特点 |
|---|---|---|
| **投影树** `projTree` | `Parse(preproc.Project(content).Source)` | 语法干净（每组只剩一个分支），QN 稳定；**只覆盖一个分支** |
| **原始树** `rawTree` | `Parse(preproc.NormalizeMacros(content))` | 保留 `preproc_ifdef/preproc_else` 节点，**全部分支都在**；可能含 `ERROR` |

- 原始树也做类型注解宏归一化（`NormalizeMacros`），与投影树同口径，否则 `struct GTEST_API_ Foo` 会被解析成函数。
- **投影等长**（`len(proj.Source) == len(content)`）→ 两棵树的所有节点共享同一套 `StartByte/EndByte`，元素合并与关系去重只需比较整数，无需映射表。
- 退化判定 `projDegraded(projTree, rawTree)`：投影后顶层非 namespace 节点最大跨度 > 2×原始 + 500 行时，投影树不可用，改以原始树为主树（`rawTree` 置 nil）。

### 5.1 投影（Parser）

解析前把每组 `#if/#ifdef/#ifndef/#elif/#else` 中**未被选中**的分支按字节替换为空格：

- 投影结果与输入**等长**，Location（行列号）无需映射；AST 节点的字节偏移可直接用于原始源码取文本。
- 指令行本身也置空格，避免 `#` token 进入 AST。
- 未选中分支中的 `#include/#define/#undef/#pragma` 默认保留（`Options.KeepIncludes=true`），保证 IMPORT 关系不因投影丢失；其条件标签由 `Dropped` 区间回填。
- `#if 0` 组整组丢弃（含全部 `#elif/#else` 及组内 include）。
- 分支选择：优先选首个**括号平衡**的分支（避免 `#if` 劈裂的语法片段）；`Options.Branch=N` 可指定统一分支序号（1 起，零值表示按 `Pass` 策略）。
- 续行处理：多行指令（`#if ... \`）与保留的多行宏（`#define M(x) \`）必须整组置空/整组保留，否则悬挂反斜杠会把后续行甚至 `#endif` 吞进宏体。

### 5.2 元素合并（Collector）

Collector 依次遍历三组树根收集元素，`AddDefinition` 按 `QN + Kind + Path` 幂等去重：

1. **主树**（投影树；退化时为原始树）
2. **原始树**（`RawRoot()`，非 nil 时）——补齐嵌套组合与其余分支的元素
3. **分支回补树**（`AltRoots()`，仅当原始树不可用时存在）

原始树本身在语法未被劈裂时就是全分支树（实测 raw 无 ERROR 比例：caffe 93.8%、property_tree 79.5%、cJSON 51.9%、protobuf 45.3%、cpp-httplib 40.9%、DuckX 30.0%），因此**大多数文件无需逐分支再投影**。

- 原始树补元素会引入 `$N` 变体（同一符号在两个分支各出现一次 → QN 消歧），这是真实的多分支定义，语义正确。
- 嵌套组合（`#ifdef OUTER` × `#ifdef INNER` 的 4 种组合）只有原始树能全覆盖；`Branch=N` 是统一序号，无法枚举笛卡尔积。

### 5.2.1 分支回补（Parser，兜底路径）

仅当**原始树不可用**（退化回退，或 `rawTree.HasError()`）时，Parser 才按 `Branch = 2..MaxBranchCount(src)` 逐分支再投影并解析，得到补充树（`SourceFile.AltRoots()`）。

- 必须**多遍**：tree-sitter 一次只解析一棵树，互斥分支无法共存于同一 AST；且同一文件内不同条件组的分支数不同，单遍无法同时覆盖所有组的第 N 分支。
- 代价：分支数为 N 的文件需解析 N 遍。实测 caffe 最大 10 分支、cpp-httplib 最大 23 分支。
- 原始树无 ERROR 时跳过该路径，省掉 N-1 遍解析。

### 5.3 分支标注（Collector）

- 条件区间来自投影结果（`Regions` 选中 + `Dropped` 未选中），元素起始字节落在哪个区间就写 `cpp.condition`（如 `#ifdef FOO` / `#elif defined(BAR)` / `#else`）。
- 嵌套条件取**最内层**标签。
- 同一符号在不同分支下的多个定义会同时出现（QN 相同按 Kind 共存，重名时 `$N` 消歧）。

### 5.4 投影退化回退

投影移除 `#if` 节点后，tree-sitter 的错误恢复可能把后续成千上万行吞进单个节点（典型触发：含 `//` 的字符串出现在 `ASSERT_ANY_THROW(...)` 之类的宏实参里）。Parser 用已解析的原始树做对比：若投影后顶层**非 namespace** 节点的最大跨度 > 2×原始 + 500 行，则**改以原始树为主树**（保留 `preproc_*` 节点，语法结构正确），`rawTree` 置 nil 避免重复遍历。此时条件标签仍由投影区间回填。

### 5.5 动作关系多树提取（Extractor）

`extractActions` 遍历 `FileContext.ActionRoots()` 给出的全部树根（主树 + 原始树 + 回补树），每条关系按 `(RelType, StartByte, EndByte)` 去重：

- **为什么必须去重**：同一动作节点在多棵树中都存在，直接遍历的重复率实测 caffe 64.9%、cpp-httplib 73.2%、DuckX 69.1%、protobuf 48.9%、cJSON 41.0%、property_tree 28.9%。
- **为什么键不含 Source/Target**：同一字节区间在不同树中解析出的 Source/Target 一致（`Definitions()` 已合并、resolver 确定性）；若真不一致，取首棵树结果更稳定。
- **死区**：`#if 0` 区间（`preproc.Result.Dead`）内的动作不产关系；Collector 侧同样跳过死区元素与 include，保证元素与关系口径一致。
- **关系覆盖**：CALL（自由/成员/构造器，含栈对象构造 `T x(args)` / `T x{args}`）、CREATE、CAST、ASSIGN（含构造器初始化列表与指定初始化 `P p{.x=1}`）、USE（含 `using ns::f;` / `using Base::m;` 与 `namespace na = ns;`）、THROW；调用表达式模板实参 `make_shared<Foo>()` 产 `TYPE_ARG Foo`。
- **索引**：`sourceOf` / 声明作用域按文件预构建「按 Start 排序的区间索引」二分定位；项目内 include 按 basename 分桶索引，避免每个动作节点 / include 重复线性扫描全部定义。

### 5.6 `#if 0` 死区

`preproc.Result.Dead` 记录 `#if 0` 组的全部分支区间（`Region.Dead=true`）。原始树会保留死区代码，因此：

- Collector 在 `collectBasicDefinitions` 入口与 `walkIncludes` 中跳过死区节点；
- Extractor 在 `add` 中跳过死区节点。

**判定必须要求「完全包含」**（`start >= r.Start && end <= r.End`），不能只看起始字节：tree-sitter 的错误恢复会在死区边界产出跨区 `ERROR` 节点（起点在死区内、终点在区外，实测可跨 667 行），只看起点会把 `#endif` 之后的整段正常代码误判为死区而丢弃（cpp-httplib `test.cc` 曾因此丢 315 个元素）。

死区代码**不入图**（不产元素、不产关系），也不打 `cpp.condition` 标签——它永远不会被编译，不应参与架构度量。

### 5.7 宏归一化（`preproc.NormalizeMacros`）

tree-sitter 不展开宏，装饰性/注解性宏会破坏语法结构（把类型解析成函数、把函数体解析成文件级块）。投影树与原始树解析前都先做**等长**宏归一化，把宏名/实参按字节置为空格，保证 AST 节点字节坐标仍可映射回原始源码（Collector 从原始源码按字节区间取文本）。

管线（`normalizeTypeMacros`，全部等长）：

1. `typeMacroBeforeRe` / `typeMacroAfterRe`：置空 `class GTEST_API_ Foo`、`struct Foo GTEST_API_` 中的类型注解宏（允许 `:`/`{` 出现在下一行）。**`class`/`struct`/`union`/`enum` 关键字不当类型名**——`enum class E {` 的 `class` 与 `E` 必须保留，否则全大写枚举名（`E`/`COLOR`）会被误判为宏而置空，导致 ENUM 元素丢失。
2. `normalizeProtoMacros`：行首 `MACRO(type) fn(` 的返回类型宏调用改写为 `type` + 等长空格（恢复函数声明）。
3. `normalizeTypeNameFunctionMacros`：`class GTEST_1_TUPLE_(T) {` 置空类型名后的宏实参，保留宏名作类型名。
4. `normalizeStatementMacros`：逐行处理
   - 独占一行的对象式宏（Unity `EXPECT_ABORT_BEGIN`）置空；
   - 行首连续对象式宏 + 内置/限定类型（`GTEST_NO_INLINE_ std::string`）置空；
   - 限定符后的函数式宏（`static GTEST_ADD_REF_(...)`）连同平衡括号置空；
   - 整行单个函数式宏调用且不带分号（`GTEST_DISABLE_MSC_WARNINGS_PUSH_(4180)`）置空；
   - 紧邻 `operator` 之前的对象式宏（`PUGIXML_FUNCTION operator&&`）置空；
   - 函数声明符之后的尾随宏（`f() PUGIXML_NOEXCEPT {`）置空。
5. `normalizeTestMacros`：`TEST(args) {` 改写为等长 `void <首个标识符>() {`，让测试体成为函数作用域而非文件级孤儿块。函数名取 args 首个标识符以保持与原始字节一致；字符串实参（doctest `TEST_CASE("...")`）无法对齐，列为已知限制（§7.1 B8）。

宏名识别：本文件有 `#define` 时用宏表；宏定义在被包含头文件（本文件不可见）时，按「全大写且含下划线」的宏名形态兜底（`isMacroLikeName`）。

## 6. QN 与解析规则

### 6.1 分隔符

- QN 分隔符为 **`::`**（`SymbolBuilder.BuildQualifiedName`，`x/cpp/symbol_builder`）。
- 文件元素 QN 为相对路径（正斜杠），与 Java 一致。

### 6.2 两段式解析

`SymbolResolver` 采用**两段式**架构（Java 为「NodeContextResolver → ExpressionSegmenter → ChainResolver」三段式）：

**段 1 归一化**：`normalize(node, src)` 把任意动作 AST 节点折叠为统一的 `expr` 描述，吸收形态差异。

```go
type expr struct {
    form     exprForm // formIdent / formMember / formScoped / formCall / formNew / formCast / formText
    node     coreast.Node
    recv     *expr    // 递归接收者
    name     string
    typeText string   // new T / (T)x / static_cast<T> 的类型文本
    text     string   // 纯文本接收者（公开 SPI 传入 receiver 字符串时）
    argc     int
}
```

覆盖的 AST 形态：`identifier`（formIdent）、`field_expression`（formMember，`argument` 即 receiver）、`qualified_identifier`（formScoped）、`call_expression`（formCall）、`new_expression`（formNew）、`cast_expression`/`static_cast`（formCast）、`template_function`、`pointer_expression`（`*p` 解引用）、`parenthesized_expression`、`subscript_expression`（`arr[i]`）。

**段 2 查找**：`resolveExpr(gc, fc, e, want)` 只依赖 `expr`，按期望类型分派：

| want | 入口 | 行为 |
|---|---|---|
| `wantValue` | `resolveValue` | 变量/字段/枚举常量 |
| `wantFunc` | `resolveFunc` | 函数/方法（含重载挑选） |
| `wantType` | `typeOf` | 类型符号；链式场景递归 `typeOf(e.recv)` 传播接收者类型 |

查找规则：

1. 短名沿词法作用域链上溯：`ns::Class::method` → `ns::Class` → `ns` → 全局。
2. 类型解析先查精确 QN，再查文件内短名，再查全局短名；相对限定名（`core::Base`）按文件内 namespace 补全。
3. 成员查找沿类型继承链（`cpp.class.superclass`）递归。

`ResolveAction` 缩为「normalize → 按 relType 选 want」的薄分派；`ResolveVar`/`ResolveFunc` 公开 SPI 也构造 `expr` 后走同一查找路径，保证入口归一。

**为什么不需要三段式**：C/C++ 的 AST 已把 receiver 与成员名分开（`call_expression.function` 为 `field_expression`，其 `argument` 即 receiver），段 1 的归一化直接吸收该结构，无需 Java 的「向上爬升取上下文 + 拉平成链」两步；且无 `super`/`Xxx.class`/方法引用/泛型实例化等语法。

### 6.2.1 链式接收者类型传播

链式调用（`f()->g()`、`a.b.c`）在段 2 的 `typeOf` 中递归传播接收者类型，否则会退化为全局短名匹配（同名方法误命中）：

| 接收者形态 | 解析方式 |
|---|---|
| `f()->g()` | 解析 `f` 得到函数元素，取其 `cpp.function.return_type_with_qn`（退回 `return_type`）作为 receiver 类型 |
| `a.b.c` | 递归 `typeOf(e.recv)`，取字段的 `cpp.field.type_with_qn`（退回 `raw_type`） |
| `makeL1(h)->g()` | 同上，自由函数返回值作接收者 |
| `this->f()->g()` | `this` → 所属类，后续段按调用/字段继续传播 |
| `*p` / `(expr)` / `arr[i]` | 归一化阶段剥去解引用/括号/下标后递归 |
| `new T()` | 直接取 `T` |
| 外部类型变量（`std::string s; s.size()`） | 声明类型不在源码中时按外部类型元素归属，产 `std::string::size`（而非退化为 `s::size`） |
| `obj(args)` 可调用对象 | 变量声明类型上的 `operator()` 重载；无匹配则不产 CALL（不误产外部函数） |
| `T(...)` 临时对象 / `throw T(...)` | 被调用名命中类型时按构造器语义：CALL 目标为**真实构造器元素**（按实参个数选重载，QN 含参数列表）；无显式构造器（隐式构造器 / typedef）时不产 CALL。THROW 目标为类型 `T` |
| `[](int v){...}(3)` 立即调用 lambda | 段 1 保留 lambda 节点，段 2 直接命中对应 Lambda 元素 |

解析顺序：段 1 已把接收者折叠为 `expr` 子树，段 2 的 `typeOf` 直接递归该子树；仅当接收者以纯文本形式（`formText`，公开 SPI 传入 `receiver` 字符串）出现时才走文本匹配（`this` / 类型名 / 简单变量）。裸标识符作接收者时**变量优先于同名类型**（`obj.method()` 语义）。

已知边界：接收者为外部类型的方法时，返回类型无法解析（无源码符号），链在此处断开并退回短名兜底。

### 6.3 typedef / using 别名链

- Collector 记录 `cpp.type.underlying`（底层类型文本）。
- Binder 在类型绑定时**递归展开**别名链（最多 8 层防环），再解析为 QN。
- **别名查找按作用域优先级**（`findAliasElem`）：限定名精确 → owner 词法作用域上溯（`Outer::Inner::v` 先查 `Outer::Inner::alias_t`）→ 文件内短名 → 全局短名（**仅唯一候选**）。全局存在多个同名别名时视为歧义、不展开，交给 `ResolveType` 按文件作用域解析。这保证嵌套类遮蔽与跨文件同名 typedef（`value_type`/`size_type`）各按自身作用域展开。
- **模板别名退化保护**：`template<class T> using Vec = T*;` 的底层展开为模板变量 `T`（无意义），此时保留别名自身 QN（`Vec`）而非 `T`。
- **concept 不参与别名展开**：C++20 concept 的 `cpp.type.underlying` 是约束表达式（如 `sizeof(T) <= 4`）而非类型，元素带 `cpp.type.is_concept=true`；Binder 的别名查找与底层类型绑定均跳过 concept，避免同名类型引用被约束表达式污染。
- **匿名承载 typedef 不展开**：`typedef struct {...} Point;` 的底层文本是匿名结构体（`struct { int x; int y; }`），不是可解析的类型名；此时别名本身即目标类型，Binder 直接绑定到 `Point`。否则变量的 `cpp.variable.type_with_qn` 会退化为匿名结构体文本，`p.x` 的接收者类型解析失败后退回全局短名扫描，命中同名参数/变量（结果随并行收集顺序变化而不确定）。
- **多词基本类型**：`unsigned long long` / `long double` / `unsigned int` / `signed char` / `long long` 等由 `IsPrimitive` 按「全部 token ∈ 算术关键字集合」识别为 `PRIMITIVE`，不再落为外部类型；`const`/`volatile` 在 `BaseTypeName` 中剥离。
- 底层类型绑定写入 `cpp.type.underlying_with_qn`（`constants.TypeUnderlyingWithQN`）。

### 6.4 外部符号兜底

未命中源码符号库时：

- 基本类型（`int`/`double`/`size_t`/… 及 `auto`）→ 产 `PRIMITIVE` 元素。
- 模板类型变量（单大写字母 `T`/`E`/`S`/`K`/`V`）→ 不产外部元素。
- 其余 → 外部符号，`NamespaceQN` 按 `::` 拆分取前缀，无 `::` 归 `<external>`。
  不复用 `model.NewExternalElement`（其按点号拆分，C++ QN 会拆错），使用 `x/cpp/helper.NewExternalElement`。

## 7. 已知边界与限制

| # | 边界 | 说明 |
|---|---|---|
| 1 | **运算符重载** | `a + b` 不建模为 `operator+` 调用（已确认的已知限制）。`operator=`/`operator==` 等**成员声明**仍作为 Method/MethodDecl 元素收集 |
| 2 | **宏不展开** | `#define` 只收集宏名+宏体，不参与符号解析；宏调用的语义无法追踪 |
| 3 | **条件编译** | 不做宏环境求解（不展开宏、不判定条件真假）；元素按「投影树 + 原始树 + 回补树」合并覆盖全部分支，动作关系遍历全部树并按字节坐标去重（见 §5.0–§5.6） |
| 4 | **模板实例化** | 不建模具体实例化（`Box<int>` 与 `Box<double>` 共用 `Box` 元素）；QN 中剥离模板实参 |
| 5 | **模板特化** | 显式特化（`template<> class X<int>`）与偏特化不单独建模，与主模板同 QN 时按重名 `$N` 处理 |
| 6 | **`= default` / `= delete`** | 无函数体，Kind 保持定义语义（Method），额外记 `cpp.is_declaration=true` |
| 7 | **函数指针变量** | `int (*fp)(int)` 识别为变量（非函数声明）；作为参数类型记 `(*)` |
| 8 | **预处理指令位置** | `#include` 出现在任意位置（含条件分支内）均被收集为文件级 Import，不区分包含位置 |
| 9 | **多继承** | `base_class_clause` 的多个基类各产一条 EXTEND；不区分 `virtual` 继承语义 |
| 10 | **匿名 namespace** | 记名 `$anon`，不与外部链接语义区分；同文件重开归一为 `$anon`，跨文件各 TU 共享 QN 的解析歧义见 §2.3.2 |
| 11 | **`extern "C"`** | `linkage_specification` 内的元素照常收集，并在 Mores 记 `cpp.linkage="C"`；`inline namespace` 记 `cpp.namespace.is_inline=true`。不做 C/C++ 链接名改写（符号名不变） |
| 12 | **头文件重复包含** | 无 include guard 求解；同一头文件被多个源 include 时各产一条 Import |
| 13 | **外部类型链式断开** | 链式接收者为外部类型（如 `std::vector<T>::begin()`）时，其返回类型无源码符号可查，链在此处断开并退回短名兜底；但**直接**成员调用按外部类型归属（`vec.push_back()` → `std::vector::push_back`） |
| 14 | **可调用对象 / 函数指针** | `obj(args)` 解析到声明类型的 `operator()`；函数指针变量调用不建模为外部函数（无匹配即不产 CALL） |

### 7.1 条件编译的遗留边界

多树方案已修复 B1（Extractor 只遍历首遍 AST）与 B4（跨分支同签名函数的动作丢失）。剩余边界：

| # | 边界 | 现象 | 影响 | 状态 |
|---|---|---|---|---|
| B2 | **原始字符串内的条件指令被误投影** | `R"(...#if 0...#endif...)"` 曾被 `scanGroups` / `blankUnselected` 当作真实条件组，内容被置空 | 原始字符串字面量内容被破坏，可能连带语法错误 | **已修复**：`updateLexState` 跟踪原始字符串状态（含自定义分隔符），其内行首 `#` 不再识别为指令 |
| B3 | **跨分支同名类型/字段被幂等去重** | `#ifdef A struct S{int a;}; #else struct S{int b;};` | 早期只保留首分支版本 | **已修复**：`$N` 消歧使第二分支产 `S$2`（各自带正确 `cpp.condition`）；同名字段产 `x` / `x$2` |
| B5 | **原始树有 ERROR 的文件降级** | 原始树含 `ERROR` 时无法作为全分支树，退回逐分支投影；嵌套组合仍可能漏 | caffe 14 个、protobuf 35 个文件受影响（实测） | 已知代价 |
| B6 | **多遍解析成本** | 原始树有 ERROR 的文件仍需解析 N+1 遍 | 极端头文件（如 cpp-httplib 单文件 23 分支）解析耗时线性放大；raw 无 ERROR 时已跳过 | 已知代价，暂不优化 |
| B7 | **`$N` 元素增量** | 原始树补齐多分支同名符号时产生 `$N` 变体（实测 caffe +16、cpp-httplib +2，其余项目 0） | QN 集合相对单树方案有增量；属真实多分支定义，语义正确 | 已接受 |
| B8 | **字符串实参测试宏** | 形如 doctest `TEST_CASE("...") { ... }` 的宏体仍为文件级块，内部 lambda/局部变量无容器 | 改写为函数需从字符串实参生成函数名，但 Collector 按 AST 字节区间从**原始源码**取文本，生成名无法与原始字节对齐；仅 `TEST`/`TEST_F`/`TEST_P` 等「首参为标识符」的宏可安全改写 | 已知限制 |

### 7.2 已修复的解析缺陷（2026-11-24/25 六项目真实代码审计）

对 caffe / cJSON / cpp-httplib / DuckX / property_tree / protobuf 做 LevelRaw 全文件覆盖审计后修复。按性质分三类：**宏归一化与语法恢复**（返回类型宏、类型注解宏、语句宏、测试宏、operator/尾随宏）、**QN 与索引**（跨文件同名、CRLF、前导 `::`、匿名 enum、`$N` 键、原型形参、namespace 重开）、**关系解析目标**（构造器 CALL、ASSIGN/THROW/CALL 目标、EXTEND 自环、PARAMETER/CONTAIN 端点）。

| 缺陷 | 修复前 | 修复方式 |
|---|---|---|
| **返回类型宏致原型丢失** | `CJSON_PUBLIC(const char*) fn(...)` 被 tree-sitter 当函数声明产出 ERROR，cJSON.h 的 83 个公开 API 原型全丢、125 个伪 VARIABLE | `preproc.normalizeProtoMacros`：把行首 `MACRO(type) fn(` 的宏调用原位改写为 `type` + 等长空格，恢复为普通函数声明 |
| **构造器 CALL 悬空** | 构造器兜底用 `T::T`（无参数列表），与元素 `T::T(...)` 不匹配，caffe 781 / httplib 159 / cJSON 16 条悬空 | 按实参个数在 `gc.FindOverloadCandidates(T::T)` 中选真实构造器；无显式构造器（隐式 / typedef）时不产 CALL |
| **ASSIGN 目标是方法** | `blob->mutable_cpu_data()[i] = v` 解析到方法，caffe 248 条 | 左值解析结果非 Variable/Field 时回退到基对象变量（`assignRoot`） |
| **THROW 目标是方法** | `throw std::runtime_error(...)` 走外部函数兜底产 METHOD | 按被调用者全名解析为类型（外部类型保留 `std::` 前缀） |
| **CALL 目标是类型** | typedef 构造 / 转换被当 CALL，httplib 22 / caffe 12 | 构造器解析仅接受 Class/Struct；CALL 目标为类型时改产 CAST |
| **EXTEND 自环** | 模板偏特化 `struct X : X<T,...>` 产自环 | 基类 QN 与自身相同时跳过 |
| **PARAMETER 目标是 namespace** | 参数类型 QN 命中 namespace | `lookupTypeQN` 不再接受 Namespace |
| **跨文件局部变量误命中** | `std::runtime_error` 的 `std` 被解析为别的文件里的局部变量 `std` | 全局短名兜底仅接受 `cpp.variable.is_global=true` 的变量与枚举常量 |
| **宏语句致 QN 残影** | Unity 的 `EXPECT_ABORT_BEGIN`/`VERIFY_FAILS_END` 独占一行且未展开，C 语法把 `MACRO FN(...)` 解析成声明，QN 含 `;`/`}`/换行（cJSON 102 条） | `preproc.normalizeStatementMacros`：把「整行只有对象式宏名」的行置空，恢复为普通语句/空语句，真实函数得以正确解析 |
| **多行类型注解宏未归一化** | `class GTEST_API_ Foo\n    : public Bar {` 的 `:` 在下一行，旧正则不匹配，`GTEST_API_` 未置空 → 类被解析成跨 2000 行的函数，并派生伪 namespace | 类型宏正则允许 `:`/`{` 出现在下一行（`[ \t]*(?:\r?\n[ \t]*)?`） |
| **行首可见性宏与返回类型粘连** | `GTEST_NO_INLINE_ GTEST_NO_TAIL_CALL_ std::string\nGetCurrent...(...)` 未置空，被解析成带 ERROR 的 qualified_identifier，QN 混入 `\r`（httplib 3 条） | `preproc.blankLeadingObjectMacros`：行首连续对象式宏后紧跟内置类型或 `X::` 限定类型时全部置空；宏定义在被包含的头文件（本文件无 `#define`）时按「全大写且含下划线」的宏名形态识别 |
| **限定符后的函数式返回类型宏** | `static GTEST_ADD_REF_(GTEST_TUPLE_ELEMENT_(0, Tuple))` 的内层宏实参被当参数列表，产出 `(0,,Tuple)` 空参数（caffe 10 条） | `preproc.blankQualifiedFunctionMacro`：`static/inline/...` 后的函数式宏连同平衡括号实参一并置空 |
| **整行函数式宏调用破坏类体** | 类体内 `GTEST_DISABLE_MSC_WARNINGS_PUSH_(4180)` 未展开被当错误声明，类体大括号失配，后续成员被吞进类内（httplib `gtest-printers.h` 的 `PrintToString` 变 VARIABLE） | `preproc.blankStandaloneFunctionMacro`：整行只有一个函数式宏调用且**不带分号**时置空宏名与实参；带分号的语句宏保持原样以保留调用关系 |
| **伪 namespace 悬空 CONTAIN** | 异常 QN 的父级 namespace 不存在 | Linker 仅当父级是真实 Namespace 元素时才建层级 CONTAIN |
| **跨文件同 QN+Kind 丢元素** | 全局索引键为 `QN\|Kind`，`static` 函数、多个示例 `main`、合并副本（protobuf php/ruby upb）跨文件同名时只留第一个；cJSON 29 个 main 只注册 3 个，protobuf 195 个文件只剩 FILE | `core.qnKey` 加入文件路径：`QN\|Kind\|Path`。同文件同 QN+Kind 仍幂等去重；声明/定义（不同 Kind）与跨文件同名定义共存 |
| **CRLF 把 `\r` 写进 QN** | `normalizeQualified` 只剥空格/`\t`/`\n`，CRLF 源文件跨行限定名残留 `\r`（protobuf 3078 个元素/71 文件） | `normalizeQualified` 增加剥 `\r` |
| **前导全局 `::` 进 QN** | 生成代码 `::ns::Type` 产出空首段（protobuf 37 个元素） | `normalizeQualified` 剥前导 `::` |
| **匿名 enum 空名** | `enum : int { A, B };` 的 `type_identifier` 文本为空（非 nil），产出空名 ENUM `ns::` 与 `ns::::A` 常量（protobuf 8 个 ENUM/25 个常量） | `enum_specifier` 归一化后名称为空时按匿名处理：不产 ENUM 元素，常量挂当前容器 |
| **宏+自定义类型返回类型粘连** | `GTEST_API_ AssertionResult AssertionSuccess()`、`PUGI__FN xml_memory_page* compact_get_page()`、`PUGI__FN xml_attribute xml_node::append_attribute()` 的宏未置空，函数名/类名粘连 | `blankLeadingObjectMacros` 增加「宏后形如 类型+声明符(」判定（`looksLikeDeclarator`） |
| **宏紧邻 operator** | `bool PUGIXML_FUNCTION operator&&(...)` → `PUGIXML_FUNCTIONoperator&&` | `blankObjectMacroBeforeOperator`：置空紧邻 `operator` 之前的对象式宏 |
| **函数声明符后尾随宏** | `void f() PUGIXML_NOEXCEPT {` 的尾随宏使整个函数定义失配（pugixml 4 处 body 变孤儿 block） | `blankTrailingMacroAfterParen`：置空 `)` 之后、`{`/`;` 之前的对象式宏 |
| **函数式宏作类型名** | `class GTEST_1_TUPLE_(T) {` 被当函数调用，容器 QN 变形为 `T()` | `normalizeTypeNameFunctionMacros`：置空类型名后的宏实参，保留宏名作类型名 |
| **测试宏体成孤儿块** | `TEST(a,b) { ... }` 的 body 成为文件级 compound_statement，内部 lambda/局部变量失去容器（httplib 99 个 lambda、protobuf 84 个 block） | `normalizeTestMacros`：改写为等长 `void <首个标识符>() {`。函数名取 args 首个标识符以保持与原始源码字节一致（Collector 从原始源码取文本）；字符串实参的 `TEST_CASE("...")` 无法字节对齐，仍保留为孤儿块（已知限制） |
| **block/lambda 序号被同名变量挤占** | 容器内存在名为 `block` 的变量时，作用域块被消歧为 `block$2` | Lambda/ScopeBlock 使用独立计数键 `#block`/`#lambda`，与普通同名元素解耦 |
| **tag 与 typedef 同名跨 Kind 误消歧** | `typedef struct cJSON {...} cJSON;` 的 struct tag 被挤成 `cJSON$2`（cJSON 3 处、httplib 2 处）；下游 `HTTPParser : public http_parser` 的 EXTEND 命中 typedef TYPE | `assignQN` 消歧键加入 Kind：`name+suffix+Kind`。QN+Kind 才是元素身份，跨 Kind 同名不触发 `$N`，tag 与 typedef 以同 QN 不同 Kind 共存 |
| **原型形参幽灵 Variable** | `f(int a, char* b);` 原型的形参被建模为 `f(int,char*)::a` / `::b`（caffe 907、httplib 796、DuckX 294、cJSON 14、property_tree 10） | 纯声明（FunctionDecl/MethodDecl）不下钻参数列表；定义形参照常收集（形参在作用域内） |
| **重开 namespace 被误消歧** | 同文件两次 `namespace ns {}` 产出 `ns` / `ns$2`，子元素前缀被传染为 `ns$2::T`，导致 `ns::T` 引用解析不到、跨重开块的声明/定义无法配对 | `assignQN` 对 `model.Namespace` 恒用裸名、不参与 `$N` 计数；重开块归一为同一 QN（全局索引按 QN+Kind+Path 去重） |
| **`enum class` 全大写名被置空** | `enum class E {` / `enum class COLOR {` 的 `class` 被当类型名、`E`/`COLOR` 被当类型注解宏置空，ENUM 元素与常量全丢 | `typeMacroAfterRe` 的替换回调跳过 `class/struct/union/enum` 关键字（`isTypeKeyword`），保留枚举名 |
| **控制流头部声明挂错作用域** | `if (int x = ...)`、`for (int i = ...)`、`catch (E e)` 的声明挂到函数体（`f()::x`/`f()::i`/`f()::e`），且第二个 `if` 内的读取误绑到第一个 `x` | Collector 新增 `collectControlFlowScope`：头部声明挂语句体块（`block$N`）；Resolver `scopeChain` 对语句头部节点补入对应块，避免跨语句/跨函数误绑 |
| **C++20 concept 不产元素** | `concept Small = ...;` 无任何元素，约束关系不可见 | `concept_definition` 归 Type：`cpp.type.underlying` 记约束表达式，`cpp.is_declaration=false` |
| **friend 声明被整体跳过** | `friend class X;` / `friend void f(T&);` 无元素、无关系，友元授权不可见 | 宿主类 Mores 记 `cpp.class.friend_types` / `cpp.class.friend_functions`；非限定友元函数注册为命名空间级 `FunctionDecl`；Extractor 产 `FRIEND` 关系 |
| **C++20 module/import 产伪 VARIABLE** | `export module foo;` / `import bar;` 被 tree-sitter 当普通 declaration，产出 `module`/`bar` 伪 VARIABLE，且无 IMPORT | Collector 按源码文本拦截顶层 module/import：不产元素；模块名记 File 的 `cpp.module.name`；`import` 合成文件级 Import（`cpp.rel.import.is_module`，`export import` 另记 `is_export`） |
| **原始字符串内条件指令被误投影** | `R"(...#if 0...#endif...)"` 的内容被置空 | `scanGroups` 与 `blankUnselected` 都跟踪原始字符串状态（含自定义分隔符），其内行首 `#` 不再识别为指令（B2） |
| **缺少链接规范 / inline namespace 元数据** | `extern "C"` 与 `inline namespace` 无任何标记 | `enrichMetadata` 沿父链记录 `cpp.linkage="C"`；namespace 记 `cpp.namespace.is_inline=true` |
| **单大写字母真实类型被当模板变量** | `struct T` 的 `T` 参数/继承/返回全丢（`IsTypeVariable` 提前短路） | `ResolveType` 先查**本文件**真实类型，未命中才按模板变量返回 nil；Binder 单字母不做全局短名扫描；`lookupTypeQN` 校验元素 Path 属当前文件 |
| **命名参数把 `类型 名` 整体当 QN** | `void f(AA x)` 的 `parameters_with_qn=["YY x"]` 查不到 → 回退外层 `O::AA` | Extractor 参数查找用 `paramTypeOnly` 剥离参数名后再查 |
| **`case` 标签枚举常量不产 USE** | `case RED:` 无 USE（仅 `Color::GREEN` 有） | `isUsePosition` 不再排除 `case_statement` |
| **typedef/using 底层类型无依赖** | `using Alias = Base;` 无任何类型关系 | `extractTypeArgRels` 增加 `model.Type` 分支（跳过 concept 与匿名 struct 承载、自引用） |
| **栈对象构造不产 CALL** | `T x(args)` / `T x{args}` 只有 ASSIGN | `handleInit` 对 `argument_list`/`initializer_list` 按实参个数解析构造器并产 CALL |
| **指定初始化无字段 ASSIGN** | `P p{.x=1}` 只产变量 ASSIGN | 新增 `handleDesignatedInit`：`initializer_pair` 的 `field_designator` 解析为字段并产 ASSIGN |
| **using 声明 / 命名空间别名无关系** | `using ns::f;` / `using Base::m;` / `namespace na = ns;` 均无产出 | 新增 `handleUsingDecl` / `handleNamespaceAlias`：按所属作用域（含 File 回退）产 USE |
| **调用表达式模板实参无 TypeArg** | `make_shared<Foo>()` 不产 `TYPE_ARG Foo` | `handleCall` 解析 `template_function` 的 `template_argument_list` 产 TYPE_ARG |
| **O(defs) 线性扫描** | `sourceOf` / 声明作用域每个动作节点扫全部定义；`findFileByInclude` 每个 include 扫全部定义 | 预构建按 Start 排序的区间索引 + basename 分桶 include 索引 |

### 7.3 Binder 阶段的已知边界（2026-11-25 审计）

Binder 负责 raw 类型文本 → QN。以下缺陷均经探针实测，按优先级记录：

| # | 优先级 | 边界 | 实测现象 | 状态 |
|---|---|---|---|---|
| B-1 | P1 | **别名展开不感知 owner 作用域** | `Outer{using alias_t=int; Inner{using alias_t=float; alias_t v;}}` → `Outer::Inner::v` 的 `type_with_qn=int`（应为 float） | **已修复**：`findAliasElem` 按 owner 词法作用域上溯 |
| B-2 | P1 | **跨文件同名别名全局扁平** | `a.cpp: typedef int value_type;` + `b.cpp: typedef float value_type;` → `A::v` 与 `B::v` 的 `type_with_qn` 都是 int | **已修复**：文件短名优先；全局短名仅唯一候选时使用 |
| B-3 | P1 | **多词基本类型未识别** | `unsigned long long` / `long double` / `unsigned int` / `signed char` / `long long` 落为外部 CLASS 符号（LevelRaw 下 PARAMETER/TYPE_ARG 目标错误） | **已修复**：`IsPrimitive` 支持多词组合；`BaseTypeName` 剥 `volatile` |
| B-4 | P2 | **模板别名展开退化** | `template<class T> using Vec = T*;` 的 `Vec<int> v` → `type_with_qn="T"`（无意义模板变量） | **已修复**：展开结果为模板变量时保留别名自身 QN |
| B-5 | P2 | **函数指针 typedef 丢形态** | `typedef void (*cb_t)(int);` 的 underlying 只记 `void`，`cb_t` 展开为 `void` | 已知限制，记录不修 |
| B-6 | P2 | **元数据键魔法字符串** | `constants.TypeUnderlying+"_with_qn"` 为裸拼接且无消费方 | **已修复**：新增 `constants.TypeUnderlyingWithQN` |
| B-7 | 边界 | **无 include 可见性** | 别名查找全局生效，不检查 `#include` | 与 §2.3.2 per-TU 可见性同源，索引层设计边界 |
| B-8 | 边界 | **`isTypeKind` 接受 Namespace** | 类型名与 namespace 同名时绑定到 namespace | 极罕见，记录不修 |

> 残留：`TypeArg` 关系由 Extractor 直接调用 `ResolveType` 产出，不带 owner 上下文，嵌套类同名别名的关系目标仍可能命中外层（`type_with_qn` 已正确）。属 Resolver 侧问题，记录待后续处理。

> `concept` 的约束表达式曾进入别名表污染同名类型引用，已修复：元素带 `cpp.type.is_concept=true`，Binder 的别名表与底层类型绑定均跳过（见 §6.3）。

### 7.4 Linker 阶段的已知边界（2026-11-25 审计）

| # | 边界 | 说明 |
|---|---|---|
| L-1 | const/ref 限定符重载坍缩 | QN 不含 `const`/`&` 限定符，`int q() const;` 与 `int q();` 同 QN+Kind+Path 被全局索引去重，只保留一个元素/一条 ImplLink。属 Collector/QN 层问题（声明路径不做消歧、类外定义走限定名早返回），修复需改 QN 契约，记录待后续 |
| L-2 | 链接规范不参与签名 | `extern "C"` 与 C++ 链接、匿名 namespace 内部链接的同名同参函数按签名合并；同一程序内混用属 ODR 违规，极罕见 |
| L-3 | 无 OVERRIDE 关系 | `model.OVERRIDE` 全项目未产出；C++ 虚函数覆盖需 Collector 建模 `virtual`/`override`，非 Linker 单层可解 |
| L-4 | ImplLink 无 Location | 仅记签名，定位由 Source/Target 元素承载 |

### 7.5 Resolver 阶段的已知 Gap（2026-11-25 审计，待修复）

| # | 优先级 | Gap | 实测证据 |
|---|---|---|---|
| R-1 | ~~P1~~ 已修复 | **ResolveType 无使用作用域** | **修复**：新增 `core.ScopedTypeResolver.ResolveTypeAt`，非限定类型先沿词法作用域链查找；Extractor 结构/动作路径与 Resolver 的 `typeOf`/`throw` 均改用它。实测 `B` 内 `D : Foo`、`new Foo()`、`(Foo*)p`、`throw Foo()` 全部命中 `B::Foo`（`TestResolverCppScopedType`） |
| R-2 | ~~P1~~ 已修复 | **`this->` 不按宿主类型解析** | **修复**：`typeOf` 对 `this` 返回宿主类型（类内取区间包含类，类外定义按作用域链上溯），成员查找沿继承链；`this->baseM()`/`this->baseFld` 命中 `Base::*` 而非同文件同名 `X::*`（`TestResolverCppThisPtr`） |
| R-3 | ~~P1~~ 已修复 | **接收者类型未知时忽略接收者回退非限定查找** | **修复**：`resolveFunc`/`resolveValue` 在接收者存在但类型未知时不再回退非限定查找（返回 nil，由降噪层过滤），避免 `zz.freeFn(1)` 误命中全局 `freeFn(int)`、`zz.baseField` 误命中 `Base::baseField`；命名空间接收者的成员可能位于 inline namespace，保留作用域回退（`TestResolverCppUnknownReceiver`） |
| R-4 | ~~P2~~ 已修复 | **returnTypeOf 不回退外部类型** | **修复**：返回类型在源码内查不到时回退为外部类型元素（跳过基本类型/模板变量），`makeName().size()` 命中 `std::string::size`；源码内返回类型仍优先（`TestResolverCppReturnExternalChain`） |
| R-5 | P2 | **using 声明 / namespace 别名不参与解析** | `using ns2::f; f(1)` → `ns1::f`；`using ns2::v; v=2` → `ns1::v`；`namespace na=ns1; na::f()` 靠短名顺序碰巧正确 |
| R-6 | P2 | **智能指针/包装类型不解包** | `std::unique_ptr<Svc> p; p->run()` → `std::unique_ptr::run`（外部方法） |
| R-7 | P2 | **auto/decltype 不推导** | `auto s = make(); s.run()` 靠文件短名唯一碰巧命中 `Svc::run`，同名方法存在时误命中 |
| R-8 | P3 | **typeOf(formMember) 用成员名查类型** | 接收者未知的 `obj.field` 回退 `lookupType("field")`，可能把成员名当类型 |
| R-9 | P3 | **lookupMember 的 want 参数未使用** | 死参数 |
| R-10 | P3 | **全局短名回退 O(defs) 且取首个** | 跨文件同名类型/函数结果依赖收集顺序；`lookupScopedFunc` 全局回退忽略实参个数 |
| R-11 | 边界 | 函数指针/成员函数指针/delete/虚派发 | `fp(1)` 无 CALL；`(s->*pm)(1)` 产畸形外部 CALL `s->*`；`delete p` 无关系；虚函数按静态类型解析 |
| R-12 | P3 | Resolver 专用测试缺上述边界 | 现有 calls/create/use/assign/cast/chain/forms/overload/inherit/scope/external/lambda/throw 未覆盖 this->、作用域化类型、using/别名、外部返回链、未知接收者 |

## 8. 与 SPI 契约的对应

| SPI | C/C++ 实现 | 说明 |
|---|---|---|
| Parser / ParserFactory | `x/cpp/cpp_parser.go` | 按扩展名选 grammar（`languageForPath`） |
| Collector | `x/cpp/collector/` | Step 0–6 收集 + 条件栈 + 匿名 struct 承载 + 元数据增强 + 展示字段填充 |
| Binder | `x/cpp/binder/` | raw 类型 → QN，typedef 别名链按作用域展开，多词基本类型识别 |
| Extractor | `x/cpp/extractor/` | 结构 + 动作 + Capture 派生 |
| Linker | `x/cpp/linker/` | Contain 拓扑 + ImplLink 签名配对 |
| NoiseFilter | `x/cpp/noise_filter/` | 复用 `core.DefaultNoiseFilter` 三态；ImplLink 恒保留 |
| SymbolResolver | `x/cpp/symbol_resolver/` | 两段式：段 1 `normalize` 归一化为 `expr` + 段 2 `resolveExpr` 作用域链/继承链查找 + 链式接收者类型传播 |
| SymbolBuilder | `x/cpp/symbol_builder/` | QN 用 `::` 拼接 |
| FileContext | `x/cpp/context/` | 内嵌 `core.BaseFileContext` + 实现 `cppctx.NamespaceAware`（`Namespaces()`）+ `ActionRoots()`（主树/原始树/回补树 + 死区） |
| GlobalContext | `x/cpp/context/` | 内嵌 `core.BaseGlobalContext`；NAMESPACE 由 Collector 建元素；全量索引 `QN\|Kind\|Path`、主索引裸 QN 定义优先、重载索引收 Function 族 |

## 9. 测试现状

- 三层测试 + e2e 黄金回归集，全部位于 `x/cpp/test/`。
- 测试文件按语言归属拆分：`*_base_test.go`（C/C++ 共享）、`*_c_test.go`（C 独有）、`*_cpp_test.go`（C++ 独有）；共享断言集中在 `assert_helper.go`。
- e2e 基线：`x/cpp/testdata/e2e/baseline.golden.jsonl`（1710 元素 + 3209 关系，LevelBalanced）。
- 元素覆盖（基线实测）：FILE 117 / NAMESPACE 62 / CLASS 30 / STRUCT 145 / ENUM 22 / ENUM_CONSTANT 49 / TYPE 42 / MACRO 53 / FIELD 206 / FUNCTION 171 / FUNCTION_DECL 66 / METHOD 89 / METHOD_DECL 99 / VARIABLE 492 / SCOPE_BLOCK 39 / LAMBDA 28。
- 关系覆盖（基线实测）：CONTAIN 2077 / USE 384 / ASSIGN 193 / TYPE_ARG 132 / CALL 94 / IMPL_LINK 89 / PARAMETER 73 / IMPORT 45 / RETURN 37 / EXTEND 29 / CAPTURE 24 / CAST 15 / CREATE 8 / THROW 7 / FRIEND 2。
- 基线（LevelBalanced）中 PARAMETER/RETURN 大幅减少，是因为当前样例的参数/返回类型多为基本类型（`int`/`void`），被外部符号降噪过滤；CONTAIN/USE/ASSIGN 等指向源码符号的关系不受影响。
- 测试用例：
  | 测试 | 覆盖 |
  |---|---|
  | `TestCollectorC` | C 函数/声明/结构体/枚举/typedef/宏/全局与局部变量/匿名 struct 承载 |
  | `TestCollectorCBase` | 基准元素：文件/函数+原型/全局变量/结构体/枚举/typedef/宏 |
  | `TestCollectorCFunc` | 函数族：`(void)`/`()`/可变参数/static/inline/递归/函数指针返回/数组指针返回 |
  | `TestCollectorCDeclDef` | 同文件声明+定义共享 QN、前向声明覆盖、extern 声明+定义、头文件原型 |
  | `TestCollectorCStruct` | 具名/嵌套/位域/匿名 union 成员/匿名 struct 多声明器/union |
  | `TestCollectorCEnum` | 具名/显式值/匿名常量挂文件级/前向声明/枚举类型变量 |
  | `TestCollectorCTypedef` | 基础/指针/数组/函数指针别名、多声明器、链式、匿名 struct 承载 |
  | `TestCollectorCMacro` | 对象宏/函数式宏/空宏/多行宏/条件分支同名宏 |
  | `TestCollectorCVariable` | 全局/static/const/局部/块作用域/多声明器/混合声明器 |
  | `TestCollectorCParam` | 具名/无名指针/多级指针/数组/函数指针/const/struct+enum 关键字 |
  | `TestCollectorCConditional` | `#ifdef`/`#ifndef`/`#if`/`#elif`/`#else`/嵌套分支标注 |
  | `TestCollectorCEdge` | 类型引用负例/块内重名/重载式命名/无名指针参数/嵌套结构体 |
  | `TestCollectorCpp` | namespace/class/继承/模板/纯虚/访问级别/enum class/类外定义 |
  | `TestCollectorCppBase` | 基准元素：16 种 ElementKind 冒烟（File/Namespace/Class/Struct/Enum/EnumConstant/Function/FunctionDecl/Method/MethodDecl/Field/Type/Variable/Macro/Lambda/ScopeBlock） |
  | `TestCollectorCppNamespace` | 嵌套 namespace / C++17 `a::b` / inline / 匿名 `$anon` / alias / using / 重开归一（`reopen_ns` 无 `$2`） |
  | `TestCollectorCppModule` | C++20 `export module` 记 File Mores、`import` 不产伪 VARIABLE |
  | `TestCollectorCppLinkage` | `extern "C"` 元素记 `cpp.linkage`、`inline namespace` 记 `cpp.namespace.is_inline` |
  | `TestCollectorCppDeclDef` | 同文件声明+定义、头文件原型、前向声明覆盖、类外定义、extern 声明+定义、只声明不定义、原型形参不下钻 |
  | `TestCollectorCppClass` | 访问级别、嵌套 class/struct/enum、friend、static/const/mutable 成员、final、抽象类 |
  | `TestCollectorCppCtorDtor` | 默认/带参/explicit/拷贝/移动/双参构造、析构、`= default`/`= delete`、委托构造、成员初始化列表 |
  | `TestCollectorCppOperator` | 算术/比较/赋值/下标/调用/自增自减/转换运算符/内存分配/逻辑位运算、成员与非成员 |
  | `TestCollectorCppTemplate` | 类/函数模板、可变参数、默认模板参数、全特化/偏特化（QN 剥离实参）、别名模板、concept |
  | `TestCollectorCppInherit` | 单/多继承、virtual 继承、菱形、private/protected 继承、override/final |
  | `TestCollectorCppEnum` | 普通 enum / enum class / 指定底层类型 / 前向声明 / 匿名 enum / 显式值 |
  | `TestCollectorCppTypedefUsing` | typedef 基础/指针/引用/数组/函数指针/多声明器/链式、using、别名模板、匿名 struct 承载、具名 tag 与 typedef 同名共存 |
  | `TestCollectorCppVariable` | 全局/static/const/constexpr/thread_local/extern/alignas、auto、引用/右值引用、指针、数组、结构化绑定、多声明器 |
  | `TestCollectorCppFunction` | 自由函数、可变参数、noexcept、constexpr、尾置返回、默认实参、重载组、static/inline、extern "C" |
  | `TestCollectorCppParam` | 值/指针/多级指针/左值引用/右值引用/const/数组/函数指针/默认值/可变参数/无名参数 |
  | `TestCollectorCppLambda` | 无捕获/值捕获/引用捕获/全捕获、generic、mutable、嵌套、作字段/实参/返回值 |
  | `TestCollectorCppScope` | 裸 block / if / else / for / range-for / while / do / switch case / try-catch / 嵌套 block / 块内重名 / 局部类型 |
  | `TestCollectorCppMacro` | 对象宏/函数式宏/可变参数宏/多行宏/空宏/字符串化/连接/条件分支同名宏/#undef |
  | `TestCollectorCppConditional` | `#if`/`#ifdef`/`#ifndef`/`#elif`/`#else`/嵌套取最内层/`#pragma` |
  | `TestConditionalNestedActions` | 嵌套 4 组合分支：4 个函数各产 1 条 CALL（原始树补齐） |
  | `TestConditionalNestedConditions` | 嵌套 4 组合的 `cpp.condition` 标签（`#ifdef INNER` / `#else`） |
  | `TestConditionalDeadZone` | `#if 0` 死区：`dead_fn()` 及 `dead_call` 关系不入图 |
  | `TestConditionalDeadZoneSpan` | 跨死区 ERROR 节点：死区判定须完全包含，`#endif` 后代码不得被吞 |
  | `TestConditionalNoDuplicateActions` | 多树遍历去重：`g()` 内 CALL 恰为 2 条，全局无重复关系 |
  | `TestCollectorCppEdge` | 重名消歧、匿名 struct/union 承载、类型引用负例、前向声明、空 struct/函数、无名参数 |
  | `TestCollectorConditional` | 条件编译分支标注 |
  | `TestExtractorStructural` | Import/Contain/Extend/Parameter/Return/TypeArg（基础样例） |
  | `TestExtractorActions` | Call/Assign/Use/Cast（基础样例） |
  | `TestExtractorImplLink` | 跨文件签名配对、static 不配对、类外定义配对（基础样例） |
  | `TestExtractorContainNamespace` | namespace 层级拓扑 |
  | `TestExtractorCppStructural` | 全形态：Import（项目内/系统/嵌套路径）、Contain（File/Namespace/类/函数 + 去重）、Extend（单/多继承）、Parameter/Return（源码类型 + void 不产）、TypeArg（字段/局部/全局 + 模板实参 + 基本类型不产） |
  | `TestExtractorCppActions` | 全形态：Call（自由/重载/命名空间限定/成员/指针成员/静态/const/operator()）、Create+构造器 CALL、Cast（C 风格 + 四种显式转换 + (void) 抑制）、Assign（初始化/赋值/字段/复合 + Mores）、Use（字段/变量）、Throw（new/T(...)/var 三形态） |
  | `TestExtractorCppCapture` | 值捕获/引用捕获/全捕获/嵌套 lambda/赋值推导/无捕获不产/lambda 内部变量不捕获/去重 |
  | `TestExtractorCppImplLink` | 1 头多源、签名、类外定义、static 不配对、只声明不配对、类内 inline 不配对 |
  | `TestExtractorCppFriend` | `friend class` / `friend` 函数 → FRIEND 关系（宿主类 → 友元类型/函数） |
  | `TestExtractorCppModule` | C++20 模块导入合成 IMPORT（`cpp.rel.import.is_module` / `is_export`） |
  | `TestExtractorCppStackCtorCall` | 栈对象构造 `T x(args)` / `T x{args}` → 构造器 CALL（按实参个数选重载） |
  | `TestExtractorCppDesignatedInit` | 指定初始化 `P p{.x=1}` → 字段 ASSIGN |
  | `TestExtractorCppUsingDecl` | `using ns::f;` / `using Base::m;` / `namespace na = ns;` → USE |
  | `TestExtractorCppCallTemplateArg` | 调用表达式模板实参 `make_shared<Foo>()` → `TYPE_ARG Foo` |
  | `TestExtractorCppImplLinkSignature` | ImplLink 签名规范化：typedef 别名 / struct tag / 数组↔指针 / 基本类型拼写 / const&指针重载区分 |
  | `TestExtractorCppImplLinkMultiDecl` | 同一函数多声明全部建 ImplLink |
  | `TestExtractorCppImplLinkStatic` | static 成员函数配对、static 自由函数不配对 |
  | `TestExtractorCppNestedNamespace` | C++17 嵌套 namespace 中间层级元素与边、无悬空 Namespace→File |
  | `TestExtractorCppOutOfLineFileRoot` | 类外/跨文件定义产 File→元素 边 |
  | `TestLinkerCppTopology` | 文件/命名空间拓扑：File→顶层元素、Namespace→SubNamespace/File、类外定义文件根、类内成员非文件子节点 |
  | `TestLinkerCppImplLinkDirection` | ImplLink 方向定义→声明 |
  | `TestResolverCall` | 自由函数/成员/指针成员/静态/命名空间限定调用 |
  | `TestResolverCreate` | `new` 表达式 + 构造器 CALL |
  | `TestResolverUse` / `TestResolverAssign` | 变量/字段/枚举常量读写 |
  | `TestResolverCast` | C 风格强转 + `static_cast` |
  | `TestResolverFieldInitializer` | 构造器成员初始化列表 |
  | `TestResolverCapture` | Lambda 捕获 |
  | `TestResolverAnonymousTypedefField` | 匿名 struct 承载 typedef 的字段解析（`typedef struct{...} Point` 的 `p.x` 绑定到 `Point::x`） |
  | `TestResolverChain` | 链式接收者类型传播：`f()->g()` / `a.b.c` / `this->f()->g()` / 自由函数返回值 / 同名方法区分 |
  | `TestResolverCppForms` | 段 1 六种 exprForm：formIdent/formMember/formScoped/formCall/formNew/formCast |
  | `TestResolverCppOverload` | 按实参个数挑选重载（自由函数 + 成员方法，0/1/2 参） |
  | `TestResolverCppInherit` | 继承链查找：直接成员/一级基类/二级基类（菱形）/覆盖命中派生类 |
  | `TestResolverCppScope` | 相对限定名补全、短名作用域上溯、嵌套类成员、块内遮蔽 |
  | `TestResolverCppExternal` | 外部类型方法归属（`std::string::size`）、模板变量不产依赖、基本类型 PRIMITIVE、外部类型 NamespaceQN |
  | `TestCollectorCppDisplayFields` | 展示层三字段：namespace 层级、嵌套类型 TypeQN、自由函数无宿主、跨文件类外定义、File 三字段空 |
  | `TestCollectorCppDisplayFieldsEdge` | 展示层三字段边界：三层 namespace / C++17 嵌套 / inline / 匿名 / 重开归一、三层嵌套类型、union/enum、模板嵌套、typedef 与匿名承载、局部类型（方法内/自由函数/块/lambda/局部套局部）、跨文件类外定义（有/无 namespace 块）、纯 C、`extern "C"`、运算符、`= default` |
  | `TestCollectorCppDisplayFieldsInvariant` | 横切不变量（遍历全部 collector 样例 804 元素 / 35 文件）：File 三字段空、NamespaceQN 必为真实 namespace、EnclosingQN == ParentQN（namespace 侧剥离 `$N`）、TypeQN 必为具名类型（同 QN 跨 Kind 时按全部 Kind 判定）且是展示层 QN 的段边界前缀、TypeQN 不得是函数 QN |
  | `TestCollectorCppCrossFileSameQN` | 跨文件同 QN+Kind 共存（`dup_files/a.cpp` 与 `b.cpp` 各定义 `main()`），全局索引按 Path 维度保留两者 |
  | `TestResolverCppLambdaVar` | lambda 变量调用、立即调用 lambda |
  | `TestResolverCppCast` | C 风格 + static/dynamic/const/reinterpret_cast、`(void)` 抑制 |
  | `TestResolverCppThrow` | throw new T / throw T(...) / throw var |
  | `TestBinderCppConceptNotAlias` | concept 不进别名表、不产 `underlying_with_qn`；同名类型引用不被约束表达式污染 |
  | `TestBinderCppAliasScope` | 嵌套类同名别名遮蔽、跨文件同名 typedef 各按本文件展开、模板别名退化保留自身 QN |
  | `TestBinderCppMultiWordPrimitive` | 多词基本类型（`unsigned long long` 等）识别为 PRIMITIVE，`volatile` 剥离 |

- 包级单元测试（不依赖完整流水线）：
  - `core/global_context_test.go`：`TestGlobalContextCrossFileSameQN`（跨文件同 QN+Kind 共存）、`TestGlobalContextSameFileSameQN`（同文件同键幂等）、`TestGlobalContextDeclDefSameFile`（声明/定义同键时定义覆盖）。
  - `x/cpp/collector/normalize_test.go`：`TestNormalizeQualifiedCRLF`（剥 `\r`）、`TestNormalizeQualifiedLeadingGlobal`（剥前导 `::`）。
  - `x/cpp/preproc/projector_test.go`：投影/宏归一化各形态（类型注解宏、返回类型宏、语句宏、测试宏、函数式宏作类型名、operator/尾随宏等）；原始字符串内的 `#if 0` 不被识别为指令（B2，含自定义分隔符）。

## 10. 测试命令

```bash
go test ./x/cpp/...                                  # 全部 C/C++ 测试
go test ./x/cpp/test -run TestE2EGolden -update      # 刷新黄金基线
go run ./cmd/analyzer -lang cpp -path <dir> -out-dir <out>
go run ./cmd/analyzer -lang c   -path <dir> -out-dir <out>
```

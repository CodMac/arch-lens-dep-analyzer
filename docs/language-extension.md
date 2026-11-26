# 语言扩展 SPI 设计

> 适用版本：当前主干

## 1. 概述

引擎采用**全局注册表 + init() 自动注册**的语言扩展模型。core 定义契约（SPI 接口），各语言实现契约并通过 `init()` 注册，引擎通过注册表按 `Language` 分派。

```
新语言 x/go
  ├─ 实现全部 SPI（collector/binder/extractor/...）
  └─ init() 注册 → import _ ".../x/go" 即接入
```

## 2. 核心注册表

```go
// core/language.go
type Language string

const (
    LangJava Language = "java"  // 已实现
    LangGo   Language = "go"    // 已声明，尚未实现（见 roadmap.md）
    LangC    Language = "c"     // 已实现（x/cpp 共用实现）
    LangCPP  Language = "cpp"   // 已实现（x/cpp 共用实现）
)

// 全局注册表（各语言 init() 调用）
func Register(impl *LanguageImpl)  // 注册语言实现（重复注册覆盖）
func Get(lang Language) (*LanguageImpl, error) // 获取（未注册则报错）

// LanguageImpl 聚合一个语言的全部 SPI 实现
type LanguageImpl struct {
    Language       Language
    Parser         ParserFactory     // 创建语言解析器
    Collector      Collector
    Binder         Binder
    Extractor      Extractor
    Linker         Linker
    NoiseFilter    NoiseFilter
    SymbolResolver SymbolResolver
    SymbolBuilder  SymbolBuilder
}
```

**设计要点**：各语言聚合为单一 `LanguageImpl`，一次注册全部能力，结构清晰。

## 3. SPI 接口清单

| 接口 | 方法 | 职责 |
|---|---|---|
| `ParserFactory` | `NewParser() Parser` | 创建语言解析器 |
| `Parser` | `ParseFile(path) (ast.SourceFile, error)` + `Close()` | 解析源文件为 AST |
| `Collector` | `CollectDefinitions(src ast.SourceFile) (FileContext, error)` | 收集符号定义 |
| `Binder` | `BindSymbols(ctx GlobalContext) error` | 符号绑定 |
| `Extractor` | `Extract(relPath string, ctx GlobalContext) ([]*model.DependencyRelation, error)` | 提取关系 |
| `Linker` | `LinkHierarchy(ctx GlobalContext) []*model.DependencyRelation` | 层级拓扑 |
| `NoiseFilter` | `SetLevel(FilterLevel)` + `IsNoise(rel) bool` | 降噪 |
| `SymbolResolver` | `BuildQualifiedName` + `IsPrimitive` + `ResolveType` + `ResolveFunc` + `ResolveVar` + `ResolveAction` | 符号解析 |
| `SymbolBuilder` | `BuildQualifiedName(parentQN, name) string` + `IsPrimitive(typeName) bool` | QN 构建 |

> **上下文分层（2026-11-06 接口化 / 2026-11-09 语言实现下沉）**：`FileContext` / `GlobalContext` 均为**接口**，SPI 签名只使用接口类型。通用索引/锁由 `core.BaseFileContext` / `core.BaseGlobalContext` 提供；**语言专属语义（包/命名空间注册、重载索引 kind、命名空间列表）由 `x/{lang}/context` 实现**，core 不含语言硬编码。详见 [architecture.md](architecture.md) §3.3.1。

### 3.1 新语言必须提供的上下文

```go
// x/{lang}/context/file_context.go
type FileContext struct{ *core.BaseFileContext }
func NewFileContext(...) *FileContext
// 单包语义（Java）额外实现本包 PackageAwareFileContext；
// 多命名空间语义（C++）额外实现本包的 NamespaceAware。

// x/{lang}/context/global_context.go
type GlobalContext struct{ *core.BaseGlobalContext }
func NewGlobalContext(r core.SymbolResolver, b core.SymbolBuilder) core.GlobalContext
func (gc *GlobalContext) RegisterFileContext(fc core.FileContext)   // 先注册包/ns 再委托 Base
```

- 语言专属能力用**可选能力接口**表达（`javactx.PackageAwareFileContext` / `cppctx.NamespaceAware`），接口归语言包所有，core 接口不含语言概念。
- 全局索引（主键/声明-定义替换/重载 kind）由语言包提供 `core.DefinitionIndex` 实现，构造 `BaseGlobalContext` 时注入。
- 语言专属导入属性写入 `ImportInfo.Attrs`（key 带语言前缀）。
- 在 `LanguageImpl.NewGlobalContext` 中挂载工厂；引擎经该字段创建上下文。

## 4. 注册流程（以 Java 为例）

```go
// x/java/init.go
func init() {
    resolver := symbol_resolver.New()
    core.Register(&core.LanguageImpl{
        Language:       core.LangJava,
        Parser:         &JavaParserFactory{},
        Collector:      collector.New(resolver),
        Binder:         binder.New(),
        Extractor:      extractor.New(),
        Linker:         linker.New(),
        NoiseFilter:    noise_filter.New(core.LevelBalanced),
        SymbolResolver: resolver,
        SymbolBuilder:  symbol_builder.New(),

        NewGlobalContext: javactx.NewGlobalContext,
    })
}
```

**接入方式**：任意入口只需 `import _ "github.com/CodMac/arch-lens-dep-analyzer/x/java"`，init() 自动完成注册。

### 4.1 一次注册多种语言（C/C++ 模式）

`x/cpp/init.go` 用同一套 SPI 实例注册两个 `Language`：

```go
func init() {
    resolver := symbol_resolver.New()
    builder := symbol_builder.New()
    impl := func(lang core.Language) *core.LanguageImpl {
        return &core.LanguageImpl{
            Language: lang, Parser: &CppParserFactory{},
            Collector: collector.New(resolver), Binder: binder.New(resolver),
            Extractor: extractor.New(resolver), Linker: linker.New(),
            NoiseFilter: noise_filter.New(core.LevelBalanced),
            SymbolResolver: resolver, SymbolBuilder: builder,
            NewGlobalContext: cppctx.NewGlobalContext,
        }
    }
    core.Register(impl(core.LangC))
    core.Register(impl(core.LangCPP))
}
```

适用前提：两种语言的 AST 与语义足够接近（C 是 C++ 子集），差异仅在 Parser 层（按扩展名选 grammar）。若语义差异大（如 Go 的隐式接口实现），应各自独立实现。

## 5. AST 抽象（core/ast）

引擎用**薄封装接口**隔离 AST 库：

```go
// core/ast/node.go
type Node interface {
    Kind() string
    Utf8Text(src []byte) string
    ChildByFieldName(name string) Node
    ChildByFieldNameRecursive(name string) Node
    NamedChild(i int) Node
    NamedChildCount() int
    // ...导航能力
}

type SourceFile interface {
    Root() Node
    Content() []byte
    Path() string
}
```

**实现方**：`x/{lang}/ast/` 提供 `Node` 适配器（包装 `*sitter.Node`），把具体节点映射到接口（`x/java/ast/node.go`、`x/cpp/ast/node.go`）。

> 注意：tree-sitter 节点是含 C 指针的 struct，**不能用 `==` 比较**；`Equal` 必须比较节点 `Id()`（见 `x/cpp/ast/node.go`）。

**好处**：
- core/engine/model 完全不感知 tree-sitter。
- 换 AST 库（如 go-tree-sitter 升级、或未来换解析器）只需改适配器。
- 测试可用 mock Node 构造任意 AST 形状。

## 6. 新增语言步骤（Go 为例）

1. **创建目录**：`x/go/`。
2. **AST 适配**：`x/go/ast/` 实现 core/ast.Node（包装 tree-sitter-go）。
3. **实现 SPI**：collector / binder / extractor / linker / noise_filter / symbol_resolver / symbol_builder。
4. **注册**：`x/go/init.go` 调 `core.Register`。
5. **测试**：建 testdata/go/，沿用三层测试模式。

> 语言实现边界与能力要求，见 [languages/java.md](languages/java.md) 与 [languages/cpp.md](languages/cpp.md)；多语言推进优先级见 [roadmap.md](roadmap.md)。

## 7. 设计权衡

**为什么保留注册表 + init() 而非显式注册？**

| 方案 | 优点 | 缺点 |
|---|---|---|
| 全局注册表 + init() | 新语言只需 import blank；引擎零改动 | 隐式依赖；注册顺序不可控（靠 init 保证） |
| 显式注册 | 依赖可见 | 每加语言要改入口代码 |
| 编译期自注册 | 最灵活 | Go 无原生支持，复杂度高 |

选择「注册表 + init()」：Go 的 init() 保证所有依赖包先初始化，注册顺序稳定；`import _` 接入方式对使用者最友好。

## 8. 上下文基类的语言泄漏与已知 Gap（2026-11-25 审计，待修复）

`core.BaseFileContext` / `core.BaseGlobalContext` 目前承载了**语言语义**：索引主键、声明/定义替换规则、重载索引 kind 都由 core 硬编码。这与「core 只保留语言无关契约」的分层目标冲突，审计出以下 Gap。

### 8.1 索引主键应下沉

| 位置 | 当前主键 | 编码的语言语义 |
|---|---|---|
| `BaseFileContext.declDefIndex` | `QN + "\|" + Kind` | C/C++ 同文件「声明 → 定义」原地替换 |
| `BaseGlobalContext.qnIndex` | `QN + "\|" + Kind + "\|" + Path` | C/C++ 声明/定义共存 + 跨文件同名（static/main/amalgamation）共存 |
| `BaseGlobalContext.primaryIndex` | 裸 `QN` | 「定义优先、纯声明回填」查找语义 |
| `BaseGlobalContext.overloadIndex` | 去参 `QN` | 仅 C/C++ 需要；Java 只有 Method |

**已下沉（2026-11-26）**：`qnIndex` / `primaryIndex` / `overloadIndex` 及 `qnKey` / `replaceDefinitionLocked` / `isOverloadKind` 已从 `core.BaseGlobalContext` 移出，改由语言实现提供 `core.DefinitionIndex`：

```go
type DefinitionIndex interface {
    // added=false 表示同键重复；replaced 非 nil 表示定义原地替换了声明。
    Add(def *DefinitionEntry) (replaced *DefinitionEntry, added bool)
    FindByQualifiedName(qn string) (*DefinitionEntry, bool)
    FindOverloadCandidates(noParamsQN string) ([]*DefinitionEntry, bool)
}
```

- `x/cpp/context/definition_index.go`：全量键 `QN|Kind|Path`、主索引定义优先、重载索引收 Function 族。
- `x/java/context/definition_index.go`：全量键 `QN|Kind|Path`、重载索引仅 Method。
- `BaseGlobalContext` 只保留 `fileContexts` / `definitions` 容器与锁；`AddDefinition` 按索引返回的 `replaced` 同步切片顺序，查找方法仅做加锁委托。
- `GlobalContextRules` 已删除（其唯一字段 `OverloadKinds` 被索引实现取代）。

### 8.2 文件上下文（`core/file_context.go`）

| # | 优先级 | Gap | 说明 |
|---|---|---|---|
| F-1 | ~~P1~~ 已修复 | `qualifiedNameMap` 只按 QN 索引 | 与 `declDefIndex` 的 `QN+Kind` 不一致：C++ 同 QN 不同 Kind（`FUNCTION` / `FUNCTION_DECL`）时后者覆盖前者，查找结果随注册顺序变化。**修复**：改存 `QN -> []*DefinitionEntry`，`FindByQualifiedName` 定义优先（`core/file_context_test.go` 回归） |
| F-2 | ~~P1~~ 已修复 | 声明/定义替换语义在 core | **修复**：新增 `core.FileIndexPolicy`（`ShouldReplace` / `IsDefinition`），`BaseFileContext` 只按策略执行；`x/cpp/context` 注入声明→定义替换与定义优先，Java 不设置（无声明概念） |
| F-3 | ~~P2~~ 已修复 | `PackageAwareFileContext` 在 core | **修复**：接口下沉到 `x/java/context`，与 `cppctx.NamespaceAware` 归属一致 |
| F-4 | ~~P2~~ 已修复 | `DefinitionEntry.IsDeclaration` 在 core | **修复**：删除该字段，声明标记改存元素元数据 `cpp.is_declaration`（`x/cpp/context.IsDeclaration` 读取）；Collector 在注册时写入 |
| F-5 | 非 Gap | `Imports` 以短名为键 | 契约语义是「源码中引用该导入所用的名字」：Java 为 import 简单名，C++ 为 include 路径；语言差异已由 `ImportInfo.Attrs` 承载，键本身语言无关，保持现状 |
| F-6 | P3 | 返回内部切片/映射 | `Definitions()`、`Imports()`、`FindByShortName`、`FindByKind` 直接返回内部引用，调用方可篡改 |
| F-7 | P3 | 零值 `BaseFileContext` 不安全 | 未走 `NewBaseFileContext` 时 map 为 nil，`AddDefinition` 会 panic（仅 `declDefIndex` 有惰性初始化） |

### 8.3 全局上下文（`core/global_context.go`）

| # | 优先级 | Gap | 说明 |
|---|---|---|---|
| G-1 | ~~P0~~ 已修复 | 注册顺序不确定 → 解析结果随机 | Collector 并行调用 `RegisterFileContext`（`engine/stage.go`），`primaryIndex` 先到先得。实测：`a.cpp`/`b.cpp` 各定义 `struct Dup`，`c.cpp` 的 `new Dup()` 连续 6 次命中 `a,b,a,b,a,a`。**修复**：并行收集结果暂存，按相对路径排序后串行注册（回归测试 `engine/determinism_test.go`） |
| G-2 | ~~P1~~ 已修复 | 全局查找 Kind 盲 | `FindByQualifiedName` 只按 QN。**修复**：新增 `FindAllByQualifiedName(qn)` 返回同 QN 全部条目（定义优先），调用方按 Kind 挑选；C++ Resolver 的类型/成员/变量查找已改用 |
| G-3 | ~~P1~~ 已修复 | 无全局短名/按 Kind 索引 | 语言侧只能 `Definitions()` 全量扫描（O(defs)）。**修复**：`DefinitionIndex` 维护 `shortNameIndex`，新增 `FindByShortName(name)`；C++ Resolver 三处全局短名回退已改用它 |
| G-4 | P2 | `RegisterFileContext` 非模板方法 | Java 覆盖后「先注册包 → 再委托 Base」两段各自加锁，非原子，且子类必须知道 Base 的调用顺序 |
| G-5 | P2 | `addDefinitionLocked` / `replaceDefinitionLocked` 不可复用 | 未导出，语言实现无法在保持锁语义的前提下定制索引 |
| G-6 | P3 | `FindOverloadCandidates` 返回内部切片 | 调用方可篡改索引 |
| G-7 | P3 | 替换与 `qnIndex` 更新耦合 | `addDefinitionLocked` 内手工同步 `qnIndex`/`primaryIndex`/`overloadIndex` 三处，新增索引易漏 |
| G-8 | P3 | `primaryIndex` 折叠跨文件同名 QN | 折叠后无公开 API 取「全部同名定义」，语言侧无法显式处理歧义 |
| G-9 | ~~P3~~ 已修复 | `GlobalContextRules` 策略不足 | 只有 `OverloadKinds`，无法表达主键/替换/可见性策略。**修复**：删除 `GlobalContextRules`，由 `DefinitionIndex` 承担键/替换/重载策略 |

### 8.4 可见性

| # | 优先级 | Gap | 说明 |
|---|---|---|---|
| V-1 | ~~P1~~ 已修复 | 全局上下文无「按文件可见性」视图 | Java 在语言层用 `fc.Imports()` 做 import 可见性；C++ 无对应能力（对应 `languages/cpp.md` B-7）。**修复**：新增 `core.FileVisibility`，`x/cpp/context` 实现：本文件 + include 可达文件（精确/后缀匹配）+ 全局作用域定义。Resolver 接入留待 R-1/R-3 |
| V-2 | ~~P2~~ 已修复 | 缺统一可见性能力契约 | **修复**：`core.FileVisibility.VisibleDefinitions(fromFile)` 作为公共可选能力接口，各语言按自身规则（import/include/namespace/package 私有）实现 |

### 8.5 修复优先级

- **P0**：G-1（注册顺序确定化：按路径排序注册 + 歧义显式判 nil，禁止「先到先得」）。
- **P1**：~~主键与三索引下沉~~（已由 `DefinitionIndex` 完成）、~~F-1~~、~~G-2~~、~~G-3~~、~~V-1~~。
- **P2**：~~F-3~~/~~F-4~~（F-5 经复核非 Gap）、G-4/G-5/G-6、~~V-2~~。
- **P3**：F-6/F-7、G-7/G-8/G-9。

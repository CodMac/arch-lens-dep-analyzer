package preproc

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	p := filepath.Join("..", "testdata", "preproc", name)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// TestProjectLengthPreserved 投影必须等长（Location 不变的前提）。
func TestProjectLengthPreserved(t *testing.T) {
	for _, name := range []string{
		"split_args.cpp", "has_include.cpp", "if_zero.cpp",
		"member_init.cpp", "nested.cpp", "guard.h", "macro_class.h",
	} {
		src := loadFixture(t, name)
		res := Project(src)
		if len(res.Source) != len(src) {
			t.Errorf("%s: length changed %d -> %d", name, len(src), len(res.Source))
		}
	}
}

// TestProjectLineStructurePreserved 投影后行数与每行长度必须不变。
func TestProjectLineStructurePreserved(t *testing.T) {
	src := loadFixture(t, "split_args.cpp")
	res := Project(src)
	origLines := bytes.Split(src, []byte("\n"))
	newLines := bytes.Split(res.Source, []byte("\n"))
	if len(origLines) != len(newLines) {
		t.Fatalf("line count changed %d -> %d", len(origLines), len(newLines))
	}
	for i := range origLines {
		if len(origLines[i]) != len(newLines[i]) {
			t.Fatalf("line %d length changed %d -> %d", i+1, len(origLines[i]), len(newLines[i]))
		}
	}
}

// TestSplitArgs 跨 #ifdef 的函数实参：投影后括号必须平衡。
func TestSplitArgs(t *testing.T) {
	src := loadFixture(t, "split_args.cpp")
	res := Project(src)

	if got := bracketBalance(res.Source, 0, len(res.Source)); got != 0 {
		t.Errorf("projected source unbalanced: %d", got)
	}
	// 首分支（_WIN32）被选中，static_cast 保留，ptr, size 被置空。
	if !bytes.Contains(res.Source, []byte("static_cast<char *>(ptr)")) {
		t.Errorf("chosen branch content missing")
	}
	if bytes.Contains(res.Source, []byte("ptr, size,")) {
		t.Errorf("dropped branch content still present")
	}
	if len(res.Dropped) == 0 {
		t.Errorf("expected dropped branch recorded")
	}
}

// TestIfZeroDropped #if 0 整组丢弃。
func TestIfZeroDropped(t *testing.T) {
	src := loadFixture(t, "if_zero.cpp")
	res := Project(src)
	if bytes.Contains(res.Source, []byte("dead_function")) {
		t.Errorf("#if 0 body should be blanked")
	}
	if !bytes.Contains(res.Source, []byte("live_function")) {
		t.Errorf("live function should survive")
	}
}

// TestHasInclude 无法静态判定的条件选首分支。
func TestHasInclude(t *testing.T) {
	src := loadFixture(t, "has_include.cpp")
	res := Project(src)
	if got := bracketBalance(res.Source, 0, len(res.Source)); got != 0 {
		t.Errorf("unbalanced: %d", got)
	}
	if !bytes.Contains(res.Source, []byte("namespace demo")) {
		t.Errorf("namespace lost")
	}
}

// TestMemberInit 构造器初始化列表跨分支。
func TestMemberInit(t *testing.T) {
	src := loadFixture(t, "member_init.cpp")
	res := Project(src)
	if got := bracketBalance(res.Source, 0, len(res.Source)); got != 0 {
		t.Errorf("unbalanced: %d", got)
	}
	if !bytes.Contains(res.Source, []byte("session(other.session)")) {
		t.Errorf("chosen member init missing")
	}
}

// TestNested 嵌套条件逐组独立决策。
func TestNested(t *testing.T) {
	src := loadFixture(t, "nested.cpp")
	res := Project(src)
	if got := bracketBalance(res.Source, 0, len(res.Source)); got != 0 {
		t.Errorf("unbalanced: %d", got)
	}
	if len(res.Regions) == 0 {
		t.Errorf("expected chosen regions")
	}
}

// TestGuard 包含守卫：选守卫体，#else 丢弃。
func TestGuard(t *testing.T) {
	src := loadFixture(t, "guard.h")
	res := Project(src)
	if !bytes.Contains(res.Source, []byte("class Guarded")) {
		t.Errorf("guarded body missing")
	}
	if bytes.Contains(res.Source, []byte("class Fallback")) {
		t.Errorf("#else body should be blanked")
	}
}

// TestMacroClass 类型注解宏归一化。
func TestMacroClass(t *testing.T) {
	src := loadFixture(t, "macro_class.h")
	res := Project(src)
	if len(res.MacroAnns) == 0 {
		t.Fatalf("expected macro annotation recorded")
	}
	if res.MacroAnns[0].Macro != "GTEST_API_" || res.MacroAnns[0].Type != "Test" {
		t.Errorf("unexpected ann: %+v", res.MacroAnns[0])
	}
	if bytes.Contains(res.Source, []byte("GTEST_API_")) {
		t.Errorf("macro token should be blanked")
	}
	if !bytes.Contains(res.Source, []byte("class")) || !bytes.Contains(res.Source, []byte("Test {")) {
		t.Errorf("class declaration should remain recognizable")
	}
}

// TestProtoMacroReturnType 返回类型宏 `MACRO(type) fn(...)` 应还原为普通函数声明。
func TestProtoMacroReturnType(t *testing.T) {
	src := []byte("CJSON_PUBLIC(const char*) cJSON_Version(void);\n" +
		"CJSON_PUBLIC(cJSON *) cJSON_Parse(const char *value);\n" +
		"TEST_F(Foo, Bar) {}\n")
	out := NormalizeMacros(src)
	if len(out) != len(src) {
		t.Fatalf("length changed: %d -> %d", len(src), len(out))
	}
	if bytes.Contains(out, []byte("CJSON_PUBLIC")) {
		t.Errorf("return-type macro should be rewritten: %q", out)
	}
	if !bytes.Contains(out, []byte("const char*")) || !bytes.Contains(out, []byte("cJSON_Version(void);")) {
		t.Errorf("first prototype not restored: %q", out)
	}
	if !bytes.Contains(out, []byte("cJSON *")) || !bytes.Contains(out, []byte("cJSON_Parse(const char *value);")) {
		t.Errorf("second prototype not restored: %q", out)
	}
	// 带逗号的宏（TEST_F）不是返回类型宏，由 normalizeTestMacros 转成函数定义。
	if bytes.Contains(out, []byte("TEST_F(Foo, Bar)")) {
		t.Errorf("test macro should be rewritten to a function: %q", out)
	}
}

// TestTestMacroBody `TEST(args) { ... }` 应改写为等长函数定义，使 body 获得容器。
func TestTestMacroBody(t *testing.T) {
	src := []byte("TEST(Suite, Name) {\n  auto f = [] { return 1; };\n}\n" +
		"TEST_F(Foo, Bar) {}\n")
	out := NormalizeMacros(src)
	if len(out) != len(src) {
		t.Fatalf("length changed: %d -> %d", len(src), len(out))
	}
	if bytes.Contains(out, []byte("TEST(Suite, Name)")) || bytes.Contains(out, []byte("TEST_F(Foo, Bar)")) {
		t.Errorf("test macro call should be rewritten: %q", out)
	}
	// 函数名取 args 首个标识符（与原始字节一致，保证 Collector 取文本正确）。
	if !bytes.Contains(out, []byte("void Suite()")) {
		t.Errorf("expected void Suite(): %q", out)
	}
	if !bytes.Contains(out, []byte("void Foo()")) {
		t.Errorf("expected void Foo(): %q", out)
	}
}

// TestTypeMacroMultiline 类型注解宏后接换行再 `:`/`{` 时也应归一化。
func TestTypeMacroMultiline(t *testing.T) {
	src := []byte("class GTEST_API_ ScopedReporter\n    : public Base {\n};\n")
	out := NormalizeMacros(src)
	if len(out) != len(src) {
		t.Fatalf("length changed: %d -> %d", len(src), len(out))
	}
	if bytes.Contains(out, []byte("GTEST_API_")) {
		t.Errorf("type macro should be blanked: %q", out)
	}
	if !bytes.Contains(out, []byte("class ")) || !bytes.Contains(out, []byte("ScopedReporter\n    : public Base {")) {
		t.Errorf("class declaration must remain: %q", out)
	}
}

// TestEnumClassAllCapsName `enum class E {` 不得被 typeMacroAfterRe 误判为
// 「类型名 + 宏」：group2 是 class 关键字，group4 是枚举名，两者都必须保留。
func TestEnumClassAllCapsName(t *testing.T) {
	src := []byte("enum class E { A };\nenum struct COLOR : int { RED };\n")
	out := NormalizeMacros(src)
	if len(out) != len(src) {
		t.Fatalf("length changed: %d -> %d", len(src), len(out))
	}
	if !bytes.Contains(out, []byte("enum class E {")) {
		t.Errorf("enum class E must remain: %q", out)
	}
	if !bytes.Contains(out, []byte("enum struct COLOR : int {")) {
		t.Errorf("enum struct COLOR must remain: %q", out)
	}
}

// TestStatementMacro 独占一行的对象式宏调用应被置空，避免 C 声明歧义吞并后续代码。
func TestStatementMacro(t *testing.T) {
	src := []byte("#define EXPECT_BEGIN \\\n    if (x) {\n" +
		"#define EXPECT_END \\\n    }\n" +
		"void f(void) {\n    EXPECT_BEGIN\n    g(1);\n    EXPECT_END\n}\n" +
		"void h(void) { EXPECT_BEGIN g(2); EXPECT_END }\n")
	out := NormalizeMacros(src)
	if len(out) != len(src) {
		t.Fatalf("length changed: %d -> %d", len(src), len(out))
	}
	// 独立成行的宏被置空。
	if bytes.Contains(out, []byte("\n    EXPECT_BEGIN\n")) || bytes.Contains(out, []byte("\n    EXPECT_END\n")) {
		t.Errorf("standalone statement macro should be blanked: %q", out)
	}
	// 同一行的用法保持不变（不在本次处理范围）。
	if !bytes.Contains(out, []byte("void h(void) { EXPECT_BEGIN g(2); EXPECT_END }")) {
		t.Errorf("inline macro usage should be untouched: %q", out)
	}
	// #define 行必须保留。
	if !bytes.Contains(out, []byte("#define EXPECT_BEGIN")) {
		t.Errorf("#define must be preserved: %q", out)
	}
}

// TestLeadingObjectMacros 行首可见性宏序列后紧跟类型时应置空，避免与返回类型粘连。
func TestLeadingObjectMacros(t *testing.T) {
	src := []byte("#define GTEST_NO_INLINE_ __attribute__((noinline))\n" +
		"#define GTEST_NO_TAIL_CALL_ __attribute__((no_instrument_function))\n" +
		"GTEST_NO_INLINE_ GTEST_NO_TAIL_CALL_ std::string\n" +
		"GetCurrentOsStackTraceExceptTop(UnitTest* u, int skip_count) {\n  return \"\";\n}\n")
	out := NormalizeMacros(src)
	if len(out) != len(src) {
		t.Fatalf("length changed: %d -> %d", len(src), len(out))
	}
	if bytes.Contains(out, []byte("GTEST_NO_INLINE_ GTEST_NO_TAIL_CALL_ std::string")) {
		t.Errorf("leading visibility macros should be blanked: %q", out)
	}
	if !bytes.Contains(out, []byte("std::string\nGetCurrentOsStackTraceExceptTop")) {
		t.Errorf("return type and function name must remain: %q", out)
	}
}

// TestLeadingMacroCustomType 行首注解宏后接自定义类型 + 函数名时应置空宏。
func TestLeadingMacroCustomType(t *testing.T) {
	src := []byte("#define GTEST_API_ __attribute__((visibility(\"default\")))\n" +
		"#define PUGI__FN inline\n" +
		"GTEST_API_ AssertionResult AssertionSuccess();\n" +
		"PUGI__FN xml_memory_page* compact_get_page(const void* object, int offset)\n" +
		"PUGI__FN xml_attribute xml_node::append_attribute(const char_t* name_)\n" +
		"  { return xml_attribute(); }\n" +
		"GTEST_API_ SomeType plain_variable;\n")
	out := NormalizeMacros(src)
	if len(out) != len(src) {
		t.Fatalf("length changed: %d -> %d", len(src), len(out))
	}
	if bytes.Contains(out, []byte("GTEST_API_ AssertionResult")) || bytes.Contains(out, []byte("PUGI__FN xml_")) {
		t.Errorf("leading annotation macros should be blanked: %q", out)
	}
	if !bytes.Contains(out, []byte("AssertionResult AssertionSuccess();")) {
		t.Errorf("declarator must remain: %q", out)
	}
	if !bytes.Contains(out, []byte("xml_node::append_attribute(const char_t* name_)")) {
		t.Errorf("qualified declarator must remain: %q", out)
	}
	// 无 `(` 的变量声明不应触发置空。
	if !bytes.Contains(out, []byte("GTEST_API_ SomeType plain_variable;")) {
		t.Errorf("plain variable declaration should be untouched: %q", out)
	}
}

// TestMacroBeforeOperator 紧邻 operator 之前的对象式宏应置空。
func TestMacroBeforeOperator(t *testing.T) {
	src := []byte("#define PUGIXML_FUNCTION inline\n" +
		"bool PUGIXML_FUNCTION operator&&(const xml_attribute& lhs, bool rhs);\n")
	out := NormalizeMacros(src)
	if len(out) != len(src) {
		t.Fatalf("length changed: %d -> %d", len(src), len(out))
	}
	if bytes.Contains(out, []byte("PUGIXML_FUNCTIONoperator")) {
		t.Errorf("macro before operator should be blanked: %q", out)
	}
	if !bytes.Contains(out, []byte("operator&&(const xml_attribute& lhs, bool rhs);")) {
		t.Errorf("operator declarator must remain: %q", out)
	}
}

// TestTrailingMacroAfterParen 函数声明符之后的尾随宏应置空。
func TestTrailingMacroAfterParen(t *testing.T) {
	src := []byte("#define PUGIXML_NOEXCEPT noexcept\n" +
		"PUGI__FN void f(int a) PUGIXML_NOEXCEPT\n{\n  g(a);\n}\n" +
		"int h() PUGIXML_NOEXCEPT;\n")
	out := NormalizeMacros(src)
	if len(out) != len(src) {
		t.Fatalf("length changed: %d -> %d", len(src), len(out))
	}
	if bytes.Contains(out, []byte("PUGIXML_NOEXCEPT\n{")) || bytes.Contains(out, []byte("PUGIXML_NOEXCEPT;")) {
		t.Errorf("trailing macro should be blanked: %q", out)
	}
	if !bytes.Contains(out, []byte("void f(int a) ")) || !bytes.Contains(out, []byte("int h() ")) {
		t.Errorf("declarator must remain: %q", out)
	}
}

// TestFunctionMacroAsTypeName 函数式宏作类型名时应置空实参、保留宏名。
func TestFunctionMacroAsTypeName(t *testing.T) {
	src := []byte("#define GTEST_1_TUPLE_(T) tuple<T##0>\n" +
		"class GTEST_1_TUPLE_(T) {\n public:\n  int f0_;\n};\n")
	out := NormalizeMacros(src)
	if len(out) != len(src) {
		t.Fatalf("length changed: %d -> %d", len(src), len(out))
	}
	if bytes.Contains(out, []byte("class GTEST_1_TUPLE_(T)")) {
		t.Errorf("macro args should be blanked: %q", out)
	}
	if !bytes.Contains(out, []byte("class GTEST_1_TUPLE_ ")) {
		t.Errorf("macro name should remain as type name: %q", out)
	}
}

// TestQualifiedFunctionMacro 限定符后的函数式返回类型宏应连同实参置空。
func TestQualifiedFunctionMacro(t *testing.T) {
	src := []byte("#define GTEST_ADD_REF_(T) typename std::add_lvalue_reference<T>::type\n" +
		"#define GTEST_TUPLE_ELEMENT_(i, T) typename std::tuple_element<i, T>::type\n" +
		"  static GTEST_ADD_REF_(GTEST_TUPLE_ELEMENT_(0, Tuple))\n" +
		"  Field(Tuple& t) { return t.f0_; }\n")
	out := NormalizeMacros(src)
	if len(out) != len(src) {
		t.Fatalf("length changed: %d -> %d", len(src), len(out))
	}
	if bytes.Contains(out, []byte("static GTEST_ADD_REF_(")) {
		t.Errorf("qualified function macro should be blanked: %q", out)
	}
	if !bytes.Contains(out, []byte("  static ")) || !bytes.Contains(out, []byte("Field(Tuple& t)")) {
		t.Errorf("qualifier and function name must remain: %q", out)
	}
}

// TestStandaloneFunctionMacro 整行的函数式宏调用应置空，避免破坏类体大括号。
func TestStandaloneFunctionMacro(t *testing.T) {
	src := []byte("#define PUSH_(n) _Pragma(\"push\")\n" +
		"class A {\n  PUSH_(4180)\n  void f();\n};\n" +
		"void g() {\n  PUSH_(1);\n}\n" +
		"void h() { PUSH_(2); }\n")
	out := NormalizeMacros(src)
	if len(out) != len(src) {
		t.Fatalf("length changed: %d -> %d", len(src), len(out))
	}
	if bytes.Contains(out, []byte("\n  PUSH_(4180)\n")) {
		t.Errorf("standalone annotation macro should be blanked: %q", out)
	}
	if !bytes.Contains(out, []byte("void f();")) || !bytes.Contains(out, []byte("};")) {
		t.Errorf("class body must remain: %q", out)
	}
	// 带分号的是语句宏，调用关系需保留，不得置空。
	if !bytes.Contains(out, []byte("\n  PUSH_(1);\n")) {
		t.Errorf("statement macro with semicolon must be kept: %q", out)
	}
}

// TestKeepIncludes 未选中分支中的 #include 默认保留。
func TestKeepIncludes(t *testing.T) {
	src := loadFixture(t, "has_include.cpp")
	withKeep := ProjectWithOptions(src, Options{Pass: PassFirst, KeepIncludes: true})
	withoutKeep := ProjectWithOptions(src, Options{Pass: PassFirst, KeepIncludes: false})
	// #else 分支（未选中）中的 include 应保留 / 可关闭。
	if !bytes.Contains(withKeep.Source, []byte("sys/un.h")) {
		t.Errorf("include in dropped branch should be kept")
	}
	if bytes.Contains(withoutKeep.Source, []byte("sys/un.h")) {
		t.Errorf("include should be blanked when KeepIncludes=false")
	}
}

// TestKeepMultilineMacro 未选中分支中的多行宏必须整组保留（含续行）。
// 只保留首行会留下悬挂的 `\`，把后续行乃至 #endif 吞进宏体，条件组永不闭合。
func TestKeepMultilineMacro(t *testing.T) {
	src := []byte("#ifdef A\nint a;\n#else\n#define M(x) \\\n  ((x) + 1)\n#endif\nint tail;\n")
	res := Project(src)
	if got := bracketBalance(res.Source, 0, len(res.Source)); got != 0 {
		t.Errorf("unbalanced: %d", got)
	}
	if !bytes.Contains(res.Source, []byte("((x) + 1)")) {
		t.Errorf("continuation lines of kept macro should be preserved")
	}
	if !bytes.Contains(res.Source, []byte("int tail;")) {
		t.Errorf("code after #endif should survive")
	}
}

// TestBlankMultilineDirective 未选中分支中的多行指令（如多行 #if 条件）必须整组置空。
func TestBlankMultilineDirective(t *testing.T) {
	src := []byte("#if defined(A) || \\\n    defined(B)\nint x;\n#endif\nint y;\n")
	res := Project(src)
	if got := bracketBalance(res.Source, 0, len(res.Source)); got != 0 {
		t.Errorf("unbalanced: %d", got)
	}
	if bytes.Contains(res.Source, []byte("defined(B)")) {
		t.Errorf("continuation line of directive should be blanked")
	}
	if !bytes.Contains(res.Source, []byte("int y;")) {
		t.Errorf("code after #endif should survive")
	}
}

// TestPassLast 末分支策略（分支回补用）。
func TestPassLast(t *testing.T) {
	src := loadFixture(t, "split_args.cpp")
	first := ProjectWithOptions(src, Options{Pass: PassFirst, KeepIncludes: true})
	last := ProjectWithOptions(src, Options{Pass: PassLast, KeepIncludes: true})
	if bytes.Equal(first.Source, last.Source) {
		t.Errorf("first/last passes should differ")
	}
	if !bytes.Contains(last.Source, []byte("ptr, size,")) {
		t.Errorf("last pass should choose #else branch")
	}
}

// TestDirectiveInCommentIgnored 注释中的 #if 不参与配对。
func TestDirectiveInCommentIgnored(t *testing.T) {
	src := []byte("// #if 0\n/* #endif */\nint x;\n")
	res := Project(src)
	if len(res.Regions) != 0 || len(res.Dropped) != 0 {
		t.Errorf("comment directives should be ignored, got regions=%d dropped=%d", len(res.Regions), len(res.Dropped))
	}
	if !bytes.Contains(res.Source, []byte("int x;")) {
		t.Errorf("code should be untouched")
	}
}

// TestUnbalancedFallback 所有分支都不平衡时回退首分支。
func TestUnbalancedFallback(t *testing.T) {
	src := []byte("#ifdef A\n{\n#else\n}\n#endif\n")
	res := Project(src)
	if len(res.Source) != len(src) {
		t.Fatalf("length changed")
	}
	if !strings.Contains(string(res.Source), "{") {
		t.Errorf("fallback should choose first branch")
	}
}

package preproc

import (
	"bytes"
	"regexp"
)

// 类型注解宏：位于 class/struct/enum/union 关键字与类型名之间（或类型名之后），
// 形如 `class GTEST_API_ Foo : public Bar {`。tree-sitter 会把这类声明解析成
// function_definition（宏被当作函数名），类型信息因此丢失，事后无法可靠修补。
// 这里在解析前把宏 token 按字节置空格（等长），使语法恢复为正常的类型声明。
//
// 只匹配行首的声明，且要求宏名全大写、后接 `:` 或 `{`，避免误伤普通代码。
// Go regexp 不支持 lookahead，故把结尾的 `:`/`{` 也捕获后原样输出。
var (
	// class <MACRO> Name [: {]
	// 允许 `:`/`{` 出现在下一行（如 `class GTEST_API_ Foo\n    : public Bar {`）。
	typeMacroBeforeRe = regexp.MustCompile(`(?m)^([ \t]*(?:class|struct|enum|union)[ \t]+)([A-Z_][A-Z0-9_]*)([ \t]+)([A-Za-z_]\w*)([ \t]*(?:\r?\n[ \t]*)?)([:{])`)
	// class Name <MACRO> [: {]
	typeMacroAfterRe = regexp.MustCompile(`(?m)^([ \t]*(?:class|struct|enum|union)[ \t]+)([A-Za-z_]\w*)([ \t]+)([A-Z_][A-Z0-9_]*)([ \t]*(?:\r?\n[ \t]*)?)([:{])`)
	// 返回类型宏：`CJSON_PUBLIC(const char*) fn(...)`。
	// 宏实参本身就是返回类型，tree-sitter 会把它当函数声明而产出 ERROR。
	// 仅匹配「行首全大写宏 + 单实参 + 后接 函数名(」的形态，避免误伤 TEST_F(...) 等。
	protoMacroRe = regexp.MustCompile(`(?m)^([ \t]*)([A-Z_][A-Z0-9_]*)\(([^(),\n]*)\)([ \t]+)([A-Za-z_]\w*[ \t]*\()`)
	// 对象式宏定义：`#define NAME`（NAME 后不紧跟 `(`）。
	// 用于识别「独占一行的宏调用」（如 Unity 的 EXPECT_ABORT_BEGIN）。
	objectMacroDefRe = regexp.MustCompile(`(?m)^[ \t]*#[ \t]*define[ \t]+([A-Za-z_]\w*)(\()?`)
)

// NormalizeMacros 仅做类型注解宏归一化（不投影条件分支），返回等长新切片。
//
// 供「原始树」解析使用：原始树保留全部分支（preproc_* 节点），但若不做宏归一化，
// `struct GTEST_API_ Foo` 会被 tree-sitter 解析成函数定义，产生错误的元素与 QN。
// 与投影树的归一化口径一致，保证两棵树的字节坐标可比较。
func NormalizeMacros(src []byte) []byte {
	out, _ := normalizeTypeMacros(src)
	return out
}

// normalizeTypeMacros 置空类型注解宏，返回等长新切片与归一化记录。
func normalizeTypeMacros(src []byte) ([]byte, []MacroAnn) {
	var anns []MacroAnn

	out := typeMacroBeforeRe.ReplaceAllFunc(src, func(m []byte) []byte {
		sub := typeMacroBeforeRe.FindSubmatch(m)
		anns = append(anns, MacroAnn{Macro: string(sub[2]), Type: string(sub[4])})
		return rebuildWithBlanked(m, sub, 2)
	})
	out = typeMacroAfterRe.ReplaceAllFunc(out, func(m []byte) []byte {
		sub := typeMacroAfterRe.FindSubmatch(m)
		anns = append(anns, MacroAnn{Macro: string(sub[4]), Type: string(sub[2])})
		return rebuildWithBlanked(m, sub, 4)
	})
	out = normalizeProtoMacros(out)
	out = normalizeTypeNameFunctionMacros(out)
	out = normalizeStatementMacros(out)
	out = normalizeTestMacros(out, src)
	return out, anns
}

// typeNameFunctionMacroRe 匹配「函数式宏直接充当类型名」的声明：
// `class GTEST_1_TUPLE_(T) {`。tree-sitter 会把 `GTEST_1_TUPLE_(T)` 解析成
// 函数式调用，导致类型名丢失/类体失配。这里把实参 `(...)` 置空，保留宏名
// 作为类型名（`class GTEST_1_TUPLE_    {`），语法恢复为正常类型声明。
var typeNameFunctionMacroRe = regexp.MustCompile(`(?m)^([ \t]*(?:class|struct|union|enum)[ \t]+)([A-Z_][A-Z0-9_]*)\(`)

// normalizeTypeNameFunctionMacros 置空「函数式宏作类型名」的实参。
func normalizeTypeNameFunctionMacros(src []byte) []byte {
	out := make([]byte, len(src))
	copy(out, src)
	for _, idx := range typeNameFunctionMacroRe.FindAllSubmatchIndex(src, -1) {
		// idx: [fullStart, fullEnd, g1s,g1e, g2s,g2e]；g2e 即 `(` 的位置。
		if len(idx) < 6 {
			continue
		}
		open := idx[5]
		if open >= len(src) || src[open] != '(' {
			continue
		}
		depth := 0
		end := -1
		for k := open; k < len(src); k++ {
			switch src[k] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					end = k + 1
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			continue
		}
		blankRange(out, open, end)
	}
	return out
}

// normalizeStatementMacros 置空「独占一行的对象式宏调用」。
//
// 典型场景（Unity 测试）：
//
//	#define EXPECT_ABORT_BEGIN \
//	    ... {
//	void f(void) {
//	    EXPECT_ABORT_BEGIN
//	    TEST_ASSERT_EQUAL_STRING_LEN(NULL, "bar", 1);
//	    VERIFY_FAILS_END
//	}
//
// 宏未展开时，C 语法把 `EXPECT_ABORT_BEGIN TEST_ASSERT_...(...)` 解析成
// 「类型 + 函数声明器」，后续整段代码被吞进 QN（含 `;`/`}`/换行）。
// 把独占一行的宏名置空后，语句恢复为普通表达式/空语句，真实函数得以正确解析。
//
// 仅处理「整行只有宏名（可带注释）」的形态，不触碰 `MACRO x;` 等同行用法。
func normalizeStatementMacros(src []byte) []byte {
	objMacros := objectMacroNames(src)
	fnMacros := functionMacroNames(src)
	// 即便本文件没有 #define，注解宏也可能定义在被包含的头文件中，
	// 故仍需逐行做「宏名形态」兜底（见 isMacroLikeName）。
	out := make([]byte, len(src))
	copy(out, src)

	pos := 0
	for pos < len(src) {
		eol := bytes.IndexByte(src[pos:], '\n')
		if eol < 0 {
			eol = len(src)
		} else {
			eol += pos
		}
		line := src[pos:eol]
		// 1) 整行只有对象式宏名：语句宏（EXPECT_ABORT_BEGIN）。
		if name := standaloneMacroName(line); name != "" && objMacros[name] {
			if i := bytes.Index(line, []byte(name)); i >= 0 {
				blankRange(out, pos+i, pos+i+len(name))
			}
		}
		// 2) 行首对象式宏序列 + 内置/限定类型：可见性/属性宏（GTEST_NO_INLINE_）。
		blankLeadingObjectMacros(out, src, pos, eol, objMacros)
		// 3) 限定符 + 函数式宏（返回类型宏，GTEST_ADD_REF_(...)）。
		blankQualifiedFunctionMacro(out, src, pos, eol, fnMacros)
		// 4) 整行只有一个函数式宏调用：语句宏（GTEST_DISABLE_MSC_WARNINGS_PUSH_(4180)）。
		blankStandaloneFunctionMacro(out, src, pos, eol, fnMacros)
		// 5) 紧邻 operator 之前的对象式宏（PUGIXML_FUNCTION operator&&）。
		blankObjectMacroBeforeOperator(out, src, pos, eol, objMacros)
		// 6) 函数声明符之后的尾随宏（`f(...) PUGIXML_NOEXCEPT {`）。
		blankTrailingMacroAfterParen(out, src, pos, eol, objMacros)
		pos = eol + 1
	}
	return out
}

// builtinTypeWords 是可用于判定「宏后面跟的是类型」的内置类型关键字。
var builtinTypeWords = map[string]bool{
	"void": true, "bool": true, "char": true, "short": true, "int": true,
	"long": true, "float": true, "double": true, "signed": true, "unsigned": true,
	"wchar_t": true, "char8_t": true, "char16_t": true, "char32_t": true,
	"size_t": true, "ssize_t": true, "auto": true,
}

// declQualifierWords 是函数声明前的限定符关键字。
var declQualifierWords = map[string]bool{
	"static": true, "inline": true, "virtual": true, "constexpr": true,
	"explicit": true, "friend": true, "extern": true, "consteval": true,
	"constinit": true,
}

// blankLeadingObjectMacros 置空行首连续的对象式宏，前提是其后紧跟
// 内置类型关键字或 `X::` 限定类型（说明这些宏是可见性/属性注解）。
//
// 例：`GTEST_NO_INLINE_ GTEST_NO_TAIL_CALL_ std::string` → 两个宏置空。
func blankLeadingObjectMacros(out, src []byte, pos, eol int, macros map[string]bool) {
	i := pos
	for i < eol && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	if i >= eol || src[i] == '#' {
		return
	}
	var spans [][2]int
	for {
		j := i
		for j < eol && (src[j] == ' ' || src[j] == '\t') {
			j++
		}
		s := j
		for j < eol && isIdentChar(src[j]) {
			j++
		}
		if j == s {
			break
		}
		name := string(src[s:j])
		// 宏可能定义在其它头文件（如 gtest-port.h），本文件看不到 #define，
		// 故对「全大写且含下划线」的宏名同样接受。
		if !macros[name] && !isMacroLikeName(name) {
			break
		}
		spans = append(spans, [2]int{s, j})
		i = j
	}
	if len(spans) == 0 {
		return
	}
	j := i
	for j < eol && (src[j] == ' ' || src[j] == '\t') {
		j++
	}
	s := j
	for j < eol && isIdentChar(src[j]) {
		j++
	}
	next := string(src[s:j])
	qualified := j+1 < eol && src[j] == ':' && src[j+1] == ':'
	// 自定义类型返回类型：`GTEST_API_ AssertionResult AssertionSuccess(...)`、
	// `PUGI__FN xml_memory_page* compact_get_page(...)`、
	// `PUGI__FN xml_attribute xml_node::append_attribute(...)`。
	// 判据：宏之后形如「类型 + 声明符(」。
	if !builtinTypeWords[next] && !qualified && !looksLikeDeclarator(src[:eol], s) {
		return
	}
	for _, sp := range spans {
		blankRange(out, sp[0], sp[1])
	}
}

// looksLikeDeclarator 判定 line[from:] 是否形如「类型 + 声明符(」。
// 用于识别行首注解宏之后的真实函数声明（宏 + 自定义类型 + 函数名）。
// 只扫描到第一个 `(` / `=` / `;`，避免把 `MACRO Type var = f();` 误判为声明。
func looksLikeDeclarator(line []byte, from int) bool {
	i := from
	idents := 0
	qualified := false
	for i < len(line) {
		c := line[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == ':' && i+1 < len(line) && line[i+1] == ':':
			qualified = true
			i += 2
		case c == '*' || c == '&':
			i++
		case c == '<':
			depth := 0
			for i < len(line) {
				if line[i] == '<' {
					depth++
				} else if line[i] == '>' {
					depth--
					if depth == 0 {
						i++
						break
					}
				}
				i++
			}
		case isIdentChar(c):
			for i < len(line) && isIdentChar(line[i]) {
				i++
			}
			idents++
		case c == '(':
			return qualified || idents >= 2
		default:
			return false
		}
	}
	return false
}

// blankTrailingMacroAfterParen 置空函数声明符 `)` 之后的尾随对象式宏。
//
// 例：`PUGI__FN void f() PUGIXML_NOEXCEPT {` → 置空 PUGIXML_NOEXCEPT。
// 未展开时 tree-sitter 会把尾随宏当作声明符的一部分，导致整个函数定义失配。
// 仅当宏后紧跟 `{` / `;` / 行尾时处理。
func blankTrailingMacroAfterParen(out, src []byte, pos, eol int, macros map[string]bool) {
	line := src[pos:eol]
	// 找到最后一个 `)`（声明符结束），其后只允许空白 + 宏 + 空白 + `{`/`;`/行尾。
	close := bytes.LastIndexByte(line, ')')
	if close < 0 {
		return
	}
	i := close + 1
	for i < len(line) && (line[i] == ' ' || line[i] == '\t' || line[i] == '\r') {
		i++
	}
	s := i
	for i < len(line) && isIdentChar(line[i]) {
		i++
	}
	if i == s {
		return
	}
	name := string(line[s:i])
	if !macros[name] && !isMacroLikeName(name) {
		return
	}
	j := i
	for j < len(line) && (line[j] == ' ' || line[j] == '\t' || line[j] == '\r') {
		j++
	}
	if j < len(line) && line[j] != '{' && line[j] != ';' {
		return
	}
	blankRange(out, pos+s, pos+i)
}

// blankObjectMacroBeforeOperator 置空紧邻 operator 之前的对象式宏。
//
// 例：`bool PUGIXML_FUNCTION operator&&(...)` → `bool              operator&&(...)`。
// 未展开时 tree-sitter 会把 `PUGIXML_FUNCTIONoperator&&` 当作函数名。
func blankObjectMacroBeforeOperator(out, src []byte, pos, eol int, macros map[string]bool) {
	line := src[pos:eol]
	idx := bytes.Index(line, []byte("operator"))
	if idx < 0 {
		return
	}
	j := idx
	for j > 0 && (line[j-1] == ' ' || line[j-1] == '\t') {
		j--
	}
	end := j
	for j > 0 && isIdentChar(line[j-1]) {
		j--
	}
	if j == end {
		return
	}
	name := string(line[j:end])
	if !macros[name] && !isMacroLikeName(name) {
		return
	}
	blankRange(out, pos+j, pos+end)
}

// blankQualifiedFunctionMacro 置空「限定符 + 函数式宏」的返回类型宏。
//
// 例：`static GTEST_ADD_REF_(GTEST_TUPLE_ELEMENT_(0, Tuple))`
//
//	→ `static`（宏及其实参置空），下一行的 `Field(...)` 恢复正常解析。
func blankQualifiedFunctionMacro(out, src []byte, pos, eol int, macros map[string]bool) {
	i := pos
	for i < eol && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	if i >= eol || src[i] == '#' {
		return
	}
	s := i
	for i < eol && isIdentChar(src[i]) {
		i++
	}
	if !declQualifierWords[string(src[s:i])] {
		return
	}
	for i < eol && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	s = i
	for i < eol && isIdentChar(src[i]) {
		i++
	}
	name := string(src[s:i])
	if !macros[name] && !isMacroLikeName(name) {
		return
	}
	for i < eol && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	if i >= eol || src[i] != '(' {
		return
	}
	depth := 0
	end := -1
	for k := i; k < eol; k++ {
		switch src[k] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = k + 1
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return
	}
	blankRange(out, s, end)
}

// blankStandaloneFunctionMacro 置空「整行只有一个函数式宏调用」的宏名与实参。
//
// 例（类体内）：
//
//	GTEST_DISABLE_MSC_WARNINGS_PUSH_(4180)
//
// 未展开时 tree-sitter 会把该调用当错误声明，导致类体大括号失配、后续成员
// 被吞进类内。置空后仅剩空行，语法恢复。分号与行尾注释保留。
func blankStandaloneFunctionMacro(out, src []byte, pos, eol int, macros map[string]bool) {
	i := pos
	for i < eol && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	if i >= eol || src[i] == '#' {
		return
	}
	s := i
	for i < eol && isIdentChar(src[i]) {
		i++
	}
	name := string(src[s:i])
	if !macros[name] && !isMacroLikeName(name) {
		return
	}
	if i >= eol || src[i] != '(' {
		return
	}
	depth := 0
	end := -1
	for k := i; k < eol; k++ {
		switch src[k] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = k + 1
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return
	}
	// 实参之后只允许空白与注释，且**不得带分号**：
	// 带分号的是普通语句宏（如 TEST_ASSERT_*），其调用关系需保留；
	// 不带分号的多为属性/告警注解宏，未展开时才会破坏语法。
	j := end
	for j < eol && (src[j] == ' ' || src[j] == '\t' || src[j] == '\r') {
		j++
	}
	if j < eol && !(src[j] == '/' && j+1 < eol && (src[j+1] == '/' || src[j+1] == '*')) {
		return
	}
	blankRange(out, s, end)
}

// testMacroNames 是「宏调用后紧跟函数体 `{`」的测试框架宏。
// 未展开时 `TEST(a,b) { ... }` 的 body 会成为文件级 compound_statement，
// 内部 lambda/局部变量失去容器（QN 退化为裸 lambda$N / block$N）。
var testMacroNames = map[string]bool{
	"TEST": true, "TEST_F": true, "TEST_P": true,
	"TYPED_TEST": true, "TYPED_TEST_P": true,
}

// normalizeTestMacros 把 `TEST(args) {` 改写为等长的 `void <name>() {`，
// 使测试体恢复为普通函数定义（获得函数容器与可解析 QN）。
// `<name>` 由 args 消毒得到（非标识符字符转 `_`），超长时截断以保持等长。
func normalizeTestMacros(out, src []byte) []byte {
	pos := 0
	for pos < len(src) {
		eol := bytes.IndexByte(src[pos:], '\n')
		if eol < 0 {
			eol = len(src)
		} else {
			eol += pos
		}
		line := src[pos:eol]
		i := 0
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		s := i
		for i < len(line) && isIdentChar(line[i]) {
			i++
		}
		name := string(line[s:i])
		if !testMacroNames[name] || i >= len(line) || line[i] != '(' {
			pos = eol + 1
			continue
		}
		depth := 0
		end := -1
		for k := i; k < len(line); k++ {
			switch line[k] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					end = k + 1
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			pos = eol + 1
			continue
		}
		j := end
		for j < len(line) && (line[j] == ' ' || line[j] == '\t' || line[j] == '\r') {
			j++
		}
		hasBody := j < len(line) && line[j] == '{'
		if !hasBody && j >= len(line) {
			// `{` 可能在下一行（doctest 风格）：向后跳过空白/换行再看一个字符。
			k := eol + 1
			for k < len(src) && (src[k] == ' ' || src[k] == '\t' || src[k] == '\r' || src[k] == '\n') {
				k++
			}
			hasBody = k < len(src) && src[k] == '{'
		}
		if !hasBody {
			pos = eol + 1
			continue
		}
		repl := synthTestFuncDecl(string(line[i+1:end-1]), end-s)
		if repl == "" {
			pos = eol + 1
			continue
		}
		copy(out[pos+s:pos+end], repl)
		pos = eol + 1
	}
	return out
}

// synthTestFuncDecl 由测试宏实参生成等长的 `void <name>()` 声明。
// width 是原 `MACRO(args)` 的字节长度；无法容纳时返回 ""。
//
// 关键约束：Collector 从**原始源码**按 AST 节点字节区间取文本（投影等长特性），
// 因此生成的函数名必须是原始 args 起始处的**同一段字节**——否则 Collector 会
// 读到被改写的错位文本（如 `ProductionCode2, IgnoredTes`）。故 name 取 args 的
// 首个标识符（通常是测试套件名），并保证其字节区间与原文字节一致。
// args 不以标识符开头（如字符串实参的 TEST_CASE）时返回 ""（跳过改写）。
func synthTestFuncDecl(args string, width int) string {
	i := 0
	for i < len(args) && (args[i] == ' ' || args[i] == '\t') {
		i++
	}
	s := i
	if i >= len(args) || !isIdentChar(args[i]) || (args[i] >= '0' && args[i] <= '9') {
		return ""
	}
	for i < len(args) && isIdentChar(args[i]) {
		i++
	}
	name := args[s:i]
	const prefix, suffix = "void ", "()"
	max := width - len(prefix) - len(suffix)
	if max < 1 {
		return ""
	}
	if len(name) > max {
		name = name[:max]
	}
	repl := prefix + name + suffix
	for len(repl) < width {
		repl += " "
	}
	return repl
}

// blankRange 把 [from, to) 置为空格（保留换行）。
func blankRange(b []byte, from, to int) {
	for i := from; i < to && i < len(b); i++ {
		if b[i] != '\n' && b[i] != '\r' {
			b[i] = ' '
		}
	}
}

// isMacroLikeName 判定标识符是否形如宏名：全大写/数字/下划线，且含至少一个下划线。
// 用于识别定义在其它头文件、本文件看不到 #define 的注解宏。
func isMacroLikeName(s string) bool {
	if len(s) < 3 || !bytes.ContainsRune([]byte(s), '_') {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c == '_' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func isIdentChar(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// objectMacroNames 收集文件内定义的对象式宏名（NAME 后不紧跟 `(`）。
func objectMacroNames(src []byte) map[string]bool {
	names := make(map[string]bool)
	for _, m := range objectMacroDefRe.FindAllSubmatch(src, -1) {
		if len(m[2]) != 0 { // 函数式宏
			continue
		}
		names[string(m[1])] = true
	}
	return names
}

// functionMacroNames 收集文件内定义的函数式宏名（NAME 后紧跟 `(`）。
func functionMacroNames(src []byte) map[string]bool {
	names := make(map[string]bool)
	for _, m := range objectMacroDefRe.FindAllSubmatch(src, -1) {
		if len(m[2]) == 0 {
			continue
		}
		names[string(m[1])] = true
	}
	return names
}

// standaloneMacroName 返回「整行只有宏名（可带行/块注释）」时的宏名，否则返回 ""。
func standaloneMacroName(line []byte) string {
	t := bytes.TrimSpace(line)
	if len(t) == 0 || t[0] == '#' {
		return ""
	}
	if i := bytes.Index(t, []byte("//")); i >= 0 {
		t = bytes.TrimSpace(t[:i])
	}
	if i := bytes.Index(t, []byte("/*")); i >= 0 {
		t = bytes.TrimSpace(t[:i])
	}
	if len(t) == 0 {
		return ""
	}
	for i := 0; i < len(t); i++ {
		c := t[i]
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9') {
			return ""
		}
	}
	return string(t)
}

// normalizeProtoMacros 把「返回类型宏」调用 `MACRO(type)` 原位改写为 `type` + 等长空格。
//
// 例：`CJSON_PUBLIC(const char*) cJSON_Version(void);`
//
//	→ `              const char* cJSON_Version(void);`
//
// 改写后语法恢复为普通函数声明，Collector 可正常产出 FUNCTION_DECL。
// 输出与输入等长，Location 无需映射。
func normalizeProtoMacros(src []byte) []byte {
	out := make([]byte, len(src))
	copy(out, src)
	for _, idx := range protoMacroRe.FindAllSubmatchIndex(src, -1) {
		// idx: [fullStart, fullEnd, g1s,g1e, g2s,g2e, g3s,g3e, g4s,g4e, g5s,g5e]
		if len(idx) < 12 {
			continue
		}
		g2s, g3s, g3e := idx[4], idx[6], idx[7]
		inner := src[g3s:g3e]
		if len(inner) == 0 {
			continue
		}
		// 宏调用区间为 [宏名起始, 右括号+1)，即 [g2s, g3e+1)。
		copy(out[g2s:g2s+len(inner)], inner)
		for i := g2s + len(inner); i < g3e+1 && i < len(out); i++ {
			out[i] = ' '
		}
	}
	return out
}

// rebuildWithBlanked 按捕获组重建匹配文本，把第 blankIdx 组替换为等长空格。
// 其余捕获组原样拼接，保证输出长度与输入一致。
func rebuildWithBlanked(m []byte, sub [][]byte, blankIdx int) []byte {
	out := make([]byte, 0, len(m))
	for i := 1; i < len(sub); i++ {
		if i == blankIdx {
			out = append(out, make([]byte, len(sub[i]))...)
			for j := len(out) - len(sub[i]); j < len(out); j++ {
				out[j] = ' '
			}
			continue
		}
		out = append(out, sub[i]...)
	}
	// 长度兜底：若拼接长度异常，回退为原始匹配（不破坏等长约束）。
	if len(out) != len(m) {
		copy(out, m)
		return out[:len(m)]
	}
	return out
}

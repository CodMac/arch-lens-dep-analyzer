package preproc

import "regexp"

// 类型注解宏：位于 class/struct/enum/union 关键字与类型名之间（或类型名之后），
// 形如 `class GTEST_API_ Foo : public Bar {`。tree-sitter 会把这类声明解析成
// function_definition（宏被当作函数名），类型信息因此丢失，事后无法可靠修补。
// 这里在解析前把宏 token 按字节置空格（等长），使语法恢复为正常的类型声明。
//
// 只匹配行首的声明，且要求宏名全大写、后接 `:` 或 `{`，避免误伤普通代码。
// Go regexp 不支持 lookahead，故把结尾的 `:`/`{` 也捕获后原样输出。
var (
	// class <MACRO> Name [: {]
	typeMacroBeforeRe = regexp.MustCompile(`(?m)^([ \t]*(?:class|struct|enum|union)[ \t]+)([A-Z_][A-Z0-9_]*)([ \t]+)([A-Za-z_]\w*)([ \t]*)([:{])`)
	// class Name <MACRO> [: {]
	typeMacroAfterRe = regexp.MustCompile(`(?m)^([ \t]*(?:class|struct|enum|union)[ \t]+)([A-Za-z_]\w*)([ \t]+)([A-Z_][A-Z0-9_]*)([ \t]*)([:{])`)
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
	return out, anns
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

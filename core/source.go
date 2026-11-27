package core

import "bytes"

// NormalizeEOL 把源码中的 CRLF 归一化为 LF。
//
// 解析器在读取源文件后统一调用，使 AST 文本、Mores 中的源码原文（raw_text /
// value_expression 等）与检出环境的换行风格无关：同一份代码在 Windows（CRLF）
// 与 Linux（LF）下产出完全相同的结果与 e2e golden。
func NormalizeEOL(content []byte) []byte {
	if !bytes.Contains(content, []byte("\r\n")) {
		return content
	}
	return bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
}

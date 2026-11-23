package cpp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/CodMac/arch-lens-dep-analyer/core"
	coreast "github.com/CodMac/arch-lens-dep-analyer/core/ast"
	cppast "github.com/CodMac/arch-lens-dep-analyer/x/cpp/ast"
	"github.com/CodMac/arch-lens-dep-analyer/x/cpp/preproc"
	sitter "github.com/tree-sitter/go-tree-sitter"
	treeSitterC "github.com/tree-sitter/tree-sitter-c/bindings/go"
	treeSitterCpp "github.com/tree-sitter/tree-sitter-cpp/bindings/go"
)

// CppParserFactory 实现 core.ParserFactory，创建 C/C++ 语言解析器。
type CppParserFactory struct{}

// NewParser 创建一个 C/C++ parser 实例。
func (f *CppParserFactory) NewParser() core.Parser {
	return &cppParser{parser: sitter.NewParser()}
}

type cppParser struct {
	parser *sitter.Parser
}

// ParseFile 解析指定路径的 C/C++ 源文件。
func (p *cppParser) ParseFile(path string) (coreast.SourceFile, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return p.parseBytes(path, content)
}

// parseBytes 解析源码字节内容。
// 按扩展名选择 grammar：
//   - .c / .h 纯 C 优先用 c grammar（cpp 是 C 超集，.h 默认 cpp 以兼容 C++ 头文件，
//     但纯 C 项目 .c 用 c grammar 可获得与源语言一致的 AST）
//   - .cpp/.hpp/.cc/.cxx 用 cpp grammar
func (p *cppParser) parseBytes(path string, content []byte) (coreast.SourceFile, error) {
	lang := sitter.NewLanguage(languageForPath(path))
	if err := p.parser.SetLanguage(lang); err != nil {
		return nil, err
	}

	// 条件编译投影：把未选中的 #if 分支按字节置空格（等长），
	// 避免预处理分支劈裂语法结构导致 root-level ERROR。
	proj := preproc.Project(content)

	// 原始树：始终解析一次。
	//   - 用于退化检测（替代 projDegraded 内部的二次解析）
	//   - 语法未被条件编译劈裂时，tree-sitter 会把 #if 解析为 preproc_* 节点并
	//     保留全部分支体，rawTree 即「全分支树」，供 Collector/Extractor 补齐
	//   - 需做与投影树相同的类型注解宏归一化，否则 `struct GTEST_API_ Foo`
	//     会被解析成函数，产生错误元素（两棵树必须同口径）
	rawSrc := preproc.NormalizeMacros(content)
	rawTree := p.parser.Parse(rawSrc, nil)
	if rawTree == nil {
		return nil, fmt.Errorf("tree-sitter failed to parse %s", path)
	}

	tree := p.parser.Parse(proj.Source, nil)
	if tree == nil {
		rawTree.Close()
		return nil, fmt.Errorf("tree-sitter failed to parse %s", path)
	}

	// 投影退化检测：投影移除 #if 节点后，tree-sitter 的某些错误恢复会把
	// 后续成千上万行吞进单个节点（如含 "//" 的字符串触发误判）。
	// 此时退回原始源码解析：保留 preproc_* 节点，语法结构正确。
	// 投影区间仍随结果携带（等长特性使区间对原始源码同样有效）。
	degraded := projDegraded(tree.RootNode(), rawTree.RootNode())
	if degraded {
		tree.Close()
		tree = rawTree
		rawTree = nil
	}
	sf := cppast.NewProjectedSourceFile(path, content, tree, &proj)

	// 原始树只有语法完好（无 ERROR）时才是可信的「全分支树」。
	// 含 ERROR 时 tree-sitter 的错误恢复会产出跨条件边界的垃圾节点
	// （如把 `#endif } void f()` 整段吞成函数名），必须弃用。
	rawUsable := rawTree != nil && !rawTree.RootNode().HasError()
	if rawUsable {
		sf.SetRawRoot(rawTree)
	} else if rawTree != nil {
		rawTree.Close()
		rawTree = nil
	}

	// 分支回补：首遍只保留每组一个分支，#elif/#else 独有符号会丢失。
	// 原始树可用时它本身就是全分支树，无需逐分支再投影；
	// 退化时主树已是原始树（同样含全部分支），也无需补；
	// 仅当主树是投影树且原始树含 ERROR 时才按分支序号补几遍。
	if !rawUsable && !degraded {
		if len(proj.Dropped) > 0 {
			for branch := 2; branch <= maxBranchCount(content); branch++ {
				alt := preproc.ProjectWithOptions(content, preproc.Options{
					Pass:         preproc.PassFirst,
					Branch:       branch,
					KeepIncludes: true,
				})
				if altTree := p.parser.Parse(alt.Source, nil); altTree != nil {
					sf.AddAltRoot(altTree)
				}
			}
		}
	}
	return sf, nil
}

// maxBranchCount 返回文件中单个条件组的最大分支数（#if/#elif.../#else）。
// 用于决定分支回补需要额外投影几遍。
func maxBranchCount(src []byte) int {
	return preproc.MaxBranchCount(src)
}

// projDegraded 判断投影后的语法树是否发生「巨型节点吞并」退化。
//
// 判据必须与原始解析对比：合法的整文件 namespace 同样会跨全文件，
// 只有「原始解析没有、投影后才有」的巨型节点才算退化。
func projDegraded(projRoot, rawRoot *sitter.Node) bool {
	if projRoot == nil || rawRoot == nil {
		return false
	}
	if maxTopSpan(projRoot) <= 1000 {
		return false
	}
	// 投影后出现原始解析没有的巨型节点（2 倍且多出 500 行以上）。
	return maxTopSpan(projRoot) > 2*maxTopSpan(rawRoot)+500
}

// maxTopSpan 返回顶层子节点中最大的行跨度。
// namespace_definition 除外：投影把被 #if 劈裂的 namespace 合并成完整节点
// 是期望效果（前缀恢复的来源），不算退化。
func maxTopSpan(root *sitter.Node) int {
	m := 0
	for i := 0; i < int(root.ChildCount()); i++ {
		c := root.Child(uint(i))
		if c.Kind() == "namespace_definition" {
			continue
		}
		if v := int(c.EndPosition().Row) - int(c.StartPosition().Row); v > m {
			m = v
		}
	}
	return m
}

// Close 释放 parser 底层资源。
func (p *cppParser) Close() {
	p.parser.Close()
}

// languageForPath 根据文件扩展名选择 tree-sitter grammar 指针。
func languageForPath(path string) unsafe.Pointer {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".c":
		return treeSitterC.Language()
	case ".cpp", ".hpp", ".cc", ".cxx", ".h":
		// .h 默认 cpp grammar（超集，兼容纯 C 与 C++ 头文件）；
		// 其余 C++ 扩展名用 cpp grammar。
		return treeSitterCpp.Language()
	default:
		// 未知扩展名默认 cpp（超集）。
		return treeSitterCpp.Language()
	}
}

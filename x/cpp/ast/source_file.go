package ast

import (
	coreast "github.com/CodMac/arch-lens-dep-analyer/core/ast"
	"github.com/CodMac/arch-lens-dep-analyer/x/cpp/preproc"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// SourceFile 包装 tree-sitter 语法树，实现 core/ast.SourceFile 接口。
//
// content 始终是**原始源码**：投影后的源码与原始源码等长，因此 AST 节点的
// 字节偏移可直接用于原始源码取文本（Utf8Text 得到真实代码而非空格）。
type SourceFile struct {
	path    string
	content []byte
	tree    *sitter.Tree
	proj    *preproc.Result
	// rawTree 是原始源码（未投影）的解析树。
	// tree-sitter 把 #if/#ifdef 解析为 preproc_ifdef/preproc_else 节点并保留全部
	// 分支体，因此 rawTree 在语法未被条件编译劈裂时就是「全分支树」，可补足
	// 投影树只覆盖单个分支的缺口。退化回退时 rawTree 为 nil（tree 已是原始树）。
	rawTree *sitter.Tree
	rawRoot coreast.Node
	// altRoots 是「分支回补」的补充投影树（Branch=2,3,...）。
	// 仅当 rawTree 不可用（有 ERROR）时才生成，用于补齐 #elif/#else 独有符号。
	altTrees []*sitter.Tree
	altRoots []coreast.Node
}

// NewSourceFile 创建 SourceFile 适配器（无预处理投影）。
func NewSourceFile(path string, content []byte, tree *sitter.Tree) *SourceFile {
	return &SourceFile{path: path, content: content, tree: tree}
}

// NewProjectedSourceFile 创建带条件编译投影信息的 SourceFile。
func NewProjectedSourceFile(path string, content []byte, tree *sitter.Tree, proj *preproc.Result) *SourceFile {
	return &SourceFile{path: path, content: content, tree: tree, proj: proj}
}

// Preproc 返回条件编译投影结果（未投影时为 nil）。
func (sf *SourceFile) Preproc() *preproc.Result { return sf.proj }

// SetRawRoot 设置原始解析树（全分支树）。退化回退时不调用（tree 已是原始树）。
func (sf *SourceFile) SetRawRoot(tree *sitter.Tree) {
	if tree == nil {
		return
	}
	sf.rawTree = tree
	sf.rawRoot = Wrap(tree.RootNode())
}

// RawRoot 返回原始解析树根节点；退化回退时为 nil。
func (sf *SourceFile) RawRoot() coreast.Node { return sf.rawRoot }

// AddAltRoot 追加一棵分支回补投影树。
func (sf *SourceFile) AddAltRoot(tree *sitter.Tree) {
	if tree == nil {
		return
	}
	sf.altTrees = append(sf.altTrees, tree)
	sf.altRoots = append(sf.altRoots, Wrap(tree.RootNode()))
}

// AltRoots 返回分支回补的补充投影树根节点列表。
func (sf *SourceFile) AltRoots() []coreast.Node { return sf.altRoots }

// Root 返回语法树根节点。
func (sf *SourceFile) Root() coreast.Node {
	if sf.tree == nil {
		return nil
	}
	return Wrap(sf.tree.RootNode())
}

// Content 返回源码字节内容。
func (sf *SourceFile) Content() []byte { return sf.content }

// Path 返回文件路径。
func (sf *SourceFile) Path() string { return sf.path }

// Close 释放语法树资源。
func (sf *SourceFile) Close() {
	if sf.tree != nil {
		sf.tree.Close()
		sf.tree = nil
	}
	if sf.rawTree != nil {
		sf.rawTree.Close()
		sf.rawTree = nil
	}
	sf.rawRoot = nil
	for _, t := range sf.altTrees {
		if t != nil {
			t.Close()
		}
	}
	sf.altTrees = nil
	sf.altRoots = nil
}

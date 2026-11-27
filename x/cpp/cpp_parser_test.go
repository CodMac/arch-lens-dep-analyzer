package cpp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseFileNormalizesCRLF 源码换行归一化：CRLF 与 LF 输入解析后内容一致，
// 避免不同机器的检出换行风格传导到 AST 文本与 e2e golden。
func TestParseFileNormalizesCRLF(t *testing.T) {
	dir := t.TempDir()
	crlf := filepath.Join(dir, "a.cpp")
	if err := os.WriteFile(crlf, []byte("int f() {\r\n  return 1;\r\n}\r\n"), 0644); err != nil {
		t.Fatal(err)
	}
	lf := filepath.Join(dir, "b.cpp")
	if err := os.WriteFile(lf, []byte("int f() {\n  return 1;\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	parser := (&CppParserFactory{}).NewParser()
	defer parser.Close()

	crlfFile, err := parser.ParseFile(crlf)
	if err != nil {
		t.Fatal(err)
	}
	lfFile, err := parser.ParseFile(lf)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(crlfFile.Content()), "\r") {
		t.Fatalf("CRLF source not normalized: %q", crlfFile.Content())
	}
	if string(crlfFile.Content()) != string(lfFile.Content()) {
		t.Fatalf("CRLF/LF content differ after normalization:\n%q\n%q", crlfFile.Content(), lfFile.Content())
	}
}

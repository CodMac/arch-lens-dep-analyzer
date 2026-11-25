package collector

import "testing"

// TestNormalizeQualifiedCRLF CRLF 源文件的跨行限定名不得把 \r 写进 QN。
func TestNormalizeQualifiedCRLF(t *testing.T) {
	got := normalizeQualified("TextFormatConformanceTestSuiteImpl<MessageType>::\r     TextFormatConformanceTestSuiteImpl")
	want := "TextFormatConformanceTestSuiteImpl::TextFormatConformanceTestSuiteImpl"
	if got != want {
		t.Fatalf("normalizeQualified = %q, want %q", got, want)
	}
}

// TestNormalizeQualifiedLeadingGlobal 前导全局作用域 `::` 不是 QN 的一部分。
func TestNormalizeQualifiedLeadingGlobal(t *testing.T) {
	cases := map[string]string{
		"::_pbi::DescriptorTable":           "_pbi::DescriptorTable",
		"::google::protobuf::internal::Foo": "google::protobuf::internal::Foo",
		"::std::false_type":                 "std::false_type",
		"app::core::Base":                   "app::core::Base",
	}
	for in, want := range cases {
		if got := normalizeQualified(in); got != want {
			t.Errorf("normalizeQualified(%q) = %q, want %q", in, got, want)
		}
	}
}

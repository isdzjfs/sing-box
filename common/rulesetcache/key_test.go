package rulesetcache

import "testing"

func TestKeyUsesFormatAndURL(t *testing.T) {
	t.Parallel()
	base := Key("binary", "https://example.com/rules.srs")
	if base != Key("binary", "https://example.com/rules.srs") {
		t.Fatal("identical format and URL produced different keys")
	}
	if base == Key("source", "https://example.com/rules.srs") {
		t.Fatal("format did not participate in key")
	}
	if base == Key("binary", "https://example.com/other.srs") {
		t.Fatal("URL did not participate in key")
	}
}

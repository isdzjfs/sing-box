package rule

import (
	"strings"
	"testing"
)

// The expected jsDelivr URLs below were verified to return HTTP 200 with a valid SRS payload.
func TestRuleSetMirrorURLs(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		source   string
		expected string
	}{
		{
			name:     "github raw with refs/heads",
			source:   "https://github.com/MetaCubeX/meta-rules-dat/raw/refs/heads/sing/geo/geosite/cn.srs",
			expected: "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@sing/geo/geosite/cn.srs",
		},
		{
			name:     "github raw with nested path",
			source:   "https://github.com/MetaCubeX/meta-rules-dat/raw/refs/heads/sing/geo-lite/geoip/apple.srs",
			expected: "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@sing/geo-lite/geoip/apple.srs",
		},
		{
			name:     "github raw with hyphenated branch",
			source:   "https://github.com/DustinWin/ruleset_geodata/raw/refs/heads/sing-box-ruleset/spotify.srs",
			expected: "https://cdn.jsdelivr.net/gh/DustinWin/ruleset_geodata@sing-box-ruleset/spotify.srs",
		},
		{
			name:     "github raw without refs/heads",
			source:   "https://github.com/MetaCubeX/meta-rules-dat/raw/sing/geo/geoip/cn.srs",
			expected: "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@sing/geo/geoip/cn.srs",
		},
		{
			name:     "raw.githubusercontent.com",
			source:   "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/refs/heads/sing/geo/geoip/private.srs",
			expected: "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@sing/geo/geoip/private.srs",
		},
		{
			name:     "path containing an exclamation mark",
			source:   "https://github.com/MetaCubeX/meta-rules-dat/raw/refs/heads/sing/geo/geosite/geolocation-!cn.srs",
			expected: "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@sing/geo/geosite/geolocation-!cn.srs",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			urls := ruleSetMirrorURLs(testCase.source)
			if len(urls) == 0 {
				t.Fatalf("no mirrors derived for %s", testCase.source)
			}
			if urls[0] != testCase.expected {
				t.Errorf("first mirror\n got: %s\nwant: %s", urls[0], testCase.expected)
			}
			for _, url := range urls {
				if url == testCase.source {
					t.Errorf("mirror list must not repeat the source URL")
				}
				if !strings.HasSuffix(url, testCase.source[strings.LastIndex(testCase.source, "/"):]) {
					t.Errorf("mirror %s does not preserve the file name", url)
				}
			}
		})
	}
}

// Sources that are not hosted on GitHub have no mirror, which is how they opt out of the rewrite
// without any special casing. anti-ad.net is the real-world example.
func TestRuleSetMirrorURLsNonGitHub(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		"https://anti-ad.net/anti-ad-sing-box.srs",
		"https://example.com/github.com/owner/repo/raw/refs/heads/main/x.srs",
		"http://github.com/owner/repo/raw/refs/heads/main/x.srs", // plain HTTP is not rewritten
	} {
		if urls := ruleSetMirrorURLs(source); len(urls) != 0 {
			t.Errorf("expected no mirrors for %s, got %v", source, urls)
		}
	}
}

func TestRuleSetMirrorURLsAreDistinct(t *testing.T) {
	t.Parallel()
	urls := ruleSetMirrorURLs("https://github.com/MetaCubeX/meta-rules-dat/raw/refs/heads/sing/geo/geosite/cn.srs")
	seen := make(map[string]bool, len(urls))
	for _, url := range urls {
		if seen[url] {
			t.Errorf("duplicate mirror URL %s", url)
		}
		seen[url] = true
	}
	if len(urls) < 2 {
		t.Errorf("expected several mirror edges, got %d", len(urls))
	}
}

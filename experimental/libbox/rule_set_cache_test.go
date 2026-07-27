package libbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sagernet/sing-box/common/rulesetcache"
	"github.com/sagernet/sing/service/filemanager"
)

func TestCollectRuleSetCacheReferences(t *testing.T) {
	workingDirectory := t.TempDir()
	profileDirectory := filepath.Join(workingDirectory, "profiles")
	if err := os.MkdirAll(profileDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	oldWorkingPath, oldTempPath := sWorkingPath, sTempPath
	sWorkingPath, sTempPath = workingDirectory, workingDirectory
	t.Cleanup(func() {
		sWorkingPath, sTempPath = oldWorkingPath, oldTempPath
	})

	profiles := map[string]string{
		"default.json": `{
			"route": {
				"rule_set": [
					{"type":"remote","tag":"one","format":"binary","url":"https://example.com/one.srs"},
					{"type":"local","tag":"local","format":"binary","path":"local.srs"}
				]
			}
		}`,
		"custom.json": `{
			"experimental":{"cache_file":{"path":"custom.db"}},
			"route":{"rule_set":[
				{"type":"remote","tag":["a","b"],"format":"source","url":"https://example.com/{tag}.json"}
			]}
		}`,
	}
	for name, content := range profiles {
		if err := os.WriteFile(filepath.Join(profileDirectory, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	ctx := baseContext(nil)
	referencesByPath, err := collectRuleSetCacheReferences(ctx, profileDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defaultReferences := referencesByPath[filemanager.BasePath(ctx, "cache.db")]
	if _, loaded := defaultReferences[rulesetcache.Key("binary", "https://example.com/one.srs")]; !loaded {
		t.Fatal("missing default cache reference")
	}
	customReferences := referencesByPath[filemanager.BasePath(ctx, "custom.db")]
	for _, tag := range []string{"a", "b"} {
		if _, loaded := customReferences[rulesetcache.Key("source", "https://example.com/"+tag+".json")]; !loaded {
			t.Fatalf("missing custom cache reference for %s", tag)
		}
	}
}

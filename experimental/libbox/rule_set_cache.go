package libbox

import (
	"context"
	stdjson "encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sagernet/sing-box/common/rulesetcache"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/experimental/cachefile"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/service/filemanager"
)

const ruleSetCachePathRegistryName = "rule-set-cache-paths.json"

// PruneRuleSetCache scans every saved profile, then removes stale shared rule-set entries that no
// profile can reference. SenVPN calls this once at process startup, never during VPN reloads.
func PruneRuleSetCache(profileDirectory string) (int32, error) {
	ctx := baseContext(nil)
	referencesByPath, err := collectRuleSetCacheReferences(ctx, profileDirectory)
	if err != nil {
		return 0, err
	}
	defaultCachePath := filemanager.BasePath(ctx, "cache.db")
	if referencesByPath[defaultCachePath] == nil {
		referencesByPath[defaultCachePath] = make(map[string]struct{})
	}

	knownPaths, err := loadRuleSetCachePaths()
	if err != nil {
		return 0, err
	}
	for cachePath := range referencesByPath {
		knownPaths[cachePath] = struct{}{}
	}
	if err = saveRuleSetCachePaths(knownPaths); err != nil {
		return 0, err
	}

	var deleted int
	now := time.Now()
	for cachePath := range knownPaths {
		pruned, pruneErr := cachefile.PruneRuleSetCache(cachePath, referencesByPath[cachePath], now)
		if pruneErr != nil {
			return int32(deleted), E.Cause(pruneErr, "prune rule-set cache ", cachePath)
		}
		deleted += pruned
	}
	return int32(deleted), nil
}

func collectRuleSetCacheReferences(
	ctx context.Context,
	profileDirectory string,
) (map[string]map[string]struct{}, error) {
	referencesByPath := make(map[string]map[string]struct{})
	entries, err := os.ReadDir(profileDirectory)
	if err != nil {
		if os.IsNotExist(err) {
			return referencesByPath, nil
		}
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		profilePath := filepath.Join(profileDirectory, entry.Name())
		content, readErr := os.ReadFile(profilePath)
		if readErr != nil {
			return nil, E.Cause(readErr, "read profile ", entry.Name())
		}
		options, parseErr := parseConfig(ctx, string(content))
		if parseErr != nil {
			// Abort the whole pass rather than treating an unreadable profile as having no
			// references and deleting cache data it may still need after the profile is repaired.
			return nil, E.Cause(parseErr, "parse profile ", entry.Name())
		}
		cachePath := "cache.db"
		if options.Experimental != nil &&
			options.Experimental.CacheFile != nil &&
			options.Experimental.CacheFile.Path != "" {
			cachePath = options.Experimental.CacheFile.Path
		}
		cachePath = filemanager.BasePath(ctx, cachePath)
		references := referencesByPath[cachePath]
		if references == nil {
			references = make(map[string]struct{})
			referencesByPath[cachePath] = references
		}
		if options.Route == nil {
			continue
		}
		for _, ruleSet := range options.Route.RuleSet {
			if ruleSet.Type != C.RuleSetTypeRemote {
				continue
			}
			for _, tag := range ruleSet.Tag {
				sourceURL := strings.ReplaceAll(ruleSet.RemoteOptions.URL, C.RuleSetTagPlaceholder, tag)
				references[rulesetcache.Key(ruleSet.Format, sourceURL)] = struct{}{}
			}
		}
	}
	return referencesByPath, nil
}

func loadRuleSetCachePaths() (map[string]struct{}, error) {
	knownPaths := make(map[string]struct{})
	content, err := os.ReadFile(filepath.Join(sWorkingPath, ruleSetCachePathRegistryName))
	if err != nil {
		if os.IsNotExist(err) {
			return knownPaths, nil
		}
		return nil, err
	}
	var paths []string
	if err = stdjson.Unmarshal(content, &paths); err != nil {
		return nil, E.Cause(err, "decode rule-set cache path registry")
	}
	for _, path := range paths {
		if path != "" {
			knownPaths[path] = struct{}{}
		}
	}
	return knownPaths, nil
}

func saveRuleSetCachePaths(knownPaths map[string]struct{}) error {
	paths := make([]string, 0, len(knownPaths))
	for path := range knownPaths {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	content, err := stdjson.Marshal(paths)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(sWorkingPath, ruleSetCachePathRegistryName), content, 0o600)
}

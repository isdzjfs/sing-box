package cachefile

import (
	"bytes"
	"os"
	"strings"
	"time"

	"github.com/sagernet/bbolt"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/rulesetcache"
	"github.com/sagernet/sing/common"
)

// ruleSetContentKeyLength is the length of a rule-set content key: the hex encoding of a SHA-256
// sum, as produced by the rule-set cache package. Anything else in the shared bucket is a legacy
// tag key.
const ruleSetContentKeyLength = 64

// sweepUnknownBuckets drops buckets left behind by a configuration that no longer declares them,
// both at the top level and inside each cacheID bucket.
//
// Names are collected before anything is deleted. bbolt does not define the behaviour of mutating a
// bucket while a cursor is walking it, which is what deleting from inside ForEach does.
func sweepUnknownBuckets(tx *bbolt.Tx) error {
	var rootNames [][]byte
	err := tx.ForEach(func(name []byte, b *bbolt.Bucket) error {
		if name[0] == 0 {
			var childNames [][]byte
			err := b.ForEachBucket(func(k []byte) error {
				if !common.Contains(bucketNameList, string(k)) {
					childNames = append(childNames, bytes.Clone(k))
				}
				return nil
			})
			if err != nil {
				return err
			}
			for _, childName := range childNames {
				// The child, not the cacheID bucket this is iterating: deleting `name` here looked
				// for a sub-bucket named after the parent, never found one, and left the unknown
				// bucket in place.
				_ = b.DeleteBucket(childName)
			}
			return nil
		}
		bucketName := string(name)
		if !(common.Contains(bucketNameList, bucketName) || strings.HasPrefix(bucketName, fakeipBucketPrefix)) {
			rootNames = append(rootNames, bytes.Clone(name))
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, name := range rootNames {
		_ = tx.DeleteBucket(name)
	}
	return nil
}

// sweepLegacyRuleSetCache drops rule-set payloads written before content addressing.
//
// Entries used to be namespaced by cacheID and keyed by tag. They are now addressed by a hash of
// the format and source URL in one bucket shared across cacheIDs, so no reader can ever reach the
// old ones again. Nothing else removes them either: `rule_set` is a legitimate bucket name, so
// sweepUnknownBuckets treats the stale per-cacheID copies as wanted and keeps them, and bbolt never
// returns freed pages to the filesystem. A rule-set payload is hundreds of kilobytes and there is
// one per rule-set per profile, so leaving them is a permanent and not small cost.
//
// Run on every open rather than behind a version marker: it is a key scan over one bucket, it is
// idempotent, and once there is nothing stale it writes nothing.
func sweepLegacyRuleSetCache(tx *bbolt.Tx) error {
	var cacheIDs [][]byte
	err := tx.ForEach(func(name []byte, b *bbolt.Bucket) error {
		if name[0] == 0 && b.Bucket(bucketRuleSet) != nil {
			cacheIDs = append(cacheIDs, bytes.Clone(name))
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, cacheID := range cacheIDs {
		if bucket := tx.Bucket(cacheID); bucket != nil {
			_ = bucket.DeleteBucket(bucketRuleSet)
		}
	}
	// A deployment without a cache_id wrote its tag-keyed entries into the same top-level bucket the
	// content keys now use, so there those are told apart by the shape of the key rather than by
	// which bucket they are in.
	bucket := tx.Bucket(bucketRuleSet)
	if bucket == nil {
		return nil
	}
	var legacyKeys [][]byte
	err = bucket.ForEach(func(k []byte, _ []byte) error {
		if !isRuleSetContentKey(k) {
			legacyKeys = append(legacyKeys, bytes.Clone(k))
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, key := range legacyKeys {
		_ = bucket.Delete(key)
	}
	return nil
}

// isRuleSetContentKey reports whether key has the shape of a content key. A tag long enough to be
// mistaken for one would also have to be 64 characters of nothing but lowercase hex.
func isRuleSetContentKey(key []byte) bool {
	if len(key) != ruleSetContentKeyLength {
		return false
	}
	for _, char := range key {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

// PruneRuleSetCache removes content-addressed entries that are no longer referenced by any
// profile and have reached the update time recorded with their last successful download.
func PruneRuleSetCache(
	path string,
	referencedKeys map[string]struct{},
	now time.Time,
) (int, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	db, err := bbolt.Open(path, 0o666, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		return 0, err
	}
	defer db.Close()

	var deleted int
	err = db.Update(func(tx *bbolt.Tx) error {
		var pruneErr error
		deleted, pruneErr = pruneUnusedRuleSetCache(tx, referencedKeys, now)
		return pruneErr
	})
	return deleted, err
}

func pruneUnusedRuleSetCache(
	tx *bbolt.Tx,
	referencedKeys map[string]struct{},
	now time.Time,
) (int, error) {
	bucket := tx.Bucket(bucketRuleSet)
	if bucket == nil {
		return 0, nil
	}
	var staleKeys [][]byte
	err := bucket.ForEach(func(key []byte, value []byte) error {
		if !isRuleSetContentKey(key) {
			return nil
		}
		if _, referenced := referencedKeys[string(key)]; referenced {
			return nil
		}
		var savedSet adapter.SavedBinary
		if unmarshalErr := savedSet.UnmarshalBinary(value); unmarshalErr != nil {
			// A malformed entry is left for normal cache recovery. Startup maintenance must never
			// turn a decoding failure into broad or speculative deletion.
			return nil
		}
		updateInterval := savedSet.UpdateInterval
		if updateInterval <= 0 {
			updateInterval = rulesetcache.DefaultUpdateInterval
		}
		if savedSet.LastUpdated.IsZero() || !now.Before(savedSet.LastUpdated.Add(updateInterval)) {
			staleKeys = append(staleKeys, bytes.Clone(key))
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, key := range staleKeys {
		if err = bucket.Delete(key); err != nil {
			return 0, err
		}
	}
	return len(staleKeys), nil
}

package cachefile

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagernet/bbolt"
)

const testContentKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func openTestDB(t *testing.T) *bbolt.DB {
	t.Helper()
	db, err := bbolt.Open(filepath.Join(t.TempDir(), "cache.db"), 0o666, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestIsRuleSetContentKey(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		key      string
		expected bool
	}{
		{testContentKey, true},
		{"geosite-cn", false},
		{"", false},
		{strings.Repeat("a", 63), false},
		{strings.Repeat("a", 65), false},
		// Right length, but uppercase hex is not what hex.EncodeToString produces.
		{strings.ToUpper(testContentKey), false},
		{strings.Repeat("z", 64), false},
	} {
		if got := isRuleSetContentKey([]byte(testCase.key)); got != testCase.expected {
			t.Errorf("isRuleSetContentKey(%q) = %v, want %v", testCase.key, got, testCase.expected)
		}
	}
}

// The per-cacheID copies are unreachable under content addressing and no other sweep removes them,
// because `rule_set` is a legitimate bucket name.
func TestSweepLegacyRuleSetCacheDropsPerCacheIDCopies(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	cacheID := append([]byte{0}, []byte("profile-1")...)
	err := db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(cacheID)
		if err != nil {
			return err
		}
		ruleSets, err := bucket.CreateBucketIfNotExists(bucketRuleSet)
		if err != nil {
			return err
		}
		if err = ruleSets.Put([]byte("geosite-cn"), []byte("payload")); err != nil {
			return err
		}
		// A sibling bucket under the same cacheID must survive.
		selected, err := bucket.CreateBucketIfNotExists(bucketSelected)
		if err != nil {
			return err
		}
		return selected.Put([]byte("group"), []byte("node"))
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err = db.Update(sweepLegacyRuleSetCache); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	err = db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(cacheID)
		if bucket == nil {
			t.Fatal("cacheID bucket was removed")
		}
		if bucket.Bucket(bucketRuleSet) != nil {
			t.Error("stale per-cacheID rule_set bucket survived the sweep")
		}
		selected := bucket.Bucket(bucketSelected)
		if selected == nil || string(selected.Get([]byte("group"))) != "node" {
			t.Error("sweep removed an unrelated bucket under the same cacheID")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// Without a cache_id the old tag-keyed entries share the bucket the content keys now use, so they
// are told apart by key shape.
func TestSweepLegacyRuleSetCacheDropsTagKeysAndKeepsContentKeys(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	err := db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(bucketRuleSet)
		if err != nil {
			return err
		}
		if err = bucket.Put([]byte("geosite-cn"), []byte("legacy")); err != nil {
			return err
		}
		if err = bucket.Put([]byte("geoip-cn"), []byte("legacy")); err != nil {
			return err
		}
		return bucket.Put([]byte(testContentKey), []byte("current"))
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err = db.Update(sweepLegacyRuleSetCache); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	err = db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketRuleSet)
		if bucket == nil {
			t.Fatal("shared rule_set bucket was removed")
		}
		if got := bucket.Get([]byte(testContentKey)); string(got) != "current" {
			t.Errorf("content-addressed entry was lost, got %q", got)
		}
		for _, tag := range []string{"geosite-cn", "geoip-cn"} {
			if bucket.Get([]byte(tag)) != nil {
				t.Errorf("legacy entry %q survived the sweep", tag)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestSweepLegacyRuleSetCacheIsIdempotent(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	err := db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(bucketRuleSet)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(testContentKey), []byte("current"))
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Runs on every open, so a second pass over an already-clean file must be a no-op.
	for range 2 {
		if err = db.Update(sweepLegacyRuleSetCache); err != nil {
			t.Fatalf("sweep: %v", err)
		}
	}

	err = db.View(func(tx *bbolt.Tx) error {
		if got := tx.Bucket(bucketRuleSet).Get([]byte(testContentKey)); string(got) != "current" {
			t.Errorf("repeated sweep changed a live entry, got %q", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// The unknown-bucket sweep used to delete by the parent's name from inside the child iteration, so
// it never actually removed anything nested under a cacheID.
func TestSweepUnknownBucketsRemovesNestedUnknownBucket(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	cacheID := append([]byte{0}, []byte("profile-1")...)
	err := db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(cacheID)
		if err != nil {
			return err
		}
		if _, err = bucket.CreateBucketIfNotExists([]byte("no_longer_declared")); err != nil {
			return err
		}
		_, err = bucket.CreateBucketIfNotExists(bucketSelected)
		return err
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err = db.Update(sweepUnknownBuckets); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	err = db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(cacheID)
		if bucket == nil {
			t.Fatal("cacheID bucket was removed")
		}
		if bucket.Bucket([]byte("no_longer_declared")) != nil {
			t.Error("unknown nested bucket survived the sweep")
		}
		if bucket.Bucket(bucketSelected) == nil {
			t.Error("sweep removed a declared bucket")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
}

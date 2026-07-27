package rulesetcache

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

const DefaultUpdateInterval = 24 * time.Hour

// Key identifies the representation named by a remote rule-set URL. Download transports are
// deliberately excluded so public rule-sets can be reused across profiles and proxy selections.
func Key(format string, url string) string {
	hash := sha256.Sum256([]byte(format + "\x00" + url))
	return hex.EncodeToString(hash[:])
}

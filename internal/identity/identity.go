// Package identity derives stable item IDs and store keys from bookmark URLs.
package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
)

// ItemID returns the stable 12-hex-character item ID for rawURL.
//
// rawURL must be hashed byte-for-byte as Linkding returned it. No
// normalization is permitted: the ID has to stay 1:1 with Linkding's own
// duplicate key.
func ItemID(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(sum[:])[:12]
}

// StoreKey derives the grouping key from rawURL's hostname.
//
// Unlike ItemID, StoreKey normalizes: the hostname is lowercased, one leading
// "www." is removed, and the port is dropped. If rawURL cannot be parsed into
// an absolute URL with a host, StoreKey returns "unknown" and ok=false.
func StoreKey(rawURL string) (key string, ok bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return "unknown", false
	}

	host := strings.ToLower(parsed.Hostname())
	host = strings.TrimPrefix(host, "www.")
	if host == "" {
		return "unknown", false
	}
	return host, true
}

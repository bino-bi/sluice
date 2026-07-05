// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice

import (
	"github.com/bino-bi/sluice/internal/policycache"
)

// cacheKey builds the rewrite-cache key for req when caching is enabled
// and applicable. Caching requires a configured cache, a policy engine
// that can report its snapshot identity, and a clean parse (parseErr nil).
// A zero snapshot version means no snapshot is active, so the request is
// left uncacheable (default-deny still applies downstream).
func (s *Service) cacheKey(req QueryRequest, parseErr error) (policycache.Key, bool) {
	if s.opts.Cache == nil || parseErr != nil {
		return policycache.Key{}, false
	}
	si, ok := s.opts.Policy.(snapshotInfoer)
	if !ok {
		return policycache.Key{}, false
	}
	version, digest, keyHeaders, allHeaders := si.SnapshotInfo()
	if version == 0 {
		return policycache.Key{}, false
	}
	return policycache.BuildKey(version, digest, req.SQL, req.User, req.Facts, keyHeaders, allHeaders), true
}

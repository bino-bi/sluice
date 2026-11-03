// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// Algorithm names the generator algorithms. A change of any algorithm
// that changes the values must change it, so old snapshots show stale.
const Algorithm = "synth/v1"

// SpecHash returns the hash of the parts of spec that change the data:
// "sha256:" and the hex digest of Algorithm and the canonical JSON of
// spec. build, spikeWarning and strictTags are left out.
func SpecHash(spec *apitypes.SyntheticSpec) (string, error) {
	if spec == nil {
		return "", errors.New("synthetic spec is nil")
	}
	// A JSON round trip gives a deep copy that is safe to clear.
	raw, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	var c apitypes.SyntheticSpec
	if err := json.Unmarshal(raw, &c); err != nil {
		return "", err
	}
	c.Build = apitypes.SyntheticBuild{}
	c.Privacy = clearReportOnly(c.Privacy)
	for i := range c.Tables {
		c.Tables[i].Privacy = clearReportOnly(c.Tables[i].Privacy)
		for name, col := range c.Tables[i].Columns {
			col.Privacy = clearReportOnly(col.Privacy)
			c.Tables[i].Columns[name] = col
		}
	}
	// encoding/json writes struct fields in order and map keys sorted.
	canon, err := json.Marshal(&c)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(Algorithm))
	h.Write([]byte{0})
	h.Write(canon)
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// clearReportOnly clears the privacy fields that do not change the data.
// A level left empty becomes nil, so it hashes as if it was not set.
func clearReportOnly(p *apitypes.SyntheticPrivacy) *apitypes.SyntheticPrivacy {
	if p == nil {
		return nil
	}
	p.SpikeWarning = nil
	p.StrictTags = nil
	if p.MinGroupSize == nil && p.ClipOutliers == nil && p.ExtremeJitter == nil && p.RejectExactMatches == nil {
		return nil
	}
	return p
}

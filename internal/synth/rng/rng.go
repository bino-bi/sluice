// SPDX-License-Identifier: AGPL-3.0-or-later

// Package rng gives the synthetic data builder its random streams. Each
// stream is keyed from the seed and the place it is used for, so a new
// column or table does not change the values of the others. The number
// algorithms live here and not in math/rand/v2, whose Float64 and IntN
// may change between Go releases.
package rng

import (
	"crypto/hmac"
	"crypto/sha256"
	"math"
	"math/bits"
	"math/rand/v2"
)

// Version is the domain prefix of every stream key. A change of any
// algorithm in this package must change it.
const Version = "sluice-synth/v1"

// Stream purposes.
const (
	PurposeValues  = "values"
	PurposeNulls   = "nulls"
	PurposeJitter  = "jitter"
	PurposeUnique  = "unique"
	PurposeShuffle = "shuffle"
	PurposeFanOut  = "fanout"
	PurposeOrphans = "orphans"
	PurposeCopula  = "copula"
	PurposeNoise   = "noise"
	PurposeSample  = "sample"
)

// JitterBy is the jitter purpose of one by-group. key is the group key
// (profile.GroupKey); it goes into the HMAC input only.
func JitterBy(key string) string { return "jitter-by:" + key }

// Key derives the key of one stream from the seed.
func Key(seed []byte, name, table, column, purpose string) [32]byte {
	m := hmac.New(sha256.New, seed)
	for i, s := range []string{Version, name, table, column, purpose} {
		if i > 0 {
			m.Write([]byte{0})
		}
		m.Write([]byte(s))
	}
	var k [32]byte
	copy(k[:], m.Sum(nil))
	return k
}

// Stream is a deterministic source of random numbers.
type Stream struct {
	src *rand.ChaCha8
}

// New returns the stream for key.
func New(key [32]byte) *Stream {
	return &Stream{src: rand.NewChaCha8(key)}
}

// For returns the stream of one purpose. It is New(Key(...)).
func For(seed []byte, name, table, column, purpose string) *Stream {
	return New(Key(seed, name, table, column, purpose))
}

// String and GoString keep the generator state, which is derived from
// the seed, out of formatted logs and errors.
func (s *Stream) String() string { return "rng.Stream" }

// GoString implements fmt.GoStringer, see String.
func (s *Stream) GoString() string { return "rng.Stream" }

// Uint64 returns 64 random bits.
func (s *Stream) Uint64() uint64 { return s.src.Uint64() }

// Float64 returns a number in [0, 1) with 53 random bits.
func (s *Stream) Float64() float64 {
	return float64(s.src.Uint64()>>11) * 0x1p-53
}

// IntN returns a number in [0, n). It panics when n <= 0.
func (s *Stream) IntN(n int) int {
	if n <= 0 {
		panic("rng: IntN with n <= 0")
	}
	// Lemire's multiply and shift with rejection, so every result has the
	// same probability.
	un := uint64(n)
	hi, lo := bits.Mul64(s.src.Uint64(), un)
	if lo < un {
		thresh := -un % un
		for lo < thresh {
			hi, lo = bits.Mul64(s.src.Uint64(), un)
		}
	}
	return int(hi)
}

// Norm returns a standard normal number (Box-Muller, two draws).
func (s *Stream) Norm() float64 {
	u1 := 1 - s.Float64() // in (0, 1], so the log is finite
	u2 := s.Float64()
	// The explicit conversions stop the compiler from fusing the products
	// into FMA instructions, which would change the last bit on arm64.
	r := math.Sqrt(float64(-2 * math.Log(u1)))
	return float64(r * math.Cos(float64(2*math.Pi*u2)))
}

// Laplace returns a Laplace(0, b) number: the difference of two
// exponential draws. 1 - Float64() is in (0, 1], so the logs are finite.
func (s *Stream) Laplace(b float64) float64 {
	u1 := 1 - s.Float64()
	u2 := 1 - s.Float64()
	return float64(b * (math.Log(u2) - math.Log(u1)))
}

// Package ids generates prefixed, time-sortable identifiers.
package ids

import (
	"crypto/rand"
	"encoding/binary"
	"time"
)

// alphabet is lowercase Crockford-style base32 (no i, l, o, u) so IDs are
// easy to read aloud and safe in URLs.
const alphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// New returns an ID of the form "<prefix>_<time><random>", where the time
// component sorts lexicographically by millisecond and the random suffix
// carries 60 bits of entropy.
func New(prefix string) string {
	ms := uint64(time.Now().UnixMilli())
	var tb [8]byte
	binary.BigEndian.PutUint64(tb[:], ms)

	buf := make([]byte, 0, len(prefix)+1+9+12)
	buf = append(buf, prefix...)
	buf = append(buf, '_')
	// 45 bits of time (9 base32 chars) covers ~1100 years of milliseconds.
	for i := range 9 {
		shift := uint(5 * (8 - i))
		buf = append(buf, alphabet[(ms>>shift)&31])
	}
	buf = append(buf, Random(12)...)
	return string(buf)
}

// Random returns n random characters from the base32 alphabet.
func Random(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("ids: crypto/rand unavailable: " + err.Error())
	}
	for i := range b {
		b[i] = alphabet[int(b[i])&31]
	}
	return string(b)
}

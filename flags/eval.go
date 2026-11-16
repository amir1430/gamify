// Package flags is the opt-in module for feature flags that operators create in the admin
// console (flagsadmin), with no deploy: an on/off switch, a percentage rollout and a per-flag list
// of testers. Game clients read the caller's flags with the RPC flags.get; Go code reads them with
// Service.Bool. Reads use an in-memory copy, reloaded every Config.ReloadEvery.
//
// A player's value for a flag, in this order: no flag with the key → the reader's fallback; flag
// off → false, testers included; a tester → true; else true for percent% of players.
//
// Who is in the percent comes from a frozen hash of the flag's key and the player's ID: raising
// the percent only adds players, and each flag picks its own players. Changing the hash would
// reshuffle every rollout in production, so its test vectors never change.
package flags

import (
	"crypto/sha256"
	"encoding/binary"
	"strconv"
)

// bucket is the player's fixed place in a flag's rollout, 0..9999. Frozen.
func bucket(key string, userID int64) uint64 {
	sum := sha256.Sum256([]byte(key + ":" + strconv.FormatInt(userID, 10)))
	return binary.BigEndian.Uint64(sum[:8]) % 10000
}

// entry is one flag in the in-memory snapshot.
type entry struct {
	id      int64
	key     string
	enabled bool
	percent int
	testers map[int64]struct{}
}

// value is the flag's value for the player.
func (e entry) value(userID int64) bool {
	if !e.enabled {
		return false
	}
	if _, ok := e.testers[userID]; ok {
		return true
	}
	return bucket(e.key, userID) < uint64(e.percent)*100
}

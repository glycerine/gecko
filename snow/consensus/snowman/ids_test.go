// (c) 2019-2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package snowman

import (
	"github.com/ava-labs/gecko/ids"
	"github.com/ava-labs/gecko/snow/choices"
)

var (
	Genesis = &Blk{
		id:     ids.Empty.Prefix(0),
		status: choices.Accepted,
	}
)

func Matches(a, b []ids.ID) bool {
	if len(a) != len(b) {
		return false
	}
	set := ids.Set{}
	set.Add(a...)
	for _, id := range b {
		if !set.Contains(id) {
			return false
		}
	}
	return true
}
func MatchesShort(a, b []ids.ShortID) bool {
	if len(a) != len(b) {
		return false
	}
	set := ids.ShortSet{}
	set.Add(a...)
	for _, id := range b {
		if !set.Contains(id) {
			return false
		}
	}
	return true
}

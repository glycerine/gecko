// (c) 2019-2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package secp256k1fx

import (
	"errors"
)

var (
	errNoValueOutput = errors.New("output has no value")
)

// TransferOutput ...
type TransferOutput struct {
	Amt      uint64 `serialize:"true"`
	Locktime uint64 `serialize:"true"`

	OutputOwners `serialize:"true"`
}

// Amount returns the quantity of the asset this output consumes
func (out *TransferOutput) Amount() uint64 { return out.Amt }

// Verify ...
func (out *TransferOutput) Verify() error {
	switch {
	case out == nil:
		return errNilOutput
	case out.Amt == 0:
		return errNoValueInput
	default:
		return out.OutputOwners.Verify()
	}
}

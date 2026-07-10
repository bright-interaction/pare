// SPDX-License-Identifier: LicenseRef-Pare-Sustainable-Use-License
// Copyright (c) Bright Interaction

package ledger

import "fmt"

// Amount is money in minor units (öre). 1 SEK = 100 öre. Money is never a
// float; all arithmetic stays in integer öre.
type Amount int64

// SEK builds an Amount from whole kronor and öre.
func SEK(kronor int64, ore int64) Amount {
	return Amount(kronor*100 + ore)
}

// String renders the amount as "kr,öre" (Swedish decimal comma).
func (a Amount) String() string {
	v, sign := int64(a), ""
	if v < 0 {
		v, sign = -v, "-"
	}
	return fmt.Sprintf("%s%d,%02d", sign, v/100, v%100)
}

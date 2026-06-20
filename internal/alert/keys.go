package alert

import "fmt"

const statePriceKey = "STATE#PRICE"

// OwnerPK is the partition key for a tester's alerts: the API key is the tenant.
func OwnerPK(ownerID string) string { return "OWNER#" + ownerID }

// AlertSK is the sort key for a single alert.
func AlertSK(id string) string { return "ALERT#" + id }

// StatePK / StateSK address the singleton last-price item in the same table.
func StatePK() string { return statePriceKey }
func StateSK() string { return statePriceKey }

// GSIPK is the sparse GSI partition key for armed alerts, split by direction.
func GSIPK(d Direction) string { return "ARMED#" + string(d) }

// GSISK formats a price as a fixed-width, zero-padded decimal so that
// lexicographic ordering on the GSI sort key matches numeric ordering.
// Width is 11 chars: 8 integer digits + '.' + 2 decimals (covers up to 99,999,999.99).
func GSISK(price float64) string { return fmt.Sprintf("%011.2f", price) }

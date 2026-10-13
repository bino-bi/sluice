# SPDX-License-Identifier: CC-BY-4.0
package sluice

import rego.v1

# No opinion by default. The YAML policies decide.
default main := {"abstain": true}

main := {
	"allow": false,
	"deny_reason": {"message": "customers and payments must not be read together"},
} if {
	reads("shop.main.customers")
	reads("shop.main.payments")
}

reads(key) if {
	some t in input.tables
	t.key == key
}

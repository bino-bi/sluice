# SPDX-License-Identifier: CC-BY-4.0
package sluice

import rego.v1

default main := {
    "allow": false,
    "deny_reason": {"message": "only analysts read the shop catalog"},
}

# Analysts read the shop catalog. Each table gets a filter on the tenant of the subject.
main := {
    "allow": true,
    "row_filters": [tenant_filter(t.key) | some t in input.tables],
} if {
    "analysts" in input.subject.groups
    every t in input.tables {
        t.catalog == "shop"
    }
}

tenant_filter(table) := {
    "table": table,
    "predicate": {
        "column": "tenant_id",
        "op": "Equals",
        "value": input.subject.claims.tenantId,
    },
}

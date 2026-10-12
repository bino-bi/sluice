<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# rebac

A `RelationshipPolicy` asks OpenFGA if a subject is a `viewer` of a table.

## Run

```bash
examples/rebac/serve.sh
```

The script creates `data/shop.db` from `seed.sql`. It starts `openfga-stub.py` on port 8091 and Sluice on `http://localhost:8080`. Then run the steps in order from this folder.

| Step | Shows |
|---|---|
| `steps/01-alice.sh` | Alice is a viewer of `orders` and reads it. |
| `steps/02-bob.sh` | Bob has no relation. The query is denied. |
| `steps/03-check-request.sh` | The check that Sluice sent for Bob. |

`openfga-stub.py` stands in for an OpenFGA server. It answers the check endpoint from `tuples.json` and stores each request in `data/checks.jsonl`. To use a real server, change `backend.endpoint` and `backend.storeId` in `policies.d/relationship.yaml`.

`expected/` holds the real output of each step. See [the examples guide](../README.md) for the layout.

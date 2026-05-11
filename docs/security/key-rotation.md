<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Key rotation

Three long-lived secrets power Sluice. Each has its own rotation story.

## API-key pepper (`identity.apiKeyPepperRef`)

Used as the HMAC key over the key material. Rotating the pepper
invalidates every issued API key.

1. Stand up a second Sluice replica with the new pepper and a second
   `identity.apiKeyPepperRef` listed as an accepted verifier (v0.3 —
   until then, stage rotation via a short maintenance window).
2. Reissue API keys to users.
3. Drop the old pepper.

## JWKS / SubjectBinding

JWKS is pulled from the issuer on demand and cached with a 10 min TTL.
To roll a signing key:

1. Publish the new key alongside the old one in the issuer's JWKS
   endpoint.
2. Issue tokens signed by the new `kid`.
3. After the old tokens expire, drop the old `kid` from the JWKS.

Sluice will pick up the new `kid` automatically on the next unknown-kid
miss (rate-limited refresh with a 30 s floor).

## Audit genesis (`audit.file.genesisRef`)

The genesis seed anchors the first record of the chain. Rotating it
starts a new chain — existing files are unaffected but cannot be
replayed together with the new ones.

1. Verify the current chain (`sluice audit verify`).
2. Freeze writes (stop the Sluice replica).
3. Archive the current audit directory.
4. Generate a new seed (`openssl rand -hex 32`).
5. Boot with the new genesisRef; a fresh chain starts.

Record the old genesis seed in your vault alongside the archived
files — you will need it to verify history later.

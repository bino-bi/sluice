# --8<-- [start:pull]
VERSION=0.1.0-rc.3
docker pull ghcr.io/bino-bi/sluice:${VERSION}
docker run --rm ghcr.io/bino-bi/sluice:${VERSION} version
# --8<-- [end:pull]

# --8<-- [start:cosign]
cosign verify ghcr.io/bino-bi/sluice:${VERSION} \
  --certificate-identity-regexp 'https://github.com/bino-bi/sluice/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
# --8<-- [end:cosign]

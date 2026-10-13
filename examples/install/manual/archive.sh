# --8<-- [start:download]
VERSION=0.1.0-rc.3
BASE=https://github.com/bino-bi/sluice/releases/download/v${VERSION}
curl -fsSLO ${BASE}/sluice_${VERSION}_linux_amd64.tar.gz
curl -fsSLO ${BASE}/checksums.txt
sha256sum --check --ignore-missing checksums.txt
tar -xzf sluice_${VERSION}_linux_amd64.tar.gz
./sluice version
# --8<-- [end:download]

# --8<-- [start:cosign]
curl -fsSLO ${BASE}/checksums.txt.sig
curl -fsSLO ${BASE}/checksums.txt.pem
cosign verify-blob \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity-regexp 'https://github.com/bino-bi/sluice/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
# --8<-- [end:cosign]

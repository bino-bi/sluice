# Use printf, not echo. A newline at the end changes the hash.
sluice audit verify data/audit \
  --anchor "$(printf '%s' audit-log-demo-genesis | sha256sum | cut -d' ' -f1)"

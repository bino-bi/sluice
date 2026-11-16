# --8<-- [start:up]
docker build -t sluice:dev .
cd examples/hello-sluice
python3 ../seed.py seed.sql data/shop.db
# The container runs as user 65532 and writes the audit log to data/.
sudo chown -R 65532:65532 data
SLUICE_IMAGE=sluice:dev docker compose up
# --8<-- [end:up]

# --8<-- [start:cli]
docker compose exec sluice /usr/local/bin/sluice audit verify data/audit
# --8<-- [end:cli]

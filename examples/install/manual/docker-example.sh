# --8<-- [start:up]
docker build -t sluice:dev .
cd examples/hello-sluice
python3 ../seed.py seed.sql data/shop.db
SLUICE_IMAGE=sluice:dev docker compose up
# --8<-- [end:up]

# --8<-- [start:cli]
docker compose exec sluice /usr/local/bin/sluice audit verify data/audit
# --8<-- [end:cli]

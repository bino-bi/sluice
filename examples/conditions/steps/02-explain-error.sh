sluice policy explain --policies-dir policies.d \
  --user bob --groups analysts \
  --sql "SELECT id, card_number FROM shop.main.payments"

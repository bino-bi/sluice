SELECT c.id, email.total_cents FROM shop.main.customers c JOIN shop.main.orders email ON email.customer_id = c.id

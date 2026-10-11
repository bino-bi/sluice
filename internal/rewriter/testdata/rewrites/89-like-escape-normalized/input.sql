SELECT o.id FROM pg.public.orders o WHERE o.status LIKE 'open!_%' ESCAPE '!' OR o.note NOT ILIKE '%#%%' ESCAPE '#'

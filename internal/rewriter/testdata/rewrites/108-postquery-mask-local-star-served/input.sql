SELECT e.id FROM pg.hr.employees e WHERE e.id IN (SELECT e.* FROM (SELECT id FROM pg.hr.badge) e)

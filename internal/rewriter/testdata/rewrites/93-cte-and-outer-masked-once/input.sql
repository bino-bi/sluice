WITH employees AS (SELECT id, email FROM pg.hr.employees) SELECT employees.email, x.email AS again FROM employees JOIN pg.hr.employees x ON x.id = employees.id

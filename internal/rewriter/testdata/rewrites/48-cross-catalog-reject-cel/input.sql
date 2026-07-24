SELECT p.name, m.salary FROM pg.hr.person p JOIN mysql_erp.hr.compensation m ON p.id = m.person_id

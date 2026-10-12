SELECT x.ssn FROM pg.hr.employees e, LATERAL (SELECT e.*) x

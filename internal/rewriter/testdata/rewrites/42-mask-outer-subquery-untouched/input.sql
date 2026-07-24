SELECT ssn FROM pg.hr.emp WHERE dept IN (SELECT dept FROM pg.hr.mgr)

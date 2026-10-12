// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !race

package pgquery_test

func raceDisableSync() {}
func raceEnableSync()  {}

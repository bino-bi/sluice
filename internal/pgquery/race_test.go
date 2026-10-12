// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build race

package pgquery_test

import "runtime"

func raceDisableSync() { runtime.RaceDisable() }
func raceEnableSync()  { runtime.RaceEnable() }

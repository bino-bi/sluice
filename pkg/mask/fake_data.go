// SPDX-License-Identifier: Apache-2.0

package mask

// Curated deterministic word lists for the fake mask. These are small on
// purpose: the fake mask replaces a real value with a plausible-looking
// stand-in, and a keyed hash selects the entry so the same input always
// maps to the same fake (joins stay consistent). A large random-data
// dependency (gofakeit et al.) is deliberately avoided.

var fakeFirstNames = []string{
	"Alex", "Blair", "Casey", "Devon", "Ellis", "Finley", "Gray", "Harper",
	"Indigo", "Jordan", "Kai", "Lane", "Morgan", "Noah", "Oakley", "Parker",
	"Quinn", "Riley", "Sage", "Tatum", "Uma", "Val", "Wren", "Xen", "Yael", "Zion",
}

var fakeLastNames = []string{
	"Adams", "Brooks", "Carter", "Diaz", "Evans", "Foster", "Gomez", "Hayes",
	"Ingram", "Jensen", "Klein", "Lopez", "Meyer", "Nolan", "Owens", "Price",
	"Quezada", "Reyes", "Stone", "Tran", "Underwood", "Vega", "Walsh", "Xu", "Young", "Zhang",
}

var fakeCompanies = []string{
	"Acme", "Globex", "Initech", "Umbrella", "Hooli", "Vehement", "Massive",
	"Vandelay", "Stark", "Wayne", "Wonka", "Cyberdyne", "Soylent", "Gringotts",
}

var fakeCities = []string{
	"Springfield", "Rivertown", "Lakeview", "Fairfax", "Kingsport", "Northgate",
	"Westhaven", "Eastwood", "Millbrook", "Ashford", "Cedarville", "Brookfield",
}

var fakeCountries = []string{
	"Aldland", "Bracia", "Cadonia", "Doria", "Estara", "Ferland", "Galvia",
	"Haldistan", "Iberis", "Jorvik", "Kelmar", "Lorien",
}

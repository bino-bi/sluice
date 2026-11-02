// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

// Vocabularies of loremText and randomText. The fake word lists live in
// pkg/mask (mask.FakeWords), so the fake mask and the fake generator
// share them.

var loremWords = []string{
	"lorem", "ipsum", "dolor", "sit", "amet", "consectetur", "adipiscing", "elit",
	"sed", "do", "eiusmod", "tempor", "incididunt", "ut", "labore", "et",
	"dolore", "magna", "aliqua", "enim", "ad", "minim", "veniam", "quis",
	"nostrud", "exercitation", "ullamco", "laboris", "nisi", "aliquip", "ex", "ea",
	"commodo", "consequat", "duis", "aute", "irure", "in", "reprehenderit", "voluptate",
	"velit", "esse", "cillum", "fugiat", "nulla", "pariatur", "excepteur", "sint",
	"occaecat", "cupidatat", "non", "proident", "sunt", "culpa", "qui", "officia",
	"deserunt", "mollit", "anim", "id", "est", "laborum", "perspiciatis", "unde",
	"omnis", "iste", "natus", "error", "voluptatem", "accusantium", "doloremque", "laudantium",
	"totam", "rem", "aperiam", "eaque", "ipsa", "quae", "ab", "illo",
	"inventore", "veritatis", "quasi", "architecto", "beatae", "vitae", "dicta", "explicabo",
	"nemo", "ipsam", "quia", "voluptas", "aspernatur", "aut", "odit", "fugit",
	"consequuntur", "magni", "dolores", "eos", "ratione", "sequi", "nesciunt", "neque",
	"porro", "quisquam", "dolorem", "adipisci", "numquam", "eius", "modi", "tempora",
	"incidunt", "magnam", "quaerat", "minima", "nostrum", "exercitationem", "ullam", "corporis",
	"suscipit", "laboriosam", "aliquid", "commodi", "autem", "vel", "eum", "iure",
	"quam", "nihil", "molestiae", "illum", "facilis", "expedita", "distinctio", "libero",
	"tempore", "soluta", "nobis", "eligendi", "optio", "cumque", "impedit", "quo",
	"minus", "maxime", "placeat", "facere", "possimus", "assumenda", "repellendus", "temporibus",
}

var englishWords = []string{
	"the", "of", "and", "to", "in", "is", "it", "that",
	"was", "for", "on", "are", "with", "as", "they", "be",
	"at", "one", "have", "this", "from", "by", "word", "but",
	"what", "some", "we", "can", "out", "other", "were", "all",
	"there", "when", "up", "use", "your", "how", "said", "each",
	"time", "will", "way", "about", "many", "then", "them", "write",
	"would", "like", "so", "these", "long", "make", "thing", "see",
	"him", "two", "has", "look", "more", "day", "could", "go",
	"come", "did", "number", "sound", "most", "people", "over", "know",
	"water", "than", "call", "first", "who", "may", "down", "side",
	"been", "now", "find", "any", "new", "work", "part", "take",
	"get", "place", "made", "live", "where", "after", "back", "little",
	"only", "round", "man", "year", "came", "show", "every", "good",
	"give", "our", "under", "name", "very", "through", "just", "form",
	"sentence", "great", "think", "say", "help", "low", "line", "differ",
	"turn", "cause", "much", "mean", "before", "move", "right", "boy",
	"old", "too", "same", "tell", "does", "set", "three", "want",
	"air", "well", "also", "play", "small", "end", "put", "home",
	"read", "hand", "port", "large", "spell", "add", "even", "land",
}

var germanWords = []string{
	"der", "die", "und", "in", "den", "von", "zu", "das",
	"mit", "sich", "des", "auf", "ist", "im", "nicht", "ein",
	"als", "auch", "es", "an", "werden", "aus", "er", "hat",
	"dass", "sie", "nach", "wird", "bei", "einer", "um", "am",
	"sind", "noch", "wie", "einem", "über", "einen", "so", "zum",
	"war", "haben", "nur", "oder", "aber", "vor", "zur", "bis",
	"mehr", "durch", "man", "dann", "soll", "wenn", "schon", "kann",
	"gegen", "vom", "können", "zwei", "jahr", "haus", "zeit", "tag",
	"weg", "stadt", "land", "welt", "leben", "arbeit", "frage", "bild",
	"seite", "teil", "stunde", "woche", "monat", "wasser", "geld", "buch",
	"straße", "schule", "kind", "frau", "mann", "freund", "name", "wort",
	"ende", "anfang", "grund", "beispiel", "recht", "ort", "raum", "ziel",
	"gut", "neu", "groß", "klein", "alt", "lang", "kurz", "hoch",
	"weit", "schnell", "leicht", "schwer", "früh", "spät", "warm", "kalt",
	"sehen", "geben", "gehen", "kommen", "machen", "sagen", "finden", "bleiben",
	"liegen", "stehen", "denken", "nehmen", "tun", "dürfen", "halten", "nennen",
	"zeigen", "führen", "sprechen", "bringen", "laufen", "fahren", "meinen", "fragen",
	"kennen", "gelten", "stellen", "spielen", "arbeiten", "brauchen", "folgen", "lernen",
	"heute", "immer", "wieder", "hier", "dort", "oft", "gern", "bald",
}

var streetSuffixes = []string{"Street", "Road", "Avenue", "Lane", "Way"}

func vocabulary(name string) []string {
	switch name {
	case "en":
		return englishWords
	case "de":
		return germanWords
	}
	return loremWords
}

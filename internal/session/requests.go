package session

import (
	"strings"

	"deepflow/internal/domain"
)

var (
	questionWords = map[string]bool{"what": true, "why": true, "how": true, "when": true, "where": true, "who": true, "does": true, "is": true, "are": true, "explain": true}
	negations     = map[string]bool{"no": true, "not": true, "dont": true, "don't": true, "stop": true, "never": true}
	requestCues   = map[string]bool{"give": true, "make": true, "want": true, "start": true, "play": true, "show": true, "lets": true, "let's": true, "another": true, "new": true, "can": true, "could": true, "create": true, "try": true, "open": true, "please": true, "load": true, "need": true}
	gameWords     = map[string]bool{"game": true, "games": true, "experiment": true, "simulation": true, "play": true}
)

// DetectCardRequest spots a student asking for a card directly, such as "give me
// a game". It's deliberately simple and needs no model: an explicit request has
// to work immediately, however the Evaluator is doing.
func DetectCardRequest(text string) (domain.CellType, bool) {
	lower := strings.ToLower(strings.TrimSpace(text))
	words := strings.FieldsFunc(lower, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '\'')
	})
	if len(words) == 0 || questionWords[words[0]] || strings.Contains(lower, "game theory") {
		return "", false
	}
	cue := len(words) <= 3
	var target domain.CellType
	for _, w := range words {
		if negations[w] {
			return "", false
		}
		cue = cue || requestCues[w]
		switch {
		case w == "quiz" || w == "mcq" || w == "mcqs":
			target = domain.CellQuiz
		case gameWords[w] && target == "":
			target = domain.CellGame
		case w == "flashcard" || w == "flashcards":
			target = domain.CellConcept
		case w == "map" || w == "maps":
			target = domain.CellMap
		}
	}
	for phrase, t := range map[string]domain.CellType{
		"test me": domain.CellQuiz, "another question": domain.CellQuiz, "next question": domain.CellQuiz, "key idea": domain.CellConcept,
	} {
		if strings.Contains(lower, phrase) {
			target = t
		}
	}
	if target == "" || !cue {
		return "", false
	}
	return target, true
}

var mapWords = map[string]bool{
	"history": true, "historical": true, "geography": true, "empire": true, "empires": true, "dynasty": true, "dynasties": true,
	"civilisation": true, "civilisations": true, "civilization": true, "civilizations": true, "war": true, "wars": true,
	"revolution": true, "colonial": true, "colonialism": true, "ancient": true, "medieval": true, "mughal": true, "mughals": true,
	"maurya": true, "mauryan": true, "gupta": true, "river": true, "rivers": true, "mountain": true, "mountains": true,
	"continent": true, "continents": true, "countries": true, "capitals": true, "climate": true, "ocean": true, "oceans": true,
	"map": true, "maps": true, "atlas": true, "monsoon": true, "monsoons": true, "explorers": true, "exploration": true,
}

// IsMapSubject spots subjects where a map helps, from the words alone so it's
// ready immediately. The model catches what this misses once it writes the index.
func IsMapSubject(title, inquiry string) bool {
	words := strings.FieldsFunc(strings.ToLower(title+" "+inquiry), func(r rune) bool { return !(r >= 'a' && r <= 'z') })
	for _, w := range words {
		if mapWords[w] {
			return true
		}
	}
	return false
}

package evaluator

import (
	"encoding/json"
	"unicode/utf8"

	"deepflow/internal/domain"
)

// substantiveLength is the rune count at which a dialogue message can count
// as an explanation rather than a passing remark.
const substantiveLength = 80

// Guard deterministically limits what the model can claim about understanding.
// Evidence must come from something the learner wrote, durable evidence must
// come from a retrieval of an earlier session's learning, and strong evidence
// needs a submission or substantive explanation. It returns the adjusted claim,
// or false if the claim must be discarded.
func Guard(c EvidenceClaim, src domain.Event) (EvidenceClaim, bool) {
	switch domain.EvidenceTier(c.Tier) {
	case domain.TierWeak, domain.TierModerate, domain.TierStrong, domain.TierDurable:
	default:
		return c, false
	}
	if !src.Type.LearnerAuthored() {
		return c, false
	}
	if domain.EvidenceTier(c.Tier) == domain.TierDurable && !fromEarlierSession(src) {
		c.Tier = string(domain.TierStrong)
	}
	if domain.EvidenceTier(c.Tier) == domain.TierStrong && !isSubmission(src) && utf8.RuneCountInString(eventText(src)) < substantiveLength {
		c.Tier = string(domain.TierModerate)
	}
	return c, true
}

func fromEarlierSession(e domain.Event) bool {
	if e.Type != domain.EvRetrievalAnswered {
		return false
	}
	var p struct {
		FromSession string `json:"fromSession"`
	}
	_ = json.Unmarshal(e.Payload, &p)
	return p.FromSession != "" && p.FromSession != e.SessionID
}

func isSubmission(e domain.Event) bool {
	switch e.Type {
	case domain.EvQuizAnswered, domain.EvThoughtSubmitted, domain.EvRetrievalAnswered:
		return true
	}
	return false
}

func eventText(e domain.Event) string {
	var p struct {
		Text    string `json:"text"`
		Concept string `json:"concept"`
		Reason  string `json:"reason"`
	}
	_ = json.Unmarshal(e.Payload, &p)
	switch {
	case p.Text != "":
		return p.Text
	case p.Concept != "":
		return p.Concept
	}
	return p.Reason
}

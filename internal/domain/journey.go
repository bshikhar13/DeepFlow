package domain

// StepsPerTopic is how many achievements finish a topic on the journey.
const StepsPerTopic = 3

// Journey is a pursuit's index and how far the learner has come. Steps are
// earned by doing (a correct quiz answer, a game level), never by reading.
type Journey struct {
	Topics  []JourneyTopic `json:"topics"`
	Current int            `json:"current"`
	// UsesMaps is the model's view of whether places matter to this subject.
	UsesMaps bool `json:"usesMaps,omitempty"`
}

// JourneyTopic is one stop on the path.
type JourneyTopic struct {
	Title     string `json:"title"`
	Milestone string `json:"milestone"`
	Progress  int    `json:"progress"`
	Goal      int    `json:"goal"`
	Done      bool   `json:"done"`
}

// Complete reports whether every topic is finished.
func (j *Journey) Complete() bool { return j.Current >= len(j.Topics) }

// CurrentTopic returns the topic in progress, if any.
func (j *Journey) CurrentTopic() (JourneyTopic, bool) {
	if j == nil || j.Complete() {
		return JourneyTopic{}, false
	}
	return j.Topics[j.Current], true
}

// AddStep records one achievement. It returns the milestone topic if this
// step finished it, which also moves the learner to the next topic.
func (j *Journey) AddStep() (finished *JourneyTopic) {
	if j == nil || j.Complete() {
		return nil
	}
	t := &j.Topics[j.Current]
	t.Progress++
	if t.Progress < t.Goal {
		return nil
	}
	t.Done = true
	j.Current++
	done := *t
	return &done
}

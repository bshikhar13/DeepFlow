package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// StreamName is the Fail/Delay/Override key for Mock.Stream.
const StreamName = "stream"

// Mock is a deterministic offline provider. It follows a small database
// indexing storyline so the full learning journey can be exercised without a
// model, and produces generic, topic-named content for anything else.
type Mock struct {
	mu        sync.Mutex
	fails     map[string]error
	delays    map[string]time.Duration
	overrides map[string][]string
	calls     map[string]int
	stepDelay time.Duration
}

// NewMock returns a Mock with no scripted behaviour.
func NewMock() *Mock {
	return &Mock{fails: map[string]error{}, delays: map[string]time.Duration{}, overrides: map[string][]string{}, calls: map[string]int{}}
}

// Name implements Provider.
func (m *Mock) Name() string { return "mock" }

// Fail makes calls to name return err. A nil err clears it.
func (m *Mock) Fail(name string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err == nil {
		delete(m.fails, name)
	} else {
		m.fails[name] = err
	}
}

// Delay makes calls to name wait for d, honoring cancellation.
func (m *Mock) Delay(name string, d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.delays[name] = d
}

// Override queues raw responses returned by the next calls to name.
func (m *Mock) Override(name string, responses ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.overrides[name] = append(m.overrides[name], responses...)
}

// Calls reports how many times name was called.
func (m *Mock) Calls(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[name]
}

// SetStreamStepDelay paces streamed words, for realistic demos and tests.
func (m *Mock) SetStreamStepDelay(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stepDelay = d
}

func (m *Mock) enter(ctx context.Context, name string) (string, error) {
	m.mu.Lock()
	m.calls[name]++
	d, err := m.delays[name], m.fails[name]
	var override string
	if q := m.overrides[name]; len(q) > 0 {
		override, m.overrides[name] = q[0], q[1:]
	}
	m.mu.Unlock()
	if d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return override, err
}

// Stream implements Provider.
func (m *Mock) Stream(ctx context.Context, req TextRequest, onDelta func(string)) (Usage, error) {
	override, err := m.enter(ctx, StreamName)
	if err != nil {
		return Usage{}, err
	}
	text := override
	if text == "" {
		last := ""
		if n := len(req.Messages); n > 0 {
			last = req.Messages[n-1].Content
		}
		if containsAny(last, "open this session") {
			text = mockOpening(req.System)
		} else {
			text = mockDialogue(last, topicOf(req.System))
		}
	}
	m.mu.Lock()
	step := m.stepDelay
	m.mu.Unlock()
	for _, w := range strings.SplitAfter(text, " ") {
		if step > 0 {
			select {
			case <-time.After(step):
			case <-ctx.Done():
				return Usage{}, ctx.Err()
			}
		} else if err := ctx.Err(); err != nil {
			return Usage{}, err
		}
		onDelta(w)
	}
	return Usage{InputTokens: 10, OutputTokens: int64(len(text) / 4)}, nil
}

// Structured implements Provider.
func (m *Mock) Structured(ctx context.Context, req StructuredRequest, out any) (Usage, error) {
	override, err := m.enter(ctx, req.Name)
	if err != nil {
		return Usage{}, err
	}
	raw := override
	if raw == "" {
		m.mu.Lock()
		calls := m.calls[req.Name]
		m.mu.Unlock()
		v, ok := mockStructured(req.Name, req.Prompt, calls)
		if !ok {
			return Usage{}, fmt.Errorf("mock: no response for %q", req.Name)
		}
		b, _ := json.Marshal(v)
		raw = string(b)
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return Usage{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	return Usage{InputTokens: 10, OutputTokens: int64(len(raw) / 4)}, nil
}

var topicRe = regexp.MustCompile(`(?m)^Topic: (.+)$`)
var currentRe = regexp.MustCompile(`(?m)^Current topic: (.+)$`)
var triggerRe = regexp.MustCompile(`(?s)<trigger>(.*?)</trigger>`)

func topicOf(s string) string {
	if m := topicRe.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	return "this subject"
}

func currentTopicOf(s string) string {
	if m := currentRe.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func triggerOf(s string) string {
	if m := triggerRe.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	return s
}

func containsAny(s string, words ...string) bool {
	s = strings.ToLower(s)
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

func isForces(s string) bool {
	return containsAny(s, "force", "motion", "newton", "speed", "physics", "friction")
}

func isMaths(s string) bool {
	return containsAny(s, "complex number", "imaginary", "conjugate", "algebra", "maths", "math")
}

func isGeography(s string) bool {
	return containsAny(s, "geography", "river", "mountain", "continent", "capital", "monsoon")
}

func isHistory(s string) bool {
	return containsAny(s, "history", "mughal", "empire", "freedom", "civilisation", "civilization")
}

func mockOpening(system string) string {
	if isForces(system) {
		return "Quick one: you push a toy car and a real car with the same force. Which speeds up more? Why?"
	}
	return fmt.Sprintf("What do you already know about %s? Tell me one thing.", topicOf(system))
}

func mockDialogue(last, topic string) string {
	switch {
	case containsAny(last, " game", "play", "experiment"):
		return "Your new game is on the Game card. Have fun!"
	case containsAny(last, "quiz", "test me"):
		return "Fresh questions are on the Quiz card. Good luck!"
	case isMaths(topic):
		return "A circuit's opposition to current might be \\(Z=3+4i\\) ohms. Its size is \\(\\sqrt{3^2+4^2}=5\\) ohms. So what current flows from a 10 V supply?"
	case containsAny(last, "always"):
		return "Good guess, so let's test it. Can you think of one time it isn't true?"
	case containsAny(last, "what if"):
		return "Great question. Try the new What If card and predict what happens."
	case containsAny(last, "because"):
		return "Nice reasoning! What would change if it were twice as big?"
	case containsAny(last, "what is", "explain", "how does", "why"):
		return fmt.Sprintf("Here's the short version: in %s, look for what causes what. Which part feels confusing?", topic)
	}
	return "Interesting! Why do you think that happens?"
}

func q(prompt string, answer int, why string, options ...string) map[string]any {
	return map[string]any{"prompt": prompt, "options": options, "answer": answer, "why": why}
}

var mathsQuestions = []map[string]any{
	q("What is \\(i^2\\)?", 1, "By definition, \\(i^2=-1\\).", "\\(1\\)", "\\(-1\\)", "\\(i\\)", "\\(0\\)"),
	q("What is the conjugate of \\(3+4i\\)?", 2, "Keep the real part and flip the sign of the imaginary part.", "\\(-3+4i\\)", "\\(4+3i\\)", "\\(3-4i\\)", "\\(-3-4i\\)"),
	q("What is \\(|3+4i|\\)?", 0, "\\(\\sqrt{3^2+4^2}=5\\).", "\\(5\\)", "\\(7\\)", "\\(25\\)", "\\(1\\)"),
	q("Simplify \\((1+i)(1-i)\\).", 3, "\\(1-i^2=1+1=2\\).", "\\(0\\)", "\\(2i\\)", "\\(1\\)", "\\(2\\)"),
	q("What is \\(\\frac{1}{i}\\)?", 1, "Multiply top and bottom by \\(i\\) to get \\(-i\\).", "\\(i\\)", "\\(-i\\)", "\\(1\\)", "\\(-1\\)"),
}

var forceQuestions = []map[string]any{
	q("What does a force do to a still object?", 1, "A force can start, stop or change motion.", "Nothing", "Can make it move", "Makes it lighter", "Changes its colour"),
	q("Same push, heavier cart. What happens?", 0, "More mass means less acceleration.", "It speeds up less", "It speeds up more", "No change", "It floats"),
	q("Which unit measures force?", 2, "Force is measured in newtons.", "Metre", "Second", "Newton", "Kilogram"),
	q("What slows a sliding book?", 3, "Friction acts against sliding.", "Light", "Magnetism", "Sound", "Friction"),
	q("Double the force on the same cart?", 1, "Acceleration doubles: a = F ÷ m.", "Half the acceleration", "Double the acceleration", "Same acceleration", "It stops"),
	q("Which force pulls things to Earth?", 0, "Gravity pulls masses together.", "Gravity", "Friction", "Tension", "Thrust"),
	q("A balanced tug of war rope will…", 2, "Equal and opposite forces cancel.", "Speed up", "Break at once", "Stay still", "Spin"),
	q("Speed is distance divided by…", 1, "Speed = distance ÷ time.", "Mass", "Time", "Force", "Height"),
}

func mockQuiz(topic string, offset int) map[string]any {
	const batch = 5
	out := make([]any, 0, batch)
	for i := 0; i < batch; i++ {
		n := offset*batch + i
		if isMaths(topic) {
			base := mathsQuestions[n%len(mathsQuestions)]
			clone := map[string]any{"prompt": base["prompt"], "options": base["options"], "answer": base["answer"], "why": base["why"]}
			if n >= len(mathsQuestions) {
				clone["prompt"] = fmt.Sprintf("%v (round %d)", base["prompt"], n/len(mathsQuestions)+1)
			}
			out = append(out, clone)
			continue
		}
		if isForces(topic) {
			base := forceQuestions[n%len(forceQuestions)]
			if n < len(forceQuestions) {
				out = append(out, base)
				continue
			}
			clone := map[string]any{"prompt": fmt.Sprintf("%v (round %d)", base["prompt"], n/len(forceQuestions)+1), "options": base["options"], "answer": base["answer"], "why": base["why"]}
			out = append(out, clone)
			continue
		}
		out = append(out, q(fmt.Sprintf("Question %d on %s: which is the best next step?", n+1, topic), n%4, "Mock answer: the real model writes real questions.", "Option A", "Option B", "Option C", "Option D"))
	}
	return map[string]any{"questions": out}
}

func place(label string, lat, lon float64, fact, when string) map[string]any {
	return map[string]any{"label": label, "lat": lat, "lon": lon, "fact": fact, "when": when}
}

func mockMap(topic string) map[string]any {
	if isGeography(topic) {
		return map[string]any{"title": "Great rivers", "connect": false, "places": []any{
			place("Haridwar", 29.95, 78.16, "Where the Ganga reaches the plains.", ""),
			place("Varanasi", 25.32, 83.01, "An ancient city on the Ganga.", ""),
			place("Kolkata", 22.57, 88.36, "Near where the Ganga meets the sea.", ""),
			place("Nashik", 19.99, 73.79, "The Godavari begins near here.", ""),
		}}
	}
	if isHistory(topic) {
		return map[string]any{"title": "Mughal cities", "connect": false, "places": []any{
			place("Panipat", 29.39, 76.97, "Babur's victory here began Mughal rule.", "1526"),
			place("Agra", 27.18, 78.01, "Home of the Taj Mahal.", "1632"),
			place("Fatehpur Sikri", 27.09, 77.66, "Akbar's planned capital.", "1571"),
			place("Delhi", 28.66, 77.23, "Shah Jahan built the Red Fort here.", "1648"),
		}}
	}
	return map[string]any{"title": "Places to know", "connect": false, "places": []any{
		place("Delhi", 28.61, 77.21, "India's capital.", ""), place("Mumbai", 19.08, 72.88, "A major port.", ""),
	}}
}

func mockGame(topic string, round int) map[string]any {
	empty := map[string]any{"places": []any{}, "toleranceKm": 0, "params": []any{}, "output": map[string]any{"label": "", "expr": "", "unit": ""}, "levels": []any{}, "note": "", "basis": "none", "mode": "none", "items": []any{}, "groups": []any{}}
	merge := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range empty {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	if isGeography(topic) {
		return merge(map[string]any{
			"kind": "locate", "title": "River hunt", "goal": "Tap where each place is.", "toleranceKm": 200,
			"places": []any{
				place("Varanasi", 25.32, 83.01, "On the Ganga.", ""), place("Kolkata", 22.57, 88.36, "Near the Ganga's mouth.", ""), place("Nashik", 19.99, 73.79, "Near the Godavari's source.", ""),
			},
		})
	}
	if isMaths(topic) {
		return merge(map[string]any{
			"kind": "target", "title": "Stretch the arrow", "goal": "Set \\(a\\) and \\(b\\) so that \\(|a+bi|\\) hits the target.",
			"params": []any{
				map[string]any{"id": "a", "label": "Real part \\(a\\)", "min": 0, "max": 12, "step": 1, "value": 1, "unit": ""},
				map[string]any{"id": "b", "label": "Imaginary part \\(b\\)", "min": 0, "max": 12, "step": 1, "value": 1, "unit": ""},
			},
			"output": map[string]any{"label": "Size \\(|a+bi|\\)", "expr": "sqrt(a^2 + b^2)", "unit": ""},
			"levels": []any{
				map[string]any{"target": 5, "tolerance": 0.05, "hint": "Try \\(3^2+4^2\\)."},
				map[string]any{"target": 13, "tolerance": 0.05, "hint": "Think of \\(5\\) and \\(12\\)."},
			},
			"note": "Whole numbers only.", "basis": "illustrative",
		})
	}
	if isForces(topic) {
		return merge(map[string]any{
			"kind": "target", "title": "Push the cart", "goal": "Set the force and mass to hit the target speed-up.",
			"params": []any{
				map[string]any{"id": "force", "label": "Push", "min": 0, "max": 100, "step": 1, "value": 10, "unit": "N"},
				map[string]any{"id": "mass", "label": "Cart mass", "min": 1, "max": 20, "step": 1, "value": 10, "unit": "kg"},
			},
			"output": map[string]any{"label": "Acceleration", "expr": "force / mass", "unit": "m/s²"},
			"levels": []any{
				map[string]any{"target": 5, "tolerance": 0.2, "hint": "Try a bigger push."},
				map[string]any{"target": 2, "tolerance": 0.1, "hint": "A heavier cart speeds up less."},
				map[string]any{"target": 12, "tolerance": 0.5, "hint": "Big push, light cart."},
			},
			"note": "Ignores friction.", "basis": "illustrative",
		})
	}
	if isHistory(topic) {
		return merge(map[string]any{
			"kind": "sort", "title": "Time machine", "goal": "Put these in order, oldest first.", "mode": "order",
			"items": []any{
				map[string]any{"label": "Indus Valley cities", "group": ""}, map[string]any{"label": "Maurya Empire", "group": ""},
				map[string]any{"label": "Mughal Empire", "group": ""}, map[string]any{"label": "Indian independence", "group": ""},
			},
		})
	}
	return merge(map[string]any{
		"kind": "sort", "title": "Sort it out", "goal": "Put each one where it belongs.", "mode": "groups",
		"groups": []any{"Fact", "Opinion"},
		"items": []any{
			map[string]any{"label": "Water boils when heated", "group": "Fact"}, map[string]any{"label": "Summer is the best season", "group": "Opinion"},
			map[string]any{"label": "The Earth orbits the Sun", "group": "Fact"}, map[string]any{"label": "Maths is more fun than art", "group": "Opinion"},
		},
	})
}

func mockJourney(topic string) map[string]any {
	t := func(title, milestone string) map[string]any {
		return map[string]any{"title": title, "milestone": milestone}
	}
	switch {
	case isGeography(topic):
		return map[string]any{"uses_maps": true, "topics": []any{t("Where rivers begin", "Source Finder"), t("River plains", "Plain Explorer"), t("Reaching the sea", "Delta Master")}}
	case isMaths(topic):
		return map[string]any{"topics": []any{t("Imaginary numbers", "Imaginary Explorer"), t("Conjugates", "Mirror Master"), t("Modulus", "Size Finder")}}
	case isForces(topic):
		return map[string]any{"topics": []any{t("What is a force?", "Force Finder"), t("Mass and speed-up", "Cart Master"), t("Friction", "Grip Expert"), t("Gravity", "Gravity Guru")}}
	case isHistory(topic):
		return map[string]any{"uses_maps": true, "topics": []any{t("Early cities", "City Scout"), t("Great empires", "Empire Expert"), t("Road to freedom", "Freedom Scholar")}}
	}
	return map[string]any{"topics": []any{t("The big idea", "Idea Finder"), t("How it works", "Explainer"), t("Try it out", "Problem Solver")}}
}

func mockStructured(name, prompt string, calls int) (any, bool) {
	topic := topicOf(prompt)
	current := currentTopicOf(prompt)
	trigger := triggerOf(prompt)
	subject := topic + " " + current
	switch name {
	case "concept.content":
		if isMaths(subject) {
			return map[string]any{"title": "Modulus", "idea": "The size of \\(a+bi\\) is \\(\\sqrt{a^2+b^2}\\).", "example": "\\(|3+4i|=\\sqrt{9+16}=5\\)", "emoji": "📐", "related": []string{"Conjugate", "Argand plane"}}, true
		}
		if isForces(subject) {
			return map[string]any{"title": "Force", "idea": "A push or a pull that can change how something moves.", "example": "Kicking a football: your foot's force sends it flying.", "emoji": "⚡", "related": []string{"Friction", "Mass", "Gravity"}}, true
		}
		return map[string]any{"title": "The big idea", "idea": "The one thing to remember about " + topic + ".", "example": "An everyday example would go here.", "emoji": "💡", "related": []string{}}, true
	case "quiz.batch":
		return mockQuiz(subject, calls-1), true
	case "game.spec":
		return mockGame(subject, calls-1), true
	case "journey.index":
		return mockJourney(topic), true
	case "map.places":
		return mockMap(subject), true
	case "thought.scenario":
		if isForces(subject) {
			return map[string]any{"scenario": "Imagine there's no friction anywhere.", "question": "You kick a ball on the ground. What happens?", "reveal": "It keeps rolling forever: nothing slows it down."}, true
		}
		return map[string]any{"scenario": "Imagine the main rule of " + topic + " stopped working.", "question": "What changes first?", "reveal": "Things that depended on that rule change first."}, true
	case "thought.respond":
		return map[string]any{"feedback": "Compare your guess with what happens. What surprised you?"}, true
	case "evaluator.assess":
		out := mockAssess(trigger)
		if strings.Contains(prompt, "type=retrieval.answered") && !containsAny(trigger, "don't know", "dont know", "not sure", "no idea") {
			out["evidence"] = []any{map[string]any{"concept": "core idea", "tier": "durable", "correctness": "correct", "reasoning": "adequate", "rationale": "Remembered it without help after a break.", "uncertainty": "One try.", "source_event_id": ""}}
			out["recommendations"] = []any{}
		}
		return out, true
	case "session.consolidate":
		return map[string]any{"summary": "You practised " + topic + ".", "insights": []string{}, "gaps": []string{"Try it on a new problem"}, "open_questions": []string{"What's next in " + topic + "?"}}, true
	case "session.retrieval":
		return map[string]any{"questions": []any{
			map[string]any{"prompt": "From memory: what's the main idea from last time?", "concept": "core idea"},
			map[string]any{"prompt": "When would that idea not work?", "concept": "boundary case"},
		}}, true
	}
	return nil, false
}

func mockAssess(trigger string) map[string]any {
	out := map[string]any{
		"intent":       map[string]any{"summary": "Exploring the current question", "kind": "foundation", "shifted": false},
		"concepts":     []string{},
		"observations": []string{"The student replied in their own words."},
		"inferences":   []any{},
		"evidence":     []any{}, "gaps": []string{}, "open_questions": []string{}, "crystallize": []any{},
		"readiness": "explain", "recommendations": []any{},
	}
	switch {
	case containsAny(trigger, "what if"):
		out["intent"] = map[string]any{"summary": "Testing a what-if idea", "kind": "tangent", "shifted": true}
		out["concepts"] = []string{"what if"}
		out["open_questions"] = []string{trigger}
		out["crystallize"] = []any{map[string]any{"type": "question", "content": trigger, "authorship": "learner"}}
		out["recommendations"] = []any{
			map[string]any{"action": "REPLACE", "cell_id": "", "target_type": "concept", "cell_type": "thought", "objective": "Explore the student's what-if", "reason": "You asked a what-if, so here's one to try.", "priority": "high"},
			map[string]any{"action": "UPDATE", "cell_id": "", "target_type": "quiz", "cell_type": "quiz", "objective": "Quiz on the student's what-if", "reason": "The quiz follows your question.", "priority": "medium"},
		}
	case containsAny(trigger, "always"):
		out["concepts"] = []string{"first idea"}
		out["inferences"] = []any{map[string]any{"claim": "May be treating a common case as a rule", "confidence": "medium", "misconception": true}}
		out["evidence"] = []any{map[string]any{"concept": "first idea", "tier": "weak", "correctness": "incorrect", "reasoning": "weak", "rationale": "Stated a rule with no exceptions.", "uncertainty": "One message.", "source_event_id": ""}}
		out["gaps"] = []string{"When the rule doesn't hold"}
		out["crystallize"] = []any{map[string]any{"type": "hypothesis", "content": trigger, "authorship": "learner"}}
		out["readiness"] = "another_representation"
	case containsAny(trigger, "because") && len(trigger) > 40:
		out["concepts"] = []string{"cause and effect"}
		out["evidence"] = []any{map[string]any{"concept": "cause and effect", "tier": "strong", "correctness": "correct", "reasoning": "strong", "rationale": "Explained why, in their own words.", "uncertainty": "Not tested after a break yet.", "source_event_id": ""}}
		out["crystallize"] = []any{map[string]any{"type": "insight", "content": clipTo(trigger, 120), "authorship": "learner"}}
		out["readiness"] = "harder"
		out["recommendations"] = []any{map[string]any{"action": "DEEPEN", "cell_id": "", "target_type": "quiz", "cell_type": "quiz", "objective": "Harder questions", "reason": "You explained it, so the quiz steps up.", "priority": "medium"}}
	case containsAny(trigger, "don't know", "dont know", "not sure", "no idea"):
		out["gaps"] = []string{"Couldn't recall this yet"}
		out["evidence"] = []any{map[string]any{"concept": "core idea", "tier": "weak", "correctness": "incorrect", "reasoning": "na", "rationale": "Said they couldn't recall it.", "uncertainty": "One try.", "source_event_id": ""}}
		out["recommendations"] = []any{map[string]any{"action": "SIMPLIFY", "cell_id": "", "target_type": "concept", "cell_type": "concept", "objective": "Rebuild the foundations of the core idea", "reason": "Let's revisit the basics.", "priority": "medium"}}
	case len(trigger) > 80:
		out["concepts"] = []string{"core idea"}
		out["evidence"] = []any{map[string]any{"concept": "core idea", "tier": "moderate", "correctness": "partial", "reasoning": "adequate", "rationale": "Explained it in their own words.", "uncertainty": "Not tried on something new.", "source_event_id": ""}}
	}
	return out
}

func clipTo(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Package agents implements the specialized Cell Agents. Agents supply data
// for fixed UI components; none of their output is ever executed.
package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"deepflow/internal/domain"
	"deepflow/internal/expr"
	"deepflow/internal/harness"
	"deepflow/internal/provider"
)

// New creates a fresh agent for a cell type.
func New(t domain.CellType) harness.Agent {
	switch t {
	case domain.CellDialogue:
		return dialogueAgent{}
	case domain.CellConcept:
		return conceptAgent{}
	case domain.CellGame:
		return gameAgent{}
	case domain.CellQuiz:
		return quizAgent{}
	case domain.CellJourney:
		return journeyAgent{}
	case domain.CellThought:
		return thoughtAgent{}
	case domain.CellDocument:
		return documentAgent{}
	case domain.CellMap:
		return mapAgent{}
	}
	panic("unknown cell type " + string(t))
}

// Hard length limits, in characters, so cards stay readable for students
// however wordy a model is.
const (
	limitQuestion = 140
	limitOption   = 70
	limitWhy      = 130
	limitIdea     = 130
	limitExample  = 140
	limitTitle    = 48
	limitGoal     = 90
	limitHint     = 80
	limitReply    = 200
	limitChip     = 24
	limitItem     = 44
	limitLabel    = 30
	limitUnit     = 10
	maxTopics     = 8
)

// clip shortens text to n characters at a word boundary. Formulas are never cut,
// and text with maths gets more room, because LaTeX source is much longer than
// what it renders to.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	hasMath := strings.Contains(s, `\(`) || strings.Contains(s, `\[`) || strings.Contains(s, "$")
	if hasMath {
		n *= 2
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)[:n-1]
	cut := string(r)
	if i := strings.LastIndexByte(cut, ' '); i > n/2 {
		cut = cut[:i]
	}
	for _, pair := range [][2]string{{`\(`, `\)`}, {`\[`, `\]`}} {
		if open := strings.LastIndex(cut, pair[0]); open >= 0 && open > strings.LastIndex(cut, pair[1]) {
			cut = cut[:open]
		}
	}
	cut = strings.TrimRight(cut, " ,;:.")
	if cut == "" {
		return s // the whole thing is one long formula: keep it, it can't be cut
	}
	return cut + "…"
}

func textPayload(e domain.Event) string {
	var p struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(e.Payload, &p)
	return p.Text
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

const sharedPrinciples = `You are part of DeepFlow, a learning app for school students in classes 6 to 12.
Use words a student in the stated class knows. Be accurate, friendly and very brief: students are put off by walls of text. Make them think and do, and give a direct answer whenever they ask for one.
Write all maths and science formulas in LaTeX: \( ... \) inline and \[ ... \] on its own line, such as \(x^2+1\). Never use other notation for formulas. Plain numbers can stay as plain text.`

// ValidContent reports whether saved content has the shape today's card
// expects. Sessions saved by an earlier version can hold older shapes, and those
// cells are rebuilt instead of being restored.
func ValidContent(t domain.CellType, raw json.RawMessage) bool {
	var v map[string]json.RawMessage
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	has := func(keys ...string) bool {
		for _, k := range keys {
			if _, ok := v[k]; !ok {
				return false
			}
		}
		return true
	}
	switch t {
	case domain.CellConcept:
		return has("title", "idea")
	case domain.CellGame:
		return has("kind", "title", "won")
	case domain.CellQuiz:
		return has("id", "prompt", "options")
	case domain.CellJourney:
		return has("topics", "shown")
	case domain.CellThought:
		return has("scenario", "question")
	case domain.CellMap:
		return has("title", "places")
	}
	return true
}

// briefing renders the context an agent should work from. "Topic:" and
// "Current topic:" are stable line prefixes.
func briefing(in harness.AgentInput, objective string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Topic: %s\n", in.Context.PursuitTitle)
	if in.Context.Grade > 0 {
		fmt.Fprintf(&b, "Student: class %d\n", in.Context.Grade)
	}
	if t, ok := in.Context.Journey.CurrentTopic(); ok {
		fmt.Fprintf(&b, "Current topic: %s\n", t.Title)
	}
	if in.Context.Inquiry != "" {
		fmt.Fprintf(&b, "Student's question: %s\n", in.Context.Inquiry)
	}
	if objective == "" {
		objective = in.Cell.Objective
	}
	fmt.Fprintf(&b, "Objective: %s\n", objective)
	if in.Context.Intent != "" {
		fmt.Fprintf(&b, "Student's current aim: %s\n", in.Context.Intent)
	}
	list := func(name string, items []string) {
		if len(items) > 0 {
			fmt.Fprintf(&b, "%s: %s\n", name, strings.Join(items, "; "))
		}
	}
	list("Concepts in play", in.Context.Concepts)
	list("Student's ideas", in.Context.Hypotheses)
	list("Possible misconceptions", in.Context.Misconceptions)
	list("Known gaps", in.Context.Gaps)
	for _, d := range in.Context.Demonstrated {
		fmt.Fprintf(&b, "Shown (%s evidence): %s\n", d.Tier, d.Text)
	}
	for _, r := range in.Context.Recent {
		fmt.Fprintf(&b, "Recent %s: %s\n", r.Kind, r.Text)
	}
	return b.String()
}

func adjustment(d harness.Directive) string {
	switch d.Kind {
	case "deepen":
		return "\nMake this harder than before."
	case "simplify":
		return "\nMake this simpler than before and go back to basics."
	case "refresh":
		return "\nThe student asked for something different: take a clearly new angle."
	}
	return ""
}

func structured(ctx context.Context, p provider.Provider, name, system, prompt string, schema map[string]any, out any) error {
	_, err := p.Structured(ctx, provider.StructuredRequest{Role: provider.RoleCell, Name: name, System: sharedPrinciples + "\n\n" + system, Prompt: prompt, Schema: schema}, out)
	return err
}

// --- Chat ---

// DialogueContent is the Chat cell's content. Messages load from the API.
type DialogueContent struct {
	Streaming bool `json:"streaming"`
}

type dialogueAgent struct{}

func (dialogueAgent) Type() domain.CellType { return domain.CellDialogue }

func dialogueSystem(in harness.AgentInput) string {
	return sharedPrinciples + `

You are the student's tutor in a chat. Reply in at most 2 or 3 short sentences, about 40 words. Ask one question at a time, and use everyday examples. If they ask for more detail, you may write a little more.

The app has other cards beside this chat, and you can see below what the student did in them. Build on it, such as a quiz question they got wrong. Don't run games or quizzes in the chat: if they ask for one, the app is already putting it on the Game card or Quiz card, so say so in one short, cheerful line.

` + briefing(in, "Keep the student curious and thinking")
}

func (dialogueAgent) Init(ctx context.Context, in harness.AgentInput) (harness.AgentOutput, error) {
	out := harness.AgentOutput{Content: mustJSON(DialogueContent{})}
	if len(in.History) > 0 {
		return out, nil
	}
	text, err := streamReply(ctx, in, []provider.Turn{{Role: "user", Content: "Open this session. Ask me one short, fun question to find out what I already think."}})
	if err != nil {
		return out, err
	}
	out.Publish = []domain.Event{{Type: domain.EvAgentMessage, Payload: mustJSON(map[string]string{"text": text})}}
	return out, nil
}

func streamReply(ctx context.Context, in harness.AgentInput, turns []provider.Turn) (string, error) {
	var full strings.Builder
	_, err := in.Provider.Stream(ctx, provider.TextRequest{Role: provider.RoleDialogue, System: dialogueSystem(in), Messages: turns}, func(s string) {
		full.WriteString(s)
		in.Delta(s)
	})
	return full.String(), err
}

func (dialogueAgent) Handle(ctx context.Context, in harness.AgentInput, ev domain.Event) (harness.AgentOutput, error) {
	turns := in.History
	if len(turns) == 0 {
		turns = []provider.Turn{{Role: "user", Content: textPayload(ev)}}
	}
	text, err := streamReply(ctx, in, turns)
	if err != nil {
		return harness.AgentOutput{}, err
	}
	return harness.AgentOutput{Publish: []domain.Event{{Type: domain.EvAgentMessage, Payload: mustJSON(map[string]string{"text": text})}}}, nil
}

func (dialogueAgent) Update(ctx context.Context, in harness.AgentInput, d harness.Directive) (harness.AgentOutput, error) {
	return harness.AgentOutput{}, nil
}

func (dialogueAgent) Retain(content, state json.RawMessage) []domain.Artifact { return nil }

// --- Key Idea ---

// ConceptContent is the Key Idea flip card: the idea on the front, an example on the back.
type ConceptContent struct {
	Title   string   `json:"title"`
	Idea    string   `json:"idea"`
	Example string   `json:"example"`
	Emoji   string   `json:"emoji"`
	Related []string `json:"related"`
}

const conceptSystem = `You write a flip card for one key idea. "title" is 1 to 3 words. "idea" is one short sentence, at most 20 words. "example" is one everyday example, at most 20 words. "emoji" is a single emoji. "related" has up to 3 related ideas of 1 or 2 words.`

type conceptAgent struct{}

func (conceptAgent) Type() domain.CellType { return domain.CellConcept }

func (conceptAgent) generate(ctx context.Context, in harness.AgentInput, objective, extra string) (harness.AgentOutput, error) {
	var c ConceptContent
	if err := structured(ctx, in.Provider, "concept.content", conceptSystem, briefing(in, objective)+extra, conceptSchema, &c); err != nil {
		return harness.AgentOutput{}, err
	}
	c.Title, c.Idea, c.Example = clip(c.Title, limitTitle), clip(c.Idea, limitIdea), clip(c.Example, limitExample)
	if len(c.Related) > 3 {
		c.Related = c.Related[:3]
	}
	for i := range c.Related {
		c.Related[i] = clip(c.Related[i], limitChip)
	}
	if utf8.RuneCountInString(c.Emoji) > 4 {
		c.Emoji = "💡"
	}
	return harness.AgentOutput{Content: mustJSON(c)}, nil
}

func (a conceptAgent) Init(ctx context.Context, in harness.AgentInput) (harness.AgentOutput, error) {
	return a.generate(ctx, in, "", "")
}

func (a conceptAgent) Handle(ctx context.Context, in harness.AgentInput, ev domain.Event) (harness.AgentOutput, error) {
	var p struct {
		Concept string `json:"concept"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	if p.Concept == "" {
		return harness.AgentOutput{}, nil
	}
	return a.generate(ctx, in, "Explain "+p.Concept, "\nThe student tapped this idea: "+p.Concept)
}

func (a conceptAgent) Update(ctx context.Context, in harness.AgentInput, d harness.Directive) (harness.AgentOutput, error) {
	out, err := a.generate(ctx, in, d.Objective, adjustment(d))
	if err != nil {
		return out, err
	}
	var c ConceptContent
	_ = json.Unmarshal(out.Content, &c)
	for _, dem := range in.Context.Demonstrated {
		strong := dem.Tier == string(domain.TierStrong) || dem.Tier == string(domain.TierDurable)
		if strong && strings.Contains(strings.ToLower(dem.Text), strings.ToLower(c.Title)) {
			out.Requests = append(out.Requests, harness.CellRequest{
				Kind: "replace", CellType: domain.CellThought,
				Objective: "Stretch the student's understanding of " + c.Title,
				Reason:    "You've got " + c.Title + ", so here's a harder puzzle.",
			})
			break
		}
	}
	return out, nil
}

func (conceptAgent) Retain(content, state json.RawMessage) []domain.Artifact {
	return []domain.Artifact{{Content: content}}
}

// --- Quiz ---

// QuizQuestion is a full question. Answer and Why stay on the server until the
// student answers, so the page never holds the answer in advance.
type QuizQuestion struct {
	ID      string   `json:"id"`
	Prompt  string   `json:"prompt"`
	Options []string `json:"options"`
	Answer  int      `json:"answer"`
	Why     string   `json:"why"`
}

// QuizContent is what the student sees: one question at a time.
type QuizContent struct {
	ID      string      `json:"id"`
	Prompt  string      `json:"prompt"`
	Options []string    `json:"options"`
	Topic   string      `json:"topic"`
	Level   int         `json:"level"`
	Streak  int         `json:"streak"`
	Right   int         `json:"right"`
	Result  *QuizResult `json:"result,omitempty"`
}

// QuizResult appears once the current question is answered.
type QuizResult struct {
	Correct bool   `json:"correct"`
	Chosen  int    `json:"chosen"`
	Answer  int    `json:"answer"`
	Why     string `json:"why"`
}

// QuizResultEvent tells the harness how an answer went.
type QuizResultEvent struct {
	Correct       bool   `json:"correct"`
	Question      string `json:"question"`
	Topic         string `json:"topic"`
	SourceEventID string `json:"sourceEventId"`
}

type quizState struct {
	Current *QuizQuestion  `json:"current,omitempty"`
	Queue   []QuizQuestion `json:"queue,omitempty"`
	Asked   []string       `json:"asked,omitempty"`
	Level   int            `json:"level"`
	Streak  int            `json:"streak"`
	Misses  int            `json:"misses"`
	Right   int            `json:"right"`
	Total   int            `json:"total"`
}

const (
	maxQuizLevel  = 3
	askedMemory   = 40
	prefetchBelow = 2
	levelUpStreak = 3
	levelDownMiss = 2
)

const quizSystem = `You write a quiz for a school student. Write 5 multiple-choice questions in one batch, at the stated difficulty (1 easy, 2 medium, 3 hard).
"prompt" is at most 18 words. "options" has 3 or 4 short choices, at most 8 words each, with exactly one correct. "answer" is the zero-based index of the correct option, and vary its position. "why" is one sentence, at most 18 words, saying why it's right. Test understanding and reasoning, not just memory. Don't repeat listed questions.`

type quizAgent struct{}

func (quizAgent) Type() domain.CellType { return domain.CellQuiz }

func topicLabel(in harness.AgentInput) string {
	if t, ok := in.Context.Journey.CurrentTopic(); ok {
		return t.Title
	}
	return in.Context.PursuitTitle
}

func (quizAgent) batch(ctx context.Context, in harness.AgentInput, st *quizState, objective, extra string) ([]QuizQuestion, error) {
	prompt := briefing(in, objective) + extra + "\nDifficulty: " + strconv.Itoa(st.Level)
	if len(st.Asked) > 0 {
		prompt += "\nAlready asked: " + strings.Join(st.Asked, " | ")
	}
	var r struct {
		Questions []QuizQuestion `json:"questions"`
	}
	if err := structured(ctx, in.Provider, "quiz.batch", quizSystem, prompt, quizSchema, &r); err != nil {
		return nil, err
	}
	asked := map[string]bool{}
	for _, a := range st.Asked {
		asked[a] = true
	}
	out := make([]QuizQuestion, 0, len(r.Questions))
	for _, q := range r.Questions {
		q.Prompt = clip(q.Prompt, limitQuestion)
		if q.Prompt == "" || asked[q.Prompt] || len(q.Options) < 2 || len(q.Options) > 4 || q.Answer < 0 || q.Answer >= len(q.Options) {
			continue
		}
		for i := range q.Options {
			q.Options[i] = clip(q.Options[i], limitOption)
		}
		q.Why = clip(q.Why, limitWhy)
		q.ID = domain.NewID("q")
		asked[q.Prompt] = true
		out = append(out, q)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no usable quiz questions", provider.ErrMalformed)
	}
	return out, nil
}

func quizView(q *QuizQuestion, st *quizState, topic string, result *QuizResult) QuizContent {
	return QuizContent{ID: q.ID, Prompt: q.Prompt, Options: q.Options, Topic: topic, Level: st.Level, Streak: st.Streak, Right: st.Right, Result: result}
}

func (st *quizState) advance() bool {
	if len(st.Queue) == 0 {
		return false
	}
	q := st.Queue[0]
	st.Queue = st.Queue[1:]
	st.Current = &q
	st.Asked = append(st.Asked, q.Prompt)
	if n := len(st.Asked); n > askedMemory {
		st.Asked = st.Asked[n-askedMemory:]
	}
	return true
}

func (a quizAgent) Init(ctx context.Context, in harness.AgentInput) (harness.AgentOutput, error) {
	st := quizState{Level: 1}
	qs, err := a.batch(ctx, in, &st, "", "")
	if err != nil {
		return harness.AgentOutput{}, err
	}
	st.Queue = qs
	st.advance()
	return harness.AgentOutput{Content: mustJSON(quizView(st.Current, &st, topicLabel(in), nil)), LocalState: mustJSON(st)}, nil
}

func (a quizAgent) Handle(ctx context.Context, in harness.AgentInput, ev domain.Event) (harness.AgentOutput, error) {
	var st quizState
	var view QuizContent
	_ = json.Unmarshal(in.Cell.LocalState, &st)
	_ = json.Unmarshal(in.Cell.Content, &view)
	switch ev.Type {
	case domain.EvQuizAnswered:
		var p struct {
			QuestionID string `json:"questionId"`
			Choice     *int   `json:"choice"`
		}
		_ = json.Unmarshal(ev.Payload, &p)
		q := st.Current
		if q == nil || view.Result != nil || p.QuestionID != q.ID || p.Choice == nil || *p.Choice < 0 || *p.Choice >= len(q.Options) {
			return harness.AgentOutput{}, nil // stale, repeated or impossible: nothing happened
		}
		correct := *p.Choice == q.Answer
		st.Total++
		if correct {
			st.Right++
			st.Streak++
			st.Misses = 0
			if st.Streak%levelUpStreak == 0 && st.Level < maxQuizLevel {
				st.Level++
			}
		} else {
			st.Streak = 0
			st.Misses++
			if st.Misses >= levelDownMiss && st.Level > 1 {
				st.Level--
				st.Misses = 0
			}
		}
		out := harness.AgentOutput{
			Content:    mustJSON(quizView(q, &st, view.Topic, &QuizResult{Correct: correct, Chosen: *p.Choice, Answer: q.Answer, Why: q.Why})),
			LocalState: mustJSON(st),
			Publish: []domain.Event{{Type: domain.EvQuizResult, Payload: mustJSON(QuizResultEvent{
				Correct: correct, Question: q.Prompt, Topic: view.Topic, SourceEventID: ev.ID,
			})}},
		}
		if len(st.Queue) < prefetchBelow {
			out.Then = &harness.Directive{Kind: "prefetch"}
		}
		return out, nil
	case domain.EvQuizNext:
		if !st.advance() {
			qs, err := a.batch(ctx, in, &st, "", "")
			if err != nil {
				return harness.AgentOutput{}, err
			}
			st.Queue = qs
			st.advance()
		}
		return harness.AgentOutput{Content: mustJSON(quizView(st.Current, &st, topicLabel(in), nil)), LocalState: mustJSON(st)}, nil
	}
	return harness.AgentOutput{}, nil
}

func (a quizAgent) Update(ctx context.Context, in harness.AgentInput, d harness.Directive) (harness.AgentOutput, error) {
	var st quizState
	_ = json.Unmarshal(in.Cell.LocalState, &st)
	if st.Level == 0 {
		st.Level = 1
	}
	if d.Kind == "prefetch" {
		qs, err := a.batch(ctx, in, &st, "", "")
		if err != nil {
			return harness.AgentOutput{}, err
		}
		st.Queue = append(st.Queue, qs...)
		return harness.AgentOutput{LocalState: mustJSON(st)}, nil
	}
	switch d.Kind {
	case "deepen":
		if st.Level < maxQuizLevel {
			st.Level++
		}
	case "simplify":
		if st.Level > 1 {
			st.Level--
		}
	}
	qs, err := a.batch(ctx, in, &st, d.Objective, adjustment(d))
	if err != nil {
		return harness.AgentOutput{}, err
	}
	st.Queue = qs
	if d.Kind != "refresh" {
		// New questions wait their turn: the one on screen, and any feedback on
		// it, stays until the student moves on.
		return harness.AgentOutput{LocalState: mustJSON(st)}, nil
	}
	st.advance()
	return harness.AgentOutput{Content: mustJSON(quizView(st.Current, &st, topicLabel(in), nil)), LocalState: mustJSON(st)}, nil
}

func (quizAgent) Retain(content, state json.RawMessage) []domain.Artifact {
	var st quizState
	_ = json.Unmarshal(state, &st)
	return []domain.Artifact{{Content: mustJSON(map[string]int{"right": st.Right, "answered": st.Total})}}
}

// --- Game ---

// GameContent is a game plus the student's progress in it.
type GameContent struct {
	domain.GameSpec
	ID    string `json:"id"` // new for every game, so the page starts each one clean
	Won   []int  `json:"won"`
	Stars int    `json:"stars"`
}

const gameSystem = `You design one short game for a school student, about the current topic. Pick the kind that suits it.

kind "target": a live experiment for topics with numbers (maths, physics, chemistry, economics). The student moves 1 to 3 sliders to bring an output to a target. "output.expr" uses only numbers, slider ids, + - * / ^, parentheses and min max abs sqrt log log2 log10 exp floor ceil. Slider ids are simple names, never function names. Give 2 or 3 levels, each reachable with the sliders, and level 1 must not be already won at the starting values. Tolerance is how close counts. "hint" is at most 10 words. "note" states the model's main simplification in at most 10 words. "basis" is "illustrative". Set "mode" to "none", with empty "items" and "groups".

kind "sort": for topics without formulas (history, biology, civics, language). Set "mode" to "order" with 3 to 6 "items" listed in the correct order, or "groups" with 2 or 3 "groups" and 4 to 8 "items", each naming its group. Item labels are at most 5 words. Set "basis" to "none", with empty "params" and "levels", and an empty "output".

kind "locate": for places (geography, and history where the place matters). The student taps each place on an outline map. Give 3 to 6 "places" with "label", real "lat" and "lon" in degrees, and a one-line "fact". "toleranceKm" is how close counts, from 100 for a small region to 800 for the world. Use only well-known places whose position you're sure of. Set "basis" and "mode" to "none", with other lists empty.

Use "locate" for geography, and often for history. "title" is 2 to 4 fun words. "goal" says what to do, in at most 12 words.`

type gameAgent struct{}

func (gameAgent) Type() domain.CellType { return domain.CellGame }

func (gameAgent) generate(ctx context.Context, in harness.AgentInput, objective, extra string, stars int) (harness.AgentOutput, error) {
	prompt := briefing(in, objective) + extra
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		var g domain.GameSpec
		if err := structured(ctx, in.Provider, "game.spec", gameSystem, prompt, gameSchema, &g); err != nil {
			return harness.AgentOutput{}, err
		}
		clipGame(&g) // before validating, so clipping can't create duplicates unnoticed
		if err := expr.ValidateGame(g); err != nil {
			lastErr = err
			prompt = briefing(in, objective) + extra + "\nYour previous game was rejected: " + err.Error()
			continue
		}
		return harness.AgentOutput{Content: mustJSON(GameContent{GameSpec: g, ID: domain.NewID("game"), Won: []int{}, Stars: stars})}, nil
	}
	return harness.AgentOutput{}, fmt.Errorf("%w: %v", provider.ErrMalformed, lastErr)
}

func clipGame(g *domain.GameSpec) {
	g.Title, g.Goal, g.Note = clip(g.Title, limitTitle), clip(g.Goal, limitGoal), clip(g.Note, limitHint)
	g.Output.Label, g.Output.Unit = clip(g.Output.Label, limitLabel), clip(g.Output.Unit, limitUnit)
	for i := range g.Levels {
		g.Levels[i].Hint = clip(g.Levels[i].Hint, limitHint)
	}
	for i := range g.Params {
		g.Params[i].Label, g.Params[i].Unit = clip(g.Params[i].Label, limitLabel), clip(g.Params[i].Unit, limitUnit)
	}
	for i := range g.Groups {
		g.Groups[i] = clip(g.Groups[i], limitChip)
	}
	for i := range g.Items {
		g.Items[i].Label, g.Items[i].Group = clip(g.Items[i].Label, limitItem), clip(g.Items[i].Group, limitChip)
	}
	clipPlaces(g.Places)
}

func clipPlaces(places []domain.GamePlace) {
	for i := range places {
		p := &places[i]
		p.Label, p.Fact, p.When = clip(p.Label, limitChip), clip(p.Fact, limitWhy), clip(p.When, limitLabel)
	}
}

func (a gameAgent) Init(ctx context.Context, in harness.AgentInput) (harness.AgentOutput, error) {
	return a.generate(ctx, in, "", "", 0)
}

func (a gameAgent) Handle(ctx context.Context, in harness.AgentInput, ev domain.Event) (harness.AgentOutput, error) {
	var c GameContent
	_ = json.Unmarshal(in.Cell.Content, &c)
	switch ev.Type {
	case domain.EvGameLevelWon:
		var attempt expr.Attempt
		if err := json.Unmarshal(ev.Payload, &attempt); err != nil || !expr.VerifyWin(c.GameSpec, attempt) {
			return harness.AgentOutput{}, nil
		}
		for _, w := range c.Won {
			if w == attempt.Level {
				return harness.AgentOutput{}, nil
			}
		}
		c.Won = append(c.Won, attempt.Level)
		c.Stars++
		return harness.AgentOutput{
			Content: mustJSON(c),
			Publish: []domain.Event{{Type: domain.EvGameStar, Payload: mustJSON(map[string]any{"game": c.Title, "level": attempt.Level})}},
		}, nil
	case domain.EvGameNext:
		return a.generate(ctx, in, "", "\nMake a different game from: "+c.Title, c.Stars)
	}
	return harness.AgentOutput{}, nil
}

func (a gameAgent) Update(ctx context.Context, in harness.AgentInput, d harness.Directive) (harness.AgentOutput, error) {
	var c GameContent
	_ = json.Unmarshal(in.Cell.Content, &c)
	return a.generate(ctx, in, d.Objective, adjustment(d), c.Stars)
}

func (gameAgent) Retain(content, state json.RawMessage) []domain.Artifact {
	return []domain.Artifact{{Content: content}}
}

// --- My Journey ---

// JourneyTopicView is one stop on the path.
type JourneyTopicView struct {
	Title     string `json:"title"`
	Milestone string `json:"milestone"`
	Progress  int    `json:"progress"`
	Goal      int    `json:"goal"`
	Done      bool   `json:"done"`
}

// ShownItem is one concept the student has evidence for.
type ShownItem struct {
	Concept string `json:"concept"`
	Tier    string `json:"tier"`
	Why     string `json:"why"`
}

// JourneyContent is the index, where the student is on it, and what they've shown.
type JourneyContent struct {
	Topics   []JourneyTopicView `json:"topics"`
	Current  int                `json:"current"`
	Complete bool               `json:"complete"`
	Next     string             `json:"next"`
	Shown    []ShownItem        `json:"shown"`
}

const journeySystem = `Write the index for this subject at the student's class level: 4 to 7 topics in a sensible learning order. "title" is 1 to 4 words. "milestone" is a fun 1 or 2 word badge earned by finishing that topic, like "Force Finder". Set "uses_maps" to true if where things happened or are located matters to the subject, as in history and geography.`

type journeyAgent struct{}

func (journeyAgent) Type() domain.CellType { return domain.CellJourney }

var tierRank = map[string]int{"weak": 1, "moderate": 2, "strong": 3, "durable": 4}

func journeyView(j *domain.Journey, demonstrated []harness.DemonstratedItem) JourneyContent {
	c := JourneyContent{Topics: []JourneyTopicView{}, Shown: []ShownItem{}}
	if j != nil {
		c.Current, c.Complete = j.Current, j.Complete()
		for _, t := range j.Topics {
			c.Topics = append(c.Topics, JourneyTopicView{Title: t.Title, Milestone: t.Milestone, Progress: t.Progress, Goal: t.Goal, Done: t.Done})
		}
		if t, ok := j.CurrentTopic(); ok {
			left := t.Goal - t.Progress
			step := "steps"
			if left == 1 {
				step = "step"
			}
			c.Next = fmt.Sprintf("%d %s to %s", left, step, t.Milestone)
		} else {
			c.Next = "Journey complete!"
		}
	}
	index := map[string]int{}
	for _, d := range demonstrated {
		concept, why, _ := strings.Cut(d.Text, ": ")
		if i, seen := index[concept]; seen {
			if tierRank[d.Tier] > tierRank[c.Shown[i].Tier] {
				c.Shown[i].Tier, c.Shown[i].Why = d.Tier, why
			}
			continue
		}
		index[concept] = len(c.Shown)
		c.Shown = append(c.Shown, ShownItem{Concept: concept, Tier: d.Tier, Why: why})
	}
	return c
}

func (journeyAgent) build(ctx context.Context, in harness.AgentInput) (harness.AgentOutput, error) {
	if in.Context.Journey != nil {
		return harness.AgentOutput{Content: mustJSON(journeyView(in.Context.Journey, in.Context.Demonstrated))}, nil
	}
	var r struct {
		Topics []struct {
			Title     string `json:"title"`
			Milestone string `json:"milestone"`
		} `json:"topics"`
		UsesMaps bool `json:"uses_maps"`
	}
	if err := structured(ctx, in.Provider, "journey.index", journeySystem, briefing(in, "Map out this subject"), journeySchema, &r); err != nil {
		return harness.AgentOutput{}, err
	}
	j := domain.Journey{UsesMaps: r.UsesMaps}
	if len(r.Topics) > maxTopics {
		r.Topics = r.Topics[:maxTopics]
	}
	for _, t := range r.Topics {
		title := clip(t.Title, limitTitle)
		if title == "" {
			continue
		}
		milestone := clip(t.Milestone, limitTitle)
		if milestone == "" {
			milestone = title
		}
		j.Topics = append(j.Topics, domain.JourneyTopic{Title: title, Milestone: milestone, Goal: domain.StepsPerTopic})
	}
	if len(j.Topics) == 0 {
		return harness.AgentOutput{}, fmt.Errorf("%w: empty index", provider.ErrMalformed)
	}
	return harness.AgentOutput{
		Content: mustJSON(journeyView(&j, in.Context.Demonstrated)),
		Publish: []domain.Event{{Type: domain.EvJourneySet, Payload: mustJSON(j)}},
	}, nil
}

func (a journeyAgent) Init(ctx context.Context, in harness.AgentInput) (harness.AgentOutput, error) {
	return a.build(ctx, in)
}
func (a journeyAgent) Handle(ctx context.Context, in harness.AgentInput, ev domain.Event) (harness.AgentOutput, error) {
	return a.build(ctx, in)
}
func (a journeyAgent) Update(ctx context.Context, in harness.AgentInput, d harness.Directive) (harness.AgentOutput, error) {
	return a.build(ctx, in)
}

// Retain is empty: the journey is stored with the pursuit.
func (journeyAgent) Retain(content, state json.RawMessage) []domain.Artifact { return nil }

// --- Map ---

// MapContent is the Map card: places on an outline map.
type MapContent struct {
	Title   string             `json:"title"`
	Places  []domain.GamePlace `json:"places"`
	Connect bool               `json:"connect"`
}

const mapSystem = `You choose places for an outline map of the land, with no country borders drawn. Pick 3 to 10 places that matter to the current topic: cities, sites, rivers' key points, battles or trade stops. "label" is 1 to 3 words. "lat" and "lon" are real coordinates in degrees: use only places whose position you're sure of, and for an ancient place use its modern location. "fact" is one short line, at most 15 words, about why it matters. "when" is a short date if time matters, else empty. Set "connect" to true only if the order is a real route or journey, listed in order. "title" is 2 to 4 words.`

type mapAgent struct{}

func (mapAgent) Type() domain.CellType { return domain.CellMap }

func (mapAgent) generate(ctx context.Context, in harness.AgentInput, objective, extra string) (harness.AgentOutput, error) {
	prompt := briefing(in, objective) + extra
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		var c MapContent
		if err := structured(ctx, in.Provider, "map.places", mapSystem, prompt, mapSchema, &c); err != nil {
			return harness.AgentOutput{}, err
		}
		c.Title = clip(c.Title, limitTitle)
		clipPlaces(c.Places)
		if err := expr.ValidatePlaces(c.Places, 2, 12); err != nil {
			lastErr = err
			prompt = briefing(in, objective) + extra + "\nYour previous list was rejected: " + err.Error()
			continue
		}
		return harness.AgentOutput{Content: mustJSON(c)}, nil
	}
	return harness.AgentOutput{}, fmt.Errorf("%w: %v", provider.ErrMalformed, lastErr)
}

func (a mapAgent) Init(ctx context.Context, in harness.AgentInput) (harness.AgentOutput, error) {
	return a.generate(ctx, in, "", "")
}
func (mapAgent) Handle(ctx context.Context, in harness.AgentInput, ev domain.Event) (harness.AgentOutput, error) {
	return harness.AgentOutput{}, nil
}
func (a mapAgent) Update(ctx context.Context, in harness.AgentInput, d harness.Directive) (harness.AgentOutput, error) {
	return a.generate(ctx, in, d.Objective, adjustment(d))
}
func (mapAgent) Retain(content, state json.RawMessage) []domain.Artifact {
	return []domain.Artifact{{Content: content}}
}

// --- What If ---

// ThoughtContent is a What If card. Reveal is empty until the student responds.
type ThoughtContent struct {
	Scenario string `json:"scenario"`
	Question string `json:"question"`
	Reveal   string `json:"reveal,omitempty"`
	Feedback string `json:"feedback,omitempty"`
	Response string `json:"response,omitempty"`
}

type thoughtState struct {
	Reveal    string   `json:"reveal"`
	Responses []string `json:"responses,omitempty"`
}

const thoughtSystem = `Write a short "what if" puzzle that changes one thing and tests an assumption. "scenario" is at most 15 words, "question" at most 12 words, and "reveal" says what actually happens in at most 20 words. The reveal is shown only after the student guesses.`
const thoughtRespondSystem = `The student guessed the outcome of a "what if". In at most 20 words, compare their guess with what happens, kindly.`

type thoughtAgent struct{}

func (thoughtAgent) Type() domain.CellType { return domain.CellThought }

func (thoughtAgent) scenario(ctx context.Context, in harness.AgentInput, objective, extra string, prior thoughtState) (harness.AgentOutput, error) {
	var r struct {
		Scenario string `json:"scenario"`
		Question string `json:"question"`
		Reveal   string `json:"reveal"`
	}
	if err := structured(ctx, in.Provider, "thought.scenario", thoughtSystem, briefing(in, objective)+extra, thoughtSchema, &r); err != nil {
		return harness.AgentOutput{}, err
	}
	return harness.AgentOutput{
		Content:    mustJSON(ThoughtContent{Scenario: clip(r.Scenario, limitQuestion), Question: clip(r.Question, limitQuestion)}),
		LocalState: mustJSON(thoughtState{Reveal: clip(r.Reveal, limitExample), Responses: prior.Responses}),
	}, nil
}

func (a thoughtAgent) Init(ctx context.Context, in harness.AgentInput) (harness.AgentOutput, error) {
	return a.scenario(ctx, in, "", "", thoughtState{})
}

func (thoughtAgent) Handle(ctx context.Context, in harness.AgentInput, ev domain.Event) (harness.AgentOutput, error) {
	if ev.Type != domain.EvThoughtSubmitted {
		return harness.AgentOutput{}, nil
	}
	var c ThoughtContent
	var st thoughtState
	_ = json.Unmarshal(in.Cell.Content, &c)
	_ = json.Unmarshal(in.Cell.LocalState, &st)
	response := textPayload(ev)
	var r struct {
		Feedback string `json:"feedback"`
	}
	prompt := briefing(in, "") + "\nScenario: " + c.Scenario + "\nWhat happens: " + st.Reveal + "\nStudent's guess:\n<trigger>" + response + "</trigger>"
	if err := structured(ctx, in.Provider, "thought.respond", thoughtRespondSystem, prompt, thoughtRespondSchema, &r); err != nil {
		return harness.AgentOutput{}, err
	}
	c.Reveal, c.Feedback, c.Response = st.Reveal, clip(r.Feedback, limitReply), response
	st.Responses = append(st.Responses, response)
	return harness.AgentOutput{Content: mustJSON(c), LocalState: mustJSON(st)}, nil
}

func (a thoughtAgent) Update(ctx context.Context, in harness.AgentInput, d harness.Directive) (harness.AgentOutput, error) {
	var st thoughtState
	_ = json.Unmarshal(in.Cell.LocalState, &st)
	return a.scenario(ctx, in, d.Objective, adjustment(d), st)
}

func (thoughtAgent) Retain(content, state json.RawMessage) []domain.Artifact {
	var st thoughtState
	_ = json.Unmarshal(state, &st)
	return []domain.Artifact{{Content: mustJSON(map[string]any{"last": content, "responses": st.Responses}), LearnerAuthored: len(st.Responses) > 0}}
}

// --- My Notes ---

// DocumentContent is the My Notes cell's content. Objects load from the API.
type DocumentContent struct {
	ObjectCount int `json:"objectCount"`
}

type documentAgent struct{}

func (documentAgent) Type() domain.CellType { return domain.CellDocument }

func (documentAgent) build(in harness.AgentInput) (harness.AgentOutput, error) {
	return harness.AgentOutput{Content: mustJSON(DocumentContent{ObjectCount: len(in.Context.Objects)})}, nil
}
func (a documentAgent) Init(ctx context.Context, in harness.AgentInput) (harness.AgentOutput, error) {
	return a.build(in)
}
func (a documentAgent) Handle(ctx context.Context, in harness.AgentInput, ev domain.Event) (harness.AgentOutput, error) {
	return a.build(in)
}
func (a documentAgent) Update(ctx context.Context, in harness.AgentInput, d harness.Directive) (harness.AgentOutput, error) {
	return a.build(in)
}

// Retain is empty: Living Document objects are stored durably as they change.
func (documentAgent) Retain(content, state json.RawMessage) []domain.Artifact { return nil }

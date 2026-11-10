package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"deepflow/internal/domain"
	"deepflow/internal/expr"
	"deepflow/internal/harness"
	"deepflow/internal/provider"
)

func input(m *provider.Mock, t domain.CellType) harness.AgentInput {
	return harness.AgentInput{
		Cell:     domain.Cell{ID: "c1", Type: t, Objective: "Learn about forces"},
		Context:  harness.ContextView{PursuitTitle: "Force and Motion", Grade: 7, Inquiry: "Why do things move?"},
		Provider: m,
		Delta:    func(string) {},
		History:  []provider.Turn{{Role: "user", Content: "hello"}},
	}
}

func payload(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func apply(in *harness.AgentInput, out harness.AgentOutput) {
	if out.Content != nil {
		in.Cell.Content = out.Content
	}
	if out.LocalState != nil {
		in.Cell.LocalState = out.LocalState
	}
}

func TestEachAgentInitProducesValidContent(t *testing.T) {
	for _, ct := range []domain.CellType{domain.CellDialogue, domain.CellConcept, domain.CellGame, domain.CellQuiz, domain.CellJourney, domain.CellThought, domain.CellDocument, domain.CellMap} {
		a := New(ct)
		if a.Type() != ct {
			t.Errorf("type %s", a.Type())
		}
		out, err := a.Init(context.Background(), input(provider.NewMock(), ct))
		if err != nil {
			t.Errorf("%s: %v", ct, err)
			continue
		}
		var v map[string]any
		if err := json.Unmarshal(out.Content, &v); err != nil || len(v) == 0 {
			t.Errorf("%s: content %s (%v)", ct, out.Content, err)
		}
	}
}

func TestPromptsNameTheStudentsClass(t *testing.T) {
	if p := briefing(input(provider.NewMock(), domain.CellQuiz), ""); !strings.Contains(p, "class 7") {
		t.Fatal(p)
	}
}

func TestClipEnforcesHardLimits(t *testing.T) {
	long := strings.Repeat("word ", 100)
	got := clip(long, 40)
	if utf8.RuneCountInString(got) > 40 || !strings.HasSuffix(got, "…") {
		t.Fatalf("%q", got)
	}
	if clip("short", 40) != "short" {
		t.Fatal("short text must be untouched")
	}
}

func TestClipKeepsMathsWhole(t *testing.T) {
	s := "The size is \\(\\sqrt{3^2+4^2}=5\\) ohms and then quite a lot more words follow here"
	got := clip(s, 30)
	if strings.Count(got, "\\(") != strings.Count(got, "\\)") {
		t.Fatalf("cut through a formula: %q", got)
	}
	long := "\\(\\frac{a+b+c+d+e+f+g+h+i+j+k+l+m+n}{o+p+q+r+s+t+u+v+w+x+y+z+1+2+3+4+5+6}\\)"
	if got := clip(long, limitOption); got != long {
		t.Fatalf("an answer that's one formula must survive: %q", got)
	}
	display := "Here: \\[x^2+y^2+z^2=1\\] and lots and lots of more text after that one"
	got = clip(display, 12)
	if strings.Contains(got, "\\[") && !strings.Contains(got, "\\]") {
		t.Fatalf("cut through a display formula: %q", got)
	}
}

func TestPromptsAskForRenderableMaths(t *testing.T) {
	if !strings.Contains(sharedPrinciples, "\\(") {
		t.Fatal("agents must be told how to write maths")
	}
}

func TestChatPointsToTheGameCardRatherThanPlaying(t *testing.T) {
	sys := dialogueSystem(input(provider.NewMock(), domain.CellDialogue))
	if !strings.Contains(sys, "Game card") {
		t.Fatal(sys)
	}
}

func TestDialogueStreamsAndPublishesAgentMessage(t *testing.T) {
	in := input(provider.NewMock(), domain.CellDialogue)
	var got strings.Builder
	in.Delta = func(s string) { got.WriteString(s) }
	out, err := New(domain.CellDialogue).Handle(context.Background(), in, domain.Event{Type: domain.EvUserMessage, Payload: payload(map[string]string{"text": "hi"})})
	if err != nil {
		t.Fatal(err)
	}
	if got.Len() == 0 || len(out.Publish) != 1 || out.Publish[0].Type != domain.EvAgentMessage {
		t.Fatalf("%q %+v", got.String(), out.Publish)
	}
}

func TestDialogueOpensNewConversationOnly(t *testing.T) {
	in := input(provider.NewMock(), domain.CellDialogue)
	n := 0
	in.Delta = func(string) { n++ }
	if _, err := New(domain.CellDialogue).Init(context.Background(), in); err != nil || n != 0 {
		t.Fatalf("existing history must stay quiet: %d %v", n, err)
	}
	in.History = nil
	if _, err := New(domain.CellDialogue).Init(context.Background(), in); err != nil || n == 0 {
		t.Fatalf("new conversation should open: %d %v", n, err)
	}
}

// --- Quiz ---

func quizRig(t *testing.T, m *provider.Mock) (harness.Agent, harness.AgentInput, QuizContent) {
	t.Helper()
	a := New(domain.CellQuiz)
	in := input(m, domain.CellQuiz)
	out, err := a.Init(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	apply(&in, out)
	var c QuizContent
	json.Unmarshal(out.Content, &c)
	return a, in, c
}

func answer(t *testing.T, a harness.Agent, in *harness.AgentInput, c QuizContent, choice int) (harness.AgentOutput, QuizContent) {
	t.Helper()
	out, err := a.Handle(context.Background(), *in, domain.Event{ID: "ev1", Type: domain.EvQuizAnswered, Payload: payload(map[string]any{"questionId": c.ID, "choice": choice})})
	if err != nil {
		t.Fatal(err)
	}
	apply(in, out)
	var next QuizContent
	json.Unmarshal(in.Cell.Content, &next)
	return out, next
}

func correctChoice(in harness.AgentInput) int {
	var st quizState
	json.Unmarshal(in.Cell.LocalState, &st)
	return st.Current.Answer
}

func TestQuizHidesAnswerUntilAnswered(t *testing.T) {
	_, in, c := quizRig(t, provider.NewMock())
	if c.Prompt == "" || len(c.Options) < 2 || c.Result != nil {
		t.Fatalf("%+v", c)
	}
	if strings.Contains(string(in.Cell.Content), `"answer"`) || strings.Contains(string(in.Cell.Content), `"why"`) {
		t.Fatalf("answer leaked to the client: %s", in.Cell.Content)
	}
}

func TestQuizCorrectAnswerPublishesResult(t *testing.T) {
	a, in, c := quizRig(t, provider.NewMock())
	out, next := answer(t, a, &in, c, correctChoice(in))
	if next.Result == nil || !next.Result.Correct || next.Result.Why == "" || next.Streak != 1 {
		t.Fatalf("%+v", next)
	}
	if len(out.Publish) != 1 || out.Publish[0].Type != domain.EvQuizResult {
		t.Fatalf("%+v", out.Publish)
	}
	var r QuizResultEvent
	json.Unmarshal(out.Publish[0].Payload, &r)
	if !r.Correct || r.SourceEventID != "ev1" || r.Question == "" {
		t.Fatalf("%+v", r)
	}
}

func TestQuizWrongAnswerShowsCorrectOne(t *testing.T) {
	a, in, c := quizRig(t, provider.NewMock())
	right := correctChoice(in)
	_, next := answer(t, a, &in, c, (right+1)%len(c.Options))
	if next.Result.Correct || next.Result.Answer != right || next.Streak != 0 {
		t.Fatalf("%+v", next)
	}
}

func TestQuizNextShowsANewQuestionWithoutAModelCall(t *testing.T) {
	m := provider.NewMock()
	a, in, c := quizRig(t, m)
	answer(t, a, &in, c, 0)
	calls := m.Calls("quiz.batch")
	out, err := a.Handle(context.Background(), in, domain.Event{Type: domain.EvQuizNext})
	if err != nil {
		t.Fatal(err)
	}
	var next QuizContent
	json.Unmarshal(out.Content, &next)
	if next.ID == c.ID || next.Result != nil || next.Prompt == "" || m.Calls("quiz.batch") != calls {
		t.Fatalf("%+v calls %d", next, m.Calls("quiz.batch"))
	}
}

func TestQuizIgnoresRepeatAndInvalidAnswers(t *testing.T) {
	a, in, c := quizRig(t, provider.NewMock())
	for _, bad := range []map[string]any{{"questionId": "stale", "choice": 0}, {"questionId": c.ID, "choice": 99}, {"questionId": c.ID, "choice": -1}} {
		out, err := a.Handle(context.Background(), in, domain.Event{Type: domain.EvQuizAnswered, Payload: payload(bad)})
		if err != nil || out.Content != nil || len(out.Publish) != 0 {
			t.Fatalf("%v accepted: %+v %v", bad, out, err)
		}
	}
	answer(t, a, &in, c, 0)
	out, err := a.Handle(context.Background(), in, domain.Event{Type: domain.EvQuizAnswered, Payload: payload(map[string]any{"questionId": c.ID, "choice": 0})})
	if err != nil || len(out.Publish) != 0 {
		t.Fatalf("double answer counted: %+v", out)
	}
}

func TestQuizPrefetchesBeforeRunningOut(t *testing.T) {
	m := provider.NewMock()
	a, in, c := quizRig(t, m)
	var sawPrefetch bool
	for i := 0; i < 6 && !sawPrefetch; i++ {
		out, _ := answer(t, a, &in, c, 0)
		if out.Then != nil && out.Then.Kind == "prefetch" {
			sawPrefetch = true
			before := m.Calls("quiz.batch")
			up, err := a.Update(context.Background(), in, *out.Then)
			if err != nil || up.LocalState == nil || up.Content != nil || m.Calls("quiz.batch") != before+1 {
				t.Fatalf("prefetch must only refill the queue: %+v %v", up, err)
			}
			apply(&in, up)
			break
		}
		nx, err := a.Handle(context.Background(), in, domain.Event{Type: domain.EvQuizNext})
		if err != nil {
			t.Fatal(err)
		}
		apply(&in, nx)
		json.Unmarshal(in.Cell.Content, &c)
	}
	if !sawPrefetch {
		t.Fatal("never prefetched")
	}
}

func TestQuizLevelFollowsStreak(t *testing.T) {
	a, in, c := quizRig(t, provider.NewMock())
	for i := 0; i < 3; i++ {
		_, _ = answer(t, a, &in, c, correctChoice(in))
		nx, _ := a.Handle(context.Background(), in, domain.Event{Type: domain.EvQuizNext})
		apply(&in, nx)
		json.Unmarshal(in.Cell.Content, &c)
	}
	if c.Level != 2 {
		t.Fatalf("level %d after 3 right", c.Level)
	}
	for i := 0; i < 2; i++ {
		right := correctChoice(in)
		_, _ = answer(t, a, &in, c, (right+1)%len(c.Options))
		nx, _ := a.Handle(context.Background(), in, domain.Event{Type: domain.EvQuizNext})
		apply(&in, nx)
		json.Unmarshal(in.Cell.Content, &c)
	}
	if c.Level != 1 {
		t.Fatalf("level %d after 2 wrong", c.Level)
	}
}

func TestQuizTopicChangeKeepsTheQuestionOnScreen(t *testing.T) {
	a, in, c := quizRig(t, provider.NewMock())
	answer(t, a, &in, c, 0) // feedback is showing
	var before quizState
	json.Unmarshal(in.Cell.LocalState, &before)
	out, err := a.Update(context.Background(), in, harness.Directive{Kind: "update", Objective: "Next topic: Friction"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != nil {
		t.Fatal("a topic change must not wipe the student's feedback")
	}
	var after quizState
	json.Unmarshal(out.LocalState, &after)
	if len(after.Queue) == 0 || after.Current.ID != before.Current.ID {
		t.Fatalf("%+v", after)
	}
}

func TestQuizRefreshReplacesTheQuestion(t *testing.T) {
	a, in, c := quizRig(t, provider.NewMock())
	out, err := a.Update(context.Background(), in, harness.Directive{Kind: "refresh"})
	if err != nil || out.Content == nil {
		t.Fatalf("%+v %v", out, err)
	}
	var next QuizContent
	json.Unmarshal(out.Content, &next)
	if next.ID == c.ID {
		t.Fatal("refresh should show a new question")
	}
}

func TestQuizDropsMalformedQuestions(t *testing.T) {
	m := provider.NewMock()
	m.Override("quiz.batch", `{"questions":[{"prompt":"","options":["a","b"],"answer":0,"why":"w"},{"prompt":"q","options":["a"],"answer":0,"why":"w"},{"prompt":"q","options":["a","b"],"answer":5,"why":"w"},{"prompt":"good","options":["a","b"],"answer":1,"why":"w"}]}`)
	_, in, c := quizRig(t, m)
	if c.Prompt != "good" {
		t.Fatalf("%+v", c)
	}
	var st quizState
	json.Unmarshal(in.Cell.LocalState, &st)
	if len(st.Queue) != 0 {
		t.Fatalf("%+v", st.Queue)
	}
	m2 := provider.NewMock()
	m2.Override("quiz.batch", `{"questions":[{"prompt":"","options":[],"answer":0,"why":""}]}`)
	if _, err := New(domain.CellQuiz).Init(context.Background(), input(m2, domain.CellQuiz)); err == nil {
		t.Fatal("a batch with nothing usable should fail")
	}
}

// --- Game ---

func TestGameSpecsAreValidAndWinnable(t *testing.T) {
	for _, title := range []string{"Force and Motion", "Mughal History", "Poetry", "Rivers of India geography"} {
		in := input(provider.NewMock(), domain.CellGame)
		in.Context.PursuitTitle = title
		out, err := New(domain.CellGame).Init(context.Background(), in)
		if err != nil {
			t.Fatalf("%s: %v", title, err)
		}
		var c GameContent
		json.Unmarshal(out.Content, &c)
		if err := expr.ValidateGame(c.GameSpec); err != nil {
			t.Fatalf("%s: %v", title, err)
		}
	}
}

func TestGameRetriesOnceOnInvalidSpecThenFails(t *testing.T) {
	m := provider.NewMock()
	bad := `{"kind":"target","title":"t","goal":"g","params":[{"id":"x","label":"X","min":0,"max":1,"step":0.1,"value":0.5,"unit":""}],"output":{"label":"Y","expr":"x*evil","unit":""},"levels":[{"target":1,"tolerance":0.1,"hint":""}],"note":"","basis":"illustrative","mode":"","items":[],"groups":[]}`
	m.Override("game.spec", bad, bad)
	if _, err := New(domain.CellGame).Init(context.Background(), input(m, domain.CellGame)); err == nil {
		t.Fatal("want error")
	}
	if m.Calls("game.spec") != 2 {
		t.Fatalf("calls %d", m.Calls("game.spec"))
	}
}

func TestGameRecoversOnRetry(t *testing.T) {
	m := provider.NewMock()
	m.Override("game.spec", `{"kind":"nonsense"}`)
	out, err := New(domain.CellGame).Init(context.Background(), input(m, domain.CellGame))
	if err != nil || out.Content == nil {
		t.Fatalf("%v", err)
	}
}

func won(level int, force, mass float64) json.RawMessage {
	return payload(map[string]any{"level": level, "values": map[string]float64{"force": force, "mass": mass}})
}

func TestGameLevelWonEarnsOneStarPerLevel(t *testing.T) {
	a := New(domain.CellGame)
	in := input(provider.NewMock(), domain.CellGame)
	out, _ := a.Init(context.Background(), in)
	apply(&in, out)
	win := func(p json.RawMessage) harness.AgentOutput {
		out, err := a.Handle(context.Background(), in, domain.Event{Type: domain.EvGameLevelWon, Payload: p})
		if err != nil {
			t.Fatal(err)
		}
		apply(&in, out)
		return out
	}
	if o := win(won(0, 50, 10)); len(o.Publish) != 1 || o.Publish[0].Type != domain.EvGameStar {
		t.Fatalf("%+v", o.Publish)
	}
	if o := win(won(0, 50, 10)); len(o.Publish) != 0 {
		t.Fatalf("same level counted twice: %+v", o.Publish)
	}
	if o := win(won(99, 50, 10)); len(o.Publish) != 0 {
		t.Fatalf("impossible level counted: %+v", o.Publish)
	}
	var c GameContent
	json.Unmarshal(in.Cell.Content, &c)
	if c.Stars != 1 || len(c.Won) != 1 {
		t.Fatalf("%+v", c)
	}
}

func TestGameRejectsAWinTheServerCantVerify(t *testing.T) {
	a := New(domain.CellGame)
	in := input(provider.NewMock(), domain.CellGame)
	out, _ := a.Init(context.Background(), in)
	apply(&in, out)
	for name, p := range map[string]json.RawMessage{
		"no values":        payload(map[string]int{"level": 0}),
		"off target":       won(0, 10, 10),
		"slider off range": won(0, 5000, 1000),
	} {
		o, err := a.Handle(context.Background(), in, domain.Event{Type: domain.EvGameLevelWon, Payload: p})
		if err != nil || len(o.Publish) != 0 || o.Content != nil {
			t.Errorf("%s: credited: %+v %v", name, o, err)
		}
	}
}

func TestSortWinsAreCheckedOnTheServer(t *testing.T) {
	a := New(domain.CellGame)
	in := input(provider.NewMock(), domain.CellGame)
	in.Context.PursuitTitle = "Mughal History"
	out, _ := a.Init(context.Background(), in)
	apply(&in, out)
	var c GameContent
	json.Unmarshal(out.Content, &c)
	right := []string{}
	for _, it := range c.Items {
		right = append(right, it.Label)
	}
	wrong := append([]string{right[1], right[0]}, right[2:]...)
	o, _ := a.Handle(context.Background(), in, domain.Event{Type: domain.EvGameLevelWon, Payload: payload(map[string]any{"level": 0, "order": wrong})})
	if len(o.Publish) != 0 {
		t.Fatal("wrong order earned a star")
	}
	o, _ = a.Handle(context.Background(), in, domain.Event{Type: domain.EvGameLevelWon, Payload: payload(map[string]any{"level": 0, "order": right})})
	if len(o.Publish) != 1 {
		t.Fatal("right order earned nothing")
	}
}

func TestGameNextKeepsStars(t *testing.T) {
	a := New(domain.CellGame)
	in := input(provider.NewMock(), domain.CellGame)
	out, _ := a.Init(context.Background(), in)
	apply(&in, out)
	var first GameContent
	json.Unmarshal(out.Content, &first)
	o, _ := a.Handle(context.Background(), in, domain.Event{Type: domain.EvGameLevelWon, Payload: won(0, 50, 10)})
	apply(&in, o)
	o, err := a.Handle(context.Background(), in, domain.Event{Type: domain.EvGameNext})
	if err != nil {
		t.Fatal(err)
	}
	var c GameContent
	json.Unmarshal(o.Content, &c)
	if c.Stars != 1 || len(c.Won) != 0 {
		t.Fatalf("%+v", c)
	}
	if c.ID == "" || c.ID == first.ID {
		t.Fatalf("each game needs its own id, or the page keeps stale slider state: %q %q", c.ID, first.ID)
	}
}

func TestGameLabelsAreClipped(t *testing.T) {
	long := strings.Repeat("very long label ", 20)
	m := provider.NewMock()
	spec := map[string]any{"kind": "sort", "title": "t", "goal": "g", "mode": "groups", "basis": "none", "note": "", "params": []any{}, "levels": []any{},
		"output": map[string]any{"label": "", "expr": "", "unit": ""},
		"groups": []string{"a " + long, "b " + long},
		"items":  []map[string]string{{"label": "1 " + long, "group": "a " + long}, {"label": "2 " + long, "group": "b " + long}, {"label": "3 " + long, "group": "a " + long}, {"label": "4 " + long, "group": "b " + long}},
	}
	m.Override("game.spec", string(payload(spec)))
	out, err := New(domain.CellGame).Init(context.Background(), input(m, domain.CellGame))
	if err != nil {
		t.Fatal(err)
	}
	var c GameContent
	json.Unmarshal(out.Content, &c)
	for _, g := range c.Groups {
		if utf8.RuneCountInString(g) > limitChip {
			t.Fatalf("group %q", g)
		}
	}
	for _, it := range c.Items {
		if utf8.RuneCountInString(it.Label) > limitItem {
			t.Fatalf("item %q", it.Label)
		}
	}
	if err := expr.ValidateGame(c.GameSpec); err != nil {
		t.Fatalf("clipping broke the game's own rules: %v", err)
	}
}

func TestLocateGameForGeography(t *testing.T) {
	in := input(provider.NewMock(), domain.CellGame)
	in.Context.PursuitTitle = "Rivers of India geography"
	a := New(domain.CellGame)
	out, err := a.Init(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	apply(&in, out)
	var c GameContent
	json.Unmarshal(out.Content, &c)
	if c.Kind != "locate" || len(c.Places) < 2 {
		t.Fatalf("%+v", c)
	}
	far := payload(map[string]any{"level": 0, "point": map[string]float64{"lat": -30, "lon": 150}})
	if o, _ := a.Handle(context.Background(), in, domain.Event{Type: domain.EvGameLevelWon, Payload: far}); len(o.Publish) != 0 {
		t.Fatal("a tap on the far side of the world won")
	}
	near := payload(map[string]any{"level": 0, "point": map[string]float64{"lat": c.Places[0].Lat, "lon": c.Places[0].Lon}})
	if o, _ := a.Handle(context.Background(), in, domain.Event{Type: domain.EvGameLevelWon, Payload: near}); len(o.Publish) != 1 {
		t.Fatal("a tap on the place earned nothing")
	}
}

func TestMapCardHasValidPlaces(t *testing.T) {
	in := input(provider.NewMock(), domain.CellMap)
	in.Context.PursuitTitle = "Mughal History"
	out, err := New(domain.CellMap).Init(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	var c MapContent
	json.Unmarshal(out.Content, &c)
	if c.Title == "" || len(c.Places) < 2 {
		t.Fatalf("%+v", c)
	}
	if err := expr.ValidatePlaces(c.Places, 2, 12); err != nil {
		t.Fatal(err)
	}
}

func TestMapRetriesOnceOnBadCoordinatesThenFails(t *testing.T) {
	m := provider.NewMock()
	bad := `{"title":"t","places":[{"label":"a","lat":200,"lon":0,"fact":"f","when":""},{"label":"b","lat":0,"lon":0,"fact":"f","when":""}],"connect":false}`
	m.Override("map.places", bad, bad)
	if _, err := New(domain.CellMap).Init(context.Background(), input(m, domain.CellMap)); err == nil {
		t.Fatal("want error")
	}
	if m.Calls("map.places") != 2 {
		t.Fatalf("calls %d", m.Calls("map.places"))
	}
}

func TestMapClipsLongText(t *testing.T) {
	m := provider.NewMock()
	long := strings.Repeat("word ", 80)
	m.Override("map.places", string(payload(map[string]any{"title": long, "connect": false, "places": []map[string]any{{"label": "1 " + long, "lat": 1, "lon": 1, "fact": long, "when": long}, {"label": "2 " + long, "lat": 2, "lon": 2, "fact": long, "when": long}}})))
	out, err := New(domain.CellMap).Init(context.Background(), input(m, domain.CellMap))
	if err != nil {
		t.Fatal(err)
	}
	var c MapContent
	json.Unmarshal(out.Content, &c)
	if utf8.RuneCountInString(c.Places[0].Label) > limitChip || utf8.RuneCountInString(c.Places[0].Fact) > limitWhy {
		t.Fatalf("%+v", c.Places[0])
	}
}

func TestJourneyRecordsWhetherMapsHelp(t *testing.T) {
	in := input(provider.NewMock(), domain.CellJourney)
	in.Context.PursuitTitle = "Mughal History"
	out, err := New(domain.CellJourney).Init(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	var j domain.Journey
	json.Unmarshal(out.Publish[0].Payload, &j)
	if !j.UsesMaps {
		t.Fatal("history should use maps")
	}
	in.Context.PursuitTitle = "Rivers of India geography"
	out, _ = New(domain.CellJourney).Init(context.Background(), in)
	var geo domain.Journey
	json.Unmarshal(out.Publish[0].Payload, &geo)
	if !geo.UsesMaps {
		t.Fatal("geography should use maps")
	}
	in.Context.PursuitTitle = "Force and Motion"
	in.Context.Journey = nil
	out, _ = New(domain.CellJourney).Init(context.Background(), in)
	var forces domain.Journey
	json.Unmarshal(out.Publish[0].Payload, &forces)
	if forces.UsesMaps {
		t.Fatal("forces shouldn't")
	}
}

func TestValidContent(t *testing.T) {
	cases := []struct {
		t    domain.CellType
		raw  string
		want bool
	}{
		{domain.CellConcept, `{"title":"T","definition":"old"}`, false},
		{domain.CellConcept, `{"title":"T","idea":"new","example":"e"}`, true},
		{domain.CellGame, `{"title":"Old","series":[]}`, false},
		{domain.CellGame, `{"kind":"sort","title":"T","won":[],"mode":"order","items":[]}`, true},
		{domain.CellQuiz, `{"prompt":"p"}`, false},
		{domain.CellQuiz, `{"id":"q","prompt":"p","options":["a","b"]}`, true},
		{domain.CellJourney, `{}`, false},
		{domain.CellJourney, `{"topics":[],"shown":[]}`, true},
		{domain.CellThought, `{"scenario":"s","constraint":"c","question":"q"}`, true},
		{domain.CellThought, `{"title":"x"}`, false},
		{domain.CellMap, `{"title":"x"}`, false},
		{domain.CellMap, `{"title":"x","places":[]}`, true},
		{domain.CellDialogue, `{}`, true},
		{domain.CellDocument, `{}`, true},
		{domain.CellConcept, `not json`, false},
	}
	for _, c := range cases {
		if got := ValidContent(c.t, json.RawMessage(c.raw)); got != c.want {
			t.Errorf("%s %s: %v", c.t, c.raw, got)
		}
	}
}

// --- Journey ---

func TestJourneyGeneratesIndexOnce(t *testing.T) {
	m := provider.NewMock()
	a := New(domain.CellJourney)
	in := input(m, domain.CellJourney)
	out, err := a.Init(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Publish) != 1 || out.Publish[0].Type != domain.EvJourneySet {
		t.Fatalf("%+v", out.Publish)
	}
	var j domain.Journey
	json.Unmarshal(out.Publish[0].Payload, &j)
	if len(j.Topics) < 2 || j.Topics[0].Goal != domain.StepsPerTopic {
		t.Fatalf("%+v", j)
	}
	in.Context.Journey = &j
	before := m.Calls("journey.index")
	out, err = a.Init(context.Background(), in)
	if err != nil || len(out.Publish) != 0 || m.Calls("journey.index") != before {
		t.Fatalf("existing journey regenerated: %+v %v", out, err)
	}
}

func TestJourneyShowsProgressAndEvidence(t *testing.T) {
	a := New(domain.CellJourney)
	in := input(provider.NewMock(), domain.CellJourney)
	in.Context.Journey = &domain.Journey{Current: 1, Topics: []domain.JourneyTopic{
		{Title: "A", Milestone: "M1", Goal: 3, Progress: 3, Done: true},
		{Title: "B", Milestone: "M2", Goal: 3, Progress: 2},
	}}
	in.Context.Demonstrated = []harness.DemonstratedItem{{Text: "force: explained", Tier: "strong"}, {Text: "force: more", Tier: "moderate"}, {Text: "mass: ok", Tier: "moderate"}}
	out, err := a.Update(context.Background(), in, harness.Directive{Kind: "update"})
	if err != nil {
		t.Fatal(err)
	}
	var c JourneyContent
	json.Unmarshal(out.Content, &c)
	if c.Current != 1 || c.Topics[1].Progress != 2 || c.Next == "" || c.Complete {
		t.Fatalf("%+v", c)
	}
	if !strings.Contains(c.Next, "1") {
		t.Fatalf("should say how many steps remain: %q", c.Next)
	}
	if len(c.Shown) != 2 || c.Shown[0].Concept != "force" || c.Shown[0].Tier != "strong" {
		t.Fatalf("one entry per concept, strongest tier: %+v", c.Shown)
	}
}

func TestConceptRequestsReplacementWhenFulfilled(t *testing.T) {
	m := provider.NewMock()
	a := New(domain.CellConcept)
	in := input(m, domain.CellConcept)
	out, _ := a.Init(context.Background(), in)
	apply(&in, out)
	var c ConceptContent
	json.Unmarshal(out.Content, &c)
	in.Context.Demonstrated = []harness.DemonstratedItem{{Text: strings.ToLower(c.Title), Tier: "strong"}}
	out, err := a.Update(context.Background(), in, harness.Directive{Kind: "update", Objective: "o"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Requests) != 1 || out.Requests[0].Kind != "replace" {
		t.Fatalf("%+v", out.Requests)
	}
}

func TestConceptExploredRewritesInPlace(t *testing.T) {
	m := provider.NewMock()
	a := New(domain.CellConcept)
	in := input(m, domain.CellConcept)
	out, _ := a.Init(context.Background(), in)
	apply(&in, out)
	before := m.Calls("concept.content")
	out, err := a.Handle(context.Background(), in, domain.Event{Type: domain.EvConceptExplored, Payload: json.RawMessage(`{"concept":"Friction"}`)})
	if err != nil || out.Content == nil || m.Calls("concept.content") != before+1 {
		t.Fatalf("%v %v", out, err)
	}
}

func TestThoughtRevealHiddenUntilSubmit(t *testing.T) {
	a := New(domain.CellThought)
	in := input(provider.NewMock(), domain.CellThought)
	out, err := a.Init(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	var c ThoughtContent
	json.Unmarshal(out.Content, &c)
	if c.Reveal != "" || c.Scenario == "" {
		t.Fatalf("%+v", c)
	}
	apply(&in, out)
	out, err = a.Handle(context.Background(), in, domain.Event{Type: domain.EvThoughtSubmitted, Payload: payload(map[string]string{"text": "It moves"})})
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(out.Content, &c)
	if c.Reveal == "" {
		t.Fatal("reveal should appear after submit")
	}
	arts := a.Retain(out.Content, out.LocalState)
	if len(arts) != 1 || !arts[0].LearnerAuthored || !strings.Contains(string(arts[0].Content), "It moves") {
		t.Fatalf("%+v", arts)
	}
}

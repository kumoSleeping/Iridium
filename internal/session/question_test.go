package session

import (
	"context"
	"errors"
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

// A question's card: one group per question, multiple choice where the question says; own words
// answer a question in place of a choice, and every question must be answered.
func TestQuestionCard(t *testing.T) {
	r := newRig(t, nil)
	bg := context.Background()
	ask := func() *api.Card {
		id, err := r.m.Create(bg, api.NewSession{Cwd: r.cwd, Text: "a QUESTION"}, "网页")
		if err != nil {
			t.Fatal(err)
		}
		s := r.waitState(id, "the question", func(s *api.SessionState) bool { return s.Status == api.StatusWaiting })
		c := s.Asks[0].Card
		if c.Kind != "question" || len(c.Summary) != 2 || len(c.Options) != 5 || len(c.Multi) != 1 || c.Multi[0] != 1 {
			t.Fatalf("card: %+v", c)
		}
		return c
	}

	c := ask()
	// Not every question answered; words past the questions; no such option.
	for _, a := range []api.CardAnswer{
		{Card: c.ID, Choices: []string{"0.1"}},
		{Card: c.ID, Choices: []string{"0.1"}, Words: []string{"", "", "more"}},
		{Card: c.ID, Choices: []string{"0.9", "1.0"}},
	} {
		if err := r.m.AnswerCard(bg, a); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%+v: %v, want a bad request", a, err)
		}
	}
	if err := r.m.AnswerCard(bg, api.CardAnswer{Card: c.ID, Choices: []string{"0.1", "1.0", "1.2"}}); err != nil {
		t.Fatal(err)
	}
	id := c.Session
	r.waitState(id, "the answers", func(s *api.SessionState) bool {
		return s.Last != nil && s.Last.Text == "ok: answers Which color?=Blue; Which sizes?=S, L"
	})

	// Own words for the sizes, a choice for the color; words win over a choice of the same group.
	c = ask()
	if err := r.m.AnswerCard(bg, api.CardAnswer{Card: c.ID, Choices: []string{"0.0", "1.1"}, Words: []string{"", "  XXL  "}}); err != nil {
		t.Fatal(err)
	}
	r.waitState(c.Session, "the words", func(s *api.SessionState) bool {
		return s.Last != nil && s.Last.Text == "ok: answers Which color?=Red; Which sizes?=XXL"
	})
	// Words alone answer every question too.
	c = ask()
	if err := r.m.AnswerCard(bg, api.CardAnswer{Card: c.ID, Words: []string{"green", "XS"}}); err != nil {
		t.Fatal(err)
	}
	r.waitState(c.Session, "the words alone", func(s *api.SessionState) bool {
		return s.Last != nil && s.Last.Text == "ok: answers Which color?=green; Which sizes?=XS"
	})
}

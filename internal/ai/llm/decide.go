package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
)

// This file is the decision models: they answer named questions about a
// state with probabilities instead of writing text. OpenAI's is POST
// {base}/decisions; TypeSafe's (Jev) is POST {base}/systemone.

const (
	decisionsPath = "/decisions"
	systemOnePath = "/systemone"
)

// Question is one question a decision model answers about the state. With
// Choices it picks one of them; without, it says how likely the answer is
// yes. Yes and No, when set, say what counts as each.
type Question struct {
	Name         string
	Instructions string
	Yes, No      string
	Choices      []Choice
}

// Choice is one answer a choice question may pick.
type Choice struct {
	Value       string
	Description string
}

// Answer is a decision model's answer to one question: the probability of
// yes, or the choice picked and the model's confidence in it. Refused is
// set when the model declined the question.
type Answer struct {
	Probability float64
	Choice      string
	Confidence  float64
	Refused     bool
}

// Decide asks the client's model the questions about state, through
// OpenAI Decisions or, for FormatTypeSafe, TypeSafe's System One. Every
// question is answered or refused, or it is an error.
func (c *Client) Decide(ctx context.Context, state string, qs []Question) (map[string]Answer, error) {
	if c.Model == "" {
		return nil, fmt.Errorf("llm: model is empty")
	}
	if len(qs) == 0 {
		return nil, fmt.Errorf("llm: no questions")
	}
	path, payload := decisionsPath, c.decisionsPayload(state, qs)
	if c.format() == FormatTypeSafe {
		path, payload = systemOnePath, c.systemOnePayload(state, qs)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, http.MethodPost, path, nil, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	br, err := c.body(resp.Body)
	var b []byte
	if err == nil {
		b, err = br.all()
	}
	if err != nil {
		return nil, c.scrub(err)
	}
	var out map[string]Answer
	if c.format() == FormatTypeSafe {
		out, err = readSystemOne(b)
	} else {
		out, err = readDecisions(b)
	}
	if err != nil {
		return nil, err
	}
	for _, q := range qs {
		a, ok := out[q.Name]
		if !ok {
			return nil, fmt.Errorf("%w: no answer to %q", ErrProtocol, q.Name)
		}
		if a.Refused {
			continue
		}
		if len(q.Choices) > 0 && !hasChoice(q.Choices, a.Choice) {
			return nil, fmt.Errorf("%w: unknown choice for %q", ErrProtocol, q.Name)
		}
		if !unit(a.Probability) || !unit(a.Confidence) {
			return nil, fmt.Errorf("%w: probability out of range for %q", ErrProtocol, q.Name)
		}
	}
	return out, nil
}

func hasChoice(cs []Choice, v string) bool {
	for _, c := range cs {
		if c.Value == v {
			return true
		}
	}
	return false
}

func unit(f float64) bool { return !math.IsNaN(f) && f >= 0 && f <= 1 }

func (c *Client) decisionsPayload(state string, qs []Question) map[string]any {
	questions := make([]map[string]any, len(qs))
	for i, q := range qs {
		instructions := q.Instructions
		if len(q.Choices) == 0 {
			if q.Yes != "" {
				instructions += "\nThe predicate is true when: " + q.Yes
			}
			if q.No != "" {
				instructions += "\nThe predicate is false when: " + q.No
			}
		}
		w := map[string]any{"type": "predicate", "name": q.Name, "instructions": instructions}
		if len(q.Choices) > 0 {
			choices := make([]map[string]any, len(q.Choices))
			for j, ch := range q.Choices {
				choices[j] = map[string]any{"value": ch.Value}
				if ch.Description != "" {
					choices[j]["description"] = ch.Description
				}
			}
			w["type"], w["choices"] = "choice", choices
		}
		questions[i] = w
	}
	return map[string]any{"model": c.Model, "input": state, "questions": questions}
}

func readDecisions(b []byte) (map[string]Answer, error) {
	var parsed struct {
		Answers []struct {
			Type        string   `json:"type"`
			Name        string   `json:"name"`
			Probability *float64 `json:"probability"`
			Choice      any      `json:"choice"`
			Confidence  float64  `json:"confidence"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		return nil, fmt.Errorf("%w: invalid decision JSON", ErrProtocol)
	}
	out := map[string]Answer{}
	for _, a := range parsed.Answers {
		switch a.Type {
		case "refusal":
			out[a.Name] = Answer{Refused: true}
		case "predicate":
			if a.Probability == nil {
				return nil, fmt.Errorf("%w: predicate without probability", ErrProtocol)
			}
			out[a.Name] = Answer{Probability: *a.Probability}
		case "choice":
			out[a.Name] = Answer{Choice: fmt.Sprint(a.Choice), Confidence: a.Confidence}
		}
	}
	return out, nil
}

func (c *Client) systemOnePayload(state string, qs []Question) map[string]any {
	questions := map[string]any{}
	for _, q := range qs {
		w := map[string]any{"type": "noul", "instructions": q.Instructions}
		if q.Yes != "" || q.No != "" {
			criteria := map[string]string{}
			if q.Yes != "" {
				criteria["true"] = q.Yes
			}
			if q.No != "" {
				criteria["false"] = q.No
			}
			w["criteria"] = criteria
		}
		if len(q.Choices) > 0 {
			criteria := map[string]string{}
			for _, ch := range q.Choices {
				criteria[ch.Value] = ch.Description
			}
			w["type"], w["criteria"] = "choice", criteria
		}
		questions[q.Name] = w
	}
	return map[string]any{"model": c.Model, "state": state, "questions": questions}
}

func readSystemOne(b []byte) (map[string]Answer, error) {
	var parsed struct {
		Answers map[string]struct {
			Type       string   `json:"type"`
			Noul       *float64 `json:"noul"`
			Choice     string   `json:"choice"`
			Confidence *float64 `json:"confidence"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		return nil, fmt.Errorf("%w: invalid decision JSON", ErrProtocol)
	}
	out := map[string]Answer{}
	for name, a := range parsed.Answers {
		switch a.Type {
		case "noul":
			if a.Noul == nil {
				return nil, fmt.Errorf("%w: yes/no answer without probability", ErrProtocol)
			}
			out[name] = Answer{Probability: *a.Noul}
		case "choice":
			ans := Answer{Choice: a.Choice}
			if a.Confidence != nil {
				ans.Confidence = *a.Confidence
			}
			out[name] = ans
		}
	}
	return out, nil
}

package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// MaxModels is the most model IDs ListModels returns.
const MaxModels = 1000

// maxModelPages bounds the pages an Anthropic listing follows.
const maxModelPages = 10

// ListModels returns the chat models the endpoint offers, sorted and
// without duplicates: GET {base}/models, following Anthropic's pages.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	var ids []string
	after := ""
	for page := 0; page < maxModelPages; page++ {
		q := url.Values{}
		if c.format() == FormatAnthropic {
			q.Set("limit", "1000")
			if after != "" {
				q.Set("after_id", after)
			}
		}
		resp, err := c.do(ctx, http.MethodGet, "/models", q, nil)
		if err != nil {
			return nil, err
		}
		br, err := c.body(resp.Body)
		var b []byte
		if err == nil {
			b, err = br.all()
		}
		resp.Body.Close()
		if err != nil {
			return nil, c.scrub(err)
		}
		var parsed struct {
			Data    []modelEntry `json:"data"`
			Models  []modelEntry `json:"models"`
			HasMore bool         `json:"has_more"`
			LastID  string       `json:"last_id"`
		}
		if err := json.Unmarshal(b, &parsed); err != nil {
			// A bare list of models, as some endpoints answer.
			if err := json.Unmarshal(b, &parsed.Data); err != nil {
				return nil, fmt.Errorf("llm: models: %w", err)
			}
		}
		for _, m := range append(parsed.Data, parsed.Models...) {
			// A decision model's list holds only decision models.
			if id := strings.TrimSpace(string(m)); id != "" && (c.format() == FormatTypeSafe || IsChatModel(id)) {
				ids = append(ids, id)
			}
		}
		if c.format() != FormatAnthropic || !parsed.HasMore || parsed.LastID == "" || parsed.LastID == after {
			break
		}
		after = parsed.LastID
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) > MaxModels {
		ids = ids[:MaxModels]
	}
	return ids, nil
}

// notChat are words in the IDs of models that do not chat: embeddings,
// speech, images, video, moderation and legacy completion models.
var notChat = []string{
	"embed", "tts", "whisper", "transcribe", "speech", "audio", "realtime",
	"dall-e", "gpt-image", "imagen", "image-gen", "stable-diffusion", "sdxl", "flux",
	"moderation", "rerank", "sora", "veo", "davinci", "babbage",
}

// IsChatModel reports whether id names a model that can chat, judged by
// the words providers use in the IDs of the others.
func IsChatModel(id string) bool {
	id = strings.ToLower(id)
	for _, w := range notChat {
		if strings.Contains(id, w) {
			return false
		}
	}
	return true
}

// modelEntry is one model in a listing: an object with its ID or name, or
// the ID alone.
type modelEntry string

func (m *modelEntry) UnmarshalJSON(b []byte) error {
	var id string
	if json.Unmarshal(b, &id) == nil {
		*m = modelEntry(id)
		return nil
	}
	var obj struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	*m = modelEntry(obj.ID)
	if obj.ID == "" {
		*m = modelEntry(obj.Name)
	}
	return nil
}

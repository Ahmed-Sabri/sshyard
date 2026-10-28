package openai

import (
	"errors"
	"strings"
	"testing"
)

func TestRejectIncompleteAndInvalidResponses(t *testing.T) {
	for name, body := range map[string]string{
		"empty": "", "html": "<html>gateway error</html>",
		"missing finish":      "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n",
		"done without finish": "data: [DONE]\n\n",
		"length":              "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n",
		"broken args":         `{"choices":[{"message":{"tool_calls":[{"id":"x","function":{"name":"run","arguments":"{"}}]},"finish_reason":"tool_calls"}]}`,
		"empty args":          `{"choices":[{"message":{"tool_calls":[{"id":"x","function":{"name":"run","arguments":""}}]},"finish_reason":"tool_calls"}]}`,
		"null args":           `{"choices":[{"message":{"tool_calls":[{"id":"x","function":{"name":"run","arguments":"null"}}]},"finish_reason":"tool_calls"}]}`,
		"missing id":          `{"choices":[{"message":{"tool_calls":[{"function":{"name":"run","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := (&Client{}).readStream(strings.NewReader(body), nil); err == nil {
				t.Fatal("accepted invalid response")
			}
		})
	}
}

func TestCompleteResponses(t *testing.T) {
	for _, body := range []string{
		"id\nextension: allowed\ndata: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n",
		" \n" + `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`,
	} {
		msg, err := (&Client{}).readStream(strings.NewReader(body), nil)
		if err != nil || msg.Content != "ok" {
			t.Fatalf("%+v %v", msg, err)
		}
	}
	_, err := (&Client{}).readStream(strings.NewReader("data: [DONE]\n\n"), nil)
	if !errors.Is(err, ErrIncompleteResponse) {
		t.Fatal(err)
	}
}

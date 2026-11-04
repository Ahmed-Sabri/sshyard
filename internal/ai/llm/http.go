package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// anthropicVersion is the API version an Anthropic request names unless a
// custom header sets another.
const anthropicVersion = "2023-06-01"

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// do sends one request to {base}{path} with the format's credentials and
// returns a 2xx response; any other status is an APIError.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, payload []byte) (*http.Response, error) {
	endpoint, err := c.endpoint(path)
	if err != nil {
		return nil, err
	}
	endpoint.RawQuery = query.Encode()
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, c.scrub(err)
	}
	if payload != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if payload != nil && path != decisionsPath && path != systemOnePath {
		httpReq.Header.Set("Accept", "text/event-stream")
	} else {
		httpReq.Header.Set("Accept", "application/json")
	}
	for k, v := range c.Headers {
		if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "X-Api-Key") || strings.ContainsAny(k+v, "\r\n\x00") {
			continue
		}
		httpReq.Header.Set(k, v)
	}
	if c.format() == FormatAnthropic {
		httpReq.Header.Set("X-Api-Key", c.APIKey)
		if httpReq.Header.Get("Anthropic-Version") == "" {
			httpReq.Header.Set("Anthropic-Version", anthropicVersion)
		}
	} else {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.http(endpoint).Do(httpReq)
	if err != nil {
		return nil, c.scrub(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		return nil, c.httpError(resp)
	}
	return resp, nil
}

func (c *Client) endpoint(path string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(c.BaseURL))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, ErrBaseURL
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawPath, u.RawQuery, u.Fragment = "", "", ""
	return u, nil
}

func (c *Client) http(base *url.URL) *http.Client {
	var transport http.RoundTripper
	if c.HTTP != nil {
		transport = c.HTTP.Transport
	}
	return &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("llm: too many redirects")
		}
		if req.URL.Scheme != base.Scheme || !strings.EqualFold(req.URL.Hostname(), base.Hostname()) || urlPort(req.URL) != urlPort(base) {
			return fmt.Errorf("llm: refusing redirect to %s", req.URL.Scheme+"://"+req.URL.Host)
		}
		return nil
	}}
}

// urlPort is the port a redirect is judged by. An empty port is the
// scheme default, so https://host and https://host:443 are the same service.
func urlPort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "http" {
		return "80"
	}
	return "443"
}

func (c *Client) maxBytes() int {
	if c.MaxBytes > 0 {
		return c.MaxBytes
	}
	return DefaultMaxBytes
}

func (c *Client) httpError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyCap+1))
	truncated := len(b) > errorBodyCap
	if truncated {
		b = b[:errorBodyCap]
	}
	msg := strings.TrimSpace(string(b))
	var eb struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string          `json:"message"`
		Detail  json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(b, &eb) == nil {
		var detail string
		switch {
		case eb.Error.Message != "":
			msg = eb.Error.Message
		case eb.Message != "":
			msg = eb.Message
		case json.Unmarshal(eb.Detail, &detail) == nil && detail != "":
			// TypeSafe says why in detail, a string or a list of problems.
			msg = detail
		}
	}
	return &APIError{Status: resp.StatusCode, Message: msg, Truncated: truncated, key: c.APIKey}
}

// bufioReader is a response body under the size cap. JSON is set when the
// body is one JSON document rather than an event stream.
type bufioReader struct {
	*bufio.Reader
	r    *countReader
	JSON bool
}

func (c *Client) body(body io.Reader) (*bufioReader, error) {
	r := &countReader{r: io.LimitReader(body, int64(c.maxBytes())+1), max: c.maxBytes()}
	br := &bufioReader{Reader: bufio.NewReader(r), r: r}
	// JSON-compatible endpoints may prefix a response with whitespace.
	for {
		b, err := br.Peek(1)
		if err != nil || !bytes.ContainsAny(b, " \t\r\n") {
			break
		}
		br.Discard(1)
	}
	peek, err := br.Peek(1)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, ErrTooBig) {
		return nil, err
	}
	br.JSON = len(peek) == 1 && peek[0] == '{'
	return br, nil
}

// all reads the rest of a JSON body, failing past the size cap.
func (br *bufioReader) all() ([]byte, error) {
	b, err := io.ReadAll(br)
	if err != nil && !errors.Is(err, ErrTooBig) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if errors.Is(err, ErrTooBig) || br.r.n > br.r.max {
		return nil, ErrTooBig
	}
	return b, nil
}

var errDone = errors.New("sse done")

// consumeSSE reads one SSE stream. Data lines of one event are joined
// with newlines. Comment lines (":") are ignored. CRLF is a line ending.
func consumeSSE(br *bufio.Reader, fn func(data string) error) error {
	var data []string
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		s := strings.Join(data, "\n")
		data = nil
		if s == "[DONE]" {
			return errDone
		}
		return fn(s)
	}
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if ferr := flush(); ferr != nil {
					if errors.Is(ferr, errDone) {
						return nil
					}
					return ferr
				}
			case strings.HasPrefix(line, ":"):
			case strings.HasPrefix(line, "data:"):
				rest := strings.TrimPrefix(line, "data:")
				rest = strings.TrimPrefix(rest, " ")
				data = append(data, rest)
			case strings.HasPrefix(line, "event:"), strings.HasPrefix(line, "id:"), strings.HasPrefix(line, "retry:"):
			default:
				// SSE ignores unknown fields, including names without a colon.
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if ferr := flush(); ferr != nil && !errors.Is(ferr, errDone) {
					return ferr
				}
				return nil
			}
			return err
		}
	}
}

// countReader counts bytes and returns ErrTooBig once more than max have
// been read. A body of exactly max bytes is fine.
type countReader struct {
	r   io.Reader
	n   int
	max int
}

func (c *countReader) Read(p []byte) (int, error) {
	if c.n > c.max {
		return 0, ErrTooBig
	}
	n, err := c.r.Read(p)
	c.n += n
	if c.n > c.max {
		return n, ErrTooBig
	}
	return n, err
}

// readErr is a failed read of a response: a cancelled ctx closes the body,
// which may come back as a closed connection rather than ctx's error.
func (c *Client) readErr(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return c.scrub(err)
}

func (c *Client) scrub(err error) error {
	if err == nil || c.APIKey == "" || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrTooBig) || errors.Is(err, ErrBaseURL) || errors.Is(err, ErrProtocol) || errors.Is(err, ErrIncompleteResponse) {
		return err
	}
	var api *APIError
	if errors.As(err, &api) {
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, c.APIKey) {
		return err
	}
	return errors.New(strings.ReplaceAll(msg, c.APIKey, "[redacted]"))
}

// streamError is the error event a stream may carry instead of data.
func (c *Client) streamError(msg string) error {
	return &APIError{Status: 200, Message: msg, key: c.APIKey}
}

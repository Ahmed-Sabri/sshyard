package agent

import (
	"context"
	"encoding/json"
)

// Tools is the remote tool layer. Call's output is the tool result the model sees.
type Tools interface {
	Call(ctx context.Context, name string, args json.RawMessage) (output string, exit int, err error)
	// ReadFile reads a remote file so write_file can build a diff.
	// ok is false when the file does not exist.
	ReadFile(ctx context.Context, path string) (text string, ok bool, err error)
}

// Unavailable is the tool layer before it is connected.
type Unavailable struct{}

// Call reports that remote tools are not connected yet.
func (Unavailable) Call(context.Context, string, json.RawMessage) (string, int, error) {
	return "remote tools are not connected yet", 1, nil
}

// ReadFile reports no file.
func (Unavailable) ReadFile(context.Context, string) (string, bool, error) {
	return "", false, nil
}

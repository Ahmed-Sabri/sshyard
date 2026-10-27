package acp

import (
	"context"
	"errors"
	"io/fs"
	"time"

	"github.com/Licoy/sshyard/internal/ai/agent"
	"github.com/Licoy/sshyard/internal/ai/policy"
	"github.com/Licoy/sshyard/internal/sftpsvc"
	sdk "github.com/coder/acp-go-sdk"
)

func (s *Session) ReadTextFile(ctx context.Context, p sdk.ReadTextFileRequest) (sdk.ReadTextFileResponse, error) {
	if err := s.checkSession(p.SessionId); err != nil {
		return sdk.ReadTextFileResponse{}, err
	}
	if _, err := sliceLines("", p.Line, p.Limit); err != nil {
		return sdk.ReadTextFileResponse{}, err
	}
	remote, err := s.mapPath(p.Path)
	if err != nil {
		return sdk.ReadTextFileResponse{}, err
	}
	remote, err = s.resolveFile(ctx, remote)
	if err != nil {
		return sdk.ReadTextFileResponse{}, err
	}
	act := policy.Action{Where: policy.Remote, Tool: "read_file", Path: remote}
	start := time.Now()
	v, err := s.gate.Authorize(ctx, act, "")
	if err != nil {
		return sdk.ReadTextFileResponse{}, err
	}
	if !v.Run {
		return sdk.ReadTextFileResponse{}, refusalError(v)
	}
	if s.cfg.Files == nil {
		s.finish(act, v, 1, start, 0)
		return sdk.ReadTextFileResponse{}, errNotConnected
	}
	data, _, err := s.readFile(ctx, remote)
	if err != nil {
		s.finish(act, v, 1, start, 0)
		return sdk.ReadTextFileResponse{}, err
	}
	if len(data) > readCap {
		s.finish(act, v, 1, start, len(data))
		return sdk.ReadTextFileResponse{}, errors.New("file exceeds 10MB")
	}
	text, err := sliceLines(string(data), p.Line, p.Limit)
	if err != nil {
		s.finish(act, v, 1, start, len(data))
		return sdk.ReadTextFileResponse{}, err
	}
	s.finish(act, v, 0, start, len(data))
	return sdk.ReadTextFileResponse{Content: text}, nil
}

func (s *Session) WriteTextFile(ctx context.Context, p sdk.WriteTextFileRequest) (sdk.WriteTextFileResponse, error) {
	if err := s.checkSession(p.SessionId); err != nil {
		return sdk.WriteTextFileResponse{}, err
	}
	if len(p.Content) > writeCap {
		return sdk.WriteTextFileResponse{}, errors.New("content exceeds 10MB")
	}
	remote, err := s.mapPath(p.Path)
	if err != nil {
		return sdk.WriteTextFileResponse{}, err
	}
	remote, err = s.resolveFile(ctx, remote)
	if err != nil {
		return sdk.WriteTextFileResponse{}, err
	}
	// A diff reads the current file, so authorize that read independently.
	readAct := policy.Action{Where: policy.Remote, Tool: "read_file", Path: remote}
	readStart := time.Now()
	readVerdict, err := s.gate.Authorize(ctx, readAct, "")
	if err != nil {
		return sdk.WriteTextFileResponse{}, err
	}
	if !readVerdict.Run {
		return sdk.WriteTextFileResponse{}, refusalError(readVerdict)
	}
	if s.cfg.Files == nil {
		s.finish(readAct, readVerdict, 1, readStart, 0)
		return sdk.WriteTextFileResponse{}, errNotConnected
	}
	data, ver, readErr := s.readFile(ctx, remote)
	if readErr == nil && len(data) > readCap {
		readErr = sftpsvc.ErrReadLimit
	}
	readExit := 0
	if readErr != nil {
		readExit = 1
	}
	s.finish(readAct, readVerdict, readExit, readStart, len(data))
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return sdk.WriteTextFileResponse{}, readErr
	}
	old := ""
	if readErr == nil {
		old = string(data)
	}
	diff := agent.Unified(remote, old, p.Content)
	act := policy.Action{Where: policy.Remote, Tool: "write_file", Path: remote}
	start := time.Now()
	v, err := s.gate.Authorize(ctx, act, diff)
	if err != nil {
		return sdk.WriteTextFileResponse{}, err
	}
	if !v.Run {
		return sdk.WriteTextFileResponse{}, refusalError(v)
	}
	if s.cfg.Files == nil {
		s.finish(act, v, 1, start, 0)
		return sdk.WriteTextFileResponse{}, errNotConnected
	}
	// A zero version is a file that was absent when the diff was made.
	// shortcut: the re-check and Write are not atomic, upgrade when a zero version means create-only.
	if ver == (sftpsvc.Version{}) {
		if _, _, err := s.readFile(ctx, remote); err == nil {
			s.finish(act, v, 1, start, 0)
			return sdk.WriteTextFileResponse{}, errors.New("file appeared since the diff was made")
		} else if !errors.Is(err, fs.ErrNotExist) {
			s.finish(act, v, 1, start, 0)
			return sdk.WriteTextFileResponse{}, err
		}
	}
	if _, err := s.cfg.Files.Write(remote, []byte(p.Content), ver); err != nil {
		s.finish(act, v, 1, start, 0)
		if errors.Is(err, sftpsvc.ErrChanged) {
			return sdk.WriteTextFileResponse{}, errors.New("file changed since it was read")
		}
		return sdk.WriteTextFileResponse{}, err
	}
	s.finish(act, v, 0, start, len(p.Content))
	return sdk.WriteTextFileResponse{}, nil
}

func (s *Session) RequestPermission(ctx context.Context, p sdk.RequestPermissionRequest) (sdk.RequestPermissionResponse, error) {
	if err := s.checkSession(p.SessionId); err != nil {
		return sdk.RequestPermissionResponse{}, err
	}
	pctx, cancel := context.WithCancel(ctx)
	s.trackPerm(cancel)
	defer cancel()

	act, diff := actionFrom(p.ToolCall)
	v, err := s.gate.Authorize(pctx, act, diff)
	if pctx.Err() != nil {
		return cancelledPerm(), nil
	}
	if err != nil {
		return sdk.RequestPermissionResponse{}, err
	}
	id, cancelled := PermissionChoice(v.Run, p.Options)
	if cancelled {
		return cancelledPerm(), nil
	}
	return sdk.RequestPermissionResponse{Outcome: sdk.RequestPermissionOutcome{
		Selected: &sdk.RequestPermissionOutcomeSelected{OptionId: sdk.PermissionOptionId(id)},
	}}, nil
}

func cancelledPerm() sdk.RequestPermissionResponse {
	return sdk.RequestPermissionResponse{Outcome: sdk.RequestPermissionOutcome{
		Cancelled: &sdk.RequestPermissionOutcomeCancelled{},
	}}
}

// SessionUpdate only queues. A slow UI must not block the SDK read loop.
// The queue drops the oldest update when it is full.
func (s *Session) SessionUpdate(ctx context.Context, n sdk.SessionNotification) error {
	select {
	case s.updates <- n:
		return nil
	default:
	}
	select {
	case <-s.updates:
	default:
	}
	select {
	case s.updates <- n:
	default:
	}
	return nil
}

func (s *Session) finish(act policy.Action, v policy.Verdict, exit int, start time.Time, n int) {
	s.gate.Done(act, v, exit, time.Since(start).Milliseconds(), int64(n))
}

func refusalError(v policy.Verdict) error {
	if v.Refusal != "" {
		return errors.New(v.Refusal)
	}
	return errors.New("refused")
}

func (s *Session) resolveFile(ctx context.Context, p string) (string, error) {
	if s.cfg.Files == nil {
		return p, nil
	}
	return s.cfg.Files.ResolvePath(ctx, p)
}
func (s *Session) readFile(ctx context.Context, p string) ([]byte, sftpsvc.Version, error) {
	return s.cfg.Files.ReadContext(ctx, p, readCap)
}

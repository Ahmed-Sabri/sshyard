package acp

import (
	"errors"
	"path"
	"path/filepath"
	"strings"
)

const maxPathLen = 4096

var (
	errInvalidPath = errors.New("invalid path")
	errNoRemote    = errors.New("remote working directory is not set")
)

// MapPath turns an agent path into a remote POSIX path.
// A path inside sandbox is rewritten under the remote working directory
// (follow, when it returns a POSIX absolute path, otherwise home).
// Any other POSIX absolute path is cleaned and returned as a remote path.
// Relative paths, drive paths, NUL bytes and over-long paths are rejected.
// The sandbox check uses filepath so Windows drive letters and case match.
func MapPath(sandbox, home, agentPath string, follow func() string) (string, error) {
	if agentPath == "" || strings.Contains(agentPath, "\x00") || len(agentPath) > maxPathLen {
		return "", errInvalidPath
	}
	if sandbox != "" && filepath.IsAbs(agentPath) {
		absSand, err1 := filepath.Abs(sandbox)
		absPath, err2 := filepath.Abs(agentPath)
		if err1 == nil && err2 == nil {
			rel, err := filepath.Rel(absSand, absPath)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				remote, err := remoteCwd(home, follow)
				if err != nil {
					return "", err
				}
				rel = filepath.ToSlash(rel)
				if rel == "." {
					return remote, nil
				}
				return path.Join(remote, rel), nil
			}
		}
	}
	if clean, ok := cleanPOSIX(agentPath); ok {
		return clean, nil
	}
	return "", errInvalidPath
}

func remoteCwd(home string, follow func() string) (string, error) {
	if follow != nil {
		if clean, ok := cleanPOSIX(follow()); ok {
			return clean, nil
		}
	}
	if clean, ok := cleanPOSIX(home); ok {
		return clean, nil
	}
	return "", errNoRemote
}

func cleanPOSIX(p string) (string, bool) {
	if p == "" || strings.Contains(p, "\x00") || strings.Contains(p, "\\") || !strings.HasPrefix(p, "/") {
		return "", false
	}
	c := path.Clean(p)
	if c == "." || !strings.HasPrefix(c, "/") {
		return "", false
	}
	return c, true
}

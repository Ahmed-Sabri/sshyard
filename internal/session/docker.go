package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

// ErrNoDocker is a server without the docker command; ErrDockerDenied one
// whose daemon the user may not reach (not in the docker group, and no
// sudo without a password).
var (
	ErrNoDocker     = errors.New("session: docker is not installed")
	ErrDockerDenied = errors.New("session: no permission to use docker")
)

// dockerPrelude finds docker, and runs it as the user or, when the
// daemon refuses the user, through sudo where that asks no password: $D
// is then the command. It exits 127 without docker and 77 without the
// right to use it.
const dockerPrelude = `command -v docker >/dev/null 2>&1 || exit 127
D=docker
if ! docker version --format "{{.Server.Version}}" >/dev/null 2>&1; then
  if sudo -n docker version --format "{{.Server.Version}}" >/dev/null 2>&1; then D="sudo -n docker"; else
    docker version --format "{{.Server.Version}}" 2>&1 | grep -qi "permission denied" && exit 77
  fi
fi
`

// dockerListScript prints the containers (after @C) and the images
// (after @I), one JSON object a line, the daemon's version (@V) and the
// command docker runs as (@D).
const dockerListScript = dockerPrelude + `echo "@V $($D version --format "{{.Server.Version}}" 2>/dev/null)"
echo "@D $D"
echo @C
$D ps -a --no-trunc --format "{{json .}}" || exit 1
echo @I
$D images --format "{{json .}}" || exit 1`

// Container is a Docker container as docker ps lists it.
type Container struct {
	ID      string `json:"ID"`
	Name    string `json:"Names"`
	Image   string `json:"Image"`
	Command string `json:"Command"`
	State   string `json:"State"` // running, exited, paused, created, restarting, dead
	Status  string `json:"Status"`
	Ports   string `json:"Ports"`
	Created string `json:"RunningFor"`
}

// Running reports whether the container runs (paused or not).
func (c Container) Running() bool {
	return c.State == "running" || c.State == "paused" || c.State == "restarting"
}

// Image is a Docker image as docker images lists it.
type Image struct {
	ID         string `json:"ID"`
	Repository string `json:"Repository"`
	Tag        string `json:"Tag"`
	Size       string `json:"Size"`
	Created    string `json:"CreatedSince"`
}

// Ref is the image as docker names it: repository:tag, or its ID when it
// has neither.
func (i Image) Ref() string {
	if i.Repository == "" || i.Repository == "<none>" {
		return i.ID
	}
	if i.Tag == "" || i.Tag == "<none>" {
		return i.Repository
	}
	return i.Repository + ":" + i.Tag
}

// DockerInfo is what DockerList read. Sudo is set when the user reaches
// the daemon only through sudo.
type DockerInfo struct {
	Version    string
	Sudo       bool
	Containers []Container
	Images     []Image
}

// ParseDockerList reads the output of the list script.
func ParseDockerList(out []byte) (DockerInfo, error) {
	var d DockerInfo
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	part := ""
	seen := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "@V"):
			d.Version = strings.TrimSpace(strings.TrimPrefix(line, "@V"))
		case strings.HasPrefix(line, "@D"):
			d.Sudo = strings.Contains(line, "sudo")
		case line == "@C" || line == "@I":
			part, seen = line, true
		case line == "" || !strings.HasPrefix(line, "{"):
		case part == "@C":
			var c Container
			if json.Unmarshal([]byte(line), &c) == nil && c.ID != "" {
				d.Containers = append(d.Containers, c)
			}
		case part == "@I":
			var i Image
			if json.Unmarshal([]byte(line), &i) == nil && i.ID != "" {
				d.Images = append(d.Images, i)
			}
		}
	}
	if !seen {
		return DockerInfo{}, errors.New("session: unreadable docker output")
	}
	return d, nil
}

// runDocker runs script under sh, args its $1 …, its stdout returned and
// its stderr in the error of a failure. The args are checked references
// (DockerRefOK), which hold no quote.
func runDocker(ctx context.Context, client *ssh.Client, script string, args ...string) ([]byte, error) {
	var out, errb bytes.Buffer
	cmd := "sh -c " + shellQuote(script) + " _"
	for _, a := range args {
		cmd += " " + shellQuote(a)
	}
	err := Exec(ctx, client, cmd, &out, &errb)
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		switch exit.ExitStatus() {
		case 127:
			return nil, ErrNoDocker
		case 77:
			return nil, ErrDockerDenied
		}
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return nil, errors.New(lastLine(msg))
		}
	}
	return out.Bytes(), err
}

func lastLine(s string) string {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return s
}

// DockerList reads the containers and the images of the server.
func DockerList(ctx context.Context, client *ssh.Client) (DockerInfo, error) {
	out, err := runDocker(ctx, client, dockerListScript)
	if err != nil {
		return DockerInfo{}, err
	}
	return ParseDockerList(out)
}

// DockerAction is what can be done to a container or an image.
type DockerAction string

const (
	DockerStart       DockerAction = "start"
	DockerStop        DockerAction = "stop"
	DockerRestart     DockerAction = "restart"
	DockerPause       DockerAction = "pause"
	DockerUnpause     DockerAction = "unpause"
	DockerRemove      DockerAction = "rm -f"
	DockerRemoveImage DockerAction = "rmi"
)

// dockerRef is a container or image reference docker may be given: IDs,
// names and repository:tag@digest refs, never shell syntax.
var dockerRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/@+-]*$`)

// DockerRefOK reports whether ref may go into a docker command.
func DockerRefOK(ref string) bool { return len(ref) <= 512 && dockerRef.MatchString(ref) }

func dockerActionOK(a DockerAction) bool {
	switch a {
	case DockerStart, DockerStop, DockerRestart, DockerPause, DockerUnpause, DockerRemove, DockerRemoveImage:
		return true
	}
	return false
}

// DockerCommand is the script doing action to ref, "" for an action or a
// reference that may not be.
func DockerCommand(action DockerAction, ref string) string {
	if !dockerActionOK(action) || !DockerRefOK(ref) {
		return ""
	}
	return dockerPrelude + `$D ` + string(action) + ` "$1"`
}

// DockerDo does action to the container or image ref.
func DockerDo(ctx context.Context, client *ssh.Client, action DockerAction, ref string) error {
	script := DockerCommand(action, ref)
	if script == "" {
		return errors.New("session: bad docker reference")
	}
	_, err := runDocker(ctx, client, script, ref)
	return err
}

// DockerLogs is the last lines of a container's output, stdout and
// stderr together.
func DockerLogs(ctx context.Context, client *ssh.Client, ref string, lines int) (string, error) {
	if !DockerRefOK(ref) {
		return "", errors.New("session: bad docker reference")
	}
	if lines <= 0 || lines > 5000 {
		lines = 500
	}
	out, err := runDocker(ctx, client, dockerPrelude+`$D logs --tail `+strconv.Itoa(lines)+` "$1" 2>&1`, ref)
	return string(out), err
}

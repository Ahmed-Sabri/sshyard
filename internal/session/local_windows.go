//go:build windows

package session

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// conPTY is a shell on a Windows pseudo console (ConPTY, Windows 10 1809
// and later).
type conPTY struct {
	hpc  windows.Handle
	proc windows.Handle
	in   *os.File // what is typed, to the console
	out  *os.File // what the console draws
	done chan struct{}
	once sync.Once
}

// localShell is PowerShell 7 when installed, else Windows PowerShell,
// else the command interpreter.
func localShell() string {
	for _, sh := range []string{"pwsh.exe", "powershell.exe"} {
		if p, err := exec.LookPath(sh); err == nil {
			return p
		}
	}
	if sh := os.Getenv("COMSPEC"); sh != "" {
		return sh
	}
	return "cmd.exe"
}

// localEnv is the environment of the local shell: this app's, with the
// terminal named. PowerShell 7's update notice is off unless the user
// chose otherwise: under some languages it miscounts its padding and
// ends the shell as it starts (PowerShell 7.6.6).
func localEnv(term string) []string {
	env := append([]string{"POWERSHELL_UPDATECHECK=Off"}, os.Environ()...)
	return mergeEnv(append(env, termEnv(term)...), true)
}

// envBlock is env as CreateProcess takes it: each entry ended by a NUL,
// the block by another. Entries with a NUL of their own are left out.
func envBlock(env []string) *uint16 {
	var b []uint16
	for _, kv := range env {
		if u, err := windows.UTF16FromString(kv); err == nil {
			b = append(b, u...)
		}
	}
	if len(b) == 0 {
		b = append(b, 0)
	}
	b = append(b, 0)
	return &b[0]
}

func startLocal(cols, rows int, term string) (localPTY, error) {
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, err
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		windows.CloseHandle(inR)
		windows.CloseHandle(inW)
		return nil, err
	}
	var hpc windows.Handle
	err := windows.CreatePseudoConsole(windows.Coord{X: int16(cols), Y: int16(rows)}, inR, outW, 0, &hpc)
	// The console has its own handles to these ends now.
	windows.CloseHandle(inR)
	windows.CloseHandle(outW)
	if err != nil {
		windows.CloseHandle(inW)
		windows.CloseHandle(outR)
		return nil, err
	}
	p := &conPTY{hpc: hpc, in: os.NewFile(uintptr(inW), "conpty-in"), out: os.NewFile(uintptr(outR), "conpty-out"), done: make(chan struct{})}
	if err := p.spawn(localEnv(term)); err != nil {
		p.closeConsole()
		return nil, err
	}
	go func() {
		windows.WaitForSingleObject(p.proc, windows.INFINITE)
		close(p.done)
	}()
	return p, nil
}

// spawn starts the shell attached to the console, in the user's home.
func (p *conPTY) spawn(env []string) error {
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return err
	}
	defer attrs.Delete()
	// The attribute's value is the console handle itself, not a pointer
	// to it.
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&p.hpc)), unsafe.Sizeof(p.hpc)); err != nil {
		return err
	}
	si := &windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(*si))
	// No standard handles of ours: the console's are the shell's.
	si.Flags = windows.STARTF_USESTDHANDLES
	cmd, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{localShell()}))
	if err != nil {
		return err
	}
	var dir *uint16
	if home, err := os.UserHomeDir(); err == nil {
		dir, _ = windows.UTF16PtrFromString(home)
	}
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(nil, cmd, nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT, envBlock(env), dir, &si.StartupInfo, &pi); err != nil {
		return err
	}
	windows.CloseHandle(pi.Thread)
	p.proc = pi.Process
	return nil
}

func (p *conPTY) Read(b []byte) (int, error)  { return p.out.Read(b) }
func (p *conPTY) Write(b []byte) (int, error) { return p.in.Write(b) }

func (p *conPTY) Resize(cols, rows int) error {
	return windows.ResizePseudoConsole(p.hpc, windows.Coord{X: int16(cols), Y: int16(rows)})
}

func (p *conPTY) Wait() error {
	<-p.done
	var code uint32
	if err := windows.GetExitCodeProcess(p.proc, &code); err != nil {
		return err
	}
	if code != 0 {
		return errors.New("session: the shell exited with an error")
	}
	return nil
}

// Close closes the console, which ends the shell attached to it.
func (p *conPTY) Close() error {
	p.once.Do(func() {
		p.closeConsole()
		if p.proc != 0 {
			go func() {
				<-p.done
				windows.CloseHandle(p.proc)
			}()
		}
	})
	return nil
}

func (p *conPTY) closeConsole() {
	// Closing the console waits for its output to be read: the reader
	// goroutine drains it until the pipe ends.
	windows.ClosePseudoConsole(p.hpc)
	p.in.Close()
	if p.proc == 0 {
		p.out.Close()
		return
	}
	go func() {
		<-p.done
		p.out.Close()
	}()
}

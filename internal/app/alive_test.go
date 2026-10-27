package app

import (
	"os"
	"os/exec"
	"testing"
)

// processAlive sees this process run and a finished one not.
func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("this process is not alive")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if !processAlive(pid) {
		// it may have finished already; only a running one must be seen
		t.Log("child finished before it was looked at")
	}
	cmd.Wait()
	if processAlive(pid) {
		t.Fatalf("finished process %d is alive", pid)
	}
}

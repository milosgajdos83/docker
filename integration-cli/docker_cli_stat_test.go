package main

import (
	"os/exec"
	"testing"
	"time"
)

func TestCliStatsNoStream(t *testing.T) {
	defer deleteAllContainers()
	var (
		name   = "statscontainer"
		runCmd = exec.Command(dockerBinary, "run", "-d", "--name", name, "busybox", "top")
	)
	out, _, err := runCommandWithOutput(runCmd)
	if err != nil {
		t.Fatalf("Error on container creation: %v, output: %v", err, out)
	}

	chErr := make(chan error)
	go func() {
		chErr <- exec.Command(dockerBinary, "stats", "--no-stream", name).Run()
	}()

	select {
	case err := <-chErr:
		if err != nil {
			t.Fatalf("Error running stats: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("stats did not return immediately when not streaming")
	}

	logDone("stats - --no-stream returns immediately")
}

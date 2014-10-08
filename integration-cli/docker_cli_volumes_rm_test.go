package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestVolumesRm(t *testing.T) {
	defer deleteAllContainers()
	deleteAllVolumes()

	cmd := exec.Command(dockerBinary, "volumes", "create", "--name", "foo")
	if _, err := runCommand(cmd); err != nil {
		t.Fatal(err)
	}

	cmd = exec.Command(dockerBinary, "run", "--name", "test", "-v", "foo:/foo", "busybox")
	if _, err := runCommand(cmd); err != nil {
		t.Fatal(err)
	}

	// This should fail since a container is using it
	cmd = exec.Command(dockerBinary, "volumes", "rm", "foo")
	out, _, err := runCommandWithOutput(cmd)
	if err == nil || !strings.Contains(out, "is being used") {
		t.Fatal(err, out)
	}

	cmd = exec.Command(dockerBinary, "rm", "test")
	if _, err := runCommand(cmd); err != nil {
		t.Fatal(err)
	}

	cmd = exec.Command(dockerBinary, "volumes", "rm", "foo")
	out, _, err = runCommandWithOutput(cmd)
	if err != nil {
		t.Fatal(err, out)
	}

	lines := strings.Split(strings.Trim(out, "\n "), "\n")
	if len(lines)-1 != 0 {
		t.Fatalf("Volumes not removed properly\n%q", out)
	}

	logDone("volume rm - volumes are removed")
}

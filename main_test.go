package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestStripMallocDebugEnv(t *testing.T) {
	t.Setenv("MallocStackLogging", "1")
	t.Setenv("MallocStackLoggingNoCompact", "1")
	t.Setenv("MallocNanoZone", "0")

	stripMallocDebugEnv()

	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "MallocStackLogging") {
			t.Errorf("%s survived the strip", kv)
		}
	}
	// anything outside the MallocStackLogging family is left alone
	if os.Getenv("MallocNanoZone") != "0" {
		t.Error("MallocNanoZone was dropped; only the stack-logging family should be")
	}
}

// A child spawned after the strip starts without libmalloc's warning on
// stderr — the noise that used to land in terminal panes and parsed output.
func TestChildrenStartQuiet(t *testing.T) {
	t.Setenv("MallocStackLogging", "1")

	noisy, _ := exec.Command("/bin/echo", "hi").CombinedOutput()
	stripMallocDebugEnv()
	quiet, err := exec.Command("/bin/echo", "hi").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(noisy), "MallocStackLogging") {
		t.Skip("this platform's malloc doesn't announce itself")
	}
	if strings.Contains(string(quiet), "MallocStackLogging") {
		t.Errorf("child still noisy: %q", quiet)
	}
}

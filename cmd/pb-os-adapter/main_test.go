package main

import (
	"syscall"
	"testing"
	"time"
)

func TestProcessRegistryWaitAndSignalLifecycle(t *testing.T) {
	registry := newProcessRegistry()

	record, err := registry.spawn([]string{"sleep", "5"}, "", nil)
	if err != nil {
		t.Fatalf("spawn process: %v", err)
	}
	if record.processID == "" {
		t.Fatal("expected process_id to be set")
	}
	if record.pid <= 0 {
		t.Fatalf("expected pid > 0, got %d", record.pid)
	}

	select {
	case <-record.doneCh:
		t.Fatal("expected process to still be running")
	case <-time.After(100 * time.Millisecond):
	}

	waitTimedOut := record.waitResult(true)
	if waitTimedOut["timed_out"] != true {
		t.Fatalf("expected timed_out=true, got %#v", waitTimedOut)
	}
	if waitTimedOut["running"] != true {
		t.Fatalf("expected running=true before signal, got %#v", waitTimedOut)
	}

	sent, err := record.sendSignal(syscall.SIGTERM)
	if err != nil {
		t.Fatalf("send signal: %v", err)
	}
	if !sent {
		t.Fatal("expected SIGTERM to be sent")
	}

	select {
	case <-record.doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit after SIGTERM")
	}

	result := record.waitResult(false)
	if result["exited"] != true {
		t.Fatalf("expected exited=true, got %#v", result)
	}
	if result["running"] != false {
		t.Fatalf("expected running=false, got %#v", result)
	}

	sentAgain, err := record.sendSignal(syscall.SIGTERM)
	if err != nil {
		t.Fatalf("send signal after exit: %v", err)
	}
	if sentAgain {
		t.Fatal("expected no signal to be sent after exit")
	}
}

func TestProcessRegistryUnknownProcessID(t *testing.T) {
	registry := newProcessRegistry()
	if _, ok := registry.get("proc-missing"); ok {
		t.Fatal("expected missing process_id lookup to fail")
	}
}

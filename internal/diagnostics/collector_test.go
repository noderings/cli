package diagnostics

import "testing"

func TestSummarizeHealth(t *testing.T) {
	checks := []HealthCheck{
		{Component: "a", Status: "pass"},
		{Component: "b", Status: "warn"},
		{Component: "c", Status: "fail"},
	}
	allPass, failed := SummarizeHealth(checks)
	if allPass {
		t.Fatal("expected not all pass")
	}
	if failed != 1 {
		t.Fatalf("failed=%d want 1", failed)
	}

	allPass, failed = SummarizeHealth([]HealthCheck{{Status: "pass"}, {Status: "warn"}})
	if !allPass || failed != 0 {
		t.Fatalf("warn should not count as fail: allPass=%v failed=%d", allPass, failed)
	}
}

func TestBoolHelpers(t *testing.T) {
	if boolStatus(true) != "pass" || boolStatus(false) != "fail" {
		t.Fatal("boolStatus mismatch")
	}
	if boolMessage(true, "ok", "bad") != "ok" {
		t.Fatal("boolMessage pass mismatch")
	}
	if boolMessage(false, "ok", "bad") != "bad" {
		t.Fatal("boolMessage fail mismatch")
	}
}

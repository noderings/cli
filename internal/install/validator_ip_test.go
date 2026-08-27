package install

import (
	"net"
	"strings"
	"testing"
)

func TestCheckAgentIPResultAssigned(t *testing.T) {
	t.Parallel()
	got := checkAgentIPResult("192.168.1.153", []ifaceAddr{
		{name: "lo", ip: net.ParseIP("127.0.0.1"), up: true, loopback: true},
		{name: "eth0", ip: net.ParseIP("192.168.1.153"), up: true, loopback: false},
	})
	if got.Status != "pass" {
		t.Fatalf("status=%s message=%s", got.Status, got.Message)
	}
	if !strings.Contains(got.Message, "eth0") {
		t.Fatalf("message=%s", got.Message)
	}
}

func TestCheckAgentIPResultPlaceholderNotLocal(t *testing.T) {
	t.Parallel()
	got := checkAgentIPResult("1.1.1.1", []ifaceAddr{
		{name: "eth0", ip: net.ParseIP("192.168.1.153"), up: true, loopback: false},
	})
	if got.Status != "fail" {
		t.Fatalf("status=%s", got.Status)
	}
	if !strings.Contains(got.Message, "1.1.1.1") || !strings.Contains(got.Message, "192.168.1.153") {
		t.Fatalf("message=%s", got.Message)
	}
	if !strings.Contains(got.Message, "--node-ip") {
		t.Fatalf("expected k3s --node-ip hint, got %s", got.Message)
	}
}

func TestCheckAgentIPResultLoopbackRejected(t *testing.T) {
	t.Parallel()
	got := checkAgentIPResult("127.0.0.1", []ifaceAddr{
		{name: "lo", ip: net.ParseIP("127.0.0.1"), up: true, loopback: true},
		{name: "eth0", ip: net.ParseIP("192.168.1.153"), up: true, loopback: false},
	})
	if got.Status != "fail" || !strings.Contains(got.Message, "loopback") {
		t.Fatalf("status=%s message=%s", got.Status, got.Message)
	}
}

func TestCheckAgentIPResultDownInterface(t *testing.T) {
	t.Parallel()
	got := checkAgentIPResult("10.0.0.5", []ifaceAddr{
		{name: "eth1", ip: net.ParseIP("10.0.0.5"), up: false, loopback: false},
	})
	if got.Status != "fail" || !strings.Contains(got.Message, "down") {
		t.Fatalf("status=%s message=%s", got.Status, got.Message)
	}
}

func TestCheckAgentIPResultInvalid(t *testing.T) {
	t.Parallel()
	got := checkAgentIPResult("not-an-ip", nil)
	if got.Status != "fail" || !strings.Contains(got.Message, "not a valid IP") {
		t.Fatalf("status=%s message=%s", got.Status, got.Message)
	}
}

func TestCheckAgentIPResultEmpty(t *testing.T) {
	t.Parallel()
	got := checkAgentIPResult("  ", nil)
	if got.Status != "fail" || !strings.Contains(got.Message, "--agent-ip") {
		t.Fatalf("status=%s message=%s", got.Status, got.Message)
	}
}

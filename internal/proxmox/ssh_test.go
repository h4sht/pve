package proxmox

import (
	"testing"
)

// TestAllAuthFailures_AllAuth: every error is an auth failure → true.
func TestAllAuthFailures_AllAuth(t *testing.T) {
	errs := []string{
		"192.168.1.1: ssh: handshake failed: ssh: unable to authenticate, attempted methods [none publickey], no supported methods remain",
		"192.168.1.2: ssh: handshake failed: ssh: unable to authenticate, attempted methods [none publickey], no supported methods remain",
	}
	if !allAuthFailures(errs) {
		t.Errorf("expected allAuthFailures=true when all errors are auth failures")
	}
}

// TestAllAuthFailures_PermissionDenied: "Permission denied" variant → true.
func TestAllAuthFailures_PermissionDenied(t *testing.T) {
	errs := []string{"host1: Permission denied (publickey,password)."}
	if !allAuthFailures(errs) {
		t.Errorf("expected allAuthFailures=true for 'Permission denied' error")
	}
}

// TestAllAuthFailures_Mixed: one auth, one network → false.
func TestAllAuthFailures_Mixed(t *testing.T) {
	errs := []string{
		"192.168.1.1: ssh: handshake failed: ssh: unable to authenticate, attempted methods [none publickey]",
		"192.168.1.2: dial tcp 192.168.1.2:22: connect: connection refused",
	}
	if allAuthFailures(errs) {
		t.Errorf("expected allAuthFailures=false when one error is NOT auth-related")
	}
}

// TestAllAuthFailures_AllNetwork: all network errors → false.
func TestAllAuthFailures_AllNetwork(t *testing.T) {
	errs := []string{
		"host1: dial tcp: connection refused",
		"host2: dial tcp: i/o timeout",
	}
	if allAuthFailures(errs) {
		t.Errorf("expected allAuthFailures=false for purely network errors")
	}
}

// TestAllAuthFailures_Empty: no errors → false (no failures to classify).
func TestAllAuthFailures_Empty(t *testing.T) {
	if allAuthFailures(nil) {
		t.Errorf("expected allAuthFailures=false for empty slice")
	}
}

// TestAllAuthFailures_SingleAuth: one auth failure → true.
func TestAllAuthFailures_SingleAuth(t *testing.T) {
	errs := []string{"host: unable to authenticate"}
	if !allAuthFailures(errs) {
		t.Errorf("expected allAuthFailures=true for single auth failure")
	}
}

// TestFirstNode_Normal: returns first element.
func TestFirstNode_Normal(t *testing.T) {
	nodes := []string{"192.168.1.1", "192.168.1.2"}
	if got := firstNode(nodes); got != "192.168.1.1" {
		t.Errorf("expected first node '192.168.1.1', got %q", got)
	}
}

// TestFirstNode_Empty: returns placeholder.
func TestFirstNode_Empty(t *testing.T) {
	if got := firstNode(nil); got != "<ip-pve>" {
		t.Errorf("expected '<ip-pve>' for empty nodes, got %q", got)
	}
}

// TestFirstNode_Single: returns the only element.
func TestFirstNode_Single(t *testing.T) {
	if got := firstNode([]string{"10.0.0.1"}); got != "10.0.0.1" {
		t.Errorf("expected '10.0.0.1', got %q", got)
	}
}

// TestSSHUserOrDefault_Explicit: configured SSH user wins.
func TestSSHUserOrDefault_Explicit(t *testing.T) {
	c := &Client{SSHUser: "deploy", Token: "PVEAPIToken=root@pam!tok=uuid"}
	if got := c.SSHUserOrDefault(); got != "deploy" {
		t.Errorf("expected explicit SSH user 'deploy', got %q", got)
	}
}

// TestSSHUserOrDefault_FromToken: derive user from PVEAPIToken.
func TestSSHUserOrDefault_FromToken(t *testing.T) {
	c := &Client{Token: "PVEAPIToken=admin@pam!tok=uuid"}
	if got := c.SSHUserOrDefault(); got != "admin" {
		t.Errorf("expected SSH user 'admin' derived from token, got %q", got)
	}
}

// TestSSHUserOrDefault_Fallback: fallback to root when token has no user.
func TestSSHUserOrDefault_Fallback(t *testing.T) {
	c := &Client{Token: "bad-token"}
	if got := c.SSHUserOrDefault(); got != "root" {
		t.Errorf("expected fallback SSH user 'root', got %q", got)
	}
}

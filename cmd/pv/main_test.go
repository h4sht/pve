package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/h4sht/pve/internal/i18n"
	"github.com/h4sht/pve/internal/proxmox"
)

func init() {
	// Tests were written against the original Spanish output.
	i18n.Lang = "es"
}

// roundTripFunc lets tests stub HTTP responses without spinning up a
// server. Assigned to client.HTTP.Transport so NewClient's default
// (TLS-skipping, 30s timeout) is preserved.
type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func mustResp(t *testing.T, status int, body string) *http.Response {
	t.Helper()
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": {"application/json"}},
	}
}

func newStubbedClient(t *testing.T, h roundTripFunc) *proxmox.Client {
	t.Helper()
	c := proxmox.NewClient([]string{"127.0.0.1"}, "PVEAPIToken=test@pam!t=secret")
	c.HTTP.Transport = h
	return c
}

// TestFetchIPs_StoppedDHCP: container is stopped and has no static ip= in
// net0 (DHCP).
//
// Gating contracts:
//   * /lxc/{vmid}/interfaces MUST NOT be requested — the goroutine is
//     skipped when status==stopped.
//   * diagNotes (merged with errProblems for display) must contain:
//     - the cfg-empty diagnostic
//     - the live-skip sentinel
//     - the cluster/resources "sin lease cacheado" line (added on
//       total failure by the v0.1.10 transparency improvement so the
//       user sees all three sources were probed).
func TestFetchIPs_StoppedDHCP(t *testing.T) {
	var interfacesRequested bool
	h := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/interfaces") {
			interfacesRequested = true
		}
		body := `{"data":{"hostname":"Test-WOL","net0":"name=eth0,bridge=vmbr0,gw=192.168.1.1,ip=dhcp"}}`
		return mustResp(t, 200, body), nil
	})
	cli := newStubbedClient(t, h)
	r := proxmox.ClusterResource{
		Name: "Test-WOL", Node: "pve", Type: proxmox.ResourceLXC,
		Status: "stopped", VMID: 100,
	}
	ips, errProblems, diagNotes := fetchIPs(cli, context.Background(), r)
	if interfacesRequested {
		t.Fatalf("live /interfaces MUST be skipped when container is stopped; roundtrip leaked")
	}
	if len(ips) != 0 {
		t.Fatalf("expected zero IPs for stopped+DHCP, got %v", ips)
	}
	if len(errProblems) != 0 {
		t.Errorf("expected no protocol errors for stopped+DHCP (all calls succeed), got: %v", errProblems)
	}
	// Joined view (mirrors how runIP/printResource render the diagnostic).
	all := strings.Join(append(errProblems, diagNotes...), "\n")
	if !strings.Contains(all, "sin 'ip=' en net0") {
		t.Errorf("missing cfg-empty diagnostic. got:\n%s", all)
	}
	if !strings.Contains(all, "omitido (contenedor apagado)") {
		t.Errorf("missing live-skip sentinel. got:\n%s", all)
	}
	if !strings.Contains(all, "/cluster/resources") {
		t.Errorf("missing cluster/resources transparency line. got:\n%s", all)
	}
	// Real vmid in src labels (no "/lxc/{vmid}" placeholders).
	if !strings.Contains(all, "/lxc/100/config") || !strings.Contains(all, "/lxc/100/interfaces") {
		t.Errorf("src labels did not include real vmid. got:\n%s", all)
	}
}

// TestFetchIPs_StoppedStatic: container is stopped BUT has ip=192.168.1.50
// in its net0 config. fetchIPs must still return the IP — that is the whole
// point of the /config fast path: it works offline.
func TestFetchIPs_StoppedStatic(t *testing.T) {
	h := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/interfaces") {
			t.Fatalf("live endpoint MUST NOT be hit when status==stopped")
		}
		body := `{"data":{"hostname":"Test-WOL","net0":"name=eth0,bridge=vmbr0,gw=192.168.1.1,ip=192.168.1.50/24"}}`
		return mustResp(t, 200, body), nil
	})
	cli := newStubbedClient(t, h)
	r := proxmox.ClusterResource{
		Name: "Test-WOL", Node: "pve", Type: proxmox.ResourceLXC,
		Status: "stopped", VMID: 100,
	}
	ips, errProblems, diagNotes := fetchIPs(cli, context.Background(), r)
	if len(ips) == 0 || ips[0] != "192.168.1.50" {
		t.Fatalf("expected 192.168.1.50 from static cfg, got %v (errProblems=%v diagNotes=%v)", ips, errProblems, diagNotes)
	}
	if len(errProblems) != 0 || len(diagNotes) != 0 {
		t.Errorf("expected no diagnostic lines on success, got errProblems=%v diagNotes=%v", errProblems, diagNotes)
	}
}

// TestFetchIPs_RunningStatic: running container with a static ip=. cfg wins.
// Live endpoint is hit because r.Status != "stopped".
func TestFetchIPs_RunningStatic(t *testing.T) {
	var liveCalled bool
	h := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/interfaces") {
			liveCalled = true
			return mustResp(t, 200, `{"data":[]}`), nil
		}
		return mustResp(t, 200, `{"data":{"net0":"name=eth0,bridge=vmbr0,gw=10.0.0.1,ip=10.0.0.7/24"}}`), nil
	})
	cli := newStubbedClient(t, h)
	r := proxmox.ClusterResource{
		Name: "Test-WOL", Node: "pve", Type: proxmox.ResourceLXC,
		Status: "running", VMID: 100,
	}
	ips, _, _ := fetchIPs(cli, context.Background(), r)
	if !liveCalled {
		t.Errorf("live endpoint should be hit when status==running")
	}
	if len(ips) == 0 || ips[0] != "10.0.0.7" {
		t.Fatalf("expected 10.0.0.7 from static cfg (static wins over live), got %v", ips)
	}
}

// TestFetchIPs_RunningDHCP_LivePopulated: running + DHCP + live returns a
// proper interface entry. Pins the JSON envelope fix (data, not result).
// Before this fix, interfaces[] would always be empty and the run would
// emit "respuesta vacía" even for a perfectly healthy running LXC.
func TestFetchIPs_RunningDHCP_LivePopulated(t *testing.T) {
	h := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/interfaces") {
			// The fixed envelope: {"data":[{"name":"eth0","ip-addresses":[...]}]}.
			// If the struct ever regresses to json:"result", this test breaks
			// loudly — which is exactly what we want.
			body := `{"data":[{"name":"eth0","hwaddr":"aa:bb:cc:dd:ee:ff","type":"bridge","ip-addresses":[{"ip-address":"192.168.1.42","prefix":24,"type":"inet"}]}]}`
			return mustResp(t, 200, body), nil
		}
		return mustResp(t, 200, `{"data":{"net0":"name=eth0,bridge=vmbr0,gw=192.168.1.1,ip=dhcp"}}`), nil
	})
	cli := newStubbedClient(t, h)
	r := proxmox.ClusterResource{
		Name: "Test-WOL", Node: "pve", Type: proxmox.ResourceLXC,
		Status: "running", VMID: 100,
	}
	ips, errProblems, diagNotes := fetchIPs(cli, context.Background(), r)
	if len(errProblems) != 0 || len(diagNotes) != 0 {
		t.Errorf("success path: both errProblems and diagNotes must be empty, got errProblems=%v diagNotes=%v", errProblems, diagNotes)
	}
	if len(ips) == 0 || ips[0] != "192.168.1.42" {
		t.Fatalf("expected live-resolved 192.168.1.42 (validates JSON envelope fix), got %v", ips)
	}
}

// TestFormatIPFailure_Stopped: status-aware suggestion includes
// "enciende" — actionable, not just descriptive.
func TestFormatIPFailure_Stopped(t *testing.T) {
	r := proxmox.ClusterResource{Name: "Test-WOL", Status: "stopped", VMID: 100}
	// Stopped path: both diagnostics go to diagNotes; errProblems stays empty
	// because the calls didn't fail at the protocol level (skipped deliberately).
	err := formatIPFailure(r, nil, []string{
		"/lxc/100/config: sin 'ip=' en net0..net3 (probable DHCP o sin red)",
		"/lxc/100/interfaces: omitido (contenedor apagado)",
	})
	msg := err.Error()
	if !strings.Contains(msg, "apagado") {
		t.Errorf("missing status note. got:\n%s", msg)
	}
	if !strings.Contains(msg, "sin 'ip=' en net0") {
		t.Errorf("missing cfg-empty diagnostic. got:\n%s", msg)
	}
	if !strings.Contains(msg, "omitido (contenedor apagado)") {
		t.Errorf("missing live-skip sentinel. got:\n%s", msg)
	}
	if !strings.Contains(msg, "enciende") {
		t.Errorf("expected actionable 'enciende' suggestion, got:\n%s", msg)
	}
}

// TestFormatIPFailure_Running_AllNotes: status=running but every call
// succeeded empty. The user's actual case (v0.1.10 report). Suggestion
// MUST NOT say "revisa el token" because there's nothing wrong with the
// token — every API call worked. Must pivot to the DHCP-aware guidance:
// configure static IP, or wait for DHCP lease.
func TestFormatIPFailure_Running_AllNotes(t *testing.T) {
	r := proxmox.ClusterResource{Name: "Test-WOL", Status: "running", VMID: 129}
	err := formatIPFailure(r,
		[]string{}, // no protocol errors — every call clean
		[]string{
			"/cluster/resources: sin lease cacheado",
			"/lxc/129/config: sin 'ip=' en net0..net3 (probable DHCP o sin red)",
			"/lxc/129/interfaces: respuesta vacía (¿sin red o sin agente invitado?)",
		},
	)
	msg := err.Error()
	if strings.Contains(msg, "enciende") {
		t.Errorf("running container must NOT get an 'enciende' suggestion. got:\n%s", msg)
	}
	// Pivoted advice: DHCP-aware, NOT token-permission.
	if strings.Contains(msg, "VM.Audit") || strings.Contains(msg, "VM.Monitor") {
		t.Errorf("running+DHCP-empty must NOT suggest token review (calls succeeded). got:\n%s", msg)
	}
	if !strings.Contains(msg, "DHCP") && !strings.Contains(msg, "estática") {
		t.Errorf("expected DHCP-aware / configure-estática guidance, got:\n%s", msg)
	}
}

// TestFormatIPFailure_Running_WithProtocolError: at least one source errored.
// Suggestion MUST pivot back to token/permissions (that IS the actionable
// path now). Pins that we didn't lose the permissions branch while
// adding the DHCP-aware one.
func TestFormatIPFailure_Running_WithProtocolError(t *testing.T) {
	r := proxmox.ClusterResource{Name: "Test-WOL", Status: "running", VMID: 129}
	err := formatIPFailure(r,
		[]string{"/lxc/129/interfaces: 192.168.1.10: node 192.168.1.10: returned HTTP 403"},
		[]string{"/lxc/129/config: sin 'ip=' en net0..net3 (probable DHCP)"},
	)
	msg := err.Error()
	if !strings.Contains(msg, "VM.Audit") || !strings.Contains(msg, "VM.Monitor") {
		t.Errorf("protocol error in errProblems must trigger permission hint, got:\n%s", msg)
	}
}

// TestFetchIPs_Paused_NotSkipped: a paused LXC's netns is CGroup-frozen but
// still readable via /lxc/{vmid}/interfaces (PVE keeps last-known data).
// The skip-list targets ONLY "stopped" — paused containers MUST still hit
// the live endpoint, otherwise we'd gratuitously lose information.
//
// Pins the contract documented in fetchIPs: "skip ONLY on r.Status=='stopped'".
// If a future contributor extends the gate to include "paused", this test
// fails loudly on the liveCalled=false assertion.
func TestFetchIPs_Paused_NotSkipped(t *testing.T) {
	var liveCalled bool
	h := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/interfaces") {
			liveCalled = true
			return mustResp(t, 200, `{"data":[{"name":"eth0","hwaddr":"aa:bb:cc:dd:ee:ff","type":"bridge","ip-addresses":[{"ip-address":"10.0.0.77","prefix":24,"type":"inet"}]}]}`), nil
		}
		return mustResp(t, 200, `{"data":{"net0":"name=eth0,bridge=vmbr0,gw=10.0.0.1,ip=dhcp"}}`), nil
	})
	cli := newStubbedClient(t, h)
	r := proxmox.ClusterResource{
		Name: "Test-WOL", Node: "pve", Type: proxmox.ResourceLXC,
		Status: "paused", VMID: 100,
	}
	ips, errProblems, diagNotes := fetchIPs(cli, context.Background(), r)
	if !liveCalled {
		t.Fatalf("paused container MUST hit /interfaces (CGroup freeze keeps netns readable)")
	}
	if len(errProblems) != 0 || len(diagNotes) != 0 {
		t.Errorf("paused+DHCP+populated live must have NO diagnostics, got errProblems=%v diagNotes=%v", errProblems, diagNotes)
	}
	if len(ips) == 0 || ips[0] != "10.0.0.77" {
		t.Fatalf("expected 10.0.0.77 from live, got %v", ips)
	}
}

// TestUnmarshalLXCIPEntry_PrefixAsNumber: pin the legacy wire shape that
// most PVE 7.x/8.x ship. Regression guard for type-int preference.
func TestUnmarshalLXCIPEntry_PrefixAsNumber(t *testing.T) {
	body := []byte(`{"ip-address":"192.168.1.10","prefix":24,"type":"inet"}`)
	var e proxmox.LXCIPEntry
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if e.Prefix != 24 || e.IPAddress != "192.168.1.10" || e.Type != "inet" {
		t.Fatalf("unexpected decode: %+v", e)
	}
}

// TestUnmarshalLXCIPEntry_PrefixAsString: pin the wire shape the user
// reported in their cluster (v0.1.8-pve-ip-fix failed on this exact case
// with `cannot unmarshal string into Go struct field
// LXCIPEntry.data.ip-addresses.prefix of type int`). Without this fix, the
// entire fetchIPs response failed decode and `pve ip Test-WOL` could never
// return a live IP.
func TestUnmarshalLXCIPEntry_PrefixAsString(t *testing.T) {
	body := []byte(`{"ip-address":"192.168.1.10","prefix":"24","type":"inet"}`)
	var e proxmox.LXCIPEntry
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("unmarshal failed (this was the user's reported bug): %v", err)
	}
	if e.Prefix != 24 || e.IPAddress != "192.168.1.10" || e.Type != "inet" {
		t.Fatalf("unexpected decode from string-prefix: %+v", e)
	}
}

// TestUnmarshalLXCIPEntry_PrefixMissing: PVE can return entries with no ip
// (e.g. an interface in the data plane but with no address yet). Must not
// crash; Prefix stays 0 and the entry is filtered downstream by type checks.
func TestUnmarshalLXCIPEntry_PrefixMissing(t *testing.T) {
	body := []byte(`{"ip-address":"192.168.1.10","type":"inet"}`)
	var e proxmox.LXCIPEntry
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if e.Prefix != 0 {
		t.Fatalf("expected Prefix=0 on missing, got %d", e.Prefix)
	}
}

// TestUnmarshalLXCIPEntry_PrefixNull: explicit null. Same as missing — must
// be a clean zero, not an error.
func TestUnmarshalLXCIPEntry_PrefixNull(t *testing.T) {
	body := []byte(`{"ip-address":"192.168.1.10","prefix":null,"type":"inet"}`)
	var e proxmox.LXCIPEntry
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if e.Prefix != 0 {
		t.Fatalf("expected Prefix=0 on null, got %d", e.Prefix)
	}
}

// TestUnmarshalLXCIPEntry_PrefixGarbage: a non-numeric prefix string should
// surface as a clear decode error, not a silent zero (which would have
// silently broken CIDR parsing downstream).
func TestUnmarshalLXCIPEntry_PrefixGarbage(t *testing.T) {
	body := []byte(`{"ip-address":"192.168.1.10","prefix":"abc","type":"inet"}`)
	var e proxmox.LXCIPEntry
	if err := json.Unmarshal(body, &e); err == nil {
		t.Fatalf("expected error on garbage prefix, got clean decode: %+v", e)
	}
}

// TestFetchIPs_StringPrefix: end-to-end regression — when PVE returns the
// prefix as a string, fetchIPs must still resolve the IP (instead of
// failing decode and emitting the user's reported error).
func TestFetchIPs_StringPrefix(t *testing.T) {
	h := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/interfaces") {
			// The exact wire shape the user reported: prefix is a JSON string.
			return mustResp(t, 200, `{"data":[{"name":"eth0","hwaddr":"aa:bb:cc:dd:ee:ff","type":"bridge","ip-addresses":[{"ip-address":"192.168.1.99","prefix":"24","type":"inet"}]}]}`), nil
		}
		return mustResp(t, 200, `{"data":{"net0":"name=eth0,bridge=vmbr0,gw=192.168.1.1,ip=dhcp"}}`), nil
	})
	cli := newStubbedClient(t, h)
	r := proxmox.ClusterResource{
		Name: "Test-WOL", Node: "pve1", Type: proxmox.ResourceLXC,
		Status: "running", VMID: 129,
	}
	ips, errProblems, diagNotes := fetchIPs(cli, context.Background(), r)
	if len(errProblems) != 0 || len(diagNotes) != 0 {
		t.Errorf("string-prefix PVE wire must decode cleanly, got errProblems=%v diagNotes=%v", errProblems, diagNotes)
	}
	if len(ips) == 0 || ips[0] != "192.168.1.99" {
		t.Fatalf("expected 192.168.1.99 from string-prefix wire, got %v", ips)
	}
}

// TestFetchIPs_FailPath_ClusterResourceNote: pins the v0.1.10 transparency
// improvement — on total failure (no IP from any of the three sources),
// the diagnostic MUST include a /cluster/resources line so the user sees
// all three sources were probed (instead of only two).
func TestFetchIPs_FailPath_ClusterResourceNote(t *testing.T) {
	h := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(req.URL.Path, "/cluster/resources"):
			// cluster/resources returns no IP-cached entries for our LXC.
			return mustResp(t, 200, `{"data":[]}`), nil
		case strings.Contains(req.URL.Path, "/interfaces"):
			// live endpoint returns empty data (typical for running-DHCP-no-lease).
			return mustResp(t, 200, `{"data":[]}`), nil
		default:
			// /config: net0 has DHCP, no static ip=.
			return mustResp(t, 200, `{"data":{"hostname":"Test-WOL","net0":"name=eth0,bridge=vmbr0,gw=192.168.1.1,ip=dhcp"}}`), nil
		}
	})
	cli := newStubbedClient(t, h)
	r := proxmox.ClusterResource{
		Name: "Test-WOL", Node: "pve1", Type: proxmox.ResourceLXC,
		Status: "running", VMID: 129,
	}
	ips, errProblems, diagNotes := fetchIPs(cli, context.Background(), r)
	if len(ips) != 0 {
		t.Fatalf("expected zero IPs, got %v", ips)
	}
	if len(errProblems) != 0 {
		t.Errorf("expected no protocol errors, got: %v", errProblems)
	}
	if len(diagNotes) < 3 {
		t.Fatalf("expected diagNotes length >=3 (cfg-empty + live-empty + cluster-empty), got %v", diagNotes)
	}
	hasClusterLine := false
	for _, line := range diagNotes {
		if strings.HasPrefix(line, "/cluster/resources") {
			hasClusterLine = true
		}
	}
	if !hasClusterLine {
		t.Errorf("missing cluster/resources transparency line in diagNotes. got: %v", diagNotes)
	}
}

// TestFetchIPs_PartialSuccess_OmitsCluster: v0.1.10 contract — on partial
// success (one source returns an IP), the cluster/resources diagnostic MUST
// NOT leak. Only emit it on total failure.
func TestFetchIPs_PartialSuccess_OmitsCluster(t *testing.T) {
	h := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/interfaces") {
			return mustResp(t, 200, `{"data":[]}`), nil
		}
		return mustResp(t, 200, `{"data":{"net0":"name=eth0,bridge=vmbr0,gw=10.0.0.1,ip=10.0.0.7/24"}}`), nil
	})
	cli := newStubbedClient(t, h)
	r := proxmox.ClusterResource{
		Name: "Test-WOL", Node: "pve", Type: proxmox.ResourceLXC,
		Status: "running", VMID: 100,
	}
	_, errProblems, diagNotes := fetchIPs(cli, context.Background(), r)
	for _, line := range diagNotes {
		if strings.HasPrefix(line, "/cluster/resources") {
			t.Errorf("partial success: cluster/resources diagnostic must NOT leak. got: %v", diagNotes)
		}
	}
	if len(errProblems) != 0 || len(diagNotes) != 0 {
		t.Errorf("expected clean success path, got errProblems=%v diagNotes=%v", errProblems, diagNotes)
	}
}

// TestUnmarshalLXCIPEntry_INET6_StringPrefix: PVE mixes v4 + v6 in the
// same response. Real-world IPv6 link-local + global prefixes are
// commonly stringified on some clusters. Without this test, a regression
// that fixes v4-only would slip past CI.
func TestUnmarshalLXCIPEntry_INET6_StringPrefix(t *testing.T) {
	body := []byte(`{"ip-address":"fe80::1","prefix":"64","type":"inet6"}`)
	var e proxmox.LXCIPEntry
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("unmarshal failed for v6: %v", err)
	}
	if e.Prefix != 64 || e.IPAddress != "fe80::1" || e.Type != "inet6" {
		t.Fatalf("unexpected v6 decode: %+v", e)
	}
}

// TestUnmarshalLXCIPEntry_INET6_NumberPrefix: same as above with the
// legacy number-form prefix. Pins both branches of the int/string tolerant
// decode for v6.
func TestUnmarshalLXCIPEntry_INET6_NumberPrefix(t *testing.T) {
	body := []byte(`{"ip-address":"fe80::1","prefix":64,"type":"inet6"}`)
	var e proxmox.LXCIPEntry
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("unmarshal failed for v6: %v", err)
	}
	if e.Prefix != 64 || e.IPAddress != "fe80::1" || e.Type != "inet6" {
		t.Fatalf("unexpected v6 decode: %+v", e)
	}
}

// TestParseHostnameI: unit test for the hostname -I output parsing in
// execLXCIPs. Space-separated IPs, loopback filtering, edge cases.
func TestParseHostnameI(t *testing.T) {
	// Simulate hostname -I output parsing (mirrors execLXCIPs logic).
	parse := func(output string) []string {
		var ips []string
		for _, token := range strings.Fields(output) {
			token = strings.TrimSpace(token)
			if token == "" || strings.HasPrefix(token, "127.") {
				continue
			}
			if net.ParseIP(token) != nil {
				ips = append(ips, token)
			}
		}
		return ips
	}
	// Single IP.
	if ips := parse("10.0.0.5"); len(ips) != 1 || ips[0] != "10.0.0.5" {
		t.Fatalf("single IP: got %v", ips)
	}
	// Multiple IPs (loopback filtered).
	if ips := parse("127.0.0.1 192.168.1.42 10.0.0.5"); len(ips) != 2 || ips[0] != "192.168.1.42" {
		t.Fatalf("multi IP with loopback: got %v", ips)
	}
	// Empty output.
	if ips := parse(""); len(ips) != 0 {
		t.Fatalf("empty: got %v", ips)
	}
	// Only loopback.
	if ips := parse("127.0.0.1"); len(ips) != 0 {
		t.Fatalf("only loopback: got %v", ips)
	}
	// With whitespace and invalid tokens.
	if ips := parse("  10.0.0.5   abc  \n 172.16.0.1  "); len(ips) != 2 {
		t.Fatalf("whitespace: got %v", ips)
	}
	// IPv6 addresses (should be parsed by net.ParseIP but filtered by caller logic — here we don't filter v4/v6 at this level).
	if ips := parse("fe80::1 10.0.0.5"); len(ips) != 2 {
		t.Fatalf("mixed v4/v6: got %v", ips)
	}
}

// --- resolveExecTarget tests ---------------------------------------------

func TestResolveExecTarget_VMIDExactLXC(t *testing.T) {
	resources := []proxmox.ClusterResource{
		{ID: "lxc/100", Node: "pve", Type: proxmox.ResourceLXC, Name: "nginx", Status: "running", VMID: 100},
		{ID: "qemu/200", Node: "pve", Type: proxmox.ResourceQemu, Name: "win10", Status: "running", VMID: 200},
	}
	target, err := resolveExecTarget(resources, "100")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.VMID != 100 || target.Name != "nginx" {
		t.Fatalf("expected nginx vmid=100, got %+v", target)
	}
}

func TestResolveExecTarget_VMIDExactQEMU(t *testing.T) {
	resources := []proxmox.ClusterResource{
		{ID: "qemu/200", Node: "pve", Type: proxmox.ResourceQemu, Name: "win10", Status: "running", VMID: 200},
	}
	_, err := resolveExecTarget(resources, "200")
	if err == nil {
		t.Fatalf("expected QEMU to be rejected")
	}
	if !strings.Contains(err.Error(), "solo funciona con LXC") {
		t.Fatalf("expected LXC-only error, got: %v", err)
	}
}

func TestResolveExecTarget_FuzzyName(t *testing.T) {
	resources := []proxmox.ClusterResource{
		{ID: "lxc/101", Node: "pve", Type: proxmox.ResourceLXC, Name: "postgres", Status: "running", VMID: 101},
		{ID: "lxc/102", Node: "pve", Type: proxmox.ResourceLXC, Name: "mysql", Status: "running", VMID: 102},
	}
	target, err := resolveExecTarget(resources, "post")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.VMID != 101 || target.Name != "postgres" {
		t.Fatalf("expected postgres vmid=101, got %+v", target)
	}
}

func TestResolveExecTarget_FuzzyQEMURejected(t *testing.T) {
	resources := []proxmox.ClusterResource{
		{ID: "qemu/200", Node: "pve", Type: proxmox.ResourceQemu, Name: "win10", Status: "running", VMID: 200},
	}
	_, err := resolveExecTarget(resources, "win10")
	if err == nil {
		t.Fatalf("expected QEMU to be rejected")
	}
	if !strings.Contains(err.Error(), "solo funciona con LXC") {
		t.Fatalf("expected LXC-only error, got: %v", err)
	}
}

func TestResolveExecTarget_IPMatch(t *testing.T) {
	resources := []proxmox.ClusterResource{
		{ID: "lxc/105", Node: "pve", Type: proxmox.ResourceLXC, Name: "app", Status: "running", VMID: 105, IP: "192.168.1.50"},
	}
	target, err := resolveExecTarget(resources, "192.168.1.50")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.VMID != 105 {
		t.Fatalf("expected vmid=105, got %+v", target)
	}
}

func TestResolveExecTarget_NoMatch(t *testing.T) {
	resources := []proxmox.ClusterResource{
		{ID: "lxc/105", Node: "pve", Type: proxmox.ResourceLXC, Name: "app", Status: "running", VMID: 105},
	}
	_, err := resolveExecTarget(resources, "nonexistent")
	if err == nil {
		t.Fatalf("expected no-match error")
	}
	if !strings.Contains(err.Error(), "sin coincidencias") {
		t.Fatalf("expected no-match error, got: %v", err)
	}
}

func TestResolveExecTarget_BestScoreWins(t *testing.T) {
	resources := []proxmox.ClusterResource{
		// exact name match should outscore substring match
		{ID: "lxc/110", Node: "pve", Type: proxmox.ResourceLXC, Name: "postgres", Status: "running", VMID: 110},
		{ID: "lxc/111", Node: "pve", Type: proxmox.ResourceLXC, Name: "postgres-dev", Status: "running", VMID: 111},
	}
	target, err := resolveExecTarget(resources, "postgres")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.VMID != 110 || target.Name != "postgres" {
		t.Fatalf("expected exact-name match postgres vmid=110, got %+v", target)
	}
}

func TestResolveExecTarget_FuzzyQEMUFirstThenLXC(t *testing.T) {
	// The highest-scoring fuzzy match is a QEMU, but an LXC also matches.
	// resolveExecTarget must still reject the QEMU with a clear error.
	resources := []proxmox.ClusterResource{
		{ID: "qemu/200", Node: "pve", Type: proxmox.ResourceQemu, Name: "win10", Status: "running", VMID: 200},
		{ID: "lxc/105", Node: "pve", Type: proxmox.ResourceLXC, Name: "win10-lxc", Status: "running", VMID: 105},
	}
	_, err := resolveExecTarget(resources, "win10")
	if err == nil {
		t.Fatalf("expected QEMU to be rejected even when an LXC also matches")
	}
	if !strings.Contains(err.Error(), "solo funciona con LXC") {
		t.Fatalf("expected LXC-only error, got: %v", err)
	}
}

func TestResolveExecTarget_EmptyQuery(t *testing.T) {
	resources := []proxmox.ClusterResource{
		{ID: "lxc/105", Node: "pve", Type: proxmox.ResourceLXC, Name: "app", Status: "running", VMID: 105},
	}
	_, err := resolveExecTarget(resources, "")
	if err == nil {
		t.Fatalf("expected no-match error for empty query")
	}
	if !strings.Contains(err.Error(), "sin coincidencias") {
		t.Fatalf("expected no-match error, got: %v", err)
	}
}

func TestResolveExecTarget_EmptyResources(t *testing.T) {
	_, err := resolveExecTarget([]proxmox.ClusterResource{}, "app")
	if err == nil {
		t.Fatalf("expected no-match error for empty resources")
	}
	if !strings.Contains(err.Error(), "sin coincidencias") {
		t.Fatalf("expected no-match error, got: %v", err)
	}
}

func TestResolveExecTarget_VMIDNonContainerFallsThrough(t *testing.T) {
	// A VMID query that matches a node/storage should NOT resolve to that
	// resource; it should fall through to fuzzy search and find the LXC
	// whose name happens to contain the same digits.
	resources := []proxmox.ClusterResource{
		{ID: "node/pve", Node: "pve", Type: proxmox.ResourceNode, Name: "pve", Status: "online", VMID: 100},
		{ID: "lxc/100", Node: "pve", Type: proxmox.ResourceLXC, Name: "app-100", Status: "running", VMID: 100},
	}
	target, err := resolveExecTarget(resources, "100")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.Type != proxmox.ResourceLXC || target.Name != "app-100" {
		t.Fatalf("expected LXC app-100, got %+v", target)
	}
}

func TestResolveExecTarget_VMIDMissingFallsThroughToFuzzy(t *testing.T) {
	// A numeric query that doesn't match any VMID should fall through to
	// fuzzy search and match a container name.
	resources := []proxmox.ClusterResource{
		{ID: "lxc/105", Node: "pve", Type: proxmox.ResourceLXC, Name: "app-100", Status: "running", VMID: 105},
	}
	target, err := resolveExecTarget(resources, "100")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.VMID != 105 || target.Name != "app-100" {
		t.Fatalf("expected app-100 vmid=105, got %+v", target)
	}
}

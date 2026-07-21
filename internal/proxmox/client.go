package proxmox

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/term"
)

// Client is a thin Proxmox REST client that fans out across a list of nodes.
type Client struct {
	Nodes       []string // hostnames OR IPs of each PVE node
	Token       string   // full "PVEAPIToken=USER@REALM!TOKENID=UUID" value
	IsSecure    bool     // true = https, false = http (port 8006 default for both)
	SSHUser     string   // SSH user for pct exec fallback (default: token user or root)
	SSHPassword string   // SSH password for PVE node (fallback when no keys are configured)
	HTTP        *http.Client
}

// NewClient returns a client configured with sensible defaults
// (HTTPS on 8006, TLS verification disabled by default for local LAN setups).
func NewClient(nodes []string, token string) *Client {
	tr := &http.Transport{
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true}, // LAN PVE often uses self-signed certs
		MaxIdleConns:          50,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	}
	return &Client{
		Nodes:    nodes,
		Token:    token,
		IsSecure: true,
		HTTP:     &http.Client{Transport: tr, Timeout: 30 * time.Second},
	}
}

func (c *Client) baseURL(node string) string {
	scheme := "http"
	if c.IsSecure {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:8006", scheme, node)
}

// do performs an authenticated GET against the given node's endpoint
// and decodes the JSON envelope {"data": ...}.
func (c *Client) do(ctx context.Context, node, path string, out interface{}) error {
	return c.doWithMethod(ctx, "GET", node, path, nil, out)
}

// doPost performs an authenticated POST against the given node's endpoint.
// An optional form body can be provided; if nil, an empty POST is sent.
func (c *Client) doPost(ctx context.Context, node, path string, body url.Values, out interface{}) error {
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(body.Encode())
	}
	return c.doWithMethod(ctx, "POST", node, path, reader, out)
}

// doWithMethod performs an authenticated HTTP request with the given method
// and decodes the JSON envelope {"data": ...}.
func (c *Client) doWithMethod(ctx context.Context, method, node, path string, reqBody io.Reader, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL(node)+path, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.Token)
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("node %s: %w", node, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		// Try to surface Proxmox's error envelope if present
		return fmt.Errorf("node %s returned HTTP %d: %s", node, resp.StatusCode, truncate(string(body), 300))
	}
	if out == nil {
		return nil
	}
	// Proxmox may return a single resource envelope {"data": null} on errors,
	// a list envelope {"data": [...]}, or an object envelope {"data": {...}}.
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		// Some endpoints return raw arrays; try fallback.
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("node %s: decode: %w", node, err)
		}
	}
	return nil
}

func (c *Client) GetClusterResources(ctx context.Context) ([]ClusterResource, error) {
	var lastErr error
	for _, node := range c.Nodes {
		var resp ClusterResourcesResponse
		err := c.do(ctx, node, "/api2/json/cluster/resources", &resp)
		if err != nil {
			lastErr = err
			continue
		}
		return resp.Data, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}

type getCfgHit struct {
	cfg  *LXCConfig
	err  error
	host string
}

func (c *Client) GetLXCConfig(ctx context.Context, node string, vmid int) (*LXCConfig, error) {
	if len(c.Nodes) == 0 {
		return nil, errors.New("no nodes configured (run `pve config nodes <ip1>,<ip2>`)")
	}
	pctx, pcancel := context.WithCancel(ctx)
	defer pcancel()
	ch := make(chan getCfgHit, len(c.Nodes))
	for _, host := range c.Nodes {
		go func(host string) {
			hctx, hcancel := context.WithTimeout(pctx, 6*time.Second)
			defer hcancel()
			var resp ConfigResponse
			path := fmt.Sprintf("/api2/json/nodes/%s/lxc/%d/config", url.PathEscape(node), vmid)
			err := c.do(hctx, host, path, &resp)
			if err != nil {
				ch <- getCfgHit{err: fmt.Errorf("%s: %w", host, err), host: host}
				return
			}
			cfg := &LXCConfig{raw: resp.Data}
			if v, ok := resp.Data["hostname"].(string); ok {
				cfg.Hostname = v
			}
			for k, v := range resp.Data {
				if len(k) < 3 {
					continue
				}
				if len(k) == 4 && k[:3] == "net" && k[3] >= '0' && k[3] <= '9' {
					cfg.parseNet(k, v)
					continue
				}
				if k == "ip0" || k == "ip1" || k == "ip2" || k == "ip3" {
					cfg.parseLegacyIP(k, v)
				}
			}
			ch <- getCfgHit{cfg: cfg, host: host}
		}(host)
	}
	var errs []string
	for i := 0; i < len(c.Nodes); i++ {
		h := <-ch
		if h.err == nil {
			pcancel()
			return h.cfg, nil
		}
		errs = append(errs, h.err.Error())
	}
	return nil, fmt.Errorf("no node responded for %s — try `pve config show`: %s", node, strings.Join(errs, " | "))
}

type getIfHit struct {
	ifaces []LXCInterface
	err    error
	host   string
}

func (c *Client) GetLXCInterfaces(ctx context.Context, node string, vmid int) ([]LXCInterface, error) {
	if len(c.Nodes) == 0 {
		return nil, errors.New("no nodes configured")
	}
	pctx, pcancel := context.WithCancel(ctx)
	defer pcancel()
	ch := make(chan getIfHit, len(c.Nodes))
	for _, host := range c.Nodes {
		go func(host string) {
			hctx, hcancel := context.WithTimeout(pctx, 6*time.Second)
			defer hcancel()
			var resp LXCInterfacesResponse
			path := fmt.Sprintf("/api2/json/nodes/%s/lxc/%d/interfaces", url.PathEscape(node), vmid)
			err := c.do(hctx, host, path, &resp)
			if err != nil {
				ch <- getIfHit{err: fmt.Errorf("%s: %w", host, err), host: host}
				return
			}
			ch <- getIfHit{ifaces: resp.Data, host: host}
		}(host)
	}
	var errs []string
	for i := 0; i < len(c.Nodes); i++ {
		h := <-ch
		if h.err == nil {
			pcancel()
			return h.ifaces, nil
		}
		errs = append(errs, h.err.Error())
	}
	return nil, fmt.Errorf("no node responded for %s: %s", node, strings.Join(errs, " | "))
}

func (c *Client) SSHUserOrDefault() string {
	if c.SSHUser != "" {
		return c.SSHUser
	}
	// Token format: PVEAPIToken=user@realm!tokenid=uuid
	token := c.Token
	if strings.HasPrefix(token, "PVEAPIToken=") {
		token = token[len("PVEAPIToken="):]
	}
	if idx := strings.Index(token, "!"); idx > 0 {
		userRealm := token[:idx]
		if at := strings.Index(userRealm, "@"); at > 0 {
			return userRealm[:at]
		}
	}
	return "root"
}

func (c *Client) ExecLXC(ctx context.Context, node string, vmid int, cmd []string) (string, error) {
	return c.execLXCWithCmd(ctx, node, vmid, strings.Join(escSSH(cmd), " "), false)
}

func (c *Client) ExecLXCScript(ctx context.Context, node string, vmid int, script string) (string, error) {
	return c.execLXCWithCmd(ctx, node, vmid, script, true)
}

func (c *Client) execLXCWithCmd(ctx context.Context, node string, vmid int, cmdStr string, raw bool) (string, error) {
	if len(c.Nodes) == 0 {
		return "", errors.New("no nodes configured")
	}

	user := c.SSHUserOrDefault()

	// Phase 1: try key-based auth (agent + default key files).
	var errs []string
	sshCfg, cfgErr := sshClientConfig(user)
	if cfgErr == nil {
		out, fanoutErrs := c.execLXCFanout(ctx, sshCfg, vmid, cmdStr, raw)
		if out != "" {
			return out, nil
		}
		errs = fanoutErrs
	} else {
		errs = []string{fmt.Sprintf("ssh: %v", cfgErr)}
	}

	// Phase 2: configured password fallback (non-interactive).
	configuredPasswordFailed := false
	needPassword := cfgErr != nil || allAuthFailures(errs)
	if needPassword && c.SSHPassword != "" {
		pwCfg := &ssh.ClientConfig{
			User:            user,
			Auth:            []ssh.AuthMethod{ssh.Password(c.SSHPassword)},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         3 * time.Second,
		}
		if c.autoInstallKeyFast(ctx, pwCfg) {
			if sshCfg2, cfgErr2 := sshClientConfig(user); cfgErr2 == nil {
				out2, errs2 := c.execLXCFanout(ctx, sshCfg2, vmid, cmdStr, raw)
				if out2 != "" {
					return out2, nil
				}
				errs = append(errs, errs2...)
			}
		}
		out2, errs2 := c.execLXCFanout(ctx, pwCfg, vmid, cmdStr, raw)
		if out2 != "" {
			return out2, nil
		}
		if len(errs2) > 0 {
			errs = append(errs, errs2...)
			configuredPasswordFailed = true
		}
	}

	needPassword = cfgErr != nil || allAuthFailures(errs) || configuredPasswordFailed
	if needPassword && isInteractive() {
		promptUser := user
		promptHost := firstNode(c.Nodes)
		if configuredPasswordFailed {
			fmt.Fprintln(os.Stderr, "  Saved password failed; trying interactive prompt.")
		}
		pw, perr := readPasswordInteractive(promptUser, promptHost)
		if perr != nil || len(pw) == 0 {
			return "", fmt.Errorf("ssh: auth failed. Details: %s. Copy your key with `ssh-copy-id %s@%s`", strings.Join(errs, " | "), user, firstNode(c.Nodes))
		}
		pwCfg := &ssh.ClientConfig{
			User:            user,
			Auth:            []ssh.AuthMethod{ssh.Password(string(pw))},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         3 * time.Second,
		}
		if c.autoInstallKeyFast(ctx, pwCfg) {
			if sshCfg2, cfgErr2 := sshClientConfig(user); cfgErr2 == nil {
				out2, errs2 := c.execLXCFanout(ctx, sshCfg2, vmid, cmdStr, raw)
				if out2 != "" {
					return out2, nil
				}
				errs = append(errs, errs2...)
			}
		}
		out2, errs2 := c.execLXCFanout(ctx, pwCfg, vmid, cmdStr, raw)
		if out2 != "" {
			return out2, nil
		}
		errs = append(errs, errs2...)
	}

	if len(errs) > 0 {
		errMsg := strings.Join(errs, " | ")
		if c.SSHPassword != "" {
			return "", fmt.Errorf("ssh: the password configured via `pve config ssh-password` failed for %s@%s. Details: %s", user, firstNode(c.Nodes), errMsg)
		}
		return "", fmt.Errorf("ssh: %s", errMsg)
	}
	return "", errors.New("ssh: all nodes responded but the command produced no output")
}

// execLXCFanout runs pct exec on all nodes in parallel with the given SSH config.
// Returns (stdout, collectedErrors). stdout is non-empty only on success.
func (c *Client) execLXCFanout(ctx context.Context, sshCfg *ssh.ClientConfig, vmid int, cmdStr string, raw bool) (string, []string) {
	pctx, pcancel := context.WithCancel(ctx)
	defer pcancel()
	type hit struct {
		out string
		err error
	}
	ch := make(chan hit, len(c.Nodes))
	for _, host := range c.Nodes {
		go func(host string) {
			hctx, hcancel := context.WithTimeout(pctx, 5*time.Second)
			defer hcancel()
			conn, err := dialSSH(hctx, host+":22", sshCfg)
			if err != nil {
				ch <- hit{err: fmt.Errorf("%s: %w", host, err)}
				return
			}
			defer conn.Close()
			sess, err := conn.NewSession()
			if err != nil {
				ch <- hit{err: fmt.Errorf("%s: session: %w", host, err)}
				return
			}
			defer sess.Close()
			var cmdLine string
			if raw {
				encoded := base64.StdEncoding.EncodeToString([]byte(cmdStr))
				cmdLine = fmt.Sprintf("pct exec %d -- sh -c 'printf \"%%s\\n\" %s | base64 -d | sh' 2>/dev/null", vmid, encoded)
			} else {
				cmdLine = fmt.Sprintf("pct exec %d -- sh -c '%s' 2>/dev/null", vmid, cmdStr)
			}
			out, err := sess.Output(cmdLine)
			if err != nil {
				ch <- hit{err: fmt.Errorf("%s: %w", host, err)}
				return
			}
			ch <- hit{out: strings.TrimSpace(string(out))}
		}(host)
	}
	var errs []string
	for i := 0; i < len(c.Nodes); i++ {
		h := <-ch
		if h.err == nil {
			pcancel()
			return h.out, nil
		}
		errs = append(errs, h.err.Error())
	}
	return "", errs
}

// allAuthFailures reports whether every error string in errs is an SSH auth
// failure (as opposed to network unreachable, timeout, etc).
func allAuthFailures(errs []string) bool {
	if len(errs) == 0 {
		return false
	}
	for _, e := range errs {
		if !strings.Contains(e, "unable to authenticate") &&
			!strings.Contains(e, "Permission denied") {
			return false
		}
	}
	return true
}

// firstNode returns the first node IP or "<ip-pve>" as fallback.
func firstNode(nodes []string) string {
	if len(nodes) > 0 {
		return nodes[0]
	}
	return "<ip-pve>"
}

// GetVersion probes each node for its PVE version.
func (c *Client) GetVersion(ctx context.Context, node string) (string, error) {
	var resp struct {
		Data struct {
			Version string `json:"version"`
			Release string `json:"release"`
		} `json:"data"`
	}
	if err := c.do(ctx, node, "/api2/json/version", &resp); err != nil {
		return "", err
	}
	return resp.Data.Version + "-" + resp.Data.Release, nil
}

// ChangeVMStatus sends a start/stop/restart command to a Proxmox VM or LXC.
// Valid actions are: start, stop, restart.
func (c *Client) ChangeVMStatus(ctx context.Context, node string, vmid int, typ ResourceType, action string) error {
	if len(c.Nodes) == 0 {
		return errors.New("no nodes configured")
	}
	var kind string
	switch typ {
	case ResourceLXC:
		kind = "lxc"
	case ResourceQemu:
		kind = "qemu"
	default:
		return fmt.Errorf("unsupported resource type for start/stop/restart: %s", typ)
	}
	switch action {
	case "start", "stop", "restart":
	default:
		return fmt.Errorf("invalid action: %s (use start|stop|restart)", action)
	}

	path := fmt.Sprintf("/api2/json/nodes/%s/%s/%d/status/%s", url.PathEscape(node), kind, vmid, action)
	var lastErr error
	for _, host := range c.Nodes {
		hctx, hcancel := context.WithTimeout(ctx, 10*time.Second)
		err := c.doPost(hctx, host, path, nil, nil)
		hcancel()
		if err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("no node responded for %s/%d/%s", node, vmid, action)
}

// --- helpers -------------------------------------------------------------

var portKVRe = regexp.MustCompile(`^([^=]+)=(.+)$`)

func (c *LXCConfig) parseNet(key string, raw interface{}) {
	switch v := raw.(type) {
	case nil:
		// PVE may return netN: null when a slot is reserved but unused
		// (seen in real clusters). Leave NetN untouched.
		return
	case string:
		// Format: name=eth0,ip=192.168.1.10/24,gw=...
		parts := strings.Split(v, ",")
		m := map[string]interface{}{}
		for _, p := range parts {
			if kv := portKVRe.FindStringSubmatch(p); kv != nil {
				m[kv[1]] = kv[2]
			}
		}
		switch key {
		case "net0":
			c.Net0 = m
		case "net1":
			c.Net1 = m
		case "net2":
			c.Net2 = m
		case "net3":
			c.Net3 = m
		}
		c.raw[key] = m
	default:
		switch key {
		case "net0":
			c.Net0 = toStringMap(v)
		case "net1":
			c.Net1 = toStringMap(v)
		case "net2":
			c.Net2 = toStringMap(v)
		case "net3":
			c.Net3 = toStringMap(v)
		}
	}
}

func (c *LXCConfig) parseLegacyIP(key string, raw interface{}) {
	v, _ := raw.(string)
	v = strings.TrimSpace(v)
	if v == "" {
		return
	}
	switch key {
	case "ip0":
		c.IP0 = v
	case "ip1":
		c.IP1 = v
	case "ip2":
		c.IP2 = v
	case "ip3":
		c.IP3 = v
	}
}

func toStringMap(v interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	switch m := v.(type) {
	case map[string]interface{}:
		for k, val := range m {
			out[k] = fmt.Sprintf("%v", val)
		}
	}
	return out
}

func (c *LXCConfig) HasIPs() []string {
	sentinels := map[string]struct{}{
		"dhcp": {}, "dhcp6": {}, "auto": {}, "manual": {}, "static": {},
	}
	var ips []string
	appendUnique := func(candidate string) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			return
		}
		// sentinels match on the bare address (before any "/mask").
		bare := candidate
		if i := strings.Index(candidate, "/"); i >= 0 {
			bare = candidate[:i]
		}
		if _, bad := sentinels[strings.ToLower(bare)]; bad {
			return
		}
		for _, seen := range ips {
			if seen == candidate {
				return
			}
		}
		ips = append(ips, candidate)
	}
	// Modern path: ip= / ip6= inside the parsed netN map.
	for _, n := range []map[string]interface{}{c.Net0, c.Net1, c.Net2, c.Net3} {
		if n == nil {
			continue
		}
		for _, k := range []string{"ip", "ip6"} {
			v, ok := n[k].(string)
			if !ok {
				continue
			}
			appendUnique(v)
		}
	}
	// Legacy path: ip0..ip3 top-level keys (no CIDR may be present, or it may).
	for _, raw := range []string{c.IP0, c.IP1, c.IP2, c.IP3} {
		appendUnique(raw)
	}
	return ips
}

// ValidateToken ensures the token header format is sensible.
func ValidateToken(t string) error {
	if t == "" {
		return errors.New("empty token")
	}
	if !strings.HasPrefix(t, "PVEAPIToken=") {
		return errors.New("token must start with PVEAPIToken=")
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// --- SSH helpers ---------------------------------------------------------

func sshClientConfig(user string) (*ssh.ClientConfig, error) {
	var methods []ssh.AuthMethod
	// 1. SSH agent.
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if c, err := net.Dial("unix", sock); err == nil {
			methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(c).Signers))
			// Don't close c — the Signers callback needs the connection
			// alive during the SSH handshake. It'll be GC'd afterwards.
		}
	}
	// 2. Default key files.
	home, _ := os.UserHomeDir()
	for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
		key, err := os.ReadFile(filepath.Join(home, ".ssh", name))
		if err != nil {
			continue
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			continue
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if len(methods) == 0 {
		return nil, errors.New("no SSH keys found (~/.ssh/id_ed25519, id_rsa) and no SSH agent")
	}
	return &ssh.ClientConfig{
		User:            user,
		Auth:            methods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         3 * time.Second,
	}, nil
}

// isInteractive reports whether stdin is a terminal (not a pipe/redirect).
// Used to decide whether we can prompt the user for a password.
func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

func readPasswordInteractive(user, host string) ([]byte, error) {
	fmt.Fprintf(os.Stderr, "  SSH: no hay claves SSH autorizadas. Password del nodo PVE %s@%s: ", user, strings.Split(host, ":")[0])
	defer fmt.Fprintln(os.Stderr)
	return term.ReadPassword(int(os.Stdin.Fd()))
}

// dialSSH is a tiny wrapper so tests can swap it.
var dialSSH = func(ctx context.Context, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}

func escSSH(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		// Only allow safe chars: alphanumeric, dash, dot, slash, colon, equals.
		safe := true
		for _, r := range a {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
				r == '-' || r == '.' || r == '/' || r == ':' || r == '=' || r == '_' {
				continue
			}
			safe = false
			break
		}
		if safe && a != "" {
			out = append(out, a)
		}
	}
	return out
}

// getLocalPublicKey returns the contents of the first available SSH public
// key in ~/.ssh. If none is found, it returns an empty string.
func getLocalPublicKey() (string, error) {
	home, _ := os.UserHomeDir()
	for _, name := range []string{"id_ed25519.pub", "id_rsa.pub", "id_ecdsa.pub"} {
		path := filepath.Join(home, ".ssh", name)
		if b, err := os.ReadFile(path); err == nil {
			pub := strings.TrimSpace(string(b))
			if !looksLikeSSHPublicKey(pub) {
				continue
			}
			return pub, nil
		}
	}
	return "", errors.New("no public key found in ~/.ssh")
}

// looksLikeSSHPublicKey performs a cheap sanity check on a candidate public
// key line. It does not cryptographically validate the key.
func looksLikeSSHPublicKey(s string) bool {
	if s == "" {
		return false
	}
	parts := strings.Fields(s)
	if len(parts) < 2 {
		return false
	}
	prefix := parts[0]
	return strings.HasPrefix(prefix, "ssh-") || strings.HasPrefix(prefix, "ecdsa-") || strings.HasPrefix(prefix, "sk-")
}

func (c *Client) autoInstallKeyFast(ctx context.Context, pwCfg *ssh.ClientConfig) bool {
	pub, err := getLocalPublicKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [Auto-SSH] %v. Generate one with `ssh-keygen -t ed25519` to avoid entering a password.\n", err)
		return false
	}

	var wg sync.WaitGroup
	var anySuccess int32
	var mu sync.Mutex
	var errMsgs []string

	for _, host := range c.Nodes {
		wg.Add(1)
		go func(host string) {
			defer wg.Done()
			hctx, hcancel := context.WithTimeout(ctx, 8*time.Second)
			defer hcancel()
			conn, err := dialSSH(hctx, host+":22", pwCfg)
			if err != nil {
				mu.Lock()
				errMsgs = append(errMsgs, fmt.Sprintf("%s: %v", host, err))
				mu.Unlock()
				return
			}
			defer conn.Close()

			// Idempotent install: create .ssh, ensure key is present.
			sess, err := conn.NewSession()
			if err != nil {
				mu.Lock()
				errMsgs = append(errMsgs, fmt.Sprintf("%s: session: %v", host, err))
				mu.Unlock()
				return
			}
			defer sess.Close()

			sess.Stdin = strings.NewReader(pub)
			installCmd := "key=$(cat); mkdir -p ~/.ssh && chmod 700 ~/.ssh && " +
				"(grep -qxF \"$key\" ~/.ssh/authorized_keys 2>/dev/null || printf '%s\\n' \"$key\" >> ~/.ssh/authorized_keys) && " +
				"chmod 600 ~/.ssh/authorized_keys"
			if err := sess.Run(installCmd); err == nil {
				atomic.AddInt32(&anySuccess, 1)
			} else {
				mu.Lock()
				errMsgs = append(errMsgs, fmt.Sprintf("%s: install: %v", host, err))
				mu.Unlock()
			}
		}(host)
	}
	wg.Wait()

	if atomic.LoadInt32(&anySuccess) > 0 {
		fmt.Fprintf(os.Stderr, "  [Auto-SSH] Public key installed on the PVE node. Next time it will not ask for a password.\n")
		return true
	}
	fmt.Fprintf(os.Stderr, "  [Auto-SSH] No se pudo instalar la clave:%s\n", strings.Join(errMsgs, "; "))
	return false
}

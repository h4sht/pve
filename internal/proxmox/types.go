package proxmox

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ResourceType identifies a Proxmox resource kind.
type ResourceType string

const (
	ResourceNode    ResourceType = "node"
	ResourceLXC     ResourceType = "lxc"
	ResourceQemu    ResourceType = "qemu"
	ResourceStorage ResourceType = "storage"
	ResourcePool    ResourceType = "pool"
	ResourceSdn     ResourceType = "sdn"
)

// NetworkInfo holds per-interface IP data from /cluster/resources.
// PVE 8.x returns this for both QEMU and LXC resources when IP info is
// available (cached DHCP lease, static IP on a running container, etc).
type NetworkInfo struct {
	Name        string           `json:"name"`
	IPAddresses []IPAddressEntry `json:"ipaddresses"`
}

// IPAddressEntry is one IP on a network interface as reported by
// /cluster/resources. Mirrors LXCIPEntry but uses the raw PVE key names.
type IPAddressEntry struct {
	IPAddress string `json:"ip-address"`
	Prefix    int    `json:"prefix"`
}

// HasIPs extracts usable IPs from the NetworkInfo slice, dropping
// loopback and link-local addresses. Returns bare IPs (no CIDR mask).
func (ni NetworkInfo) HasIPs() []string {
	var ips []string
	for _, addr := range ni.IPAddresses {
		ip := addr.IPAddress
		// Strip CIDR mask if present.
		if base, _, ok := strings.Cut(ip, "/"); ok {
			ip = base
		}
		ip = strings.TrimSpace(ip)
		if ip == "" || strings.HasPrefix(ip, "127.") || strings.HasPrefix(ip, "::1") || strings.HasPrefix(ip, "fe80:") {
			continue
		}
		ips = append(ips, ip)
	}
	return ips
}

// ClusterResource is a row from /cluster/resources
type ClusterResource struct {
	ID     string       `json:"id"`
	Node   string       `json:"node"`
	Type   ResourceType `json:"type"`
	IP     string       `json:"ip"`
	Name   string       `json:"name"`
	Status string       `json:"status"`
	VMID   int          `json:"vmid"`
	// optionally present depending on resource
	MaxCPU    int     `json:"maxcpu,omitempty"`
	CPU       float64 `json:"cpu,omitempty"`
	MaxMem    int64   `json:"maxmem,omitempty"`
	Mem       int64   `json:"mem,omitempty"`
	Uptime    int64   `json:"uptime,omitempty"`
	Template  int     `json:"template,omitempty"`
	NetIn     int64   `json:"netin,omitempty"`
	NetOut    int64   `json:"netout,omitempty"`
	DiskRead  int64   `json:"diskread,omitempty"`
	DiskWrite int64   `json:"diskwrite,omitempty"`
	Tags      string  `json:"tags,omitempty"`
	// Networks holds per-interface IP info (PVE 8.x). Populated alongside
	// or instead of the legacy top-level IP field for DHCP+LXC scenarios.
	Networks []NetworkInfo `json:"networks,omitempty"`
}

// LXCConfig is fetched from /nodes/{node}/lxc/{vmid}/config
type LXCConfig struct {
	Hostname   string                 `json:"hostname"`
	Nameserver string                 `json:"nameserver,omitempty"`
	Net0       map[string]interface{} `json:"net0,omitempty"`
	Net1       map[string]interface{} `json:"net1,omitempty"`
	Net2       map[string]interface{} `json:"net2,omitempty"`
	Net3       map[string]interface{} `json:"net3,omitempty"`
	// Legacy top-level per-interface IPs (distinct from netN). HasIPs() picks
	// these up alongside the parsed netN values. ipNs >= ip4 are silently
	// dropped; no field exists beyond IP3.
	IP0 string `json:"ip0,omitempty"`
	IP1 string `json:"ip1,omitempty"`
	IP2 string `json:"ip2,omitempty"`
	IP3 string `json:"ip3,omitempty"`
	// Fallback when Proxmox returns stringly typed nets
	raw map[string]interface{}
}

// LXCInterface reports live IPs from /nodes/{node}/lxc/{vmid}/interfaces
type LXCInterface struct {
	Name   string       `json:"name"`
	IP     string       `json:"ip"`
	IPs    []LXCIPEntry `json:"ip-addresses,omitempty"`
	HWAddr string       `json:"hwaddr"`
	Type   string       `json:"type"`
}

// LXCIPEntry is one address on an LXC interface as reported by PVE.
//
// Note: Prefix=0 is reachable on three paths:
//  1. PVE omits the field for an interface without an address (legitimate).
//  2. PVE sends null (legitimate, same intent).
//  3. (unreachable unless PVE bugs) a successful decode of a real zero.
//
// Callers that depend on a meaningful CIDR MUST validate Prefix > 0 before
// using it. The current consumer in cmd/pv/main.go runs every value through
// net.ParseCIDR and skips on error, so 0-prefix entries are silently
// dropped — but a future caller MUST NOT assume Prefix is meaningful
// without an explicit check.
type LXCIPEntry struct {
	IPAddress string `json:"ip-address"`
	Prefix    int    `json:"prefix"`
	Type      string `json:"type"`
}

// UnmarshalJSON tolerates PVE clusters that ship `prefix` as either a JSON
// number (24) OR a JSON string ("24"). Some 7.x / 8.x clusters have shipped
// both shapes depending on the endpoint version, and the prior strict-int
// decode failed the entire response — surfacing as a "cannot unmarshal
// string into Go struct field" diagnostic.
//
// Decimal strings ("24.0") are rejected with an error rather than
// silently coerced; CIDR prefix is an integer by definition and a
// "helpful" float-coercion would silently corrupt subnet math.
//
// Strategy: shadow-decode into a RawMessage, then try number, then string.
// Pinned by TestUnmarshalLXCIPEntry_* in cmd/pv/main_test.go.
func (e *LXCIPEntry) UnmarshalJSON(data []byte) error {
	type rawEntry struct {
		IPAddress string          `json:"ip-address"`
		Prefix    json.RawMessage `json:"prefix"`
		Type      string          `json:"type"`
	}
	var raw rawEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	e.IPAddress = raw.IPAddress
	e.Type = raw.Type
	// Missing/null prefix is legitimate (e.g. interfaces without an address).
	// Prefix stays at its zero value (0) and the consumer ignores such entries.
	if len(raw.Prefix) == 0 || string(raw.Prefix) == "null" {
		return nil
	}
	var n int
	if err := json.Unmarshal(raw.Prefix, &n); err == nil {
		e.Prefix = n
		return nil
	}
	var s string
	if err := json.Unmarshal(raw.Prefix, &s); err == nil {
		v, atoiErr := strconv.Atoi(s)
		if atoiErr != nil {
			return fmt.Errorf("prefix: cannot parse %q as int (from %s): %w", s, string(raw.Prefix), atoiErr)
		}
		e.Prefix = v
		return nil
	}
	return fmt.Errorf("prefix: cannot decode %s as int or string", string(raw.Prefix))
}

// LXCInterfacesResponse wraps /lxc/{vmid}/interfaces. The PVE LXC endpoint
// returns its array directly under the "data" envelope (NOT under "data.result"
// like the QEMU agent endpoint does). Using the wrong tag here silently yields
// an empty slice, which surfaces as "respuesta vacía" downstream even for
// running LXCs with IPs. Field name is kept `Data` to mirror ConfigResponse.
type LXCInterfacesResponse struct {
	Data []LXCInterface `json:"data"`
}

// ClusterResourcesResponse wraps /cluster/resources result
type ClusterResourcesResponse struct {
	Data []ClusterResource `json:"data"`
}

// ConfigResponse wraps a key->value config map.
type ConfigResponse struct {
	Data map[string]interface{} `json:"data"`
}

// Version is a minimal GitHub release descriptor used by the updater.
type Version struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

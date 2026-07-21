// Package i18n holds the Spanish/English string catalog for the pve CLI.
//
// The active language is chosen by the user via `pve config lang es|en` and
// persisted in config.json (Lang field). Until set, it defaults to English
// so the project ships in a language-neutral state for GitHub.
//
// Strings are grouped by a stable key (the English text). Both maps must
// contain the same keys; loadLang panics if a key is missing from the active
// map so a translator cannot silently drop a message.
package i18n

import (
	"fmt"
	"os"
	"strings"
)

// Lang is the two-letter language code currently active ("en" or "es").
// It is set once at startup by SetLang and read by T (translate) afterwards.
// Default is "en".
var Lang = "en"

// SetLang switches the active language. Unknown codes fall back to "en".
func SetLang(code string) {
	code = strings.ToLower(strings.TrimSpace(code))
	switch code {
	case "es", "en":
		Lang = code
	default:
		Lang = "en"
	}
}

// DetectSystemLang returns "es" if any of LANG/LC_ALL/LC_MESSAGES starts
// with "es", otherwise "en". Used as a one-time default when the user has
// not explicitly chosen a language.
func DetectSystemLang() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); strings.HasPrefix(strings.ToLower(v), "es") {
			return "es"
		}
	}
	return "en"
}

// T returns the translated string for the given English key.
// Arguments are substituted via fmt.Sprintf-style formatting when provided.
// If the active language is "en" (or the key is missing for the active
// language), the English key itself is returned — so adding a new string
// is always safe even before translating it.
func T(key string, args ...interface{}) string {
	s := key
	if Lang == "es" {
		if v, ok := es[key]; ok {
			s = v
		}
	} else if v, ok := en[key]; ok {
		s = v
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

// en is the authoritative catalog. Keys ARE the English strings, so the
// "en" map is technically redundant — but listing them here makes the full
// set of translatable strings discoverable and lets us detect missing keys
// at runtime (a key present in es but absent here is a bug).
var en = map[string]string{
	// usage / primer
	"usage.tagline":               "A minimal tool to inspect your LXC/VMs across one or more PVE nodes.",
	"usage.usage":                 "USAGE",
	"usage.examples":                "EXAMPLES",
	"usage.noargs":                "cluster overview (nodes + key resources)",
	"usage.ls":                    "list cluster LXCs (fast; --ips fills IPs, --all includes everything) · flags before text",
	"usage.get":                   "search a container by name/ip/vmid/hostname and show its full card (includes IP)",
	"usage.alias":                 `shorthand for "pve get"`,
	"usage.nodes":                 "PVE nodes info",
	"usage.ip":                    "print ONLY the container's IP (useful for scripts)",
	"usage.ssh":                   "SSH directly into the container (match by VMID or name)",
	"usage.exec":                  "run a command inside the LXC (default: ls -la /root/)",
	"usage.start":                 "start an LXC/VM",
	"usage.stop":                  "stop an LXC/VM",
	"usage.restart":               "restart an LXC/VM",
	"usage.top":                   "live top-like view of cluster resources",
	"usage.config":                "configure nodes, token, update repo, SSH user and SSH password",
	"usage.update":                "re-download the latest version without reinstalling",
	"usage.version":               "print the installed version",
	"usage.lang":                  "set the CLI language (es or en); persisted in config",
	"usage.config_header":         "CONFIGURATION",
	"usage.config_first":          `On first run invoke "pve config token '<PVEAPIToken=...>'" with your token,`,
	"usage.config_nodes":          "and \"pve config nodes <ipA>,<ipB>\" with the IPs of your PVE nodes.",
	"usage.config_persist":        "The token is persisted to ~/.config/pve/config.json with 0600 perms.",

	"primer.title":                "pve - Proxmox Viewer · not configured yet",
	"primer.first":                "FIRST RUN (two commands):",
	"primer.token_hint":           "(The token is created in PVE: Datacenter → Permissions → API Tokens.",
	"primer.persist":              " Config is persisted to ~/.config/pve/config.json with 0600 perms.)",
	"primer.once":                 "ONCE CONFIGURED, the commands are:",
	"primer.dashboard":            "cluster overview",
	"primer.ls":                   "list all LXC/VM",
	"primer.get":                  "search by name / VMID / IP",
	"primer.ip":                   "print ONLY the IP (great for scripts)",
	"primer.nodes":                "PVE nodes status",
	"primer.exec":                 "run a command inside the LXC (uses SSH to the PVE node)",
	"primer.config_show":          "show current config",
	"primer.update":               "auto-update",
	"primer.help":                 "full help",
	"primer.ssh_pw":               "If pve exec or pve ssh ask for a password, configure it (PVE node password, not the CT's):",

	// loadCFG
	"err.no_nodes":                "no nodes configured. Run:\n\n    pve config nodes <ip1>,<ip2>",
	"err.no_token":                "no token configured. Run:\n\n    pve config token 'PVEAPIToken=USER@REALM!TOKENID=<SECRET>'",

	// usage errors
	"err.usage_get":               "usage: pve get <text> [--node X] [--type lxc|qemu]",
	"err.usage_ip":                "usage: pve ip <text>",
	"err.usage_ssh":               "usage: pve ssh <vmid|name>",
	"err.usage_exec":              "usage: pve exec <name|ip|vmid> [command...]",
	"err.usage_action":            "usage: pve %s <name|ip|vmid>",
	"err.usage_top":               "usage: pve top [--sort cpu|mem] [--refresh N]",
	"err.usage_lang":              "usage: pve config lang es|en",

	// list flags
	"err.type_needs_value":        "--type needs a value (lxc|qemu|node|storage)",
	"err.node_needs_value":        "--node needs a value",
	"err.status_needs_value":      "--status needs a value",

	// get / ip / ssh / exec
	"msg.no_match_try_ls":         "Nothing found for %q. Try %s to see the full list.",
	"msg.matches_for":             "%d match(es) for %s",
	"err.no_match":                "no matches for %q",
	"err.no_lxc_match":            "no LXC matches %q (try `pve ls` to see names)",
	"err.no_match_try_ls":         "no matches for %q (try `pve ls` to see names)",
	"msg.stopped_ssh_warn":        "⚠ %s is stopped — SSH probably won't work until you start it",
	"msg.stopped_exec_warn":       "⚠ %s is stopped — pct exec probably won't work until you start it",
	"err.ip_resolve":              "could not resolve the IP of %s (vmid=%d) — try `pve ip %s` for details",
	"err.ssh_failed":              "ssh failed: %v",
	"err.exec_lxc_only":           "pve exec only works with LXC, but %q is a VM (type qemu). Use %s for direct SSH",

	// start/stop/restart
	"msg.action_done":             "✔ %s %s executed successfully",

	// top
	"msg.top_title":               "▌ pve top  ·  cluster resources",
	"msg.top_sorted_by":           "sorted by: %s  (Ctrl+C to exit)",
	"msg.top_footer":              "%s ready · %s running · sort: %s",

	// config
	"err.usage_config_token":      "usage: pve config token 'PVEAPIToken=USER@REALM!TOKENID=<SECRET>'",
	"err.usage_config_nodes":      "usage: pve config nodes <ip1>,<ip2>,...",
	"err.usage_config_repo":       "usage: pve config repo owner/name (for auto-updates)",
	"err.usage_config_ssh_user":   "usage: pve config ssh-user <user>",
	"err.base_url_empty":          "base_url is already empty",
	"err.unknown_config_action":   "unknown action %q (use: token|nodes|repo|base-url|ssh-user|ssh-password|lang|show)",
	"msg.token_saved":             "✔ token saved",
	"msg.nodes_saved":             "✔ %d nodes saved",
	"msg.repo_saved":              "✔ update repo: %s",
	"msg.base_url_cleared":        "✔ base_url cleared (pve update will fall back to repo if set)",
	"msg.base_url_saved":          "✔ update base_url: %s",
	"msg.base_url_note_repo":      "· note: repo=%s will be ignored while base_url is set",
	"msg.ssh_user_saved":          "✔ ssh_user saved: %s",
	"msg.ssh_pw_cleared":          "✔ ssh_password cleared (will be prompted interactively if no SSH keys)",
	"msg.ssh_pw_saved":            "✔ ssh_password saved (PVE node fallback, not the CT's)",
	"msg.lang_saved":              "✔ language set to %s",
	"msg.lang_current":            "✔ current language: %s",
	"msg.lang_detect":             "  (detected from your system)",

	// config show
	"msg.config_title":            "Current config",
	"msg.nodes_label":             "nodes:",
	"msg.token_label":             "token:",
	"msg.ssh_user_label":          "ssh_user:",
	"msg.ssh_pw_label":            "ssh_password:",
	"msg.base_url_label":          "base_url:",
	"msg.repo_label":              "repo:",
	"msg.lang_label":              "lang:",
	"msg.path_label":              "path:",
	"msg.empty_paren":             "(empty)",
	"msg.ssh_user_default":        "(default: uses the token user or 'root')",
	"msg.ssh_pw_note":             "note: ssh_password is for the PVE node, not the container",
	"msg.ssh_pw_empty_note":       "(empty — will be prompted interactively if no SSH keys)",
	"msg.base_url_empty_note":     "(empty — `pve update` will fall back to GitHub repo if set)",
	"msg.repo_ignored":            "(ignored because base_url takes precedence)",
	"msg.repo_active":             "(active: `pve update` will use it)",
	"msg.repo_empty_note":         "(empty — auto-updates won't work until you set base_url or repo)",
	"msg.lang_default":            "(default — detected from system)",

	// dashboard
	"msg.dash_title":              "▌ pve  ·  Proxmox Viewer",
	"msg.dash_subtitle":           "cluster status",
	"msg.dash_summary":            "Summary",
	"msg.dash_nodes":              "Nodes:",
	"msg.dash_lxc":                "LXC:",
	"msg.dash_vms":                "VMs:",
	"msg.dash_total":              "Total:",
	"msg.dash_resources":          "resources",
	"msg.dash_nodes_section":      "Nodes",
	"msg.dash_nodes_empty":        "(not detected by /cluster/resources)",
	"msg.dash_running":            "Running",
	"msg.dash_running_empty":      "(no LXC/VM running)",
	"msg.dash_quick_cmds":         "Quick commands:",

	// nodes
	"msg.nodes_title":             "PVE nodes",

	// ls
	"msg.label_containers":        "containers",
	"msg.label_resources":         "resources",
	"msg.label_vms":               "VMs",
	"msg.label_nodes":             "nodes",
	"msg.label_storage":           "storages",
	"msg.zero":                    "0 %s",
	"msg.no_matches":              "(no matches)",
	"msg.ls_hint":                 "Hint: pve ls --type qemu  for VMs  ·  pve ls --all  for the whole cluster",
	"msg.resolving_ips":           "⤓  resolving IPs for %d containers (parallel, 2s max each)…",
	"msg.missing_ips":             "· %d container(s) with no resolved IP",

	// printResource
	"msg.vmid_node_status":        "%s vmid %d · node %s · status %s",
	"msg.ip_cluster":              "IP (cluster/resources):",
	"msg.ip_label":                "IP:",
	"msg.ip_none":                 "--",
	"msg.problems":                "problems:",
	"msg.debug":                   "debug:",
	"msg.cpu_cores":               "CPU:  %.0f%%  (%d cores)",
	"msg.mem_of":                  "Mem:  %s / %s",
	"msg.uptime":                  "Up:   %s",

	// fetchIPs diagnostics
	"msg.diag_no_ip_cfg":          "no 'ip=' in net0..net3 (likely DHCP or no network)",
	"msg.diag_stopped":            "skipped (container is stopped)",
	"msg.diag_empty_live":         "empty response (no network or no guest agent?)",
	"msg.diag_ssh_api_errors":     "SSH skipped due to API errors (check token permissions: VM.Audit + VM.Monitor)",
	"msg.diag_cluster_empty":      "%s: r.IP empty and networks have no valid IPs",
	"msg.diag_no_ssh_keys":        "SSH: no SSH keys. Create one with `ssh-keygen -t ed25519` and copy it with `ssh-copy-id %s@<pve-ip>` (the IP is in `pve config show`)",
	"msg.diag_ssh_auth_copyid":    "SSH: auth failed (wrong password or cancelled). Copy your key with `ssh-copy-id %s@<pve-ip>` (the IP is in `pve config show`)",
	"msg.diag_ssh_auth_notauth":   "SSH auth failed — your key is not authorized on the PVE host. Run `ssh-copy-id %s@<pve-ip>` (the IP is in `pve config show`)",
	"msg.diag_ssh_exec_failed":    "SSH exec failed: %v",
	"msg.diag_no_ips_hostname":    "exec: hostname -I returned no IPs (container without network?)",
	"msg.diag_no_v4_hostname":     "exec: no valid v4 IPs in 'hostname -I' (output: %s)",

	// formatIPFailure
	"msg.ipfail_start":            "could not determine the IP of LXC %s (vmid=%d, node=%s).",
	"msg.ipfail_stopped":          "the container is stopped. PVE does not expose live interfaces for a stopped LXC, so either",
	"msg.ipfail_stopped_hint":     "start %s and run `pve ip %s` again. If you want a stable offline IP, configure a static `ip=<addr>` in net0 from PVE.",
	"msg.ipfail_proto":            "review your token (it needs VM.Audit for /config, VM.Monitor for /interfaces), or configure a static `ip=<addr>` in net0 from PVE.",
	"msg.ipfail_dhcp":             "the LXC responds but PVE has no IP for it (empty cluster + DHCP without lease). Configure a static `ip=<addr>` in net0 from PVE, or retry after DHCP assigns one with `pve ls --ips %s`.",

	// update
	"err.no_update_source":        "no update source configured. Set one:\n\n    pve config repo owner/name        (GitHub Releases)\n    pve config base-url http://host    (self-hosted)\n\nIf you just installed via curl, run install.sh again — it persists base_url.",
	"msg.checked_24h":             "(checked in the last 24h; use --force to bypass the cache)",
	"msg.up_to_date":              "✔ you are up to date (%s)",
	"msg.update_available":        "update available: %s (you have %s)",
	"msg.run_update":              "run `pve update` (without --check) to apply it.",
	"msg.updated_sha":             "✔ updated to %s (sha256:%s) — the new version will be used on the next invocation",
	"msg.updated":                 "✔ updated to %s — the new version will be used on the next invocation",
	"msg.auto_update_failed":      "✖ auto-update failed: %v",
	"msg.auto_updated":            "✔ updated to %s — the next invocation will use the new version",
	"err.usage_update":            "usage: pve update [--check] [--force]\n  --check    only compare, don't download or apply (bypasses the 24h cache)\n  --force    ignore the 24h cache and re-check now\nNo flags: applies the latest update if one is available.",
	"err.unknown_flag":            "unknown flag: %q",
	"err.url_empty":               "URL is empty",
	"err.url_invalid":             "invalid URL: %v",
	"err.url_no_host":             "URL has no host (valid example: https://example.com:8000)",
	"msg.source_local":            "source: local %s",
	"msg.source_github":           "source: GitHub %s",
}

// es is the Spanish translation. Keys MUST match en exactly.
var es = map[string]string{
	// usage / primer
	"usage.tagline":               "Una herramienta mínima para inspeccionar tus LXC/VM en uno o varios nodos PVE.",
	"usage.usage":                 "USO",
	"usage.examples":                "EJEMPLOS",
	"usage.noargs":                "vista resumida (nodos + recursos clave)",
	"usage.ls":                    "lista LXCs del cluster (rápido; --ips rellena IPs, --all incluye todo) · flags antes que el texto",
	"usage.get":                   "busca un contenedor por nombre/ip/vmid/hostname y muestra su ficha completa (incluye IP)",
	"usage.alias":                 `alias rápido de "pve get"`,
	"usage.nodes":                 "información de los nodos PVE",
	"usage.ip":                    "imprime SOLO la IP del contenedor (útil para scripts)",
	"usage.ssh":                   "SSH directo al contenedor (busca por VMID o nombre)",
	"usage.exec":                  "ejecuta un comando dentro del LXC (por defecto: ls -la /root/)",
	"usage.start":                 "enciende un LXC/VM",
	"usage.stop":                  "apaga un LXC/VM",
	"usage.restart":               "reinicia un LXC/VM",
	"usage.top":                   "vista tipo top de recursos en vivo",
	"usage.config":                "configura nodos, token, repo de updates, clave SSH y contraseña SSH",
	"usage.update":                "re-descarga la versión más reciente sin re-instalar",
	"usage.version":               "imprime la versión instalada",
	"usage.lang":                  "elige el idioma del CLI (es o en); se persiste en config",
	"usage.config_header":         "CONFIGURACIÓN",
	"usage.config_first":          `La primera vez invoca "pve config token '<PVEAPIToken=...>'" con tu token,`,
	"usage.config_nodes":          "y \"pve config nodes <ipA>,<ipB>\" con las IPs de tus nodos PVE.",
	"usage.config_persist":        "El token se persiste en ~/.config/pve/config.json con permisos 0600.",

	"primer.title":                "pve - Proxmox Viewer · aún sin configurar",
	"primer.first":                "PRIMER ARRANQUE (dos comandos):",
	"primer.token_hint":           "(El token se crea en PVE: Datacenter → Permisos → API Tokens.",
	"primer.persist":              " La config se persiste en ~/.config/pve/config.json con perms 0600.)",
	"primer.once":                 "UNA VEZ CONFIGURADO, los comandos son:",
	"primer.dashboard":            "vista resumida del cluster",
	"primer.ls":                   "lista todos los LXC/VM",
	"primer.get":                  "busca por nombre / VMID / IP",
	"primer.ip":                   "imprime SOLO la IP (ideal para scripts)",
	"primer.nodes":                "estado de los nodos PVE",
	"primer.exec":                 "ejecuta un comando dentro del LXC (usa SSH al nodo PVE)",
	"primer.config_show":          "ver la config actual",
	"primer.update":               "auto-actualización",
	"primer.help":                 "ayuda completa",
	"primer.ssh_pw":               "Si pve exec o pve ssh piden password, configurá (password del nodo PVE, no del CT):",

	// loadCFG
	"err.no_nodes":                "no hay nodos configurados. Ejecuta:\n\n    pve config nodes <ip1>,<ip2>",
	"err.no_token":                "no hay token configurado. Ejecuta:\n\n    pve config token 'PVEAPIToken=USER@REALM!TOKENID=<SECRET>'",

	// usage errors
	"err.usage_get":               "uso: pve get <texto> [--node X] [--type lxc|qemu]",
	"err.usage_ip":                "uso: pve ip <texto>",
	"err.usage_ssh":               "uso: pve ssh <vmid|nombre>",
	"err.usage_exec":              "uso: pve exec <nombre|ip|vmid> [comando...]",
	"err.usage_action":            "uso: pve %s <nombre|ip|vmid>",
	"err.usage_top":               "uso: pve top [--sort cpu|mem] [--refresh N]",
	"err.usage_lang":              "uso: pve config lang es|en",

	// list flags
	"err.type_needs_value":        "--type necesita valor (lxc|qemu|node|storage)",
	"err.node_needs_value":        "--node necesita valor",
	"err.status_needs_value":      "--status necesita valor",

	// get / ip / ssh / exec
	"msg.no_match_try_ls":         "No encontré nada para %q. Prueba %s para ver el listado completo.",
	"msg.matches_for":             "%d coincidencia(s) para %s",
	"err.no_match":                "sin coincidencias para %q",
	"err.no_lxc_match":            "ningún LXC coincide con %q (probaste `pve ls` para ver nombres?)",
	"err.no_match_try_ls":         "sin coincidencias para %q (probá `pve ls` para ver nombres)",
	"msg.stopped_ssh_warn":        "⚠ %s está stopped — SSH probablemente no funcione hasta que lo enciendas",
	"msg.stopped_exec_warn":       "⚠ %s está stopped — pct exec probablemente no funcione hasta que lo enciendas",
	"err.ip_resolve":              "no se pudo resolver la IP de %s (vmid=%d) — probá `pve ip %s` para más detalles",
	"err.ssh_failed":              "ssh falló: %v",
	"err.exec_lxc_only":           "pve exec solo funciona con LXC, pero %q es una VM (tipo qemu). Usá %s para SSH directo",

	// start/stop/restart
	"msg.action_done":             "✔ %s %s ejecutado correctamente",

	// top
	"msg.top_title":               "▌ pve top  ·  recursos del cluster",
	"msg.top_sorted_by":           "ordenado por: %s  (Ctrl+C para salir)",
	"msg.top_footer":              "%s listos · %s corriendo · orden: %s",

	// config
	"err.usage_config_token":      "uso: pve config token 'PVEAPIToken=USER@REALM!TOKENID=<SECRET>'",
	"err.usage_config_nodes":      "uso: pve config nodes <ip1>,<ip2>,...",
	"err.usage_config_repo":       "uso: pve config repo owner/name (para auto-updates)",
	"err.usage_config_ssh_user":   "uso: pve config ssh-user <usuario>",
	"err.base_url_empty":          "base_url ya está vacío",
	"err.unknown_config_action":   "acción desconocida %q (usa: token|nodes|repo|base-url|ssh-user|ssh-password|lang|show)",
	"msg.token_saved":             "✔ token guardado",
	"msg.nodes_saved":             "✔ %d nodos guardados",
	"msg.repo_saved":              "✔ repo de updates: %s",
	"msg.base_url_cleared":        "✔ base_url eliminado (pve update recurrirá a repo si está definido)",
	"msg.base_url_saved":          "✔ base_url de updates: %s",
	"msg.base_url_note_repo":      "· nota: repo=%s será ignorado mientras base_url esté definido",
	"msg.ssh_user_saved":          "✔ ssh_user guardado: %s",
	"msg.ssh_pw_cleared":          "✔ ssh_password eliminado (se pedirá interactivamente si no hay claves SSH)",
	"msg.ssh_pw_saved":            "✔ ssh_password guardado (fallback del nodo PVE, no del CT)",
	"msg.lang_saved":              "✔ idioma guardado: %s",
	"msg.lang_current":            "✔ idioma actual: %s",
	"msg.lang_detect":             "  (detectado de tu sistema)",

	// config show
	"msg.config_title":            "Config actual",
	"msg.nodes_label":             "nodos:",
	"msg.token_label":             "token:",
	"msg.ssh_user_label":          "ssh_user:",
	"msg.ssh_pw_label":            "ssh_password:",
	"msg.base_url_label":          "base_url:",
	"msg.repo_label":              "repo:",
	"msg.lang_label":              "lang:",
	"msg.path_label":              "ruta:",
	"msg.empty_paren":             "(vacío)",
	"msg.ssh_user_default":        "(por defecto, usa el usuario del token o 'root')",
	"msg.ssh_pw_note":             "nota: ssh_password es del nodo PVE, no del contenedor",
	"msg.ssh_pw_empty_note":       "(vacío — se pedirá interactivamente si no hay claves SSH)",
	"msg.base_url_empty_note":     "(vacío — `pve update` recurrirá a repo de GitHub si está definido)",
	"msg.repo_ignored":            "(ignorado porque base_url tiene precedencia)",
	"msg.repo_active":             "(activo: `pve update` lo usará)",
	"msg.repo_empty_note":         "(vacío — los auto-updates no funcionarán hasta definir base_url o repo)",
	"msg.lang_default":            "(por defecto — detectado del sistema)",

	// dashboard
	"msg.dash_title":              "▌ pve  ·  Proxmox Viewer",
	"msg.dash_subtitle":           "estado del cluster",
	"msg.dash_summary":            "Resumen",
	"msg.dash_nodes":              "Nodos:",
	"msg.dash_lxc":                "LXC:",
	"msg.dash_vms":                "VMs:",
	"msg.dash_total":              "Total:",
	"msg.dash_resources":          "recursos",
	"msg.dash_nodes_section":      "Nodos",
	"msg.dash_nodes_empty":        "(no detectados por /cluster/resources)",
	"msg.dash_running":            "En ejecución",
	"msg.dash_running_empty":      "(ningún LXC/VM corriendo)",
	"msg.dash_quick_cmds":         "Comandos rápidos:",

	// nodes
	"msg.nodes_title":             "Nodos PVE",

	// ls
	"msg.label_containers":        "contenedores",
	"msg.label_resources":         "recursos",
	"msg.label_vms":               "VMs",
	"msg.label_nodes":             "nodos",
	"msg.label_storage":           "almacenes",
	"msg.zero":                    "0 %s",
	"msg.no_matches":              "(sin coincidencias)",
	"msg.ls_hint":                 "Pista: pve ls --type qemu  para VMs  ·  pve ls --all  para todo el cluster",
	"msg.resolving_ips":           "⤓  resolviendo IPs de %d contenedores (paralelo, máx 2s cada uno)…",
	"msg.missing_ips":             "· %d contenedor(es) sin IP resuelta",

	// printResource
	"msg.vmid_node_status":        "%s vmid %d · nodo %s · estado %s",
	"msg.ip_cluster":              "IP (cluster/resources):",
	"msg.ip_label":                "IP:",
	"msg.ip_none":                 "--",
	"msg.problems":                "problemas:",
	"msg.debug":                   "debug:",
	"msg.cpu_cores":               "CPU:  %.0f%%  (%d cores)",
	"msg.mem_of":                  "Mem:  %s / %s",
	"msg.uptime":                  "Up:   %s",

	// fetchIPs diagnostics
	"msg.diag_no_ip_cfg":          "sin 'ip=' en net0..net3 (probable DHCP o sin red)",
	"msg.diag_stopped":            "omitido (contenedor apagado)",
	"msg.diag_empty_live":         "respuesta vacía (¿sin red o sin agente invitado?)",
	"msg.diag_ssh_api_errors":     "SSH omitido por errores de API (revisá permisos del token: VM.Audit + VM.Monitor)",
	"msg.diag_cluster_empty":      "%s: r.IP vacío y networks sin IPs válidas",
	"msg.diag_no_ssh_keys":        "SSH: sin claves SSH. Creá una con `ssh-keygen -t ed25519` y copiala con `ssh-copy-id %s@<ip-pve>` (la IP está en `pve config show`)",
	"msg.diag_ssh_auth_copyid":    "SSH: auth falló (password incorrecto o cancelado). Copiá tu clave con `ssh-copy-id %s@<ip-pve>` (la IP está en `pve config show`)",
	"msg.diag_ssh_auth_notauth":   "SSH auth falló — tu clave no está autorizada en el host PVE. Ejecutá `ssh-copy-id %s@<ip-pve>` (la IP está en `pve config show`)",
	"msg.diag_ssh_exec_failed":    "exec vía SSH falló: %v",
	"msg.diag_no_ips_hostname":    "exec: hostname -I no devolvió IPs (¿contenedor sin red?)",
	"msg.diag_no_v4_hostname":     "exec: sin IPs v4 válidas en 'hostname -I' (output: %s)",

	// formatIPFailure
	"msg.ipfail_start":            "no se pudo determinar la IP del LXC %s (vmid=%d, nodo=%s).",
	"msg.ipfail_stopped":          "el contenedor está apagado. PVE no expone interfaces en vivo para un LXC apagado, así que",
	"msg.ipfail_stopped_hint":     "enciende %s y vuelve a ejecutar `pve ip %s`. Si quieres una IP estable offline, configura `ip=<dir>` estática en net0 desde PVE.",
	"msg.ipfail_proto":            "revisa el token (necesita VM.Audit para /config, VM.Monitor para /interfaces), o configura `ip=<dir>` estática en net0 desde PVE.",
	"msg.ipfail_dhcp":             "el LXC responde pero PVE no trae IP (cluster vacío + DHCP sin lease). Configura `ip=<dir>` estática en net0 desde PVE, o reintentá después de que DHCP asigne con `pve ls --ips %s`.",

	// update
	"err.no_update_source":        "no hay fuente de actualizaciones configurada. Define una:\n\n    pve config repo owner/name        (GitHub Releases)\n    pve config base-url http://host    (self-hosted)\n\nSi acabas de instalar vía curl, vuelve a ejecutar install.sh — guarda base_url.",
	"msg.checked_24h":             "(ya comprobado en las últimas 24h; usa --force para ignorar la caché)",
	"msg.up_to_date":              "✔ estás al día (%s)",
	"msg.update_available":        "actualizado disponible: %s (tienes %s)",
	"msg.run_update":              "ejecuta `pve update` (sin --check) para aplicarlo.",
	"msg.updated_sha":             "✔ actualizado a %s (sha256:%s) — la nueva versión se usará en la próxima invocación",
	"msg.updated":                 "✔ actualizado a %s — la nueva versión se usará en la próxima invocación",
	"msg.auto_update_failed":      "✖ auto-update falló: %v",
	"msg.auto_updated":            "✔ actualizado a %s — la próxima invocación usará la nueva versión",
	"err.usage_update":            "uso: pve update [--check] [--force]\n  --check    sólo compara, no descarga ni aplica (omite el caché de 24h)\n  --force    ignora el caché de 24h y re-compara ahora\nSin flags: aplica la actualización más reciente si la hay.",
	"err.unknown_flag":            "flag desconocida: %q",
	"err.url_empty":               "URL vacía",
	"err.url_invalid":             "URL inválida: %v",
	"err.url_no_host":             "URL sin host (ejemplo válido: https://ejemplo.com:8000)",
	"msg.source_local":            "fuente: local %s",
	"msg.source_github":           "fuente: GitHub %s",
}

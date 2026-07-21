package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/h4sht/pve/internal/config"
	"github.com/h4sht/pve/internal/i18n"
	"github.com/h4sht/pve/internal/proxmox"
	"github.com/h4sht/pve/internal/update"
)

func usageText() string {
	return fmt.Sprintf(`pve - Proxmox Viewer (CLI)

%s

USAGE
  pve                              ← %s
  pve ls [--ips|--all] [QUERY]    ← %s
  pve get <text>                  ← %s
  pve <texto>                      ← %s
  pve nodes                        ← %s
  pve ip <texto>                   ← %s
  pve ssh <vmid|name>            ← %s
  pve exec <id> [command...]        ← %s
  pve start <id>                    ← %s
  pve stop <id>                     ← %s
  pve restart <id>                 ← %s
  pve top [--sort cpu|mem] [--refresh N]  ← %s
  pve config [token|nodes|repo|ssh-user|ssh-password|lang|show] ← %s
  pve update [--check] [--force]  ← %s
  pve version                      ← %s

EXAMPLES
  pve nginx
  pve get mysql
  pve ip postgres
  pve ls --ips
  pve ls --type lxc --node pve1
  pve exec 105                    ← ls -la /root/ of LXC 105
  pve exec postgres ls /etc
  pve start 105
  pve restart nginx
  pve top --sort mem --refresh 5
  pve update --check               ← %s
  pve config token 'PVEAPIToken=USER@pam!pve-cli=<SECRET>'
  pve config nodes 10.0.0.10,10.0.0.11
  pve config ssh-user root
  pve config ssh-password <PASSWORD>
  pve config lang es               ← %s
  pve config base-url http://your-server:8000

%s
  %s
  %s
  %s
`,
		i18n.T("usage.tagline"),
		i18n.T("usage.noargs"),
		i18n.T("usage.ls"),
		i18n.T("usage.get"),
		i18n.T("usage.alias"),
		i18n.T("usage.nodes"),
		i18n.T("usage.ip"),
		i18n.T("usage.ssh"),
		i18n.T("usage.exec"),
		i18n.T("usage.start"),
		i18n.T("usage.stop"),
		i18n.T("usage.restart"),
		i18n.T("usage.top"),
		i18n.T("usage.config"),
		i18n.T("usage.update"),
		i18n.T("usage.version"),
		i18n.T("usage.update"),
		i18n.T("usage.lang"),
		i18n.T("usage.config_header"),
		i18n.T("usage.config_first"),
		i18n.T("usage.config_nodes"),
		i18n.T("usage.config_persist"),
	)
}

func primerText() string {
	return fmt.Sprintf(`pve - Proxmox Viewer · %s

%s
  pve config nodes 10.0.0.10,10.0.0.11
  pve config token 'PVEAPIToken=USER@pam!pve-cli=<SECRET>'

  %s%s

%s
  pve               ← %s
  pve ls            ← %s
  pve get <text>   ← %s
  pve ip <texto>    ← %s
  pve nodes         ← %s
  pve exec <id>     ← %s
  pve config show   ← %s
  pve update        ← %s
  pve --help        ← %s

%s
  pve config ssh-user root
  pve config ssh-password <PASSWORD>
`,
		i18n.T("primer.title"),
		i18n.T("primer.first"),
		i18n.T("primer.token_hint"),
		i18n.T("primer.persist"),
		i18n.T("primer.once"),
		i18n.T("primer.dashboard"),
		i18n.T("primer.ls"),
		i18n.T("primer.get"),
		i18n.T("primer.ip"),
		i18n.T("primer.nodes"),
		i18n.T("primer.exec"),
		i18n.T("primer.config_show"),
		i18n.T("primer.update"),
		i18n.T("primer.help"),
		i18n.T("primer.ssh_pw"),
	)
}

var (
	headerFmt = color.New(color.FgCyan, color.Bold)
	accentFmt = color.New(color.FgHiMagenta, color.Bold)
	okFmt     = color.New(color.FgHiGreen)
	warnFmt   = color.New(color.FgHiYellow)
	errFmt    = color.New(color.FgHiRed, color.Bold)
	mutedFmt  = color.New(color.FgHiBlack)
	highlight = color.New(color.FgHiCyan, color.Bold)
)

func main() {
	// Initialize i18n from persisted config (falls back to system locale).
	if cfg, _ := config.Load(); cfg != nil && cfg.Lang != "" {
		i18n.SetLang(cfg.Lang)
	} else {
		i18n.SetLang(i18n.DetectSystemLang())
	}

	if len(os.Args) < 2 {
		// No subcommand: primer if unconfigured, dashboard if configured.
		cfg, _ := config.Load()
		if cfg == nil || len(cfg.Nodes) == 0 || cfg.Token == "" {
			fmt.Fprintln(os.Stderr)
			fmt.Fprint(os.Stderr, primerText())
			fmt.Fprintln(os.Stderr)
			return
		}
		if err := runDashboard(); err != nil {
			printErr(err)
		}
		go tryAutoUpdate()
		return
	}
	if onlyHelp(os.Args[1:]) {
		fmt.Print(usageText())
		return
	}

	cmd := os.Args[1]
	args := os.Args[2:]
	go tryAutoUpdate()

	switch cmd {
	case "version", "--version", "-v":
		fmt.Println(update.CurrentVersion)
	case "update":
		if err := runUpdate(args); err != nil {
			printErr(err)
			os.Exit(1)
		}
	case "config":
		if err := runConfig(args); err != nil {
			printErr(err)
			os.Exit(1)
		}
	case "lang":
		if err := runLang(args); err != nil {
			printErr(err)
			os.Exit(1)
		}
	case "ls", "list":
		if err := runList(args); err != nil {
			printErr(err)
			os.Exit(1)
		}
	case "nodes":
		if err := runNodes(); err != nil {
			printErr(err)
			os.Exit(1)
		}
	case "get":
		if len(args) < 1 {
			printErr(errors.New(i18n.T("err.usage_get")))
			os.Exit(1)
		}
		query := strings.Join(args, " ")
		if err := runGet(query); err != nil {
			printErr(err)
			os.Exit(1)
		}
	case "ip":
		if len(args) < 1 {
			printErr(errors.New(i18n.T("err.usage_ip")))
			os.Exit(1)
		}
		if err := runIP(strings.Join(args, " ")); err != nil {
			printErr(err)
			os.Exit(1)
		}
	case "ssh":
		if len(args) < 1 {
			printErr(errors.New(i18n.T("err.usage_ssh")))
			os.Exit(1)
		}
		if err := runSSH(strings.Join(args, " ")); err != nil {
			printErr(err)
			os.Exit(1)
		}
	case "exec":
		if len(args) < 1 {
			printErr(errors.New(i18n.T("err.usage_exec")))
			os.Exit(1)
		}
		query := args[0]
		cmd := args[1:]
		if err := runExec(query, cmd); err != nil {
			printErr(err)
			os.Exit(1)
		}
	case "start", "stop", "restart":
		if len(args) < 1 {
			printErr(fmt.Errorf("%s", i18n.T("err.usage_action", cmd)))
			os.Exit(1)
		}
		if err := runStartStopRestart(cmd, strings.Join(args, " ")); err != nil {
			printErr(err)
			os.Exit(1)
		}
	case "top":
		if err := runTop(args); err != nil {
			printErr(err)
			os.Exit(1)
		}
	default:
		query := strings.Join(os.Args[1:], " ")
		if err := runGet(query); err != nil {
			printErr(err)
			os.Exit(1)
		}
	}
}

func onlyHelp(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "help" {
			return true
		}
	}
	return false
}

// --- config loader -------------------------------------------------------

func loadCFG() (*config.Config, error) {
	c, err := config.Load()
	if err != nil {
		return nil, err
	}
	if len(c.Nodes) == 0 {
		return nil, errors.New(i18n.T("err.no_nodes"))
	}
	if c.Token == "" {
		return nil, errors.New(i18n.T("err.no_token"))
	}
	return c, proxmox.ValidateToken(redirectToken(c.Token))
}

func redirectToken(t string) string {
	if strings.HasPrefix(t, "PVEAPIToken=") {
		return t
	}
	return "PVEAPIToken=" + t
}

func newClient(c *config.Config) *proxmox.Client {
	cli := proxmox.NewClient(c.Nodes, redirectToken(c.Token))
	cli.SSHUser = c.SSHUser
	cli.SSHPassword = c.SSHPassword
	return cli
}

// --- dashboard -----------------------------------------------------------

func runDashboard() error {
	cfg, err := loadCFG()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cli := newClient(cfg)
	resources, err := cli.GetClusterResources(ctx)
	if err != nil {
		return err
	}

	nodes := proxmoxByType(resources, proxmox.ResourceNode)
	lxcs := proxmoxByType(resources, proxmox.ResourceLXC)
	vms := proxmoxByType(resources, proxmox.ResourceQemu)

	runningLXC, stoppedLXC := splitByStatus(lxcs)
	runningVM, stoppedVM := splitByStatus(vms)

	// Header
	fmt.Println()
	headerFmt.Println("  ╔══════════════════════════════════════════════════════════════╗")
	headerFmt.Println("  ║  ▌ pve  ·  Proxmox Viewer                                    ║")
	headerFmt.Printf("  ║  %-60s║\n", i18n.T("msg.dash_subtitle"))
	headerFmt.Println("  ╚══════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Summary cards
	headerFmt.Printf("  ┌─ %s ─────────────────────────────────────────────────┐\n", i18n.T("msg.dash_summary"))
	fmt.Printf("  │  %s  %-10s %s\n", okFmt.Sprint("●"), i18n.T("msg.dash_nodes"), highlight.Sprint(fmt.Sprintf("%d", len(nodes))))
	fmt.Printf("  │  %s  %-10s %s running · %s stopped\n", okFmt.Sprint("▣"), i18n.T("msg.dash_lxc")+":", okFmt.Sprint(fmt.Sprintf("%d", len(runningLXC))), errFmt.Sprint(fmt.Sprintf("%d", len(stoppedLXC))))
	fmt.Printf("  │  %s  %-10s %s running · %s stopped\n", okFmt.Sprint("▢"), i18n.T("msg.dash_vms")+":", okFmt.Sprint(fmt.Sprintf("%d", len(runningVM))), errFmt.Sprint(fmt.Sprintf("%d", len(stoppedVM))))
	fmt.Printf("  │  %s  %-10s %s %s\n", mutedFmt.Sprint("◈"), i18n.T("msg.dash_total"), highlight.Sprint(fmt.Sprintf("%d", len(resources))), i18n.T("msg.dash_resources"))
	headerFmt.Println("  └─────────────────────────────────────────────────────────────┘")
	fmt.Println()

	// Nodes section
	headerFmt.Printf("  ┌─ %s (%d) ────────────────────────────────────────────────┐\n", i18n.T("msg.dash_nodes_section"), len(nodes))
	if len(nodes) == 0 {
		mutedFmt.Printf("  │  %s\n", i18n.T("msg.dash_nodes_empty"))
		for _, addr := range cfg.Nodes {
			v, verr := cli.GetVersion(ctx, addr)
			if verr != nil {
				fmt.Printf("  │  %s  %-15s  %s\n", errFmt.Sprint("○"), addr, verr)
				continue
			}
			fmt.Printf("  │  %s  %-15s  pve %s\n", okFmt.Sprint("●"), addr, v)
		}
	} else {
		for _, n := range nodes {
			statusIcon := okFmt.Sprint("●")
			if n.Status != "online" {
				statusIcon = errFmt.Sprint("○")
			}
			fmt.Printf("  │  %s  %-14s  cpu %s/%d  mem %s/%s  up %s\n",
				statusIcon,
				color.HiMagentaString(strings.TrimSpace(n.Name)),
				mutedFmt.Sprint(fmtCPU(n.CPU)),
				n.MaxCPU,
				fmtBytes(n.Mem), fmtBytes(n.MaxMem),
				fmtDuration(time.Duration(n.Uptime)*time.Second),
			)
		}
	}
	headerFmt.Println("  └─────────────────────────────────────────────────────────────┘")
	fmt.Println()

	// Running resources side-by-side
	headerFmt.Printf("  ┌─ %s ──────────────────────────────────────────────┐\n", i18n.T("msg.dash_running"))
	if len(runningLXC) == 0 && len(runningVM) == 0 {
		mutedFmt.Printf("  │  %s\n", i18n.T("msg.dash_running_empty"))
	} else {
		fmt.Printf("  │  LXC: %s  ·  VMs: %s\n", okFmt.Sprint(fmt.Sprintf("%d", len(runningLXC))), okFmt.Sprint(fmt.Sprintf("%d", len(runningVM))))
		rows := [][]string{}
		for _, r := range append(runningLXC, runningVM...) {
			rows = append(rows, []string{
				coloredName(r.Name),
				r.Node,
				string(r.Type),
				fmtCPU(r.CPU),
				fmtBytes(r.Mem),
				fmt.Sprintf("%d", r.VMID),
			})
		}
		printTableSimple([]string{"NAME", "NODE", "TYPE", "CPU", "MEM", "VMID"}, rows)
	}
	headerFmt.Println("  └─────────────────────────────────────────────────────────────┘")
	fmt.Println()

	// Quick tips
	mutedFmt.Printf("  %s  %s  %s  %s  %s  %s\n\n",
		i18n.T("msg.dash_quick_cmds"),
		accentFmt.Sprint("pve ls"),
		accentFmt.Sprint("pve top"),
		accentFmt.Sprint("pve get <nombre>"),
		accentFmt.Sprint("pve exec <id>"),
		accentFmt.Sprint("pve start|stop|restart <id>"))
	return nil
}

func splitByStatus(rs []proxmox.ClusterResource) (running, stopped []proxmox.ClusterResource) {
	for _, r := range rs {
		if r.Status == "running" {
			running = append(running, r)
		} else {
			stopped = append(stopped, r)
		}
	}
	return
}

// --- nodes ---------------------------------------------------------------

func runNodes() error {
	cfg, err := loadCFG()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cli := newClient(cfg)
	resources, rerr := cli.GetClusterResources(ctx)
	if rerr != nil {
		return rerr
	}
	nodes := proxmoxByType(resources, proxmox.ResourceNode)

	headerFmt.Printf("\n  %s\n", i18n.T("msg.nodes_title"))
	if len(nodes) == 0 {
		for _, addr := range cfg.Nodes {
			v, verr := cli.GetVersion(ctx, addr)
			if verr != nil {
				fmt.Printf("  %s  %-15s  %s\n", errFmt.Sprint("○"), addr, verr)
				continue
			}
			fmt.Printf("  %s  %-15s  pve %s\n", okFmt.Sprint("●"), addr, v)
		}
		fmt.Println()
		return nil
	}
	for _, n := range nodes {
		statusIcon := okFmt.Sprint("●")
		if n.Status != "online" {
			statusIcon = errFmt.Sprint("○")
		}
		fmt.Printf("  %s  %-14s  cpu %s/%d  mem %s/%s  up %s\n",
			statusIcon,
			color.HiMagentaString(strings.TrimSpace(n.Name)),
			mutedFmt.Sprint(fmtCPU(n.CPU)),
			n.MaxCPU,
			fmtBytes(n.Mem), fmtBytes(n.MaxMem),
			fmtDuration(time.Duration(n.Uptime)*time.Second),
		)
	}
	fmt.Println()
	return nil
}

// --- ls ------------------------------------------------------------------

func runList(args []string) error {
	showAll, typeFilter, nodeFilter, statusFilter, query, wantIPs, err := parseListFlags(args)
	if err != nil {
		return err
	}
	cfg, lerr := loadCFG()
	if lerr != nil {
		return lerr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cli := newClient(cfg)
	resources, rerr := cli.GetClusterResources(ctx)
	if rerr != nil {
		return rerr
	}

	// 1) filter by flags + optional fuzzy query
	filtered := []proxmox.ClusterResource{}
	q := strings.ToLower(query)
	for _, r := range resources {
		if typeFilter != "" && string(r.Type) != typeFilter {
			continue
		}
		if nodeFilter != "" && r.Node != nodeFilter {
			continue
		}
		if statusFilter != "" && r.Status != statusFilter {
			continue
		}
		if query != "" {
			if typeFilter == "" &&
				r.Type != proxmox.ResourceLXC && r.Type != proxmox.ResourceQemu {
				continue
			}
			if matchScore(r, q) <= 0 {
				continue
			}
		}
		filtered = append(filtered, r)
	}

	// 2) default = LXC only (unless --all or --type was specified)
	if typeFilter == "" && !showAll {
		lxcsOnly := filtered[:0]
		for _, r := range filtered {
			if r.Type == proxmox.ResourceLXC {
				lxcsOnly = append(lxcsOnly, r)
			}
		}
		filtered = lxcsOnly
	}

	// 3) sort alphabetical
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Name < filtered[j].Name })

	label := i18n.T("msg.label_containers")
	switch {
	case showAll:
		label = i18n.T("msg.label_resources")
	case typeFilter == "qemu":
		label = i18n.T("msg.label_vms")
	case typeFilter == "node":
		label = i18n.T("msg.label_nodes")
	case typeFilter == "storage":
		label = i18n.T("msg.label_storage")
	case typeFilter == "lxc":
		label = i18n.T("msg.label_containers")
	case typeFilter != "":
		label = typeFilter
	}
	if len(filtered) == 0 {
		headerFmt.Printf("\n  %s\n", i18n.T("msg.zero", label))
		mutedFmt.Printf("  %s\n", i18n.T("msg.no_matches"))
		if query == "" {
			mutedFmt.Printf("  %s\n", i18n.T("msg.ls_hint"))
		}
		return nil
	}

	// 4) optional IP enrichment (only when --ips is set)
	var ips map[int]string
	if wantIPs {
		lxcCount := 0
		for _, r := range filtered {
			if r.Type == proxmox.ResourceLXC {
				lxcCount++
			}
		}
		if lxcCount > 0 {
			fmt.Fprintf(os.Stderr, "  %s\n", i18n.T("msg.resolving_ips", lxcCount))
			ips = resolveLXCIPs(ctx, cli, filtered)
		}
	}

	// 5) Layout: two stacked blocks (running, then stopped) for default LXC view;
	//    single-column table for everything else (--all, --type, queries).
	useSplit := !showAll && typeFilter == "" && query == "" && nodeFilter == "" && statusFilter == ""
	if useSplit {
		// Split into running vs not-running, alphabetical within each.
		var running, stopped []proxmox.ClusterResource
		for _, r := range filtered {
			if r.Status == "running" {
				running = append(running, r)
			} else {
				stopped = append(stopped, r)
			}
		}

		// Build compact rows: NAME (colored), IP, VMID.
		buildRows := func(rs []proxmox.ClusterResource, nameFn func(string) string) [][]string {
			rows := make([][]string, 0, len(rs))
			for _, r := range rs {
				ip := r.IP
				if ip == "" {
					if v, ok := ips[r.VMID]; ok {
						ip = v
					} else {
						ip = extractIPFromTags(r.Tags)
					}
				}
				ipCell := nonEmpty(ip, mutedFmt.Sprint("—"))
				if ip != "" && ip != r.IP {
					ipCell = okFmt.Sprint(ip)
				}
				rows = append(rows, []string{
					nameFn(r.Name),
					ipCell,
					fmt.Sprintf("%d", r.VMID),
				})
			}
			return rows
		}

		leftRows := buildRows(running, coloredName)
		rightRows := buildRows(stopped, func(n string) string { return errFmt.Sprint(n) })

		fmt.Println()
		headerFmt.Printf("  ● running (%d)                             ○ stopped (%d)\n", len(running), len(stopped))
		printTableSideBySide(
			[]string{headerFmt.Sprint("NAME"), headerFmt.Sprint("IP"), headerFmt.Sprint("VMID")},
			[]string{headerFmt.Sprint("NAME"), headerFmt.Sprint("IP"), headerFmt.Sprint("VMID")},
			leftRows, rightRows,
		)
	} else {
		headerFmt.Printf("\n  %d %s\n", len(filtered), label)

		includeType := showAll || (typeFilter != "" && typeFilter != "lxc")
		headers := []string{"NAME", "NODE", "STATUS", "IP", "VMID"}
		if includeType {
			headers = []string{"NAME", "NODE", "TYPE", "STATUS", "IP", "VMID"}
		}
		// Sort running first for single-column
		sort.Slice(filtered, func(i, j int) bool {
			a, b := filtered[i].Status, filtered[j].Status
			if a == "running" && b != "running" {
				return true
			}
			if b == "running" && a != "running" {
				return false
			}
			return filtered[i].Name < filtered[j].Name
		})
		rows := [][]string{}
		for _, r := range filtered {
			ip := r.IP
			if v, ok := ips[r.VMID]; ok {
				ip = v
			}
			if ip == "" {
				ip = extractIPFromTags(r.Tags)
			}
			ipCell := nonEmpty(ip, mutedFmt.Sprint("—"))
			if ip != "" && ip != r.IP {
				ipCell = okFmt.Sprint(ip)
			}
			row := []string{
				coloredName(r.Name),
				r.Node,
				colorStatus(r.Status),
				ipCell,
				fmt.Sprintf("%d", r.VMID),
			}
			if includeType {
				row = append([]string{row[0], row[1], string(r.Type)}, row[2:]...)
			}
			rows = append(rows, row)
		}
		printTableSimple(headers, rows)
		if wantIPs && ips != nil {
			missing := 0
			for _, r := range filtered {
				if r.Type == proxmox.ResourceLXC && ips[r.VMID] == "" && r.IP == "" {
					missing++
				}
			}
			if missing > 0 {
				mutedFmt.Printf("  %s\n", i18n.T("msg.missing_ips", missing))
			}
		}
	}
	fmt.Println()
	return nil
}

func parseListFlags(args []string) (showAll bool, typeFilter, nodeFilter, statusFilter, query string, wantIPs bool, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--all", "-a":
			showAll = true
		case "--ips", "-i":
			wantIPs = true
		case "--type", "-t":
			if i+1 >= len(args) {
				return false, "", "", "", "", false, errors.New(i18n.T("err.type_needs_value"))
			}
			typeFilter = args[i+1]
			i++
		case "--node", "-n":
			if i+1 >= len(args) {
				return false, "", "", "", "", false, errors.New(i18n.T("err.node_needs_value"))
			}
			nodeFilter = args[i+1]
			i++
		case "--status", "-s":
			if i+1 >= len(args) {
				return false, "", "", "", "", false, errors.New(i18n.T("err.status_needs_value"))
			}
			statusFilter = args[i+1]
			i++
		default:
			// any non-flag from here on is the search query
			query = strings.TrimSpace(strings.Join(args[i:], " "))
			return
		}
	}
	return
}

// --- get -----------------------------------------------------------------

func runGet(query string) error {
	cfg, err := loadCFG()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cli := newClient(cfg)
	resources, rerr := cli.GetClusterResources(ctx)
	if rerr != nil {
		return rerr
	}

	matches := fuzzy(resources, query)
	sort.Slice(matches, func(i, j int) bool { return matchScore(matches[i], query) > matchScore(matches[j], query) })
	if len(matches) == 0 {
		mutedFmt.Printf("\n  %s\n\n", i18n.T("msg.no_match_try_ls", query, accentFmt.Sprint("pve ls")))
		return nil
	}

	headerFmt.Printf("\n  %s\n", i18n.T("msg.matches_for", len(matches), accentFmt.Sprintf("%q", query)))
	if len(matches) == 1 || onlyLXC(matches) {
		for _, r := range matches[:min(5, len(matches))] {
			printResource(cli, ctx, r)
		}
	} else {
		rows := [][]string{}
		for _, r := range matches[:min(8, len(matches))] {
			rows = append(rows, []string{
				coloredName(r.Name),
				r.Node,
				string(r.Type),
				colorStatus(r.Status),
				nonEmpty(r.IP, mutedFmt.Sprint("—")),
				fmt.Sprintf("%d", r.VMID),
			})
		}
		printTableSimple([]string{"NAME", "NODE", "TYPE", "STATUS", "IP", "VMID"}, rows)
	}
	fmt.Println()
	return nil
}

// --- ip ------------------------------------------------------------------

func runIP(query string) error {
	cfg, err := loadCFG()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cli := newClient(cfg)
	resources, _ := cli.GetClusterResources(ctx)
	matches := fuzzy(resources, query)
	if len(matches) == 0 {
		return fmt.Errorf("%s", i18n.T("err.no_match", query))
	}
	sort.Slice(matches, func(i, j int) bool { return matchScore(matches[i], query) > matchScore(matches[j], query) })
	for _, r := range matches {
		if r.Type != proxmox.ResourceLXC {
			continue
		}
		ips, errProblems, diagNotes := fetchIPs(cli, ctx, r)
		if len(ips) == 0 {
			return formatIPFailure(r, errProblems, diagNotes)
		}
		fmt.Println(ips[0])
		return nil
	}
	return fmt.Errorf("%s", i18n.T("err.no_lxc_match", query))
}

// --- ssh ------------------------------------------------------------------

func runSSH(query string) error {
	cfg, err := loadCFG()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cli := newClient(cfg)
	resources, _ := cli.GetClusterResources(ctx)

	// Try exact VMID match first, then fuzzy by name.
	var target proxmox.ClusterResource
	if vmid, vidErr := strconv.Atoi(query); vidErr == nil {
		for _, r := range resources {
			if r.VMID == vmid && (r.Type == proxmox.ResourceLXC || r.Type == proxmox.ResourceQemu) {
				target = r
				break
			}
		}
	}
	if target.VMID == 0 {
		matches := fuzzy(resources, query)
		// Filter to containers only.
		containers := matches[:0]
		for _, m := range matches {
			if m.Type == proxmox.ResourceLXC || m.Type == proxmox.ResourceQemu {
				containers = append(containers, m)
			}
		}
		if len(containers) == 0 {
			return fmt.Errorf("%s", i18n.T("err.no_match_try_ls", query))
		}
		sort.Slice(containers, func(i, j int) bool { return matchScore(containers[i], query) > matchScore(containers[j], query) })
		target = containers[0]
	}

	// Warn if the container isn't running.
	if target.Status == "stopped" {
		warnFmt.Printf("  %s\n", i18n.T("msg.stopped_ssh_warn", target.Name))
	}

	// Resolve IP.
	ip := target.IP
	if ip == "" {
		if ip = extractIPFromTags(target.Tags); ip == "" {
			// Fallback: try the full fetchIPs pipeline.
			ips, _, _ := fetchIPs(cli, ctx, target)
			if len(ips) > 0 {
				ip = ips[0]
			}
		}
	}
	if ip == "" {
		return fmt.Errorf("%s", i18n.T("err.ip_resolve", target.Name, target.VMID, target.Name))
	}

	// Determine SSH user.
	user := cli.SSHUserOrDefault()

	mutedFmt.Printf("  ssh %s@%s  (%s, vmid=%d)\n", user, ip, target.Name, target.VMID)

	// Launch interactive SSH.
	sshCmd := exec.Command("ssh", user+"@"+ip)
	sshCmd.Stdin = os.Stdin
	sshCmd.Stdout = os.Stdout
	sshCmd.Stderr = os.Stderr
	if err := sshCmd.Run(); err != nil {
		return fmt.Errorf("%s", i18n.T("err.ssh_failed", err))
	}
	return nil
}

// --- exec ----------------------------------------------------------------

func runExec(query string, cmd []string) error {
	cfg, err := loadCFG()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cli := newClient(cfg)
	resources, _ := cli.GetClusterResources(ctx)

	target, err := resolveExecTarget(resources, query)
	if err != nil {
		return err
	}

	// Warn if the container isn't running.
	if target.Status == "stopped" {
		warnFmt.Printf("  %s\n", i18n.T("msg.stopped_exec_warn", target.Name))
	}

	// Default command: list /root/.
	if len(cmd) == 0 {
		cmd = []string{"ls", "-la", "/root/"}
	}

	mutedFmt.Printf("  exec %s (vmid=%d, node=%s): %s\n", target.Name, target.VMID, target.Node, strings.Join(cmd, " "))

	var output string
	if len(cmd) > 0 && cmd[0] == "ls" {
		output, err = cli.ExecLXCScript(ctx, target.Node, target.VMID, prettyLsScript(cmd))
	} else {
		output, err = cli.ExecLXC(ctx, target.Node, target.VMID, cmd)
	}
	if err != nil {
		return err
	}
	fmt.Println(output)
	return nil
}

func prettyLsScript(cmd []string) string {
	quoted := make([]string, 0, len(cmd)-1)
	for _, a := range cmd[1:] {
		quoted = append(quoted, shellQuote(a))
	}
	args := strings.Join(quoted, " ")
	return fmt.Sprintf(
		"if command -v lsd >/dev/null 2>&1; then lsd --icon=auto --color=always %s; "+
			"elif command -v eza >/dev/null 2>&1; then eza --icons --color=always %s; "+
			"else ls --color=always %s; fi",
		args, args, args,
	)
}

// shellQuote returns a single-quoted shell word that safely embeds s.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	// Use single quotes and escape any embedded single quote.
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func resolveExecTarget(resources []proxmox.ClusterResource, query string) (proxmox.ClusterResource, error) {
	var zero proxmox.ClusterResource
	// Exact VMID match first.
	if vmid, vidErr := strconv.Atoi(query); vidErr == nil {
		for _, r := range resources {
			if r.VMID != vmid {
				continue
			}
			if r.Type == proxmox.ResourceLXC {
				return r, nil
			}
			if r.Type == proxmox.ResourceQemu {
				return zero, fmt.Errorf("%s", i18n.T("err.exec_lxc_only", r.Name, accentFmt.Sprint("pve ssh "+query)))
			}
		}
	}

	// Fuzzy match (LXC only).
	matches := fuzzy(resources, query)
	containers := matches[:0]
	for _, m := range matches {
		if m.Type == proxmox.ResourceLXC {
			containers = append(containers, m)
		} else if m.Type == proxmox.ResourceQemu && len(containers) == 0 {
			// keep first QEMU match for a clear error message.
			containers = append(containers, m)
		}
	}
	if len(containers) == 0 {
		return zero, fmt.Errorf("%s", i18n.T("err.no_match_try_ls", query))
	}
	if containers[0].Type == proxmox.ResourceQemu {
		return zero, fmt.Errorf("%s", i18n.T("err.exec_lxc_only", containers[0].Name, accentFmt.Sprint("pve ssh "+query)))
	}
	sort.Slice(containers, func(i, j int) bool { return matchScore(containers[i], query) > matchScore(containers[j], query) })
	return containers[0], nil
}

// resolveVMTarget resolves an LXC or QEMU resource by VMID or fuzzy name.
// Used by start/stop/restart commands.
func resolveVMTarget(resources []proxmox.ClusterResource, query string) (proxmox.ClusterResource, error) {
	var zero proxmox.ClusterResource
	if vmid, err := strconv.Atoi(query); err == nil {
		for _, r := range resources {
			if r.VMID == vmid && (r.Type == proxmox.ResourceLXC || r.Type == proxmox.ResourceQemu) {
				return r, nil
			}
		}
	}
	matches := fuzzy(resources, query)
	containers := matches[:0]
	for _, m := range matches {
		if m.Type == proxmox.ResourceLXC || m.Type == proxmox.ResourceQemu {
			containers = append(containers, m)
		}
	}
	if len(containers) == 0 {
		return zero, fmt.Errorf("%s", i18n.T("err.no_match_try_ls", query))
	}
	sort.Slice(containers, func(i, j int) bool { return matchScore(containers[i], query) > matchScore(containers[j], query) })
	return containers[0], nil
}

// runStartStopRestart executes start/stop/restart on a matched LXC or VM.
func runStartStopRestart(action, query string) error {
	cfg, err := loadCFG()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cli := newClient(cfg)
	resources, rerr := cli.GetClusterResources(ctx)
	if rerr != nil {
		return rerr
	}
	target, err := resolveVMTarget(resources, query)
	if err != nil {
		return err
	}

	mutedFmt.Printf("  %s %s (vmid=%d, node=%s, type=%s)\n", action, target.Name, target.VMID, target.Node, target.Type)
	if err := cli.ChangeVMStatus(ctx, target.Node, target.VMID, target.Type, action); err != nil {
		return err
	}
	okFmt.Printf("  %s\n", i18n.T("msg.action_done", action, target.Name))
	return nil
}

// runTop shows a live top-like view of cluster resources.
func runTop(args []string) error {
	sortBy := "cpu"
	refresh := 3
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--sort", "-s":
			if i+1 < len(args) {
				sortBy = args[i+1]
				i++
			}
		case "--refresh", "-r":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil && n > 0 {
					refresh = n
				}
				i++
			}
		}
	}

	cfg, err := loadCFG()
	if err != nil {
		return err
	}

	// Capture Ctrl+C to exit cleanly.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	ticker := time.NewTicker(time.Duration(refresh) * time.Second)
	defer ticker.Stop()

	// First render immediately.
	if err := renderTop(cfg, sortBy); err != nil {
		return err
	}

	for {
		select {
		case <-sigCh:
			fmt.Println()
			return nil
		case <-ticker.C:
			if err := renderTop(cfg, sortBy); err != nil {
				return err
			}
		}
	}
}

func renderTop(cfg *config.Config, sortBy string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cli := newClient(cfg)
	resources, err := cli.GetClusterResources(ctx)
	if err != nil {
		return err
	}

	var items []proxmox.ClusterResource
	for _, r := range resources {
		if r.Type == proxmox.ResourceLXC || r.Type == proxmox.ResourceQemu {
			items = append(items, r)
		}
	}

	switch sortBy {
	case "mem":
		sort.Slice(items, func(i, j int) bool { return items[i].Mem > items[j].Mem })
	case "cpu":
		sort.Slice(items, func(i, j int) bool { return items[i].CPU > items[j].CPU })
	default:
		sort.Slice(items, func(i, j int) bool { return items[i].CPU > items[j].CPU })
	}

	// Clear screen and move cursor to top-left.
	fmt.Print("\033[2J\033[H")

	fmt.Println()
	headerFmt.Printf("  %s\n", i18n.T("msg.top_title"))
	headerFmt.Printf("  %s\n\n", i18n.T("msg.top_sorted_by", accentFmt.Sprint(sortBy)))

	rows := [][]string{}
	for _, r := range items {
		rows = append(rows, []string{
			coloredName(r.Name),
			r.Node,
			string(r.Type),
			colorStatus(r.Status),
			fmtCPU(r.CPU),
			fmtBytes(r.Mem),
			fmt.Sprintf("%d", r.VMID),
		})
	}
	printTableSimple([]string{"NAME", "NODE", "TYPE", "STATUS", "CPU", "MEM", "VMID"}, rows)
	fmt.Println()
	mutedFmt.Printf("  %s\n", i18n.T("msg.top_footer", accentFmt.Sprint("pve top"), accentFmt.Sprint("--sort cpu|mem"), accentFmt.Sprint("--refresh N")))
	return nil
}

// --- config --------------------------------------------------------------

func runConfig(args []string) error {
	c, err := config.Load()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		printCurrentConfig(c)
		return nil
	}
	action := args[0]
	rest := strings.Join(args[1:], " ")
	switch action {
	case "token":
		if rest == "" {
			return errors.New(i18n.T("err.usage_config_token"))
		}
		c.Token = rest
		cl, _ := loadCFG()
		_ = cl
		conn := redirectToken(c.Token)
		if err := proxmox.ValidateToken(conn); err != nil {
			return err
		}
		okFmt.Printf("  %s\n", i18n.T("msg.token_saved"))
	case "nodes":
		if rest == "" {
			return errors.New(i18n.T("err.usage_config_nodes"))
		}
		parts := splitTrim(rest, ",")
		c.Nodes = parts
		okFmt.Printf("  %s\n", i18n.T("msg.nodes_saved", len(parts)))
	case "repo":
		if rest == "" {
			return errors.New(i18n.T("err.usage_config_repo"))
		}
		c.Repo = rest
		okFmt.Printf("  %s\n", i18n.T("msg.repo_saved", rest))
	case "base-url", "baseurl":
		if rest == "" {
			// explicit clear — empty arg means "drop base_url, fall back to repo"
			if c.BaseURL == "" {
				return errors.New(i18n.T("err.base_url_empty"))
			}
			c.BaseURL = ""
			okFmt.Printf("  %s\n", i18n.T("msg.base_url_cleared"))
			return config.Save(c)
		}
		cleaned, verr := normalizeBaseURL(rest)
		if verr != nil {
			return verr
		}
		c.BaseURL = cleaned
		okFmt.Printf("  %s\n", i18n.T("msg.base_url_saved", cleaned))
		if c.Repo != "" {
			warnFmt.Printf("  %s\n", i18n.T("msg.base_url_note_repo", c.Repo))
		}
	case "ssh-user", "sshuser":
		if rest == "" {
			return errors.New(i18n.T("err.usage_config_ssh_user"))
		}
		c.SSHUser = rest
		okFmt.Printf("  %s\n", i18n.T("msg.ssh_user_saved", rest))
	case "ssh-password", "sshpassword":
		c.SSHPassword = rest
		if rest == "" {
			okFmt.Printf("  %s\n", i18n.T("msg.ssh_pw_cleared"))
		} else {
			okFmt.Printf("  %s\n", i18n.T("msg.ssh_pw_saved"))
		}
	case "lang":
		return runLang(args[1:])
	case "show":
		cl, lerr := loadCFG()
		if lerr != nil {
			return lerr
		}
		printCurrentConfig(cl)
		return nil
	default:
		return fmt.Errorf("%s", i18n.T("err.unknown_config_action", action))
	}
	return config.Save(c)
}

func printCurrentConfig(c *config.Config) {
	headerFmt.Printf("\n  %s\n", i18n.T("msg.config_title"))
	if len(c.Nodes) > 0 {
		fmt.Printf("  %s %s\n", i18n.T("msg.nodes_label"), strings.Join(c.Nodes, ", "))
	} else {
		mutedFmt.Printf("  %s %s\n", i18n.T("msg.nodes_label"), i18n.T("msg.empty_paren"))
	}
	if c.Token != "" {
		fmt.Printf("  %s %s\n", i18n.T("msg.token_label"), maskToken(c.Token))
	} else {
		mutedFmt.Printf("  %s %s\n", i18n.T("msg.token_label"), i18n.T("msg.empty_paren"))
	}
	if c.SSHUser != "" {
		fmt.Printf("  %s %s\n", i18n.T("msg.ssh_user_label"), c.SSHUser)
	} else {
		mutedFmt.Printf("  %s %s\n", i18n.T("msg.ssh_user_label"), i18n.T("msg.ssh_user_default"))
	}
	if c.SSHPassword != "" {
		fmt.Printf("  %s %s\n", i18n.T("msg.ssh_pw_label"), strings.Repeat("*", len(c.SSHPassword)))
		mutedFmt.Printf("  %s\n", i18n.T("msg.ssh_pw_note"))
	} else {
		mutedFmt.Printf("  %s %s\n", i18n.T("msg.ssh_pw_label"), i18n.T("msg.ssh_pw_empty_note"))
	}
	if c.BaseURL != "" {
		fmt.Printf("  %s %s\n", i18n.T("msg.base_url_label"), c.BaseURL)
	} else {
		mutedFmt.Printf("  %s %s\n", i18n.T("msg.base_url_label"), i18n.T("msg.base_url_empty_note"))
	}
	if c.Repo != "" {
		fmt.Printf("  %s %s\n", i18n.T("msg.repo_label"), c.Repo)
		if c.BaseURL != "" {
			mutedFmt.Printf("         %s\n", i18n.T("msg.repo_ignored"))
		} else {
			mutedFmt.Printf("         %s\n", i18n.T("msg.repo_active"))
		}
	} else if c.BaseURL == "" {
		mutedFmt.Printf("  %s %s\n", i18n.T("msg.repo_label"), i18n.T("msg.repo_empty_note"))
	}
	lang := c.Lang
	if lang == "" {
		mutedFmt.Printf("  %s %s %s\n", i18n.T("msg.lang_label"), i18n.DetectSystemLang(), i18n.T("msg.lang_default"))
	} else {
		fmt.Printf("  %s %s\n", i18n.T("msg.lang_label"), lang)
	}
	fmt.Printf("  %s %s\n\n", i18n.T("msg.path_label"), mustConfigPath())
}

// runLang sets or shows the CLI language (es/en). Persisted in config.json.
func runLang(args []string) error {
	c, err := config.Load()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		cur := c.Lang
		if cur == "" {
			cur = i18n.DetectSystemLang() + i18n.T("msg.lang_detect")
		}
		okFmt.Printf("  %s\n", i18n.T("msg.lang_current", cur))
		return nil
	}
	code := strings.ToLower(strings.TrimSpace(args[0]))
	if code != "es" && code != "en" {
		return errors.New(i18n.T("err.usage_lang"))
	}
	c.Lang = code
	if err := config.Save(c); err != nil {
		return err
	}
	i18n.SetLang(code)
	okFmt.Printf("  %s\n", i18n.T("msg.lang_saved", code))
	return nil
}

func mustConfigPath() string {
	p, err := config.Path()
	if err != nil {
		return "<error>"
	}
	return p
}

func maskToken(t string) string {
	t = redirectToken(t)
	if len(t) < 16 {
		return strings.Repeat("*", len(t))
	}
	return t[:10] + "…" + t[len(t)-4:]
}

// --- formatting helpers --------------------------------------------------

func proxmoxByType(rs []proxmox.ClusterResource, t proxmox.ResourceType) []proxmox.ClusterResource {
	out := []proxmox.ClusterResource{}
	for _, r := range rs {
		if r.Type == t {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func toRows(nodes []proxmox.ClusterResource) [][]string {
	rows := make([][]string, 0, len(nodes))
	for _, n := range nodes {
		rows = append(rows, []string{
			coloredName(n.Name), n.Node, string(n.Type), colorStatus(n.Status),
			nonEmpty(n.IP, mutedFmt.Sprint("—")), fmt.Sprintf("%d", n.VMID),
		})
	}
	return rows
}

func colorStatus(s string) string {
	switch s {
	case "running", "online":
		return okFmt.Sprint(s)
	case "stopped", "offline":
		return errFmt.Sprint(s)
	case "paused":
		return warnFmt.Sprint(s)
	default:
		return mutedFmt.Sprint(s)
	}
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func fmtBytes(b int64) string {
	if b <= 0 {
		return "—"
	}
	const u = 1024
	div, exp := int64(u), 0
	for n := b / u; n >= u; n /= u {
		div *= u
		exp++
	}
	if exp >= len("KMGTPE") {
		exp = len("KMGTPE") - 1
	}
	return fmt.Sprintf("%.1f%cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func fmtCPU(c float64) string {
	if c <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", c*100)
}

func fmtDuration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	days := int(d.Hours()) / 24
	if days > 0 {
		return fmt.Sprintf("%dd", days)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", h, m)
}

func splitTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func coloredName(name string) string {
	if name == "" {
		return mutedFmt.Sprint("—")
	}
	return highlight.Sprint(name)
}

func onlyLXC(rs []proxmox.ClusterResource) bool {
	if len(rs) == 0 {
		return false
	}
	for _, r := range rs {
		if r.Type != proxmox.ResourceLXC {
			return false
		}
	}
	return true
}

// --- search --------------------------------------------------------------

func fuzzy(rs []proxmox.ClusterResource, q string) []proxmox.ClusterResource {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	ql := strings.ToLower(q)
	out := []proxmox.ClusterResource{}
	for _, r := range rs {
		if matchScore(r, ql) > 0 {
			out = append(out, r)
		}
	}
	return out
}

func matchScore(r proxmox.ClusterResource, q string) int {
	name := strings.ToLower(r.Name)
	node := strings.ToLower(r.Node)
	id := strings.ToLower(r.ID)
	ip := r.IP
	tags := strings.ToLower(r.Tags)
	vmid := fmt.Sprintf("%d", r.VMID)
	score := 0
	switch {
	case name == q:
		score += 100
	case vmid == q:
		score += 90
	case strings.HasPrefix(name, q):
		score += 60
	case strings.Contains(name, q):
		score += 40
	}
	if node != "" && strings.Contains(node, q) {
		score += 5
	}
	if strings.Contains(id, q) {
		score += 15
	}
	if ip != "" && strings.Contains(ip, q) {
		score += 20
	}
	if tags != "" && strings.Contains(tags, q) {
		score += 35
	}
	return score
}

// --- LXC IP extraction ---------------------------------------------------

func printResource(cli *proxmox.Client, ctx context.Context, r proxmox.ClusterResource) {
	fmt.Println()
	headerFmt.Printf("  %s  %s\n", strings.ToUpper(string(r.Type)), highlight.Sprintf("%s", r.Name))
	fmt.Printf("  %s\n",
		i18n.T("msg.vmid_node_status", mutedFmt.Sprint("▌"), r.VMID, colorStatus(r.Node), colorStatus(r.Status)))
	if r.IP != "" {
		fmt.Printf("  %s %s\n", i18n.T("msg.ip_cluster"), okFmt.Sprintf("%s", r.IP))
	}
	var ips []string
	var errProblems, diagNotes []string
	if r.Type == proxmox.ResourceLXC {
		ips, errProblems, diagNotes = fetchIPs(cli, ctx, r)
	}
	if len(ips) > 0 {
		fmt.Printf("  %-23s %s\n", i18n.T("msg.ip_label"), okFmt.Sprint(strings.Join(ips, "  ·  ")))
	} else if r.Type == proxmox.ResourceLXC {
		mutedFmt.Printf("  %-23s %s\n", i18n.T("msg.ip_label"), i18n.T("msg.ip_none"))
		all := append(errProblems, diagNotes...)
		if len(all) > 0 {
			mutedFmt.Printf("    %-22s %s\n", i18n.T("msg.problems"), strings.Join(all, "; "))
		}
		mutedFmt.Printf("    %-22s pve get %s  ·  pve ls --ips %s\n", i18n.T("msg.debug"), r.Name, r.Name)
	} else if r.Type == proxmox.ResourceNode {
		fmt.Printf("  %s\n", i18n.T("msg.cpu_cores", r.CPU*100, r.MaxCPU))
		fmt.Printf("  %s\n", i18n.T("msg.mem_of", fmtBytes(r.Mem), fmtBytes(r.MaxMem)))
		fmt.Printf("  %s\n", i18n.T("msg.uptime", fmtDuration(time.Duration(r.Uptime)*time.Second)))
	}
}

type ipHit struct {
	v4     []string
	v6     []string
	errMsg string // protocol error only
	note   string // diagnostic on empty result or skip (set only when errMsg == "")
	src    string // "/lxc/{vmid}/config" or "/lxc/{vmid}/interfaces"
}

// fetchIPs resolves the IP of an LXC using cached, static and live sources.
// Fastest source wins. Returns IPs and two diagnostic slices for the failure renderer.
func fetchIPs(cli *proxmox.Client, ctx context.Context, r proxmox.ClusterResource) ([]string, []string, []string) {
	if r.Type != proxmox.ResourceLXC {
		return nil, nil, nil
	}
	// Fast path 1: cluster already cached the IP.
	if r.IP != "" {
		return []string{r.IP}, nil, nil
	}
	// Fast path 2: networks array (PVE 8.x returns this for DHCP LXCs
	// when the cluster has a cached lease).
	if ips := clusterResourceIPs(r.Networks); len(ips) > 0 {
		return ips, nil, nil
	}
	if ip := extractIPFromTags(r.Tags); ip != "" {
		return []string{ip}, nil, nil
	}
	cfgSrc := fmt.Sprintf("/lxc/%d/config", r.VMID)
	liveSrc := fmt.Sprintf("/lxc/%d/interfaces", r.VMID)
	clusterSrc := "/cluster/resources"
	cfgCh := make(chan ipHit, 1)
	liveCh := make(chan ipHit, 1)
	go func() {
		c, err := cli.GetLXCConfig(ctx, r.Node, r.VMID)
		if err != nil {
			cfgCh <- ipHit{errMsg: err.Error(), src: cfgSrc}
			return
		}
		var v4, v6 []string
		for _, ip := range c.HasIPs() {
			if base, _, ok := strings.Cut(ip, "/"); ok {
				ip = base
			}
			if strings.Contains(ip, ":") {
				v6 = append(v6, ip)
			} else {
				v4 = append(v4, ip)
			}
		}
		h := ipHit{v4: v4, v6: v6, src: cfgSrc}
		if len(v4)+len(v6) == 0 {
			h.note = i18n.T("msg.diag_no_ip_cfg")
		}
		cfgCh <- h
	}()
	if r.Status == "stopped" {
		liveCh <- ipHit{note: i18n.T("msg.diag_stopped"), src: liveSrc}
	} else {
		go func() {
			ifs, err := cli.GetLXCInterfaces(ctx, r.Node, r.VMID)
			if err != nil {
				liveCh <- ipHit{errMsg: err.Error(), src: liveSrc}
				return
			}
			var v4, v6 []string
			for _, i := range ifs {
				for _, a := range i.IPs {
					if a.Type != "inet" && a.Type != "inet6" {
						continue
					}
					ip, _, e := net.ParseCIDR(a.IPAddress + "/" + fmt.Sprintf("%d", a.Prefix))
					if e != nil {
						continue
					}
					if ip.To4() != nil {
						v4 = append(v4, ip.String())
					} else {
						v6 = append(v6, ip.String())
					}
				}
			}
			h := ipHit{v4: v4, v6: v6, src: liveSrc}
			if len(v4)+len(v6) == 0 {
				// live succeeded but no v4/v6 came back. Classify as a
				// diagnostic so a success-via-cfg call doesn't leak it.
				h.note = i18n.T("msg.diag_empty_live")
			}
			liveCh <- h
		}()
	}
	// Materialize both BEFORE branching: each channel has cap 1 and is sent
	// exactly once, so reading twice would deadlock on the empty-v4 corner.
	cfgH := <-cfgCh
	liveH := <-liveCh
	var errProblems []string
	if cfgH.errMsg != "" {
		errProblems = append(errProblems, cfgH.src+": "+cfgH.errMsg)
	}
	if liveH.errMsg != "" {
		errProblems = append(errProblems, liveH.src+": "+liveH.errMsg)
	}
	if len(cfgH.v4) > 0 {
		return cfgH.v4, errProblems, nil
	}
	if len(liveH.v4) > 0 {
		return liveH.v4, errProblems, nil
	}
	if len(cfgH.v6) > 0 {
		return cfgH.v6, errProblems, nil
	}
	if len(liveH.v6) > 0 {
		return liveH.v6, errProblems, nil
	}
	execSrc := fmt.Sprintf("/lxc/%d/exec", r.VMID)
	var execNote string
	if len(errProblems) > 0 {
		// API calls failed at protocol level. SSH would mask the real
		// issue. Surface the errors so the user can fix permissions.
		execNote = i18n.T("msg.diag_ssh_api_errors")
	} else if r.Status == "running" {
		execCtx, execCancel := context.WithTimeout(ctx, 6*time.Second)
		execIPs, note := execLXCIPs(cli, execCtx, r.Node, r.VMID)
		execCancel()
		if len(execIPs) > 0 {
			return execIPs, errProblems, nil
		}
		execNote = note
	}
	var diagNotes []string
	if cfgH.note != "" {
		diagNotes = append(diagNotes, cfgH.src+": "+cfgH.note)
	}
	if liveH.note != "" {
		diagNotes = append(diagNotes, liveH.src+": "+liveH.note)
	}
	// Distinguish "no networks at all" from "no IP in networks" for the
	// cluster/resources diagnostic line.
	if len(r.Networks) == 0 {
		diagNotes = append(diagNotes, clusterSrc+": sin IP reportada por /cluster/resources (ni r.IP ni networks)")
	} else {
		diagNotes = append(diagNotes, i18n.T("msg.diag_cluster_empty", clusterSrc))
	}
	if execNote != "" {
		diagNotes = append(diagNotes, execSrc+": "+execNote)
	}
	return nil, errProblems, diagNotes
}

func clusterResourceIPs(networks []proxmox.NetworkInfo) []string {
	var ips []string
	for _, ni := range networks {
		for _, ip := range ni.HasIPs() {
			if net.ParseIP(ip) != nil {
				// Prefer IPv4.
				ips = append(ips, ip)
			}
		}
	}
	// IPv4 first, then IPv6.
	var v4, v6 []string
	for _, ip := range ips {
		if strings.Contains(ip, ":") {
			v6 = append(v6, ip)
		} else {
			v4 = append(v4, ip)
		}
	}
	if len(v4) > 0 {
		return v4
	}
	return v6
}

func execLXCIPs(cli *proxmox.Client, ctx context.Context, node string, vmid int) ([]string, string) {
	output, err := cli.ExecLXC(ctx, node, vmid, []string{"hostname", "-I"})
	sshUser := cli.SSHUserOrDefault()
	if err != nil {
		errStr := err.Error()
		switch {
		case strings.Contains(errStr, "sin claves SSH"):
			// No keys found at all (non-interactive or no default key files).
			return nil, i18n.T("msg.diag_no_ssh_keys", sshUser)
		case strings.Contains(errStr, "auth failed") && strings.Contains(errStr, "ssh-copy-id"):
			// Password prompt failed or was cancelled.
			return nil, i18n.T("msg.diag_ssh_auth_copyid", sshUser)
		case strings.Contains(errStr, "unable to authenticate") || strings.Contains(errStr, "Permission denied"):
			// Keys were found but rejected by all PVE hosts (non-interactive mode).
			return nil, i18n.T("msg.diag_ssh_auth_notauth", sshUser)
		case strings.Contains(errStr, "connection refused") || strings.Contains(errStr, "i/o timeout"):
			return nil, fmt.Sprintf("SSH: no se pudo conectar al host PVE (¿SSH deshabilitado o firewall?) — %v", err)
		default:
			return nil, i18n.T("msg.diag_ssh_exec_failed", err)
		}
	}
	// hostname -I returns space-separated IPs: "10.0.0.5 172.17.0.1"
	if output == "" {
		return nil, i18n.T("msg.diag_no_ips_hostname")
	}
	var ips []string
	for _, token := range strings.Fields(output) {
		token = strings.TrimSpace(token)
		if token == "" || strings.HasPrefix(token, "127.") {
			continue
		}
		if ip := net.ParseIP(token); ip != nil && ip.To4() != nil {
			ips = append(ips, token)
		}
	}
	if len(ips) == 0 {
		return nil, i18n.T("msg.diag_no_v4_hostname", output)
	}
	return ips, ""
}

func formatIPFailure(r proxmox.ClusterResource, errProblems, diagNotes []string) error {
	var sb strings.Builder
	statusNote := ""
	if r.Status != "" && r.Status != "running" {
		statusNote = " (contenedor " + r.Status + ")"
	}
	fmt.Fprintf(&sb, "%s: no se pudo resolver la IP%s", r.Name, statusNote)
	// errProblems first (urgent), then diagNotes (informational).
	all := make([]string, 0, len(errProblems)+len(diagNotes))
	all = append(all, errProblems...)
	all = append(all, diagNotes...)
	if len(all) > 0 {
		sb.WriteString(":\n  · ")
		sb.WriteString(strings.Join(all, "\n  · "))
	}
	sb.WriteString("\n  Sugerencia: ")
	switch {
	case r.Status == "stopped" || r.Status == "paused":
		fmt.Fprintf(&sb, "%s", i18n.T("msg.ipfail_stopped_hint", r.Name, r.Name))
	case len(errProblems) > 0:
		sb.WriteString(i18n.T("msg.ipfail_proto"))
	default:
		fmt.Fprintf(&sb, "%s", i18n.T("msg.ipfail_dhcp", r.Name))
	}
	return errors.New(sb.String())
}

func extractIPFromTags(tags string) string {
	if tags == "" {
		return ""
	}
	// Take first tag segment before ';'
	first := tags
	if idx := strings.IndexByte(tags, ';'); idx >= 0 {
		first = tags[:idx]
	}
	first = strings.TrimSpace(first)
	if first == "" {
		return ""
	}
	// Try to parse as last N octets of an IPv4 address (2, 3, or 4 octets).
	parts := strings.Split(first, ".")
	if len(parts) < 2 || len(parts) > 4 {
		return ""
	}
	// Validate each part is 0-255.
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 255 {
			return ""
		}
	}
	var ip string
	switch len(parts) {
	case 2:
		ip = fmt.Sprintf("192.168.%s.%s", parts[0], parts[1])
	case 3:
		ip = fmt.Sprintf("192.%s.%s.%s", parts[0], parts[1], parts[2])
	case 4:
		ip = fmt.Sprintf("%s.%s.%s.%s", parts[0], parts[1], parts[2], parts[3])
	}
	if net.ParseIP(ip) == nil {
		return ""
	}
	return ip
}

func resolveLXCIPs(ctx context.Context, cli *proxmox.Client, rs []proxmox.ClusterResource) map[int]string {
	out := make(map[int]string, len(rs))
	type hit struct {
		vmid int
		ip   string
	}
	ch := make(chan hit, len(rs))
	sem := make(chan struct{}, 12)
	var wg sync.WaitGroup
	for _, r := range rs {
		if r.Type != proxmox.ResourceLXC {
			continue
		}
		if r.IP != "" {
			out[r.VMID] = r.IP
			continue
		}
		if ips := clusterResourceIPs(r.Networks); len(ips) > 0 {
			out[r.VMID] = ips[0]
			continue
		}
		if ip := extractIPFromTags(r.Tags); ip != "" {
			out[r.VMID] = ip
			continue
		}
		wg.Add(1)
		go func(r proxmox.ClusterResource) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			select {
			case sem <- struct{}{}:
			case <-cctx.Done():
				ch <- hit{r.VMID, ""}
				return
			}
			defer func() { <-sem }()
			ip := ""
			if cfg, err := cli.GetLXCConfig(cctx, r.Node, r.VMID); err == nil {
				if list := cfg.HasIPs(); len(list) > 0 {
					ip = list[0]
				}
			}
			ch <- hit{r.VMID, ip}
		}(r)
	}
	wg.Wait()
	close(ch)
	for h := range ch {
		if h.ip != "" {
			out[h.vmid] = h.ip
		}
	}
	return out
}

// --- update --------------------------------------------------------------

func pickUpdateSource(c *config.Config, force bool) (label string, probe func() (*update.Info, error)) {
	if c.BaseURL != "" {
		return "local " + c.BaseURL, func() (*update.Info, error) { return update.CheckLocal(c.BaseURL, force) }
	}
	if c.Repo != "" {
		return "GitHub " + c.Repo, func() (*update.Info, error) { return update.Check(c.Repo, force) }
	}
	return "", nil
}

func tryAutoUpdate() {
	// Development builds report the placeholder version and should never
	// silently replace a locally-installed binary with an older release.
	if update.CurrentVersion == update.DevVersion {
		return
	}
	c, err := config.Load()
	if err != nil {
		return
	}
	_, probe := pickUpdateSource(c, false)
	if probe == nil {
		return
	}
	if !update.NeedsCheck() {
		return
	}
	info, err := probe()
	if err != nil || info == nil {
		return
	}
	fmt.Fprintln(os.Stderr)
	mutedFmt.Fprintf(os.Stderr, "  ⤓ update %s disponible, aplicando…\n", info.Latest)
	if err := update.Apply(info); err != nil {
		errFmt.Fprintf(os.Stderr, "  %s\n", i18n.T("msg.auto_update_failed", err))
		fmt.Fprintln(os.Stderr)
		return
	}
	okFmt.Fprintf(os.Stderr, "  %s\n", i18n.T("msg.auto_updated", info.Latest))
	fmt.Fprintln(os.Stderr)
}

func runUpdate(args []string) error {
	checkOnly, force, err := parseUpdateFlags(args)
	if err != nil {
		return err
	}
	c, err := config.Load()
	if err != nil {
		return err
	}
	label, probe := pickUpdateSource(c, force)
	if probe == nil {
		return errors.New(i18n.T("err.no_update_source"))
	}
	srcKey := "msg.source_github"
	if strings.HasPrefix(label, "local") {
		srcKey = "msg.source_local"
	}
	mutedFmt.Printf("  %s\n", i18n.T(srcKey, label))
	if !force && !checkOnly && !update.NeedsCheck() {
		mutedFmt.Printf("  %s\n", i18n.T("msg.checked_24h"))
	}
	info, err := probe()
	if err != nil {
		return err
	}
	if info == nil {
		okFmt.Printf("  %s\n", i18n.T("msg.up_to_date", update.CurrentVersion))
		return nil
	}
	if checkOnly {
		fmt.Printf("  %s\n", i18n.T("msg.update_available", info.Latest, update.CurrentVersion))
		fmt.Printf("  asset:                  %s\n", info.URL)
		fmt.Println("  ejecuta `pve update` (sin --check) para aplicarlo.")
		return nil
	}
	warnFmt.Printf("  ⤓ descargando %s desde %s…\n", info.Latest, info.URL)
	if err := update.Apply(info); err != nil {
		return err
	}
	exe, _ := os.Executable()
	if fp, ferr := update.FingerprintAsset(exe); ferr == nil {
		okFmt.Printf("  %s\n", i18n.T("msg.updated_sha", info.Latest, fp))
	} else {
		okFmt.Printf("  %s\n", i18n.T("msg.updated", info.Latest))
	}
	return nil
}

func parseUpdateFlags(args []string) (checkOnly, force bool, err error) {
	for _, a := range args {
		switch a {
		case "--check":
			checkOnly = true
		case "--force", "-f":
			force = true
		case "-h", "--help":
			err = errors.New(i18n.T("err.usage_update"))
		default:
			err = fmt.Errorf("%s", i18n.T("err.unknown_flag", a))
		}
		if err != nil {
			return
		}
	}
	return
}

func normalizeBaseURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimRight(s, "/")
	if s == "" {
		return "", errors.New(i18n.T("err.url_empty"))
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("%s", i18n.T("err.url_invalid", err))
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("scheme debe ser http o https (recibido: %q)", u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New(i18n.T("err.url_no_host"))
	}
	return s, nil
}

func printErr(err error) {
	fmt.Fprintln(os.Stderr)
	errFmt.Fprintf(os.Stderr, "  ✖ %v\n", err)
	fmt.Fprintln(os.Stderr)
}

func printTableSimple(headers []string, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	// Visible widths (count runes after stripping ANSI — cells may carry colors).
	widths := make([]int, len(headers))
	for i, h := range headers {
		w := visibleLen(h) // headers are plain text
		if widths[i] < w {
			widths[i] = w
		}
	}
	for _, row := range rows {
		for i, cell := range row {
			w := visibleLen(stripAnsi(cell))
			if widths[i] < w {
				widths[i] = w
			}
		}
	}
	// Add an inter-column gap; cap to keep things sane.
	for i := range widths {
		widths[i] += 3
		if widths[i] < 6 {
			widths[i] = 6
		}
		if widths[i] > 56 {
			widths[i] = 56
		}
	}
	// Header.
	for i, h := range headers {
		gap := widths[i] - visibleLen(h)
		if gap < 0 {
			gap = 0
		}
		fmt.Printf("  %s%s", headerFmt.Sprint(h), strings.Repeat(" ", gap))
	}
	fmt.Println()
	// Rows.
	for _, row := range rows {
		for i, cell := range row {
			v := visibleLen(stripAnsi(cell))
			if v > widths[i] {
				// Overflow: truncate and lose color to keep the column aligned.
				fmt.Printf("  %s", truncateVisible(stripAnsi(cell), widths[i]))
			} else {
				gap := widths[i] - v
				fmt.Printf("  %s%s", cell, strings.Repeat(" ", gap))
			}
		}
		fmt.Println()
	}
}

func printTableSideBySide(leftHdr, rightHdr []string, leftRows, rightRows [][]string) {
	if len(leftRows) == 0 && len(rightRows) == 0 {
		return
	}

	// Compute widths for each side independently.
	lw := colWidths(leftHdr, leftRows)
	rw := colWidths(rightHdr, rightRows)

	// Separator between halves.
	sep := mutedFmt.Sprint(" │ ")

	// Header row.
	printSideRow(leftHdr, lw)
	fmt.Print(sep)
	printSideRow(rightHdr, rw)
	fmt.Println()

	// Data rows: interleave left[i] with right[i].
	maxRows := len(leftRows)
	if len(rightRows) > maxRows {
		maxRows = len(rightRows)
	}
	for i := 0; i < maxRows; i++ {
		if i < len(leftRows) {
			printSideRow(leftRows[i], lw)
		} else {
			printSidePad(lw)
		}
		fmt.Print(sep)
		if i < len(rightRows) {
			printSideRow(rightRows[i], rw)
		} else {
			printSidePad(rw)
		}
		fmt.Println()
	}
}

// colWidths computes per-column visible widths from headers and rows.
func colWidths(headers []string, rows [][]string) []int {
	w := make([]int, len(headers))
	for i, h := range headers {
		if v := visibleLen(stripAnsi(h)); w[i] < v {
			w[i] = v
		}
	}
	for _, row := range rows {
		for i, cell := range row {
			if v := visibleLen(stripAnsi(cell)); w[i] < v {
				w[i] = v
			}
		}
	}
	for i := range w {
		w[i] += 2
		if w[i] < 4 {
			w[i] = 4
		}
		if w[i] > 22 {
			w[i] = 22
		}
	}
	return w
}

func printSideRow(cells []string, widths []int) {
	for i, cell := range cells {
		v := visibleLen(stripAnsi(cell))
		if v > widths[i] {
			fmt.Print(truncateVisible(stripAnsi(cell), widths[i]))
		} else {
			gap := widths[i] - v
			fmt.Print(cell + strings.Repeat(" ", gap))
		}
	}
}

func printSidePad(widths []int) {
	for _, w := range widths {
		fmt.Print(strings.Repeat(" ", w))
	}
}

func visibleLen(s string) int {
	// Cheap visible-length calc: count runes, ignore control codes.
	n := 0
	for range s {
		n++
	}
	return n
}

func stripAnsi(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] == 0x1b {
			if i+1 < len(runes) && runes[i+1] == '[' {
				// CSI: skip ESC [ + parameters + terminator (0x40-0x7E)
				i += 2 // skip ESC [
				for i < len(runes) && (runes[i] < 0x40 || runes[i] > 0x7E) {
					i++
				}
				// skip the terminating letter (if we didn't run off the end)
			} else {
				// Bare ESC (not CSI) — skip that too.
			}
			continue
		}
		b.WriteRune(runes[i])
	}
	return b.String()
}

func truncateVisible(s string, w int) string {
	if visibleLen(s) <= w {
		return s
	}
	runes := []rune(s)
	if len(runes) > w {
		runes = runes[:w-1]
	}
	return string(runes) + "…"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

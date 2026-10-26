package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kevinburke/ssh_config"
)

// HostConfig is the resolved connection info for one alias in ~/.ssh/config.
type HostConfig struct {
	Alias         string
	HostName      string
	User          string
	Port          int
	IdentityFiles []string
	ProxyJump     string
}

// ListHosts returns host aliases defined in ~/.ssh/config (including
// Include'd files), skipping wildcard and negated patterns.
func ListHosts() ([]string, error) {
	cfg, err := parseSSHConfig(filepath.Join(homeDir(), ".ssh", "config"), 0)
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	var collect func(c *ssh_config.Config)
	collect = func(c *ssh_config.Config) {
		for _, h := range c.Hosts {
			for _, n := range h.Nodes {
				if inc, ok := n.(*ssh_config.Include); ok {
					for _, sub := range expandIncludePaths(inc.String()) {
						if parsed, err := parseSSHConfig(sub, 0); err == nil {
							collect(parsed)
						}
					}
				}
			}
			for _, p := range h.Patterns {
				name := p.String()
				if name == "" || name == "*" || strings.HasPrefix(name, "!") {
					continue
				}
				if strings.ContainsAny(name, "*?") {
					continue
				}
				if !seen[name] {
					seen[name] = true
					out = append(out, name)
				}
			}
		}
	}
	collect(cfg)
	return out, nil
}

// parseSSHConfig reads one config file; Include directives are resolved by
// the caller when listing hosts.
func parseSSHConfig(path string, depth int) (*ssh_config.Config, error) {
	if depth > 5 {
		return nil, fmt.Errorf("ssh config include depth exceeded")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open %s: %w", path, err)
	}
	defer f.Close()
	cfg, err := ssh_config.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// expandIncludePaths extracts the glob arguments from an "Include ..." line
// and returns existing, expanded file paths.
func expandIncludePaths(line string) []string {
	fields := strings.Fields(line)
	if len(fields) < 2 || !strings.EqualFold(fields[0], "Include") {
		return nil
	}
	var out []string
	for _, raw := range fields[1:] {
		p := raw
		if strings.HasPrefix(p, "~/") {
			p = filepath.Join(homeDir(), p[2:])
		} else if !filepath.IsAbs(p) {
			p = filepath.Join(homeDir(), ".ssh", p)
		}
		matches, _ := filepath.Glob(p)
		out = append(out, matches...)
	}
	return out
}

// ResolveHost resolves an alias (or a raw hostname) into connection settings
// the same way ssh(1) would: ~/.ssh/config first, then defaults.
func ResolveHost(alias string) (*HostConfig, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return nil, fmt.Errorf("host alias is required")
	}
	cfg := &HostConfig{Alias: alias, Port: 22}
	if hn := ssh_config.Get(alias, "Hostname"); hn != "" {
		cfg.HostName = hn
	} else {
		cfg.HostName = alias
	}
	if u := ssh_config.Get(alias, "User"); u != "" {
		cfg.User = u
	} else {
		cfg.User = currentUser()
	}
	if port := ssh_config.Get(alias, "Port"); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil {
			return nil, fmt.Errorf("invalid Port %q for host %s", port, alias)
		}
		cfg.Port = p
	}
	cfg.IdentityFiles = ssh_config.GetAll(alias, "IdentityFile")
	if len(cfg.IdentityFiles) == 0 {
		for _, d := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
			cfg.IdentityFiles = append(cfg.IdentityFiles, filepath.Join(homeDir(), ".ssh", d))
		}
	}
	cfg.ProxyJump = ssh_config.Get(alias, "ProxyJump")
	return cfg, nil
}

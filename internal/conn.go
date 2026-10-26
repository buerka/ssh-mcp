package internal

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

const dialTimeout = 10 * time.Second

// Pool holds one persistent SSH connection (plus an SFTP client) per host
// alias, reconnected on demand and evicted when the keepalive fails.
type Pool struct {
	Insecure bool

	mu    sync.Mutex
	conns map[string]*pconn
}

type pconn struct {
	client *ssh.Client
	sftp   *sftp.Client
}

func NewPool(insecure bool) *Pool {
	return &Pool{Insecure: insecure, conns: make(map[string]*pconn)}
}

func (p *Pool) Client(alias string) (*ssh.Client, error) {
	if c := p.get(alias); c != nil {
		return c.client, nil
	}
	cfg, err := ResolveHost(alias)
	if err != nil {
		return nil, err
	}
	client, err := p.dial(cfg, map[string]bool{cfg.Alias: true})
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if existing, ok := p.conns[alias]; ok { // raced with a concurrent dial
		p.mu.Unlock()
		client.Close()
		return existing.client, nil
	}
	p.conns[alias] = &pconn{client: client}
	p.mu.Unlock()
	go p.keepalive(alias, client)
	return client, nil
}

// SFTP returns (and caches) an SFTP client over the pooled connection.
func (p *Pool) SFTP(alias string) (*sftp.Client, error) {
	p.mu.Lock()
	c := p.conns[alias]
	p.mu.Unlock()
	if c != nil && c.sftp != nil {
		return c.sftp, nil
	}
	client, err := p.Client(alias)
	if err != nil {
		return nil, err
	}
	sc, err := sftp.NewClient(client)
	if err != nil {
		return nil, fmt.Errorf("sftp handshake with %s: %w", alias, err)
	}
	p.mu.Lock()
	if existing, ok := p.conns[alias]; ok && existing.sftp == nil {
		existing.sftp = sc
	}
	p.mu.Unlock()
	return sc, nil
}

func (p *Pool) get(alias string) *pconn {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.conns[alias]
}

func (p *Pool) drop(alias string, client *ssh.Client) {
	p.mu.Lock()
	c, ok := p.conns[alias]
	if ok && c.client == client {
		delete(p.conns, alias)
	}
	p.mu.Unlock()
	if ok {
		client.Close()
	}
}

// keepalive evicts the connection from the pool if it dies, so the next call
// reconnects transparently.
func (p *Pool) keepalive(alias string, client *ssh.Client) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
			p.drop(alias, client)
			return
		}
	}
}

func (p *Pool) dial(cfg *HostConfig, visiting map[string]bool) (*ssh.Client, error) {
	addr := net.JoinHostPort(cfg.HostName, strconv.Itoa(cfg.Port))
	cc := &ssh.ClientConfig{
		User:    cfg.User,
		Auth:    p.authMethods(cfg),
		Timeout: dialTimeout,
	}
	if hkc, err := p.hostKeyCallback(); err != nil {
		return nil, err
	} else {
		cc.HostKeyCallback = hkc
	}
	if cfg.ProxyJump != "" {
		if visiting[cfg.ProxyJump] {
			return nil, fmt.Errorf("ProxyJump cycle at %q", cfg.ProxyJump)
		}
		jump, err := p.Client(cfg.ProxyJump)
		if err != nil {
			return nil, fmt.Errorf("via jump host %s: %w", cfg.ProxyJump, err)
		}
		visiting[cfg.ProxyJump] = true
		nc, err := jump.Dial("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("dial %s via jump host: %w", addr, err)
		}
		c, chans, reqs, err := ssh.NewClientConn(nc, addr, cc)
		if err != nil {
			nc.Close()
			return nil, err
		}
		return ssh.NewClient(c, chans, reqs), nil
	}
	return ssh.Dial("tcp", addr, cc)
}

func (p *Pool) hostKeyCallback() (ssh.HostKeyCallback, error) {
	if p.Insecure {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	path := filepath.Join(homeDir(), ".ssh", "known_hosts")
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("no known_hosts file at %s; add one or start with --insecure", path)
	}
	return knownhosts.New(path)
}

func (p *Pool) authMethods(cfg *HostConfig) []ssh.AuthMethod {
	var methods []ssh.AuthMethod
	if signers := agentSigners(); len(signers) > 0 {
		methods = append(methods, ssh.PublicKeysCallback(func() ([]ssh.Signer, error) { return agentSigners(), nil }))
	}
	var keys []ssh.Signer
	for _, path := range cfg.IdentityFiles {
		path = expandHome(path)
		pem, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		signer, err := ssh.ParsePrivateKey(pem)
		if err != nil {
			continue // encrypted or not a private key; skip silently
		}
		keys = append(keys, signer)
	}
	if len(keys) > 0 {
		methods = append(methods, ssh.PublicKeys(keys...))
	}
	if pw, ok := LookupPassword(cfg.Alias, cfg.HostName); ok {
		methods = append(methods, ssh.Password(pw))
	}
	return methods
}

func agentSigners() []ssh.Signer {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil
	}
	signers, err := agent.NewClient(conn).Signers()
	if err != nil {
		return nil
	}
	return signers
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "/"
	}
	return h
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		return filepath.Join(homeDir(), strings.TrimPrefix(path, "~"))
	}
	return path
}

func currentUser() string {
	u, err := user.Current()
	if err != nil {
		return os.Getenv("USER")
	}
	return u.Username
}

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

// Backoff escalation for failed dials: 5s -> 15s -> 45s. Rapid retries feed
// sshd's PerSourcePenalties and turn a diagnosable auth error into an
// unreadable "handshake EOF" lockout, so we refuse to hammer.
var (
	BackoffBase = 5 * time.Second
	BackoffMax  = 45 * time.Second
)

// Pool holds one persistent SSH connection (plus an SFTP client) per host
// alias, reconnected on demand and evicted when the keepalive fails.
type Pool struct {
	Insecure bool

	mu         sync.Mutex
	conns      map[string]*pconn
	failCount  map[string]int
	lastFail   map[string]time.Time
	lastFailEr map[string]error
}

type pconn struct {
	client *ssh.Client
	sftp   *sftp.Client
}

func NewPool(insecure bool) *Pool {
	return &Pool{
		Insecure:   insecure,
		conns:      make(map[string]*pconn),
		failCount:  make(map[string]int),
		lastFail:   make(map[string]time.Time),
		lastFailEr: make(map[string]error),
	}
}

func (p *Pool) Client(alias string) (*ssh.Client, error) {
	if c := p.get(alias); c != nil {
		return c.client, nil
	}
	if wait, n := p.backoffRemaining(alias); wait > 0 {
		return nil, fmt.Errorf("connect to %s: backing off %ds after %d consecutive failures (rapid retries feed sshd PerSourcePenalties); last error: %v",
			alias, int(wait.Seconds()), n, p.lastFailError(alias))
	}
	cfg, err := ResolveHost(alias)
	if err != nil {
		return nil, err
	}
	methods, keys := p.authMethods(cfg)
	VLog.Printf("connect %s: user=%s %s:%d identity=%v proxyjump=%q agent_keys=%d",
		alias, cfg.User, cfg.HostName, cfg.Port, cfg.IdentityFiles, cfg.ProxyJump, countOK(keys))
	client, err := p.dial(cfg, map[string]bool{cfg.Alias: true}, methods)
	if err != nil {
		p.recordFailure(alias, err)
		return nil, fmt.Errorf("connect to %s: %w%s", alias, err, diagSuffix(cfg, keys))
	}
	p.resetFailures(alias)
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

// backoffRemaining returns how long to wait before the next dial attempt for
// alias, and the current consecutive-failure count.
func (p *Pool) backoffRemaining(alias string) (time.Duration, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := p.failCount[alias]
	if n == 0 {
		return 0, 0
	}
	d := backoffDelay(n)
	if elapsed := time.Since(p.lastFail[alias]); elapsed >= d {
		return 0, n
	}
	return d - time.Since(p.lastFail[alias]), n
}

func backoffDelay(n int) time.Duration {
	d := BackoffBase
	for i := 1; i < n && d < BackoffMax; i++ {
		d *= 3
	}
	if d > BackoffMax {
		d = BackoffMax
	}
	return d
}

func (p *Pool) recordFailure(alias string, err error) {
	p.mu.Lock()
	p.failCount[alias]++
	p.lastFail[alias] = time.Now()
	p.lastFailEr[alias] = err
	p.mu.Unlock()
	VLog.Printf("connect %s failed: %v", alias, err)
}

func (p *Pool) resetFailures(alias string) {
	p.mu.Lock()
	delete(p.failCount, alias)
	delete(p.lastFail, alias)
	delete(p.lastFailEr, alias)
	p.mu.Unlock()
}

func (p *Pool) lastFailError(alias string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastFailEr[alias]
}

// keyInfo records one candidate credential for diagnostics.
type keyInfo struct {
	Source      string // "~/.ssh/tx" or "ssh-agent"
	Fingerprint string // SHA256 fingerprint when usable
	Err         string // why it was skipped, when it was
}

func (p *Pool) authMethods(cfg *HostConfig) ([]ssh.AuthMethod, []keyInfo) {
	var methods []ssh.AuthMethod
	var keys []keyInfo

	if sock := os.Getenv("SSH_AUTH_SOCK"); sock == "" {
		keys = append(keys, keyInfo{Source: "ssh-agent", Err: "SSH_AUTH_SOCK not set"})
	} else if conn, err := net.Dial("unix", sock); err != nil {
		keys = append(keys, keyInfo{Source: "ssh-agent", Err: "unreachable: " + err.Error()})
	} else {
		signers, err := agent.NewClient(conn).Signers()
		if err != nil {
			keys = append(keys, keyInfo{Source: "ssh-agent", Err: "list failed: " + err.Error()})
		} else if len(signers) == 0 {
			keys = append(keys, keyInfo{Source: "ssh-agent", Err: "reachable but empty"})
		} else {
			methods = append(methods, ssh.PublicKeysCallback(func() ([]ssh.Signer, error) { return agentSigners(), nil }))
			for i, s := range signers {
				if i >= 5 {
					break // cap diagnostics; fingerprints only, never key material
				}
				keys = append(keys, keyInfo{Source: "ssh-agent", Fingerprint: ssh.FingerprintSHA256(s.PublicKey())})
			}
		}
	}

	for _, path := range cfg.IdentityFiles {
		path = expandHome(path)
		pem, err := os.ReadFile(path)
		if err != nil {
			keys = append(keys, keyInfo{Source: path, Err: "unreadable: " + err.Error()})
			continue
		}
		signer, err := ssh.ParsePrivateKey(pem)
		if err != nil {
			keys = append(keys, keyInfo{Source: path, Err: "skipped: " + err.Error()})
			continue
		}
		keys = append(keys, keyInfo{Source: path, Fingerprint: ssh.FingerprintSHA256(signer.PublicKey())})
		methods = append(methods, ssh.PublicKeys(signer))
	}

	if pw, ok := LookupPassword(cfg.Alias, cfg.HostName); ok {
		keys = append(keys, keyInfo{Source: "~/.ssh/ssh-mcp.json (password)", Fingerprint: "configured"})
		methods = append(methods, ssh.Password(pw))
	}
	return methods, keys
}

// diagSuffix appends the credential diagnostics to a connect error so the
// agent can tell "config not read" from "key rejected" at a glance.
func diagSuffix(cfg *HostConfig, keys []keyInfo) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "\n  resolved: user=%s %s:%d", cfg.User, cfg.HostName, cfg.Port)
	pwOK := false
	for _, k := range keys {
		if k.Err != "" {
			fmt.Fprintf(&sb, "\n  %s: %s", k.Source, k.Err)
			continue
		}
		if strings.Contains(k.Source, "password") {
			pwOK = true
		}
		fmt.Fprintf(&sb, "\n  %s: %s", k.Source, k.Fingerprint)
	}
	if !pwOK {
		fmt.Fprintf(&sb, "\n  hint: no password configured; set one with: ssh-mcp password set %s", cfg.Alias)
	}
	return sb.String()
}

func countOK(keys []keyInfo) int {
	n := 0
	for _, k := range keys {
		if k.Err == "" {
			n++
		}
	}
	return n
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

func (p *Pool) dial(cfg *HostConfig, visiting map[string]bool, methods []ssh.AuthMethod) (*ssh.Client, error) {
	addr := net.JoinHostPort(cfg.HostName, strconv.Itoa(cfg.Port))
	cc := &ssh.ClientConfig{
		User:    cfg.User,
		Auth:    methods,
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

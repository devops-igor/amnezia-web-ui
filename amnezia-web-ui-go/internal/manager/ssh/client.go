package ssh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/pkg/sftp"
	gossh "golang.org/x/crypto/ssh"
)

// SSHClient defines the contract for remote server communication via SSH and SFTP.
//
//nolint:revive
type SSHClient interface {
	RunCommand(ctx context.Context, cmd string) (stdout string, stderr string, exitCode int, err error)
	RunSudoCommand(ctx context.Context, cmd string) (stdout string, stderr string, exitCode int, err error)
	RunScript(ctx context.Context, script string) (stdout string, stderr string, exitCode int, err error)
	RunSudoScript(ctx context.Context, script string) (stdout string, stderr string, exitCode int, err error)
	UploadFile(ctx context.Context, remotePath string, content []byte, mode os.FileMode) error
	UploadSudoFile(ctx context.Context, remotePath string, content []byte, mode os.FileMode) error
	DownloadFile(ctx context.Context, remotePath string) ([]byte, error)
	FileExists(ctx context.Context, remotePath string) (bool, error)
	TestConnection(ctx context.Context) (string, error)
	Close() error
	IsAlive() bool
	GetUnderlyingClient() *gossh.Client
	GetHost() string
	GetPort() int
	GetUser() string
	GetServerID() *int64
	GetLastActive() time.Time
}

// Config specifies the connection parameters for an SSH client.
type Config struct {
	Host                      string
	Port                      int
	User                      string
	Password                  string
	PrivateKey                string
	Passphrase                string
	Timeout                   time.Duration
	KeepAlivePeriod           time.Duration
	ServerID                  *int64
	Store                     HostKeyStore
	RequireFingerprintConfirm bool
	HostKeyCallback           gossh.HostKeyCallback
}

// Client implements the SSHClient interface with pooling, sudo, and SFTP support.
type Client struct {
	cfg                 Config
	sshClient           *gossh.Client
	sftpClient          *sftp.Client
	isRoot              bool
	capturedFingerprint string
	lastActive          time.Time
	closed              bool
	mu                  sync.Mutex
}

// NewClient initializes an unconnected Client with the given configuration.
func NewClient(cfg Config) *Client {
	if cfg.Port <= 0 {
		cfg.Port = 22
	}
	if cfg.User == "" {
		cfg.User = "root"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.KeepAlivePeriod <= 0 {
		cfg.KeepAlivePeriod = 30 * time.Second
	}

	return &Client{
		cfg:        cfg,
		isRoot:     cfg.User == "root",
		lastActive: time.Now(),
	}
}

// Dial creates a new Client and immediately establishes an SSH connection.
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	c := NewClient(cfg)
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// DialFromServer constructs a Client from a models.Server instance and establishes connection.
func DialFromServer(ctx context.Context, server *models.Server, store HostKeyStore) (*Client, error) {
	if server == nil {
		return nil, errors.New("server cannot be nil")
	}

	cfg := Config{
		Host:            server.Host,
		Port:            server.SSHPort,
		User:            server.SSHUser,
		Password:        server.SSHPass,
		PrivateKey:      server.SSHKey,
		ServerID:        &server.ID,
		Store:           store,
		Timeout:         15 * time.Second,
		KeepAlivePeriod: 30 * time.Second,
	}

	return Dial(ctx, cfg)
}

// Connect establishes the SSH transport connection and completes the handshake.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.sshClient != nil && !c.closed {
		return nil
	}

	if c.cfg.Host == "" {
		return errors.New("host is required")
	}

	authMethods, err := BuildAuthMethods(c.cfg.Password, c.cfg.PrivateKey, c.cfg.Passphrase)
	if err != nil {
		return fmt.Errorf("failed to configure authentication: %w", err)
	}

	hostKeyCB := c.cfg.HostKeyCallback
	if hostKeyCB == nil {
		hostKeyCB = NewHostKeyCallback(ctx, HostKeyCallbackOptions{
			ServerID:               c.cfg.ServerID,
			Store:                  c.cfg.Store,
			RequireConfirm:         c.cfg.RequireFingerprintConfirm,
			CapturedFingerprintOut: &c.capturedFingerprint,
		})
	}

	sshConfig := &gossh.ClientConfig{
		User:            c.cfg.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCB,
		Timeout:         c.cfg.Timeout,
	}

	addr := net.JoinHostPort(c.cfg.Host, fmt.Sprintf("%d", c.cfg.Port))

	dialer := net.Dialer{Timeout: c.cfg.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", addr, err)
	}

	sshConn, chans, reqs, err := gossh.NewClientConn(conn, addr, sshConfig)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("ssh handshake failed with %s: %w", addr, err)
	}

	if c.sftpClient != nil {
		_ = c.sftpClient.Close()
		c.sftpClient = nil
	}

	c.sshClient = gossh.NewClient(sshConn, chans, reqs)
	c.closed = false
	c.lastActive = time.Now()

	return nil
}

// getSFTP returns an initialized SFTP client instance, creating one if not present or refreshing if stale.
func (c *Client) getSFTP() (*sftp.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.sshClient == nil {
		return nil, ErrNotConnected
	}

	if c.sftpClient != nil {
		// Verify if cached SFTP client session is still healthy
		if _, err := c.sftpClient.Getwd(); err == nil {
			return c.sftpClient, nil
		}
		// Stale / broken SFTP client session -> close and recreate
		_ = c.sftpClient.Close()
		c.sftpClient = nil
	}

	sftpClient, err := sftp.NewClient(c.sshClient)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize sftp client: %w", err)
	}
	c.sftpClient = sftpClient
	return c.sftpClient, nil
}

// RunCommand executes a command remotely and returns stdout, stderr, and exit code.
func (c *Client) RunCommand(ctx context.Context, cmd string) (string, string, int, error) {
	c.touch()
	c.mu.Lock()
	client := c.sshClient
	c.mu.Unlock()

	if client == nil {
		return "", "", -1, ErrNotConnected
	}

	return RunSession(ctx, client, cmd, nil)
}

// RunSudoCommand executes a command with sudo privileges, delivering passwords via stdin safely.
func (c *Client) RunSudoCommand(ctx context.Context, cmd string) (string, string, int, error) {
	c.touch()
	cleanCmd := CleanSudoCommand(cmd)
	if c.isRoot {
		return c.RunCommand(ctx, cleanCmd)
	}

	formattedCmd, stdinInput := FormatSudoCommand(cleanCmd, c.cfg.Password, false)

	c.mu.Lock()
	client := c.sshClient
	c.mu.Unlock()

	if client == nil {
		return "", "", -1, ErrNotConnected
	}

	var stdinReader *strings.Reader
	if stdinInput != "" {
		stdinReader = strings.NewReader(stdinInput)
	}

	return RunSession(ctx, client, formattedCmd, stdinReader)
}

// RunScript executes a multi-line shell script via bash stdin.
func (c *Client) RunScript(ctx context.Context, script string) (string, string, int, error) {
	c.touch()
	c.mu.Lock()
	client := c.sshClient
	c.mu.Unlock()

	if client == nil {
		return "", "", -1, ErrNotConnected
	}

	normalized := NormalizeLineEndings([]byte(script))
	return RunSession(ctx, client, "/bin/bash -s", strings.NewReader(string(normalized)))
}

// RunSudoScript executes a multi-line script with root privileges.
func (c *Client) RunSudoScript(ctx context.Context, script string) (string, string, int, error) {
	c.touch()
	if c.isRoot {
		return c.RunScript(ctx, script)
	}

	// Write script to /tmp via SFTP, then execute via sudo
	h := sha256.Sum256([]byte(script))
	tmpScript := fmt.Sprintf("/tmp/_amnz_script_%s.sh", hex.EncodeToString(h[:4]))

	normContent := NormalizeLineEndings([]byte(script))
	if err := c.UploadFile(ctx, tmpScript, normContent, 0700); err != nil {
		return "", "", -1, fmt.Errorf("failed to upload sudo script to %s: %w", tmpScript, err)
	}
	defer func() {
		_, _, _, _ = c.RunSudoCommand(ctx, fmt.Sprintf("rm -f %s", EscapeShellArg(tmpScript)))
	}()

	execCmd := fmt.Sprintf("/bin/bash %s", EscapeShellArg(tmpScript))
	return c.RunSudoCommand(ctx, execCmd)
}

// UploadFile uploads content to a remote path via SFTP.
func (c *Client) UploadFile(ctx context.Context, remotePath string, content []byte, mode os.FileMode) error {
	c.touch()
	sftpClient, err := c.getSFTP()
	if err != nil {
		return err
	}
	return SFTPUpload(ctx, sftpClient, remotePath, content, mode)
}

// UploadSudoFile uploads content to a root-owned remote path via atomic temp write and sudo move.
func (c *Client) UploadSudoFile(ctx context.Context, remotePath string, content []byte, mode os.FileMode) error {
	c.touch()
	if c.isRoot {
		return c.UploadFile(ctx, remotePath, content, mode)
	}

	tmpPath := GenerateRandomTempPath("upload")
	if err := c.UploadFile(ctx, tmpPath, content, 0600); err != nil {
		return fmt.Errorf("failed to upload temporary file: %w", err)
	}

	defer func() {
		_, _, _, _ = c.RunSudoCommand(ctx, fmt.Sprintf("rm -f %s", EscapeShellArg(tmpPath)))
	}()

	targetDir := path.Dir(remotePath)
	if _, _, code, err := c.RunSudoCommand(ctx, fmt.Sprintf("mkdir -p %s", EscapeShellArg(targetDir))); err != nil || code != 0 {
		return fmt.Errorf("failed to create target directory %s (code %d): %w", targetDir, code, err)
	}

	mvCmd := fmt.Sprintf("mv %s %s", EscapeShellArg(tmpPath), EscapeShellArg(remotePath))
	if _, stderr, code, err := c.RunSudoCommand(ctx, mvCmd); err != nil || code != 0 {
		return fmt.Errorf("failed to move file to %s (code %d, err: %s): %w", remotePath, code, stderr, err)
	}

	chmodCmd := fmt.Sprintf("chmod %o %s", mode.Perm(), EscapeShellArg(remotePath))
	if _, stderr, code, err := c.RunSudoCommand(ctx, chmodCmd); err != nil || code != 0 {
		return fmt.Errorf("failed to chmod file %s (code %d, err: %s): %w", remotePath, code, stderr, err)
	}

	chownCmd := fmt.Sprintf("chown root:root %s", EscapeShellArg(remotePath))
	if _, stderr, code, err := c.RunSudoCommand(ctx, chownCmd); err != nil || code != 0 {
		return fmt.Errorf("failed to chown file %s (code %d, err: %s): %w", remotePath, code, stderr, err)
	}

	return nil
}

// DownloadFile downloads the complete content of a remote file via SFTP.
func (c *Client) DownloadFile(ctx context.Context, remotePath string) ([]byte, error) {
	c.touch()
	sftpClient, err := c.getSFTP()
	if err != nil {
		return nil, err
	}
	return SFTPDownload(ctx, sftpClient, remotePath)
}

// FileExists checks if a remote file or directory exists via SFTP.
func (c *Client) FileExists(ctx context.Context, remotePath string) (bool, error) {
	c.touch()
	sftpClient, err := c.getSFTP()
	if err != nil {
		return false, err
	}
	return SFTPFileExists(ctx, sftpClient, remotePath)
}

// TestConnection verifies connectivity and returns OS and kernel details.
func (c *Client) TestConnection(ctx context.Context) (string, error) {
	stdout, stderr, code, err := c.RunCommand(ctx, "uname -sr && cat /etc/os-release 2>/dev/null | head -2")
	if err != nil {
		return "", fmt.Errorf("connection test failed: %w", err)
	}
	if code != 0 {
		return "", fmt.Errorf("connection test returned non-zero code %d: %s", code, stderr)
	}
	return stdout, nil
}

// IsAlive checks if the SSH transport connection is alive by sending a keepalive global request.
func (c *Client) IsAlive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.sshClient == nil {
		return false
	}

	_, _, err := c.sshClient.SendRequest("keepalive@openssh.com", true, nil)
	return err == nil
}

// Close terminates the SFTP and SSH connections.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.closed = true
	var errs []error

	if c.sftpClient != nil {
		if err := c.sftpClient.Close(); err != nil {
			errs = append(errs, err)
		}
		c.sftpClient = nil
	}

	if c.sshClient != nil {
		if err := c.sshClient.Close(); err != nil {
			errs = append(errs, err)
		}
		c.sshClient = nil
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// GetUnderlyingClient returns the raw *golang.org/x/crypto/ssh.Client.
func (c *Client) GetUnderlyingClient() *gossh.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sshClient
}

// GetHost returns the configured host address.
func (c *Client) GetHost() string {
	return c.cfg.Host
}

// GetPort returns the configured SSH port.
func (c *Client) GetPort() int {
	return c.cfg.Port
}

// GetUser returns the configured SSH username.
func (c *Client) GetUser() string {
	return c.cfg.User
}

// GetServerID returns the associated database server ID if configured.
func (c *Client) GetServerID() *int64 {
	return c.cfg.ServerID
}

// GetLastActive returns the timestamp of the last executed operation.
func (c *Client) GetLastActive() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastActive
}

// touch updates the last active timestamp.
func (c *Client) touch() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastActive = time.Now()
}

// CapturedFingerprint returns the host key fingerprint captured during handshake.
func (c *Client) CapturedFingerprint() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.capturedFingerprint
}

// Fingerprint is an alias for CapturedFingerprint.
func (c *Client) Fingerprint() string {
	return c.CapturedFingerprint()
}

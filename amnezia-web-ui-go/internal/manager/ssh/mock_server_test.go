package ssh

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	gossh "golang.org/x/crypto/ssh"
)

type CommandHandler func(cmd string, stdin []byte) (stdout, stderr string, exitCode int)

type MockSSHServer struct {
	t            *testing.T
	listener     net.Listener
	hostKey      gossh.Signer
	user         string
	password     string
	authKey      gossh.PublicKey
	baseDir      string
	mu           sync.RWMutex
	handlers     map[string]CommandHandler
	activeConns  map[net.Conn]struct{}
	closed       bool
	hangCommands bool
}

func NewMockSSHServer(t *testing.T, user, password string) *MockSSHServer {
	t.Helper()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate host key: %v", err)
	}

	hostSigner, err := gossh.NewSignerFromKey(rsaKey)
	if err != nil {
		t.Fatalf("failed to create host signer: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on local port: %v", err)
	}

	baseDir := t.TempDir()

	s := &MockSSHServer{
		t:           t,
		listener:    listener,
		hostKey:     hostSigner,
		user:        user,
		password:    password,
		baseDir:     baseDir,
		handlers:    make(map[string]CommandHandler),
		activeConns: make(map[net.Conn]struct{}),
	}

	s.setupDefaultHandlers()
	go s.acceptLoop()

	return s
}

func (s *MockSSHServer) SetAuthorizedKey(pub gossh.PublicKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authKey = pub
}

func (s *MockSSHServer) SetCommandHandler(prefix string, h CommandHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[prefix] = h
}

func (s *MockSSHServer) SetHangCommands(hang bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hangCommands = hang
}

func (s *MockSSHServer) Addr() string {
	return s.listener.Addr().String()
}

func (s *MockSSHServer) Host() string {
	addr := s.listener.Addr().(*net.TCPAddr)
	return addr.IP.String()
}

func (s *MockSSHServer) Port() int {
	addr := s.listener.Addr().(*net.TCPAddr)
	return addr.Port
}

func (s *MockSSHServer) PublicKey() gossh.PublicKey {
	return s.hostKey.PublicKey()
}

func (s *MockSSHServer) Fingerprint() string {
	return FingerprintSHA256(s.hostKey.PublicKey())
}

func (s *MockSSHServer) BaseDir() string {
	return s.baseDir
}

func (s *MockSSHServer) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	_ = s.listener.Close()

	for conn := range s.activeConns {
		_ = conn.Close()
	}
	s.mu.Unlock()
}

func (s *MockSSHServer) DisconnectAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for conn := range s.activeConns {
		_ = conn.Close()
		delete(s.activeConns, conn)
	}
}

func (s *MockSSHServer) setupDefaultHandlers() {
	s.handlers["uname -sr && cat /etc/os-release 2>/dev/null | head -2"] = func(cmd string, stdin []byte) (string, string, int) {
		return "Linux 6.8.0-generic\nPRETTY_NAME=\"Ubuntu 24.04 LTS\"", "", 0
	}

	s.handlers["uname -s && uname -n && uname -r && uname -m"] = func(cmd string, stdin []byte) (string, string, int) {
		return "Linux\ntest-host\n6.8.0-generic\nx86_64", "", 0
	}

	s.handlers[`cat /etc/os-release 2>/dev/null | grep -E '^PRETTY_NAME=' | head -1 | cut -d= -f2- | tr -d '"'`] = func(cmd string, stdin []byte) (string, string, int) {
		return "Ubuntu 24.04 LTS", "", 0
	}

	s.handlers[`cat /proc/uptime 2>/dev/null | awk '{print $1}'`] = func(cmd string, stdin []byte) (string, string, int) {
		return "54321.00", "", 0
	}

	s.handlers["docker info"] = func(cmd string, stdin []byte) (string, string, int) {
		return "Containers: 2\nRunning: 2", "", 0
	}

	s.handlers["command -v apt-get"] = func(cmd string, stdin []byte) (string, string, int) {
		return "/usr/bin/apt-get", "", 0
	}
}

func (s *MockSSHServer) acceptLoop() {
	config := &gossh.ServerConfig{
		PasswordCallback: func(conn gossh.ConnMetadata, pass []byte) (*gossh.Permissions, error) {
			s.mu.RLock()
			expectedUser := s.user
			expectedPass := s.password
			s.mu.RUnlock()

			if conn.User() == expectedUser && string(pass) == expectedPass {
				return nil, nil
			}
			return nil, fmt.Errorf("password rejected for %q", conn.User())
		},
		PublicKeyCallback: func(conn gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
			s.mu.RLock()
			authKey := s.authKey
			s.mu.RUnlock()

			if authKey != nil && bytes.Equal(key.Marshal(), authKey.Marshal()) {
				return nil, nil
			}
			return nil, fmt.Errorf("public key rejected")
		},
		KeyboardInteractiveCallback: func(conn gossh.ConnMetadata, client gossh.KeyboardInteractiveChallenge) (*gossh.Permissions, error) {
			s.mu.RLock()
			expectedPass := s.password
			s.mu.RUnlock()

			answers, err := client("", "", []string{"Password:"}, []bool{false})
			if err != nil {
				return nil, err
			}
			if len(answers) == 1 && answers[0] == expectedPass {
				return nil, nil
			}
			return nil, fmt.Errorf("keyboard interactive failed")
		},
	}
	config.AddHostKey(s.hostKey)

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}

		s.mu.Lock()
		if s.closed {
			_ = conn.Close()
			s.mu.Unlock()
			return
		}
		s.activeConns[conn] = struct{}{}
		s.mu.Unlock()

		go s.handleConn(conn, config)
	}
}

func (s *MockSSHServer) handleConn(netConn net.Conn, config *gossh.ServerConfig) {
	defer func() {
		s.mu.Lock()
		delete(s.activeConns, netConn)
		s.mu.Unlock()
		_ = netConn.Close()
	}()

	sshConn, chans, reqs, err := gossh.NewServerConn(netConn, config)
	if err != nil {
		return
	}
	defer sshConn.Close()

	// Discard or handle global requests
	go func() {
		for req := range reqs {
			if req.Type == "keepalive@openssh.com" {
				_ = req.Reply(true, nil)
			} else {
				_ = req.Reply(false, nil)
			}
		}
	}()

	for newChannel := range chans {
		switch newChannel.ChannelType() {
		case "session":
			channel, requests, err := newChannel.Accept()
			if err != nil {
				continue
			}
			go s.handleSession(channel, requests)
		default:
			_ = newChannel.Reject(gossh.UnknownChannelType, "unsupported channel type")
		}
	}
}

func (s *MockSSHServer) handleSession(ch gossh.Channel, reqs <-chan *gossh.Request) {
	defer ch.Close()

	for req := range reqs {
		switch req.Type {
		case "exec":
			s.mu.RLock()
			hang := s.hangCommands
			s.mu.RUnlock()

			if hang {
				_ = req.Reply(true, nil)
				time.Sleep(10 * time.Second)
				return
			}

			if len(req.Payload) < 4 {
				_ = req.Reply(false, nil)
				return
			}
			cmdLen := binary.BigEndian.Uint32(req.Payload[:4])
			cmd := string(req.Payload[4 : 4+cmdLen])
			_ = req.Reply(true, nil)

			// Read stdin synchronously if command expects stdin (sudo -S or bash script)
			var stdinBytes []byte
			if strings.Contains(cmd, "-S") {
				var lineBuf bytes.Buffer
				b := make([]byte, 1)
				for {
					n, err := ch.Read(b)
					if n > 0 {
						lineBuf.WriteByte(b[0])
						if b[0] == '\n' {
							break
						}
					}
					if err != nil {
						break
					}
				}
				stdinBytes = lineBuf.Bytes()
			} else if strings.Contains(cmd, "bash -s") || strings.Contains(cmd, "/bin/bash -s") {
				var buf bytes.Buffer
				b := make([]byte, 1024)
				for {
					n, err := ch.Read(b)
					if n > 0 {
						buf.Write(b[:n])
					}
					if err != nil {
						break
					}
				}
				stdinBytes = buf.Bytes()
			}

			stdout, stderr, exitCode := s.dispatchCommand(cmd, stdinBytes)

			if stdout != "" {
				_, _ = ch.Write([]byte(stdout + "\n"))
			}
			if stderr != "" {
				_, _ = ch.Stderr().Write([]byte(stderr + "\n"))
			}

			// Send exit status
			statusPayload := make([]byte, 4)
			binary.BigEndian.PutUint32(statusPayload, uint32(exitCode))
			_, _ = ch.SendRequest("exit-status", false, statusPayload)
			return

		case "subsystem":
			if len(req.Payload) < 4 {
				_ = req.Reply(false, nil)
				return
			}
			subLen := binary.BigEndian.Uint32(req.Payload[:4])
			subsystem := string(req.Payload[4 : 4+subLen])

			if subsystem == "sftp" {
				_ = req.Reply(true, nil)
				s.handleSFTP(ch)
				return
			}
			_ = req.Reply(false, nil)

		case "shell":
			_ = req.Reply(true, nil)
			// Read shell script from stdin
			scriptBytes, _ := io.ReadAll(ch)
			stdout, stderr, exitCode := s.dispatchCommand(string(scriptBytes), scriptBytes)
			if stdout != "" {
				_, _ = ch.Write([]byte(stdout + "\n"))
			}
			if stderr != "" {
				_, _ = ch.Stderr().Write([]byte(stderr + "\n"))
			}
			statusPayload := make([]byte, 4)
			binary.BigEndian.PutUint32(statusPayload, uint32(exitCode))
			_, _ = ch.SendRequest("exit-status", false, statusPayload)
			return

		case "pty-req", "env":
			_ = req.Reply(true, nil)
		default:
			_ = req.Reply(false, nil)
		}
	}
}

func (s *MockSSHServer) dispatchCommand(cmd string, stdin []byte) (stdout, stderr string, exitCode int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	trimmed := strings.TrimSpace(cmd)

	// 1. Check exact or prefix handlers
	for pattern, handler := range s.handlers {
		if trimmed == pattern || strings.HasPrefix(trimmed, pattern) {
			return handler(cmd, stdin)
		}
	}

	// 2. Direct echo commands
	if strings.HasPrefix(trimmed, "echo ") && !strings.Contains(trimmed, "| sudo") {
		arg := strings.TrimPrefix(trimmed, "echo ")
		arg = strings.Trim(arg, `"'`)
		return arg, "", 0
	}

	// 3. Sudo commands
	if strings.HasPrefix(trimmed, "sudo ") || strings.Contains(trimmed, "sudo -S") || strings.Contains(trimmed, "sudo -n") {
		// Sudo password validation if non-root
		if strings.Contains(cmd, "-S") {
			pass := strings.TrimSpace(string(stdin))
			if s.password != "" && pass != s.password && !strings.Contains(cmd, s.password) {
				return "", "sudo: 1 incorrect password attempt", 1
			}
		}
		// Echo or simple commands in sudo
		if strings.Contains(cmd, "echo") {
			return "success", "", 0
		}
		if strings.Contains(cmd, "mv ") || strings.Contains(cmd, "mkdir ") || strings.Contains(cmd, "chmod ") || strings.Contains(cmd, "chown ") || strings.Contains(cmd, "rm ") {
			return "", "", 0
		}
		return "sudo executed", "", 0
	}

	// 4. Explicit test exit codes
	if strings.Contains(trimmed, "exit 42") {
		return "", "custom error", 42
	}

	if strings.Contains(trimmed, "exit 1") {
		return "", "failure", 1
	}

	// 5. Command existence checks (command -v ...)
	if strings.HasPrefix(trimmed, "command -v") {
		return "", "not found", 1
	}

	// 6. Default fallback
	return "", "", 0
}

func (s *MockSSHServer) handleSFTP(ch gossh.Channel) {
	// Root the SFTP server to our isolated temp directory
	server, err := sftp.NewServer(ch, sftp.WithDebug(io.Discard))
	if err != nil {
		return
	}
	if err := server.Serve(); err != nil && !errors.Is(err, io.EOF) {
		_ = server.Close()
	}
}

// Helper to write files to mock server filesystem
func (s *MockSSHServer) WriteFile(relPath string, data []byte, perm os.FileMode) error {
	fullPath := filepath.Join(s.baseDir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(fullPath, data, perm)
}

// Helper to read files from mock server filesystem
func (s *MockSSHServer) ReadFile(relPath string) ([]byte, error) {
	fullPath := filepath.Join(s.baseDir, relPath)
	return os.ReadFile(fullPath)
}

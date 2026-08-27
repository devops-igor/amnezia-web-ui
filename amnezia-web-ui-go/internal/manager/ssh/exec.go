package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	gossh "golang.org/x/crypto/ssh"
)

var (
	// ErrNotConnected is returned when attempting an operation without an active SSH connection.
	ErrNotConnected = errors.New("not connected to SSH server")
	// ErrCommandTimeout is returned when a command execution exceeds its context deadline.
	ErrCommandTimeout = errors.New("command execution timed out")
)

// EscapeShellArg safely quotes a string for use as a single argument in a POSIX shell command line.
// Null bytes (\x00) are stripped to prevent argument truncation attacks.
func EscapeShellArg(arg string) string {
	sanitized := strings.ReplaceAll(arg, "\x00", "")
	if sanitized == "" {
		return "''"
	}
	// Replace single quotes with '\'', and enclose the whole string in single quotes
	return "'" + strings.ReplaceAll(sanitized, "'", `'\''`) + "'"
}

// CleanSudoCommand removes leading "sudo " prefix and redundant whitespace.
func CleanSudoCommand(cmd string) string {
	trimmed := strings.TrimSpace(cmd)
	for strings.HasPrefix(trimmed, "sudo ") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "sudo "))
	}
	return trimmed
}

// FormatSudoCommand prepares the shell command and stdin password for sudo execution.
func FormatSudoCommand(cmd, password string, isRoot bool) (formattedCmd string, stdinInput string) {
	cleanCmd := CleanSudoCommand(cmd)
	if isRoot {
		return cleanCmd, ""
	}

	escapedCmd := EscapeShellArg(cleanCmd)
	if password != "" {
		// Use -S (read password from stdin) and -p '' (no prompt)
		formattedCmd = fmt.Sprintf("sudo -S -p '' -- /bin/bash -c %s", escapedCmd)
		stdinInput = password + "\n"
	} else {
		// Non-root with passwordless sudo
		formattedCmd = fmt.Sprintf("sudo -n -p '' -- /bin/bash -c %s", escapedCmd)
		stdinInput = ""
	}

	return formattedCmd, stdinInput
}

// SafeBuffer is a thread-safe bytes.Buffer.
type SafeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *SafeBuffer) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *SafeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// RunSession executes a command on an SSH client with context cancellation, stdin support, and exit code capture.
func RunSession(ctx context.Context, client *gossh.Client, cmd string, stdin io.Reader) (stdout string, stderr string, exitCode int, err error) {
	if client == nil {
		return "", "", -1, ErrNotConnected
	}

	if err := ctx.Err(); err != nil {
		return "", "", -1, err
	}

	session, err := client.NewSession()
	if err != nil {
		return "", "", -1, fmt.Errorf("failed to open SSH session: %w", err)
	}
	defer session.Close()

	var stdoutBuf SafeBuffer
	var stderrBuf SafeBuffer

	session.Stdout = &stdoutBuf
	session.Stderr = &stderrBuf

	if stdin != nil {
		session.Stdin = stdin
	}

	// Goroutine to handle context cancellation / timeout
	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			_ = session.Signal(gossh.SIGKILL)
			_ = session.Close()
		case <-done:
		}
	}()

	runErr := session.Run(cmd)

	outStr := strings.TrimSpace(stdoutBuf.String())
	errStr := strings.TrimSpace(stderrBuf.String())

	// Check if context was canceled during execution
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return outStr, errStr, -1, fmt.Errorf("%w: %v", ErrCommandTimeout, ctxErr)
		}
		return outStr, errStr, -1, ctxErr
	}

	if runErr != nil {
		var exitErr *gossh.ExitError
		if errors.As(runErr, &exitErr) {
			return outStr, errStr, exitErr.ExitStatus(), nil
		}
		var exitMissing *gossh.ExitMissingError
		if errors.As(runErr, &exitMissing) {
			return outStr, errStr, -1, exitMissing
		}
		return outStr, errStr, -1, runErr
	}

	return outStr, errStr, 0, nil
}

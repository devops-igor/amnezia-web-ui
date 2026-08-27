package ssh

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	gossh "golang.org/x/crypto/ssh"
)

func TestSSHClientPool_GetAndReuse(t *testing.T) {
	server := NewMockSSHServer(t, "root", "pass")
	defer server.Close()

	ctx := context.Background()
	store := newMemoryHostKeyStore()

	pool := NewSSHClientPool(PoolConfig{
		IdleTimeout:     1 * time.Minute,
		KeepAlivePeriod: 10 * time.Second,
		SweepInterval:   30 * time.Second,
	}, store)
	defer pool.Close()

	sModel := &models.Server{
		ID:      300,
		Host:    server.Host(),
		SSHPort: server.Port(),
		SSHUser: "root",
		SSHPass: "pass",
	}

	// 1. First Get -> dials
	client1, err := pool.Get(ctx, sModel)
	if err != nil {
		t.Fatalf("first Get failed: %v", err)
	}
	if pool.Len() != 1 {
		t.Fatalf("expected pool length 1, got %d", pool.Len())
	}

	// 2. Second Get -> reuses same connection
	client2, err := pool.Get(ctx, sModel)
	if err != nil {
		t.Fatalf("second Get failed: %v", err)
	}

	if client1 != client2 {
		t.Fatal("expected pool to return cached client instance")
	}

	// 3. Command execution works
	out, _, code, err := client2.RunCommand(ctx, "echo reused")
	if err != nil || code != 0 || out != "reused" {
		t.Fatalf("command on pooled client failed: code=%d, err=%v, out=%s", code, err, out)
	}

	// 4. Nil server error
	if _, err := pool.Get(ctx, nil); err == nil {
		t.Fatal("expected error on nil server")
	}
}

func TestSSHClientPool_AutoReconnectOnDisconnect(t *testing.T) {
	server := NewMockSSHServer(t, "root", "pass")
	defer server.Close()

	ctx := context.Background()
	store := newMemoryHostKeyStore()

	pool := NewSSHClientPool(PoolConfig{
		IdleTimeout:     5 * time.Minute,
		KeepAlivePeriod: 100 * time.Millisecond,
	}, store)
	defer pool.Close()

	sModel := &models.Server{
		ID:      301,
		Host:    server.Host(),
		SSHPort: server.Port(),
		SSHUser: "root",
		SSHPass: "pass",
	}

	_, err := pool.Get(ctx, sModel)
	if err != nil {
		t.Fatalf("first Get failed: %v", err)
	}

	// Simulate broken connection / network drop
	server.DisconnectAll()

	// Wait briefly for TCP close propagation & ping loop
	time.Sleep(150 * time.Millisecond)

	// Next Get should detect dead client and re-dial automatically
	client2, err := pool.Get(ctx, sModel)
	if err != nil {
		t.Fatalf("reconnect Get failed: %v", err)
	}

	if !client2.IsAlive() {
		t.Fatal("expected reconnected client to be alive")
	}

	out, _, code, err := client2.RunCommand(ctx, "echo reconnected")
	if err != nil || code != 0 || out != "reconnected" {
		t.Fatalf("command failed on reconnected client: code=%d, err=%v, out=%s", code, err, out)
	}
}

func TestSSHClientPool_RemoveAndEviction(t *testing.T) {
	server := NewMockSSHServer(t, "root", "pass")
	defer server.Close()

	ctx := context.Background()
	store := newMemoryHostKeyStore()

	pool := NewSSHClientPool(PoolConfig{
		IdleTimeout:     100 * time.Millisecond,
		SweepInterval:   50 * time.Millisecond,
		KeepAlivePeriod: 500 * time.Millisecond,
	}, store)
	defer pool.Close()

	sModel := &models.Server{
		ID:      302,
		Host:    server.Host(),
		SSHPort: server.Port(),
		SSHUser: "root",
		SSHPass: "pass",
	}

	_, err := pool.Get(ctx, sModel)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if pool.Len() != 1 {
		t.Fatalf("expected len 1, got %d", pool.Len())
	}

	// Explicit Remove
	pool.Remove(302)
	if pool.Len() != 0 {
		t.Fatalf("expected len 0 after Remove, got %d", pool.Len())
	}

	// Re-add and test RemoveByKey
	_, err = pool.Get(ctx, sModel)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	pool.RemoveByKey(ServerPoolKey(sModel))
	if pool.Len() != 0 {
		t.Fatalf("expected len 0 after RemoveByKey, got %d", pool.Len())
	}

	// Re-add and test idle sweep
	_, err = pool.Get(ctx, sModel)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	// Wait for idle sweep (timeout is 100ms, sweep is 50ms)
	time.Sleep(250 * time.Millisecond)

	if pool.Len() != 0 {
		t.Fatalf("expected pool to be swept to 0, got %d", pool.Len())
	}
}

func TestSSHClientPool_Concurrency(t *testing.T) {
	server := NewMockSSHServer(t, "root", "pass")
	defer server.Close()

	ctx := context.Background()
	store := newMemoryHostKeyStore()

	pool := NewSSHClientPool(PoolConfig{
		IdleTimeout:     5 * time.Minute,
		KeepAlivePeriod: 30 * time.Second,
	}, store)
	defer pool.Close()

	var wg sync.WaitGroup
	errCh := make(chan error, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sModel := &models.Server{
				ID:      int64(400 + (idx % 3)), // 3 distinct servers pointing to mock server
				Host:    server.Host(),
				SSHPort: server.Port(),
				SSHUser: "root",
				SSHPass: "pass",
			}
			client, err := pool.Get(ctx, sModel)
			if err != nil {
				errCh <- fmt.Errorf("worker %d Get error: %w", idx, err)
				return
			}
			out, _, code, err := client.RunCommand(ctx, fmt.Sprintf("echo worker-%d", idx))
			if err != nil || code != 0 || out != fmt.Sprintf("worker-%d", idx) {
				errCh <- fmt.Errorf("worker %d run error (code %d, err %v): %s", idx, code, err, out)
				return
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent pool error: %v", err)
	}
}

func TestSSHClientPool_GetWithConfig(t *testing.T) {
	server := NewMockSSHServer(t, "root", "pass")
	defer server.Close()

	ctx := context.Background()
	store := newMemoryHostKeyStore()

	pool := NewSSHClientPool(PoolConfig{}, store)
	defer pool.Close()

	cfg := Config{
		Host:            server.Host(),
		Port:            server.Port(),
		User:            "root",
		Password:        "pass",
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	}

	client, err := pool.GetWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("GetWithConfig failed: %v", err)
	}

	if !client.IsAlive() {
		t.Fatal("expected client to be alive")
	}

	// Dial failure with invalid host
	_, err = pool.GetWithConfig(ctx, Config{
		Host:            "invalid-nonexistent-domain-test.local",
		Port:            22,
		Password:        "pass",
		Timeout:         100 * time.Millisecond,
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	})
	if err == nil {
		t.Fatal("expected error on unresolvable host")
	}
}

func TestSSHClientPool_Close(t *testing.T) {
	server := NewMockSSHServer(t, "root", "pass")
	defer server.Close()

	ctx := context.Background()
	store := newMemoryHostKeyStore()

	pool := NewSSHClientPool(PoolConfig{}, store)
	sModel := &models.Server{
		ID:      500,
		Host:    server.Host(),
		SSHPort: server.Port(),
		SSHUser: "root",
		SSHPass: "pass",
	}

	_, _ = pool.Get(ctx, sModel)
	if err := pool.Close(); err != nil {
		t.Fatalf("pool Close failed: %v", err)
	}

	// Calling Get after close should return ErrPoolClosed
	_, err := pool.Get(ctx, sModel)
	if err != ErrPoolClosed {
		t.Fatalf("expected ErrPoolClosed, got %v", err)
	}
}

func TestPoolKeyHelpers(t *testing.T) {
	k1 := PoolKey("", 0, "")
	if k1 != ":22:root" {
		t.Fatalf("expected :22:root, got %s", k1)
	}

	if ServerPoolKey(nil) != "" {
		t.Fatalf("expected empty key for nil server")
	}
}

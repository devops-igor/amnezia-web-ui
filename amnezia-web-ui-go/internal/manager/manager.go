package manager

import (
	"context"
	"fmt"
	"sync"

	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/ssh"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

// SSHClient aliases the SSHClient interface from the ssh package.
type SSHClient = ssh.SSHClient

// ProtocolManager defines the unified lifecycle contract for all VPN protocol backends.
type ProtocolManager interface {
	Protocol() string
	Install(ctx context.Context, server *models.Server, params map[string]any) error
	Uninstall(ctx context.Context, server *models.Server) error
	GetClients(ctx context.Context, server *models.Server) ([]map[string]any, error)
	AddClient(ctx context.Context, server *models.Server, clientParams map[string]any) (map[string]any, error)
	RemoveClient(ctx context.Context, server *models.Server, clientID string) error
	GetClientConfig(ctx context.Context, server *models.Server, clientID string) (string, error)
}

// Registry manages protocol manager implementations.
type Registry struct {
	mu       sync.RWMutex
	managers map[string]ProtocolManager
}

// NewRegistry creates an empty protocol manager registry.
func NewRegistry() *Registry {
	return &Registry{
		managers: make(map[string]ProtocolManager),
	}
}

// Register registers a protocol manager for a normalized protocol name.
func (r *Registry) Register(mgr ProtocolManager) {
	r.mu.Lock()
	defer r.mu.Unlock()
	proto := models.NormalizeProtocol(mgr.Protocol())
	r.managers[proto] = mgr
}

// Get retrieves a protocol manager by protocol name.
func (r *Registry) Get(proto string) (ProtocolManager, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	mgr, ok := r.managers[models.NormalizeProtocol(proto)]
	return mgr, ok
}

// List returns a list of registered protocol names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]string, 0, len(r.managers))
	for k := range r.managers {
		list = append(list, k)
	}
	return list
}

// MockProtocolManager provides a mock implementation for testing and baseline scaffolding.
type MockProtocolManager struct {
	ProtoName string
}

// NewMockProtocolManager creates a mock protocol manager for scaffolding.
func NewMockProtocolManager(protoName string) *MockProtocolManager {
	return &MockProtocolManager{ProtoName: protoName}
}

func (m *MockProtocolManager) Protocol() string {
	return m.ProtoName
}

func (m *MockProtocolManager) Install(ctx context.Context, server *models.Server, params map[string]any) error {
	if server == nil {
		return fmt.Errorf("server cannot be nil")
	}
	return nil
}

func (m *MockProtocolManager) Uninstall(ctx context.Context, server *models.Server) error {
	if server == nil {
		return fmt.Errorf("server cannot be nil")
	}
	return nil
}

func (m *MockProtocolManager) GetClients(ctx context.Context, server *models.Server) ([]map[string]any, error) {
	if server == nil {
		return nil, fmt.Errorf("server cannot be nil")
	}
	return []map[string]any{}, nil
}

func (m *MockProtocolManager) AddClient(ctx context.Context, server *models.Server, clientParams map[string]any) (map[string]any, error) {
	if server == nil {
		return nil, fmt.Errorf("server cannot be nil")
	}
	return map[string]any{"client_id": "mock-client-1"}, nil
}

func (m *MockProtocolManager) RemoveClient(ctx context.Context, server *models.Server, clientID string) error {
	if server == nil {
		return fmt.Errorf("server cannot be nil")
	}
	return nil
}

func (m *MockProtocolManager) GetClientConfig(ctx context.Context, server *models.Server, clientID string) (string, error) {
	if server == nil {
		return "", fmt.Errorf("server cannot be nil")
	}
	return "[Interface]\nPrivateKey = mock\n", nil
}

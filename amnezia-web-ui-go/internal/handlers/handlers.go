package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/devops-igor/amnezia-web-ui-go/internal/config"
	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/dns"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/mtproxyl"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/ssh"
	"github.com/devops-igor/amnezia-web-ui-go/internal/middleware"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/vpn"
)

// SSHPoolProvider defines an interface for getting and managing active SSH clients.
type SSHPoolProvider interface {
	Get(ctx context.Context, server *models.Server) (ssh.SSHClient, error)
	Remove(serverID int64)
}

// Dependencies holds all runtime dependencies required by the HTTP handlers.
type Dependencies struct {
	Config          *config.Config
	DB              *database.DB
	Registry        *manager.Registry
	SSHPool         SSHPoolProvider
	AWGManager      *awg.AWGManager
	MTProxyLManager *mtproxyl.MTProxyLManager
	DNSManager      *dns.DNSManager
	VPNService      *vpn.Service
}

// Handlers encapsulates all HTTP route handlers and business logic.
type Handlers struct {
	cfg         *config.Config
	db          *database.DB
	registry    *manager.Registry
	sshPool     SSHPoolProvider
	awgMgr      *awg.AWGManager
	mtproxylMgr *mtproxyl.MTProxyLManager
	dnsMgr      *dns.DNSManager
	vpnSvc      *vpn.Service
}

// NewHandlers creates a new Handlers instance with provided dependencies.
func NewHandlers(deps Dependencies) *Handlers {
	h := &Handlers{
		cfg:         deps.Config,
		db:          deps.DB,
		registry:    deps.Registry,
		sshPool:     deps.SSHPool,
		awgMgr:      deps.AWGManager,
		mtproxylMgr: deps.MTProxyLManager,
		dnsMgr:      deps.DNSManager,
		vpnSvc:      deps.VPNService,
	}

	if h.registry == nil {
		h.registry = manager.NewRegistry()
	}
	if h.awgMgr != nil {
		h.registry.Register(h.awgMgr)
	}
	if h.mtproxylMgr != nil {
		h.registry.Register(h.mtproxylMgr)
	}
	if h.dnsMgr != nil {
		h.registry.Register(h.dnsMgr)
	}

	return h
}

// JSON writes a typed JSON response with status code.
func (h *Handlers) JSON(w http.ResponseWriter, statusCode int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(data)
}

// JSONOK writes a 200 OK JSON response with {"status": "ok"} and optional extra key-values.
func (h *Handlers) JSONOK(w http.ResponseWriter, extra ...map[string]any) {
	resp := map[string]any{"status": "ok"}
	for _, m := range extra {
		for k, v := range m {
			resp[k] = v
		}
	}
	h.JSON(w, http.StatusOK, resp)
}

// JSONError writes a standard structured error JSON per 05-api-contract.md §1.3.
func (h *Handlers) JSONError(w http.ResponseWriter, statusCode int, errCode string, detail string) {
	middleware.WriteJSONError(w, statusCode, errCode, detail)
}

// JSONErrorWithFlag writes an error response with optional password_change_required flag.
func (h *Handlers) JSONErrorWithFlag(w http.ResponseWriter, statusCode int, errCode string, detail string, pwChangeReq bool) {
	middleware.WriteJSONErrorWithFlag(w, statusCode, errCode, detail, pwChangeReq)
}

// DecodeJSON decodes request body into the target value, validating maximum size.
func (h *Handlers) DecodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return errors.New("request body is empty")
	}
	defer r.Body.Close()
	// Limit body to 1MB
	lr := io.LimitReader(r.Body, 1048576)
	dec := json.NewDecoder(lr)
	return dec.Decode(v)
}

// GetSession extracts the authenticated SessionData from the request context.
func (h *Handlers) GetSession(r *http.Request) *models.SessionData {
	return middleware.GetSession(r.Context())
}

// GetLang extracts preferred language code from request cookie or defaults.
func (h *Handlers) GetLang(r *http.Request) string {
	if r != nil {
		if c, err := r.Cookie("lang"); err == nil && c.Value != "" {
			return c.Value
		}
		if c, err := r.Cookie("panel_lang"); err == nil && c.Value != "" {
			return c.Value
		}
	}
	return "en"
}

// Translate translates a key using request language.
func (h *Handlers) Translate(r *http.Request, key string) string {
	lang := h.GetLang(r)
	return config.T(lang, key)
}

// GenerateVPNLink converts a client configuration into a base64 encoded vpn:// link.
func GenerateVPNLink(configText string) string {
	b64 := base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(configText)))
	return fmt.Sprintf("vpn://%s", b64)
}

// BuildConnectionKitZip generates a ZIP archive byte slice containing client config files and metadata.
func BuildConnectionKitZip(name string, configContent string, vpnLink string) ([]byte, error) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	baseName := strings.TrimSpace(name)
	if baseName == "" {
		baseName = "client"
	}
	baseName = strings.ReplaceAll(baseName, "/", "_")
	baseName = strings.ReplaceAll(baseName, "\\", "_")

	// 1. .conf file
	confHeader := &zip.FileHeader{
		Name:   fmt.Sprintf("%s.conf", baseName),
		Method: zip.Deflate,
	}
	confWriter, err := zw.CreateHeader(confHeader)
	if err != nil {
		return nil, fmt.Errorf("failed to add conf to zip: %w", err)
	}
	if _, err := confWriter.Write([]byte(configContent)); err != nil {
		return nil, fmt.Errorf("failed to write conf into zip: %w", err)
	}

	// 2. .vpn link file
	if vpnLink != "" {
		vpnHeader := &zip.FileHeader{
			Name:   fmt.Sprintf("%s.vpn", baseName),
			Method: zip.Deflate,
		}
		vpnWriter, err := zw.CreateHeader(vpnHeader)
		if err != nil {
			return nil, fmt.Errorf("failed to add vpn link to zip: %w", err)
		}
		if _, err := vpnWriter.Write([]byte(vpnLink)); err != nil {
			return nil, fmt.Errorf("failed to write vpn link into zip: %w", err)
		}
	}

	// 3. Close zip writer
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("failed to finalize zip: %w", err)
	}

	return buf.Bytes(), nil
}

// GetSSHClient gets an active SSH client for the given server using the pool.
func (h *Handlers) GetSSHClient(ctx context.Context, server *models.Server) (ssh.SSHClient, error) {
	if server == nil {
		return nil, errors.New("server cannot be nil")
	}
	if h.sshPool == nil {
		return nil, errors.New("ssh pool is not configured")
	}
	return h.sshPool.Get(ctx, server)
}

// GetProtocolManager retrieves a registered ProtocolManager by protocol name.
func (h *Handlers) GetProtocolManager(proto string) (manager.ProtocolManager, error) {
	normalized := models.NormalizeProtocol(proto)
	if h.registry != nil {
		if mgr, ok := h.registry.Get(normalized); ok {
			return mgr, nil
		}
	}

	switch normalized {
	case "awg":
		if h.awgMgr != nil {
			return h.awgMgr, nil
		}
	case "telemt":
		if h.mtproxylMgr != nil {
			return h.mtproxylMgr, nil
		}
	case "dns":
		if h.dnsMgr != nil {
			return h.dnsMgr, nil
		}
	}

	return nil, fmt.Errorf("unsupported protocol: %s", proto)
}

// isProtocolInstalled checks if a protocol is marked as installed on a server.
func isProtocolInstalled(server *models.Server, proto string) bool {
	if server == nil || server.Protocols == nil {
		return false
	}
	norm := models.NormalizeProtocol(proto)
	if protoData, ok := server.Protocols[norm].(map[string]any); ok {
		if installed, ok := protoData["installed"].(bool); ok && installed {
			return true
		}
	}
	if protoData, ok := server.Protocols[proto].(map[string]any); ok {
		if installed, ok := protoData["installed"].(bool); ok && installed {
			return true
		}
	}
	return false
}

// audit writes an audit log entry for a state-changing action.
func (h *Handlers) audit(r *http.Request, event string, details ...map[string]any) {
	middleware.LogAuditEvent(r, event, details...)
}

package router

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/config"
	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

// Server wraps the standard http.Server with dynamic TLS and graceful shutdown capability.
type Server struct {
	httpServer *http.Server
	tlsConfig  *tls.Config
	tlsCert    atomic.Pointer[tls.Certificate]
	cfg        *config.Config
	db         *database.DB
	listener   net.Listener
	mu         sync.Mutex
}

// NewServer constructs an HTTP server configured with the router, listening address, and database.
func NewServer(cfg *config.Config, handler http.Handler, db ...*database.DB) *Server {
	var host string
	var port int
	if cfg != nil {
		host = cfg.Host
		port = cfg.Port
	}
	if host == "" {
		host = "0.0.0.0"
	}
	if port == 0 {
		port = 5000
	}

	addr := fmt.Sprintf("%s:%d", host, port)

	var databaseHandle *database.DB
	if len(db) > 0 {
		databaseHandle = db[0]
	}

	srv := &Server{
		cfg: cfg,
		db:  databaseHandle,
		httpServer: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
		},
	}

	return srv
}

// LoadTLSCertificate reads SSL settings from the database and constructs a TLS certificate.
func (s *Server) LoadTLSCertificate(ctx context.Context) (*tls.Certificate, error) {
	if s.db == nil {
		return nil, nil
	}

	var ssl models.SSLSettings
	if err := s.db.GetSetting(ctx, "ssl", &ssl); err != nil {
		return nil, fmt.Errorf("failed to read ssl setting: %w", err)
	}

	if !ssl.Enabled {
		return nil, nil
	}

	if ssl.CertText != "" && ssl.KeyText != "" {
		cert, err := tls.X509KeyPair([]byte(ssl.CertText), []byte(ssl.KeyText))
		if err != nil {
			return nil, fmt.Errorf("failed to parse tls x509 keypair from cert_text: %w", err)
		}
		return &cert, nil
	}

	if ssl.CertPath != "" && ssl.KeyPath != "" {
		cert, err := tls.LoadX509KeyPair(ssl.CertPath, ssl.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load tls keypair from files: %w", err)
		}
		return &cert, nil
	}

	return nil, nil
}

// ReloadTLS dynamically updates the active TLS certificate in memory.
func (s *Server) ReloadTLS(cert *tls.Certificate) {
	s.tlsCert.Store(cert)
}

// Start runs the HTTP / HTTPS server listener.
func (s *Server) Start() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cert, err := s.LoadTLSCertificate(ctx)
	if err != nil {
		slog.Warn("Failed to load dynamic TLS certificate from database", "err", err)
	}

	if cert != nil {
		s.tlsCert.Store(cert)

		s.tlsConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			CipherSuites: []uint16{
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
				tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			},
			GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
				currentCert := s.tlsCert.Load()
				if currentCert != nil {
					return currentCert, nil
				}
				return nil, errors.New("no dynamic tls certificate configured")
			},
		}

		s.httpServer.TLSConfig = s.tlsConfig

		ln, err := net.Listen("tcp", s.httpServer.Addr)
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.listener = ln
		s.mu.Unlock()

		tlsListener := tls.NewListener(ln, s.tlsConfig)
		if err := s.httpServer.Serve(tlsListener); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}

	// Plain HTTP mode
	ln, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()

	if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown initiates graceful shutdown of the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

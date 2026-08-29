package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/manager"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestServerHandlers(t *testing.T) {
	mockSSH := &testMockSSHClient{}
	h, db, _ := setupTestHandlersWithMockSSH(t, mockSSH)
	ctx := context.Background()

	// Seed server
	srv := &models.Server{
		Name:      "Production-1",
		Host:      "192.168.1.100",
		SSHPort:   22,
		SSHUser:   "root",
		SSHPass:   "pass123",
		Protocols: map[string]any{"awg": map[string]any{"port": 55424, "installed": true}, "telemt": map[string]any{"port": 443, "installed": true}, "dns": map[string]any{"port": 53, "installed": true}},
		CreatedAt: time.Now(),
	}
	serverID, err := db.CreateServer(ctx, srv)
	if err != nil {
		t.Fatalf("failed to create test server: %v", err)
	}
	mockSSH.serverID = &serverID

	r := setupFullServerRouter(h)

	t.Run("AddServerHandler Validation Failure", func(t *testing.T) {
		for _, reqBody := range []any{
			nil,
			models.AddServerRequest{Host: ""},
			map[string]any{"host": "127.0.0.1", "username": ""},
			map[string]any{"host": "", "username": "root"},
			map[string]any{"host": "127.0.0.1", "username": "root", "password": "", "private_key": ""},
		} {
			var bodyReader *bytes.Reader
			if reqBody != nil {
				b, _ := json.Marshal(reqBody)
				bodyReader = bytes.NewReader(b)
			} else {
				bodyReader = bytes.NewReader(nil)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/servers/add", bodyReader)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 validation failure, got %d", w.Code)
			}
		}
	})

	t.Run("AddServerHandler Connection Failed", func(t *testing.T) {
		body, _ := json.Marshal(models.AddServerRequest{
			Host:     "127.0.0.1",
			SSHPort:  59999,
			Username: "testuser",
			Password: "Password123!",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/servers/add", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 connection failed, got %d", w.Code)
		}
	})

	t.Run("ConfirmFingerprintHandler Validation & Success", func(t *testing.T) {
		for _, reqBody := range []any{
			nil,
			map[string]any{"host": "127.0.0.1", "username": "root", "password": "pwd", "fingerprint": ""},
			map[string]any{"host": "", "username": "root", "password": "pwd", "fingerprint": "fp"},
			map[string]any{"host": "127.0.0.1", "username": "", "password": "pwd", "fingerprint": "fp"},
			map[string]any{"host": "127.0.0.1", "username": "root", "password": "", "private_key": "", "fingerprint": "fp"},
		} {
			var bodyReader *bytes.Reader
			if reqBody != nil {
				b, _ := json.Marshal(reqBody)
				bodyReader = bytes.NewReader(b)
			} else {
				bodyReader = bytes.NewReader(nil)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/servers/confirm-fingerprint", bodyReader)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d", w.Code)
			}
		}

		body, _ := json.Marshal(models.ConfirmFingerprintRequest{
			AddServerRequest: models.AddServerRequest{
				Host:     "192.168.1.101",
				SSHPort:  22,
				Username: "root",
				Password: "secret123",
				Name:     "Test Server 2",
			},
			Fingerprint: "SHA256:abcdef1234567890",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/servers/confirm-fingerprint", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
		}
	})

	t.Run("GetServerReachabilityHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/servers/%d/reachability", serverID), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		// Invalid server ID
		reqBad := httptest.NewRequest(http.MethodGet, "/api/servers/invalid/reachability", nil)
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", wBad.Code)
		}

		// Non-existent server
		reqMissing := httptest.NewRequest(http.MethodGet, "/api/servers/99999/reachability", nil)
		wMissing := httptest.NewRecorder()
		r.ServeHTTP(wMissing, reqMissing)
		if wMissing.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", wMissing.Code)
		}
	})

	t.Run("GetAWGSpeedLimitConfigHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/servers/%d/awg/speed-limit-config", serverID), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		// Bad ID
		reqBad := httptest.NewRequest(http.MethodGet, "/api/servers/bad/awg/speed-limit-config", nil)
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", wBad.Code)
		}

		// Not found
		req404 := httptest.NewRequest(http.MethodGet, "/api/servers/88888/awg/speed-limit-config", nil)
		w404 := httptest.NewRecorder()
		r.ServeHTTP(w404, req404)
		if w404.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w404.Code)
		}
	})

	t.Run("SetAWGSpeedLimitConfigHandler", func(t *testing.T) {
		down := 100
		up := 100
		body, _ := json.Marshal(models.AwgSpeedLimitConfigRequest{
			GlobalSpeedLimitDown: &down,
			GlobalSpeedLimitUp:   &up,
		})
		req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/servers/%d/awg/speed-limit-config", serverID), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		// Bad body
		reqBad := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/servers/%d/awg/speed-limit-config", serverID), bytes.NewReader([]byte("invalid-json")))
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", wBad.Code)
		}
	})

	t.Run("ApplyDefaultSpeedLimitsHandler", func(t *testing.T) {
		// Server without configured default limits -> 400
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/awg/apply-default-speed-limits", serverID), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 when no default limits configured, got %d", w.Code)
		}

		// Configure default limits on server and apply
		defDown := 20
		defUp := 30
		cfgBody, _ := json.Marshal(models.AwgSpeedLimitConfigRequest{
			DefaultSpeedLimitDown: &defDown,
			DefaultSpeedLimitUp:   &defUp,
		})
		reqSet := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/servers/%d/awg/speed-limit-config", serverID), bytes.NewReader(cfgBody))
		wSet := httptest.NewRecorder()
		r.ServeHTTP(wSet, reqSet)
		if wSet.Code != http.StatusOK {
			t.Fatalf("expected 200 setting speed limit config, got %d", wSet.Code)
		}

		reqApply := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/awg/apply-default-speed-limits", serverID), nil)
		wApply := httptest.NewRecorder()
		r.ServeHTTP(wApply, reqApply)
		if wApply.Code != http.StatusOK {
			t.Fatalf("expected 200 applying default limits, got %d (body: %s)", wApply.Code, wApply.Body.String())
		}

		// Server with AWG not installed -> 400
		sNoAWG := &models.Server{Name: "No-AWG-Server", Host: "1.1.1.88", SSHUser: "root", Protocols: map[string]any{"telemt": map[string]any{"installed": true}}}
		sNoAWGID, _ := db.CreateServer(ctx, sNoAWG)
		reqNoAWG := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/awg/apply-default-speed-limits", sNoAWGID), nil)
		wNoAWG := httptest.NewRecorder()
		r.ServeHTTP(wNoAWG, reqNoAWG)
		if wNoAWG.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for uninstalled AWG, got %d", wNoAWG.Code)
		}

		// Get and Set speed limit config on server without AWG installed -> 400
		reqGetNoAWG := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/servers/%d/awg/speed-limit-config", sNoAWGID), nil)
		wGetNoAWG := httptest.NewRecorder()
		r.ServeHTTP(wGetNoAWG, reqGetNoAWG)
		if wGetNoAWG.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 getting speed limit config for uninstalled AWG, got %d", wGetNoAWG.Code)
		}

		reqSetNoAWG := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/servers/%d/awg/speed-limit-config", sNoAWGID), bytes.NewReader(cfgBody))
		wSetNoAWG := httptest.NewRecorder()
		r.ServeHTTP(wSetNoAWG, reqSetNoAWG)
		if wSetNoAWG.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 setting speed limit config for uninstalled AWG, got %d", wSetNoAWG.Code)
		}

		// 404 server
		req404 := httptest.NewRequest(http.MethodPost, "/api/servers/77777/awg/apply-default-speed-limits", nil)
		w404 := httptest.NewRecorder()
		r.ServeHTTP(w404, req404)
		if w404.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w404.Code)
		}
	})

	t.Run("SetClientSpeedLimitHandler", func(t *testing.T) {
		limitDown := 50
		limitUp := 50
		body, _ := json.Marshal(models.SpeedLimitRequest{
			ClientID:       "client-1",
			SpeedLimitDown: &limitDown,
			SpeedLimitUp:   &limitUp,
		})
		req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/servers/%d/connections/speed-limit", serverID), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		// Missing client_id
		badBody, _ := json.Marshal(models.SpeedLimitRequest{})
		reqBad := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/servers/%d/connections/speed-limit", serverID), bytes.NewReader(badBody))
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", wBad.Code)
		}
	})

	t.Run("ServerStatsHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/stats", serverID), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
		}
	})

	t.Run("ServerCheckHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/check", serverID), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
		}
	})

	t.Run("InstallProtocolHandler Success and Errors", func(t *testing.T) {
		body, _ := json.Marshal(models.InstallProtocolRequest{
			Protocol: "awg",
			Port:     "55424",
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/install", serverID), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
		}

		// Invalid protocol
		badProto, _ := json.Marshal(models.InstallProtocolRequest{Protocol: "unknown"})
		reqBad := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/install", serverID), bytes.NewReader(badProto))
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", wBad.Code)
		}
	})

	t.Run("UninstallProtocolHandler", func(t *testing.T) {
		body, _ := json.Marshal(models.ProtocolRequest{
			Protocol: "awg",
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/uninstall", serverID), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("ToggleContainerHandler", func(t *testing.T) {
		for _, action := range []string{"start", "stop", "restart"} {
			body, _ := json.Marshal(map[string]any{
				"protocol": "awg",
				"action":   action,
			})
			req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/container/toggle", serverID), bytes.NewReader(body))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200 for action %s, got %d", action, w.Code)
			}
		}

		// Invalid action returns 400
		bodyFallback, _ := json.Marshal(map[string]any{"protocol": "awg", "action": "unknown_action"})
		reqFallback := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/container/toggle", serverID), bytes.NewReader(bodyFallback))
		wFallback := httptest.NewRecorder()
		r.ServeHTTP(wFallback, reqFallback)
		if wFallback.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid action, got %d", wFallback.Code)
		}
	})

	t.Run("InstallProtocolHandler All Protocols", func(t *testing.T) {
		sAll := &models.Server{Name: "All-Proto-Server", Host: "1.1.1.3", SSHUser: "root", Protocols: make(map[string]any)}
		sAllID, _ := db.CreateServer(ctx, sAll)

		for _, p := range []string{"awg", "telemt", "dns"} {
			body, _ := json.Marshal(models.InstallProtocolRequest{Protocol: p, Port: "443"})
			req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/install", sAllID), bytes.NewReader(body))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200 installing %s, got %d", p, w.Code)
			}
		}
	})

	t.Run("ServerCheckHandler Bad Server ID", func(t *testing.T) {
		// Invalid server ID returns 400
		reqDirect := httptest.NewRequest(http.MethodPost, "/api/servers/99999/check", nil)
		wDirect := httptest.NewRecorder()
		h.ServerCheckHandler(wDirect, reqDirect)
		if wDirect.Code != http.StatusBadRequest && wDirect.Code != http.StatusNotFound {
			t.Errorf("expected 400 or 404, got %d", wDirect.Code)
		}
	})

	t.Run("ToggleContainerHandler Empty Action and Bad JSON", func(t *testing.T) {
		// Empty action defaults to restart
		bodyEmpty, _ := json.Marshal(map[string]any{"protocol": "awg"})
		reqEmpty := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/container/toggle", serverID), bytes.NewReader(bodyEmpty))
		wEmpty := httptest.NewRecorder()
		r.ServeHTTP(wEmpty, reqEmpty)
		if wEmpty.Code != http.StatusOK {
			t.Errorf("expected 200 for empty action (restart default), got %d", wEmpty.Code)
		}

		// Bad JSON
		reqBad := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/container/toggle", serverID), bytes.NewReader([]byte("bad")))
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for bad json, got %d", wBad.Code)
		}
	})

	t.Run("ToggleContainerHandler Rejects Unknown Protocol", func(t *testing.T) {
		// Command-injection-style protocol strings must be rejected, never interpolated
		// into a docker command.
		for _, p := range []string{"awg; malicious-cmd", "awg$(reboot)", "nonexistent", "../../etc"} {
			body, _ := json.Marshal(map[string]any{"protocol": p, "action": "start"})
			req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/container/toggle", serverID), bytes.NewReader(body))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400 for protocol %q, got %d", p, w.Code)
			}
		}
	})

	t.Run("GetServerConfigHandler Rejects Unknown Protocol", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"protocol": "../../etc/shadow"})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/server_config", serverID), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for path-injection protocol, got %d", w.Code)
		}
	})

	t.Run("SaveServerConfigHandler Rejects Unknown Protocol", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"protocol": "unknown_proto", "config": "some config content"})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/server_config/save", serverID), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for invalid protocol in SaveServerConfigHandler, got %d", w.Code)
		}
	})

	t.Run("SetClientSpeedLimitHandler Bad JSON", func(t *testing.T) {
		// Bad JSON body
		reqBad := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/servers/%d/connections/speed-limit", serverID), bytes.NewReader([]byte("bad")))
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for bad body, got %d", wBad.Code)
		}

		// Server not found
		limitDown := 10
		limitUp := 10
		bodyNF, _ := json.Marshal(models.SpeedLimitRequest{ClientID: "c1", SpeedLimitDown: &limitDown, SpeedLimitUp: &limitUp})
		reqNF := httptest.NewRequest(http.MethodPatch, "/api/servers/99999/connections/speed-limit", bytes.NewReader(bodyNF))
		wNF := httptest.NewRecorder()
		r.ServeHTTP(wNF, reqNF)
		if wNF.Code != http.StatusNotFound {
			t.Errorf("expected 404 for missing server, got %d", wNF.Code)
		}
	})

	t.Run("InstallProtocolHandler With All Optional Params", func(t *testing.T) {
		sOpt := &models.Server{Name: "Opt-Server", Host: "1.1.1.6", SSHUser: "root", Protocols: make(map[string]any)}
		sOptID, _ := db.CreateServer(ctx, sOpt)

		tlsEmu := true
		tlsDomain := "cdn.example.com"
		maxConn := 100
		profile := models.AWGProfilePro
		cpsProto := "quic"
		body, _ := json.Marshal(models.InstallProtocolRequest{
			Protocol:       "awg",
			Port:           "55425",
			TLSEmulation:   &tlsEmu,
			TLSDomain:      &tlsDomain,
			MaxConnections: &maxConn,
			AWGProfile:     &profile,
			AWGCPSProtocol: &cpsProto,
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/install", sOptID), bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
		}

		// Invalid port
		bodyBadPort, _ := json.Marshal(models.InstallProtocolRequest{Protocol: "awg", Port: "99999"})
		reqBadPort := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/install", sOptID), bytes.NewReader(bodyBadPort))
		wBadPort := httptest.NewRecorder()
		r.ServeHTTP(wBadPort, reqBadPort)
		if wBadPort.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for invalid port, got %d", wBadPort.Code)
		}

		// Bad TLS domain
		badDomain := "not a domain!"
		bodyBadDomain, _ := json.Marshal(models.InstallProtocolRequest{Protocol: "awg", Port: "443", TLSDomain: &badDomain})
		reqBadDomain := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/install", sOptID), bytes.NewReader(bodyBadDomain))
		wBadDomain := httptest.NewRecorder()
		r.ServeHTTP(wBadDomain, reqBadDomain)
		if wBadDomain.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for bad tls domain, got %d", wBadDomain.Code)
		}
	})

	t.Run("GetServerConfig and SaveServerConfig All Protocols", func(t *testing.T) {
		sCfg := &models.Server{Name: "Cfg-Server", Host: "1.1.1.4", SSHUser: "root", Protocols: map[string]any{"awg": map[string]any{"installed": true}, "telemt": map[string]any{"installed": true}, "dns": map[string]any{"installed": true}}}
		sCfgID, _ := db.CreateServer(ctx, sCfg)

		for _, p := range []string{"awg", "telemt", "dns"} {
			bodyGet, _ := json.Marshal(models.ProtocolRequest{Protocol: p})
			reqGet := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/server_config", sCfgID), bytes.NewReader(bodyGet))
			wGet := httptest.NewRecorder()
			r.ServeHTTP(wGet, reqGet)
			if wGet.Code != http.StatusOK {
				t.Fatalf("expected 200 get config %s, got %d", p, wGet.Code)
			}

			bodySave, _ := json.Marshal(models.ServerConfigSaveRequest{Protocol: p, Config: "dummy-config"})
			reqSave := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/server_config/save", sCfgID), bytes.NewReader(bodySave))
			wSave := httptest.NewRecorder()
			r.ServeHTTP(wSave, reqSave)
			if wSave.Code != http.StatusOK {
				t.Fatalf("expected 200 save config %s, got %d", p, wSave.Code)
			}
		}
	})

	t.Run("RebootServerHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/reboot", serverID), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("ClearServerHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/clear", serverID), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("DeleteServerHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/delete", serverID), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("parseServerID edge cases", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		if _, err := parseServerID(req); err == nil {
			t.Errorf("expected error for missing param")
		}
	})

	t.Run("ParseCombinedStats", func(t *testing.T) {
		raw := "===CPU===\n12.5\n===RAM===\n1000 2000\n===DISK===\n5000 10000\n===NET===\n1024 2048\n===UPTIME===\nup 2 hours\n"
		stats := parseCombinedStats(raw)
		if stats.CPU != 12.5 || stats.RAMPercent != 50.0 || stats.DiskPercent != 50.0 || stats.NetRx != 1024 || stats.NetTx != 2048 || stats.Uptime != "up 2 hours" {
			t.Errorf("parseCombinedStats failed: %+v", stats)
		}

		// Empty string
		emptyStats := parseCombinedStats("")
		if emptyStats.CPU != 0 {
			t.Errorf("expected 0 stats")
		}
	})

	t.Run("SSH Pool Failure Handling", func(t *testing.T) {
		failingPool := &testMockSSHPool{err: errors.New("ssh dial pool failed")}
		hFail := NewHandlers(Dependencies{
			Config:  h.cfg,
			DB:      db,
			SSHPool: failingPool,
		})
		rFail := setupFullServerRouter(hFail)

		// Create a server to hit
		sFail := &models.Server{Name: "Fail-Server", Host: "1.2.3.4", SSHUser: "root"}
		sFailID, _ := db.CreateServer(ctx, sFail)

		reqStats := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/stats", sFailID), nil)
		wStats := httptest.NewRecorder()
		rFail.ServeHTTP(wStats, reqStats)
		if wStats.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for SSH pool failure, got %d", wStats.Code)
		}

		reqCheck := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/check", sFailID), nil)
		wCheck := httptest.NewRecorder()
		rFail.ServeHTTP(wCheck, reqCheck)
		if wCheck.Code != http.StatusOK {
			t.Errorf("expected 200 for ServerCheck with failed status, got %d", wCheck.Code)
		}

		reqReboot := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/reboot", sFailID), nil)
		wReboot := httptest.NewRecorder()
		rFail.ServeHTTP(wReboot, reqReboot)
		if wReboot.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for SSH pool failure, got %d", wReboot.Code)
		}
	})

	t.Run("Protocol Manager Error Handling", func(t *testing.T) {
		sErr := &models.Server{Name: "Err-Server", Host: "1.1.1.1", SSHUser: "root"}
		sErrID, _ := db.CreateServer(ctx, sErr)

		failReg := manager.NewRegistry()
		failReg.Register(&mockProtocolManager{
			protocol: "awg",
			installFn: func(ctx context.Context, server *models.Server, params map[string]any) error {
				return errors.New("install error")
			},
			uninstallFn: func(ctx context.Context, server *models.Server) error {
				return errors.New("uninstall error")
			},
		})

		hError := NewHandlers(Dependencies{
			Config:   h.cfg,
			DB:       db,
			Registry: failReg,
			SSHPool:  &testMockSSHPool{client: mockSSH},
		})
		rError := setupFullServerRouter(hError)

		// Install fail
		bodyInstall, _ := json.Marshal(models.InstallProtocolRequest{Protocol: "awg", Port: "55424"})
		reqInst := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/install", sErrID), bytes.NewReader(bodyInstall))
		wInst := httptest.NewRecorder()
		rError.ServeHTTP(wInst, reqInst)
		if wInst.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 for install error, got %d", wInst.Code)
		}

		// Uninstall
		bodyUninst, _ := json.Marshal(models.ProtocolRequest{Protocol: "awg"})
		reqUninst := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/uninstall", sErrID), bytes.NewReader(bodyUninst))
		wUninst := httptest.NewRecorder()
		rError.ServeHTTP(wUninst, reqUninst)
		if wUninst.Code != http.StatusOK {
			t.Errorf("expected 200 for uninstall, got %d", wUninst.Code)
		}
	})

	t.Run("Server Handlers 404 and Error Branches", func(t *testing.T) {
		// Delete 404
		reqDel := httptest.NewRequest(http.MethodPost, "/api/servers/99999/delete", nil)
		wDel := httptest.NewRecorder()
		r.ServeHTTP(wDel, reqDel)
		if wDel.Code != http.StatusNotFound {
			t.Errorf("expected 404 for missing server delete, got %d", wDel.Code)
		}

		// Reboot 404
		reqReboot := httptest.NewRequest(http.MethodPost, "/api/servers/99999/reboot", nil)
		wReboot := httptest.NewRecorder()
		r.ServeHTTP(wReboot, reqReboot)
		if wReboot.Code != http.StatusNotFound {
			t.Errorf("expected 404 for missing server reboot, got %d", wReboot.Code)
		}

		// Clear 404
		reqClear := httptest.NewRequest(http.MethodPost, "/api/servers/99999/clear", nil)
		wClear := httptest.NewRecorder()
		r.ServeHTTP(wClear, reqClear)
		if wClear.Code != http.StatusNotFound {
			t.Errorf("expected 404 for missing server clear, got %d", wClear.Code)
		}

		// Uninstall 404 and invalid proto
		bodyAwg, _ := json.Marshal(models.ProtocolRequest{Protocol: "awg"})
		reqUninst404 := httptest.NewRequest(http.MethodPost, "/api/servers/99999/uninstall", bytes.NewReader(bodyAwg))
		wUninst404 := httptest.NewRecorder()
		r.ServeHTTP(wUninst404, reqUninst404)
		if wUninst404.Code != http.StatusNotFound {
			t.Errorf("expected 404 for missing server uninstall, got %d", wUninst404.Code)
		}

		bodyBadProto, _ := json.Marshal(models.ProtocolRequest{Protocol: "unknown_proto"})
		reqUninstBadProto := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/uninstall", serverID), bytes.NewReader(bodyBadProto))
		wUninstBadProto := httptest.NewRecorder()
		r.ServeHTTP(wUninstBadProto, reqUninstBadProto)
		if wUninstBadProto.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for unknown proto uninstall, got %d", wUninstBadProto.Code)
		}

		// Toggle container 404
		bodyToggle, _ := json.Marshal(map[string]any{"protocol": "awg", "action": "start"})
		reqToggle404 := httptest.NewRequest(http.MethodPost, "/api/servers/99999/container/toggle", bytes.NewReader(bodyToggle))
		wToggle404 := httptest.NewRecorder()
		r.ServeHTTP(wToggle404, reqToggle404)
		if wToggle404.Code != http.StatusNotFound {
			t.Errorf("expected 404 for missing server container toggle, got %d", wToggle404.Code)
		}

		// Get config 404
		reqGetCfg404 := httptest.NewRequest(http.MethodPost, "/api/servers/99999/server_config", bytes.NewReader(bodyAwg))
		wGetCfg404 := httptest.NewRecorder()
		r.ServeHTTP(wGetCfg404, reqGetCfg404)
		if wGetCfg404.Code != http.StatusNotFound {
			t.Errorf("expected 404 for missing server config, got %d", wGetCfg404.Code)
		}

		// Save config 404
		bodySaveCfg, _ := json.Marshal(models.ServerConfigSaveRequest{Protocol: "awg", Config: "[Interface]\n"})
		reqSaveCfg404 := httptest.NewRequest(http.MethodPost, "/api/servers/99999/server_config/save", bytes.NewReader(bodySaveCfg))
		wSaveCfg404 := httptest.NewRecorder()
		r.ServeHTTP(wSaveCfg404, reqSaveCfg404)
		if wSaveCfg404.Code != http.StatusNotFound {
			t.Errorf("expected 404 for missing server save config, got %d", wSaveCfg404.Code)
		}
	})

	t.Run("Speed Limit and Custom Protocol Paths", func(t *testing.T) {
		sSpeed := &models.Server{
			Name:    "Speed-Server",
			Host:    "1.1.1.5",
			SSHUser: "root",
			Protocols: map[string]any{
				"awg": map[string]any{
					"installed":        true,
					"speed_limit_down": float64(100),
					"speed_limit_up":   float64(200),
				},
				"customproto": map[string]any{
					"installed": true,
				},
			},
		}
		sSpeedID, _ := db.CreateServer(ctx, sSpeed)

		// GetAWGSpeedLimitConfigHandler with speed limits set
		reqSpeed := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/servers/%d/awg/speed-limit-config", sSpeedID), nil)
		wSpeed := httptest.NewRecorder()
		r.ServeHTTP(wSpeed, reqSpeed)
		if wSpeed.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", wSpeed.Code)
		}

		// Server with nested awg_speed_limit_config
		sSpeedNested := &models.Server{
			Name:    "Speed-Nested",
			Host:    "1.1.1.9",
			SSHUser: "root",
			Protocols: map[string]any{
				"awg": map[string]any{
					"installed": true,
					"awg_speed_limit_config": map[string]any{
						"global_speed_limit_down":  float64(500),
						"global_speed_limit_up":    float64(500),
						"default_speed_limit_down": float64(50),
						"default_speed_limit_up":   float64(50),
					},
				},
			},
		}
		sSpeedNestedID, _ := db.CreateServer(ctx, sSpeedNested)
		reqSpeedNested := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/servers/%d/awg/speed-limit-config", sSpeedNestedID), nil)
		wSpeedNested := httptest.NewRecorder()
		r.ServeHTTP(wSpeedNested, reqSpeedNested)
		if wSpeedNested.Code != http.StatusOK {
			t.Errorf("expected 200 for nested speed config, got %d", wSpeedNested.Code)
		}

		// ToggleContainerHandler for telemt
		bodyTelemtToggle, _ := json.Marshal(map[string]any{"protocol": "telemt", "action": "start"})
		reqTT := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/container/toggle", sSpeedID), bytes.NewReader(bodyTelemtToggle))
		wTT := httptest.NewRecorder()
		r.ServeHTTP(wTT, reqTT)
		if wTT.Code != http.StatusOK {
			t.Errorf("expected 200 for telemt toggle, got %d", wTT.Code)
		}

		// Custom protocol config get & save are now rejected: only whitelisted
		// protocols (awg/dns/telemt) have config paths.
		bodyCustom, _ := json.Marshal(models.ProtocolRequest{Protocol: "customproto"})
		reqCustom := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/server_config", sSpeedID), bytes.NewReader(bodyCustom))
		wCustom := httptest.NewRecorder()
		r.ServeHTTP(wCustom, reqCustom)
		if wCustom.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for unknown proto config, got %d", wCustom.Code)
		}

		// Invalid protocol config save returns 400
		bodyCustomSave, _ := json.Marshal(models.ServerConfigSaveRequest{Protocol: "unknownproto", Config: "{}"})
		reqCustomSave := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/server_config/save", sSpeedID), bytes.NewReader(bodyCustomSave))
		wCustomSave := httptest.NewRecorder()
		r.ServeHTTP(wCustomSave, reqCustomSave)
		if wCustomSave.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for invalid proto save, got %d", wCustomSave.Code)
		}

		// ConfirmFingerprintHandler empty name
		bodyCF, _ := json.Marshal(models.ConfirmFingerprintRequest{
			AddServerRequest: models.AddServerRequest{
				Host:     "1.1.1.99",
				Username: "root",
				Password: "password123",
				Name:     "",
			},
			Fingerprint: "SHA256:dummy",
		})
		reqCF := httptest.NewRequest(http.MethodPost, "/api/servers/confirm-fingerprint", bytes.NewReader(bodyCF))
		wCF := httptest.NewRecorder()
		r.ServeHTTP(wCF, reqCF)
		if wCF.Code != http.StatusOK {
			t.Errorf("expected 200 for confirm fingerprint empty name, got %d", wCF.Code)
		}
	})
	t.Run("Upload Failure and Speed Removal", func(t *testing.T) {
		failSSH := &testMockSSHClient{
			uploadFn: func(ctx context.Context, path string, content []byte) error {
				return errors.New("upload failed")
			},
		}
		hUploadFail, dbUpload, _ := setupTestHandlersWithMockSSH(t, failSSH)
		rUploadFail := setupFullServerRouter(hUploadFail)

		sBr := &models.Server{Name: "Branch-Server", Host: "1.1.1.2", SSHUser: "root", Protocols: map[string]any{"awg": map[string]any{"installed": true}}}
		sBrID, _ := dbUpload.CreateServer(ctx, sBr)

		bodySave, _ := json.Marshal(models.ServerConfigSaveRequest{Protocol: "awg", Config: "[Interface]\n"})
		reqSave := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/servers/%d/server_config/save", sBrID), bytes.NewReader(bodySave))
		wSave := httptest.NewRecorder()
		rUploadFail.ServeHTTP(wSave, reqSave)
		if wSave.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 for upload fail, got %d", wSave.Code)
		}

		// SetClientSpeedLimit with remove (0 limits) on failing SSH returns 500
		zero := 0
		bodyZero, _ := json.Marshal(models.SpeedLimitRequest{ClientID: "client-1", SpeedLimitDown: &zero, SpeedLimitUp: &zero})
		reqZero := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/servers/%d/connections/speed-limit", sBrID), bytes.NewReader(bodyZero))
		wZero := httptest.NewRecorder()
		rUploadFail.ServeHTTP(wZero, reqZero)
		if wZero.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 for speed limit removal on failing SSH, got %d", wZero.Code)
		}

		// On working router r, speed limit removal succeeds (200)
		sWork := &models.Server{Name: "Working-Server", Host: "1.1.1.3", SSHUser: "root", Protocols: map[string]any{"awg": map[string]any{"installed": true}}}
		sWorkID, _ := db.CreateServer(ctx, sWork)
		reqZeroWorking := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/servers/%d/connections/speed-limit", sWorkID), bytes.NewReader(bodyZero))
		wZeroWorking := httptest.NewRecorder()
		r.ServeHTTP(wZeroWorking, reqZeroWorking)
		if wZeroWorking.Code != http.StatusOK {
			t.Errorf("expected 200 for speed limit removal on working SSH, got %d", wZeroWorking.Code)
		}
	})
}

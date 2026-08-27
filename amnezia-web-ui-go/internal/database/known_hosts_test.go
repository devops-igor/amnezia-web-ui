package database

import (
	"context"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestKnownHostsEmptyAndNotFound(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	allHosts, err := db.GetAllKnownHosts(ctx)
	if err != nil || len(allHosts) != 0 {
		t.Fatalf("GetAllKnownHosts on empty DB = (%v, %v), want ([], nil)", allHosts, err)
	}

	kh, err := db.GetKnownHost(ctx, 999)
	if err != nil || kh != nil {
		t.Errorf("GetKnownHost(999) = (%v, %v), want (nil, nil)", kh, err)
	}

	fp, err := db.GetKnownHostFingerprint(ctx, 999)
	if err != nil || fp != "" {
		t.Errorf("GetKnownHostFingerprint(999) = (%q, %v), want (\"\", nil)", fp, err)
	}

	verified, err := db.IsKnownHostVerified(ctx, 999, "SHA256:any")
	if err != nil || verified {
		t.Errorf("IsKnownHostVerified(999) = (%v, %v), want (false, nil)", verified, err)
	}

	deleted, err := db.DeleteKnownHost(ctx, 999)
	if err != nil || deleted {
		t.Errorf("DeleteKnownHost(999) = (%v, %v), want (false, nil)", deleted, err)
	}
}

func TestKnownHostsSaveAndRetrieve(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	s1ID, _ := db.CreateServer(ctx, &models.Server{Name: "Server 1", Host: "10.0.0.1"})
	s2ID, _ := db.CreateServer(ctx, &models.Server{Name: "Server 2", Host: "10.0.0.2"})

	fp1 := "SHA256:abcd1234efgh5678ijkl9012mnop3456qrst7890uvw="
	if err := db.SaveKnownHost(ctx, s1ID, fp1); err != nil {
		t.Fatalf("SaveKnownHost s1 failed: %v", err)
	}

	fp2 := "SHA256:zzzz1234yyyy5678xxxx9012wwww3456vvvv7890uuu="
	if err := db.SaveKnownHostFingerprint(ctx, s2ID, fp2); err != nil {
		t.Fatalf("SaveKnownHostFingerprint s2 failed: %v", err)
	}

	kh1, err := db.GetKnownHost(ctx, s1ID)
	if err != nil || kh1 == nil || kh1.Fingerprint != fp1 || kh1.FirstSeen.IsZero() {
		t.Errorf("GetKnownHost(s1) failed: %+v, err=%v", kh1, err)
	}

	kh2, err := db.GetKnownHost(ctx, s2ID)
	if err != nil || kh2 == nil || kh2.Fingerprint != fp2 {
		t.Errorf("GetKnownHost(s2) failed: %+v, err=%v", kh2, err)
	}

	allPopulated, err := db.GetAllKnownHosts(ctx)
	if err != nil || len(allPopulated) != 2 {
		t.Fatalf("GetAllKnownHosts expected 2, got %d, err=%v", len(allPopulated), err)
	}
}

func TestKnownHostsVerificationAndConflict(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{Name: "Server 1", Host: "10.0.0.1"})
	fp1 := "SHA256:abcd1234efgh5678ijkl9012mnop3456qrst7890uvw="
	_ = db.SaveKnownHost(ctx, sID, fp1)

	vMatch, err := db.IsKnownHostVerified(ctx, sID, fp1)
	if err != nil || !vMatch {
		t.Errorf("IsKnownHostVerified(match) = (%v, %v), want (true, nil)", vMatch, err)
	}

	vMismatch, err := db.IsKnownHostVerified(ctx, sID, "SHA256:different_fingerprint")
	if err != nil || vMismatch {
		t.Errorf("IsKnownHostVerified(diff) = (%v, %v), want (false, nil)", vMismatch, err)
	}

	vLenMismatch, err := db.IsKnownHostVerified(ctx, sID, "short")
	if err != nil || vLenMismatch {
		t.Errorf("IsKnownHostVerified(short) = (%v, %v), want (false, nil)", vLenMismatch, err)
	}

	fpUpdated := "SHA256:NEWFINGERPRINT1234567890abcdef"
	if err := db.SaveKnownHost(ctx, sID, fpUpdated); err != nil {
		t.Fatalf("SaveKnownHost update failed: %v", err)
	}
	retFP, _ := db.GetKnownHostFingerprint(ctx, sID)
	if retFP != fpUpdated {
		t.Errorf("updated fingerprint mismatch: got %q, want %q", retFP, fpUpdated)
	}
}

func TestKnownHostsDeletionAndScanning(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	sID, _ := db.CreateServer(ctx, &models.Server{Name: "Server 1", Host: "10.0.0.1"})
	fp := "SHA256:test_delete_fingerprint"
	_ = db.SaveKnownHost(ctx, sID, fp)

	delRes, err := db.DeleteKnownHost(ctx, sID)
	if err != nil || !delRes {
		t.Fatalf("DeleteKnownHost failed: (%v, %v)", delRes, err)
	}
	delCheck, _ := db.GetKnownHost(ctx, sID)
	if delCheck != nil {
		t.Errorf("known host was not deleted")
	}

	_, _ = db.sqlDB.ExecContext(ctx, "INSERT INTO known_hosts (server_id, fingerprint, first_seen) VALUES (?, ?, ?)",
		sID, "SHA256:test_first_seen", time.Now().Format(time.RFC3339))
	khRecheck, _ := db.GetKnownHost(ctx, sID)
	if khRecheck == nil || khRecheck.FirstSeen.IsZero() {
		t.Errorf("recheck known host with explicit first_seen failed: %+v", khRecheck)
	}
}

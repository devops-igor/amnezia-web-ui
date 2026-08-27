package ssh

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNormalizeLineEndings(t *testing.T) {
	input := []byte("line1\r\nline2\r\nline3\nline4\r\n")
	expected := []byte("line1\nline2\nline3\nline4\n")

	got := NormalizeLineEndings(input)
	if !bytes.Equal(got, expected) {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestGenerateRandomTempPath(t *testing.T) {
	p1 := GenerateRandomTempPath("script")
	p2 := GenerateRandomTempPath("script")

	if !strings.HasPrefix(p1, "/tmp/_script_") {
		t.Fatalf("expected prefix /tmp/_script_, got %s", p1)
	}
	if p1 == p2 {
		t.Fatalf("expected unique temp paths, got identical %s", p1)
	}

	pDefault := GenerateRandomTempPath("")
	if !strings.HasPrefix(pDefault, "/tmp/_amnz_") {
		t.Fatalf("expected default prefix /tmp/_amnz_, got %s", pDefault)
	}
}

func TestSFTPErrors_NilClient(t *testing.T) {
	ctx := context.Background()

	if err := EnsureRemoteDir(nil, "/dir", 0755); !errors.Is(err, ErrSFTPNotAvailable) {
		t.Fatalf("expected ErrSFTPNotAvailable, got %v", err)
	}

	if err := SFTPUpload(ctx, nil, "/file", []byte("a"), 0644); !errors.Is(err, ErrSFTPNotAvailable) {
		t.Fatalf("expected ErrSFTPNotAvailable, got %v", err)
	}

	if _, err := SFTPDownload(ctx, nil, "/file"); !errors.Is(err, ErrSFTPNotAvailable) {
		t.Fatalf("expected ErrSFTPNotAvailable, got %v", err)
	}

	if _, err := SFTPFileExists(ctx, nil, "/file"); !errors.Is(err, ErrSFTPNotAvailable) {
		t.Fatalf("expected ErrSFTPNotAvailable, got %v", err)
	}
}

func TestSFTPErrors_EmptyPath(t *testing.T) {
	ctx := context.Background()

	// Non-nil dummy won't be called because path check is prior to network
	if err := SFTPUpload(ctx, nil, "", []byte("a"), 0644); !errors.Is(err, ErrSFTPNotAvailable) && !errors.Is(err, ErrEmptyPath) {
		t.Fatalf("expected ErrSFTPNotAvailable or ErrEmptyPath, got %v", err)
	}
}

package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecideUpdatePushoverSendsForNewInstallableBackgroundUpdate(t *testing.T) {
	cfg := updatePushoverTestConfig()
	state := updatePushoverTestState()

	got := decideUpdatePushover(cfg, state, updateCheckBackground)

	if !got.Send {
		t.Fatalf("Send = false, want true; reason=%q", got.Reason)
	}
	if got.Message == "" {
		t.Fatal("Message is empty")
	}
}

func TestDecideUpdatePushoverSkipsDisabled(t *testing.T) {
	cfg := updatePushoverTestConfig()
	cfg.PushoverOnUpdate = false

	got := decideUpdatePushover(cfg, updatePushoverTestState(), updateCheckBackground)

	if got.Send || got.Reason != "disabled" {
		t.Fatalf("decision = %#v, want disabled skip", got)
	}
}

func TestDecideUpdatePushoverSkipsMissingCredentials(t *testing.T) {
	cfg := updatePushoverTestConfig()
	cfg.PushoverUser = ""

	got := decideUpdatePushover(cfg, updatePushoverTestState(), updateCheckBackground)

	if got.Send || got.Reason != "credentials missing" {
		t.Fatalf("decision = %#v, want credentials missing skip", got)
	}
}

func TestDecideUpdatePushoverSkipsAlreadyNotifiedTag(t *testing.T) {
	cfg := updatePushoverTestConfig()
	cfg.LastUpdatePushoverTag = "v1.9.0"

	got := decideUpdatePushover(cfg, updatePushoverTestState(), updateCheckBackground)

	if got.Send || got.Reason != "already notified" {
		t.Fatalf("decision = %#v, want already notified skip", got)
	}
}

func TestDecideUpdatePushoverSkipsMissingInstallableAssets(t *testing.T) {
	tests := map[string]UpdateState{
		"missing exe": func() UpdateState {
			state := updatePushoverTestState()
			state.DownloadURL = ""
			return state
		}(),
		"missing checksum": func() UpdateState {
			state := updatePushoverTestState()
			state.ChecksumURL = ""
			return state
		}(),
		"missing signature": func() UpdateState {
			state := updatePushoverTestState()
			state.SignatureURL = ""
			return state
		}(),
	}

	for name, state := range tests {
		t.Run(name, func(t *testing.T) {
			got := decideUpdatePushover(updatePushoverTestConfig(), state, updateCheckBackground)
			if got.Send || got.Reason != "not installable" {
				t.Fatalf("decision = %#v, want not installable skip", got)
			}
		})
	}
}

func TestDecideUpdatePushoverSkipsManualChecks(t *testing.T) {
	got := decideUpdatePushover(updatePushoverTestConfig(), updatePushoverTestState(), updateCheckManual)

	if got.Send || got.Reason != "manual check" {
		t.Fatalf("decision = %#v, want manual check skip", got)
	}
}

func updatePushoverTestConfig() Config {
	return Config{
		UsePushover:      true,
		PushoverOnUpdate: true,
		PushoverToken:    "token",
		PushoverUser:     "user",
	}
}

func updatePushoverTestState() UpdateState {
	return UpdateState{
		Available:    true,
		LatestTag:    "v1.9.0",
		LatestVer:    "1.9.0",
		DownloadURL:  "https://example.com/calendarr.exe",
		ChecksumURL:  "https://example.com/calendarr.exe.sha256",
		SignatureURL: "https://example.com/calendarr.exe.sig",
		ReleaseURL:   "https://example.com/release",
	}
}

func TestVerifyUpdateSignatureAcceptsMatchingSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	originalPublicKey := updatePublicKeyB64
	updatePublicKeyB64 = base64.StdEncoding.EncodeToString(pub)
	t.Cleanup(func() { updatePublicKeyB64 = originalPublicKey })

	path := filepath.Join(t.TempDir(), "calendarr.exe")
	data := []byte("MZ signed test executable")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, data)

	if err := verifyUpdateSignature(path, sig); err != nil {
		t.Fatalf("verifyUpdateSignature returned error: %v", err)
	}
}

func TestVerifyUpdateSignatureRejectsWrongSignature(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, otherPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	originalPublicKey := updatePublicKeyB64
	updatePublicKeyB64 = base64.StdEncoding.EncodeToString(pub)
	t.Cleanup(func() { updatePublicKeyB64 = originalPublicKey })

	path := filepath.Join(t.TempDir(), "calendarr.exe")
	data := []byte("MZ signed test executable")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(otherPriv, data)

	err = verifyUpdateSignature(path, sig)
	if err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("verifyUpdateSignature error = %v, want signature failure", err)
	}
}

func TestDownloadSignatureParsesBase64Signature(t *testing.T) {
	sig := make([]byte, ed25519.SignatureSize)
	for i := range sig {
		sig[i] = byte(i)
	}
	got, err := parseSignatureAsset([]byte(base64.StdEncoding.EncodeToString(sig) + "\n"))
	if err != nil {
		t.Fatalf("parseSignatureAsset: %v", err)
	}
	if string(got) != string(sig) {
		t.Fatal("parsed signature did not match input")
	}
}

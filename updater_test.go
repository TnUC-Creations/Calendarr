package main

import "testing"

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
		Available:   true,
		LatestTag:   "v1.9.0",
		LatestVer:   "1.9.0",
		DownloadURL: "https://example.com/calendarr.exe",
		ChecksumURL: "https://example.com/calendarr.exe.sha256",
		ReleaseURL:  "https://example.com/release",
	}
}

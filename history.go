package main

import (
	"encoding/json"
	"os"
	"sync"
)

// HistoryEntry matches the objects in history.json.
type HistoryEntry struct {
	Timestamp string `json:"timestamp"`
	Action    string `json:"action"`
	Message   string `json:"message"`
}

// historyMu serializes load-modify-save sequences against history.json.
// Plain reads via loadHistory do not acquire it — atomic rename in
// saveHistoryLocked guarantees readers see the old or new file, never partial.
var historyMu sync.Mutex

func loadHistory() []HistoryEntry {
	data, err := os.ReadFile(dataPath(historyFile))
	if err != nil {
		return nil
	}
	var entries []HistoryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil
	}
	return entries
}

// saveHistory writes the entries atomically. Acquires historyMu so concurrent
// callers cannot interleave with appendHistory.
func saveHistory(entries []HistoryEntry) error {
	historyMu.Lock()
	defer historyMu.Unlock()
	return saveHistoryLocked(entries)
}

// saveHistoryLocked writes the entries. Caller MUST already hold historyMu.
func saveHistoryLocked(entries []HistoryEntry) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	target := dataPath(historyFile)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// appendHistory adds entries and caps the total per the configured MaxHistoryEntries.
// Holds historyMu across the load and save so two simultaneous appenders cannot
// each load the same baseline and clobber each other's appends.
func appendHistory(newEntries []HistoryEntry) {
	historyMu.Lock()
	defer historyMu.Unlock()
	existing := loadHistory()
	existing = append(existing, newEntries...)
	cap := 2000
	if cfg, err := loadConfig(); err == nil && cfg.MaxHistoryEntries > 0 {
		cap = cfg.MaxHistoryEntries
	}
	if len(existing) > cap {
		existing = existing[len(existing)-cap:]
	}
	_ = saveHistoryLocked(existing)
}

// cleanupHistoryTmp removes a leftover history.json.tmp from a crash mid-rename
// during a previous run. Called once at startup.
func cleanupHistoryTmp() {
	tmp := dataPath(historyFile) + ".tmp"
	if _, err := os.Stat(tmp); err == nil {
		_ = os.Remove(tmp)
	}
}

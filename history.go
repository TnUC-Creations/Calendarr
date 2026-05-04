package main

import (
	"encoding/json"
	"os"
)

// HistoryEntry matches the objects in history.json.
type HistoryEntry struct {
	Timestamp string `json:"timestamp"`
	Action    string `json:"action"`
	Message   string `json:"message"`
}

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

func saveHistory(entries []HistoryEntry) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(dataPath(historyFile), data, 0644)
}

// appendHistory adds entries and caps the total per the configured MaxHistoryEntries.
func appendHistory(newEntries []HistoryEntry) {
	existing := loadHistory()
	existing = append(existing, newEntries...)
	cap := 2000
	if cfg, err := loadConfig(); err == nil && cfg.MaxHistoryEntries > 0 {
		cap = cfg.MaxHistoryEntries
	}
	if len(existing) > cap {
		existing = existing[len(existing)-cap:]
	}
	_ = saveHistory(existing)
}

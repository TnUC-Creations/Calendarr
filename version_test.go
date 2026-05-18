package main

import (
	"os"
	"regexp"
	"testing"
)

func TestInstallerVersionMatchesAppVersion(t *testing.T) {
	data, err := os.ReadFile("calendarr.iss")
	if err != nil {
		t.Fatalf("read calendarr.iss: %v", err)
	}
	re := regexp.MustCompile(`(?m)^#define AppVersion "([^"]+)"`)
	match := re.FindSubmatch(data)
	if match == nil {
		t.Fatal("AppVersion define not found in calendarr.iss")
	}
	if got := string(match[1]); got != appVersion {
		t.Fatalf("installer AppVersion = %q, appVersion = %q", got, appVersion)
	}
}

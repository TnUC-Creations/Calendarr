package main

import "testing"

func TestLoadTemplates(t *testing.T) {
	loadTemplates()
	if len(pageTemplates) == 0 {
		t.Fatal("expected templates to load")
	}
}

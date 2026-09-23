package main

import "testing"

func TestTargetValidation(t *testing.T) {
	if _, err := parseTargets("windows-amd64,linux-arm64"); err != nil {
		t.Fatal(err)
	}
	if _, err := parseTargets("darwin-arm64"); err == nil {
		t.Fatal("unsupported targets must be rejected")
	}
	if _, err := parseTargets("linux-amd64,linux-amd64"); err == nil {
		t.Fatal("duplicate targets must be rejected")
	}
}

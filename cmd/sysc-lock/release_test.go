package main

import (
	"os"
	"strings"
	"testing"
)

func TestInitialReleaseContract(t *testing.T) {
	if version != "0.1.0" {
		t.Fatalf("version=%q, want 0.1.0", version)
	}
	data, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"workflow_dispatch:", "ubuntu-22.04", "CGO_ENABLED: \"1\"", "./cmd/sysc-lock", "SHA256SUMS", "sysc-lock-session.service", "--verify-tag"} {
		if !strings.Contains(string(data), required) {
			t.Errorf("release workflow missing %q", required)
		}
	}
}

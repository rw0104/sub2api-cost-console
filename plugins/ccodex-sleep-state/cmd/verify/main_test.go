package main

import "testing"

func TestSensitivePackageContentDetection(t *testing.T) {
	for _, path := range []string{"auth.json", "nested/config.toml", "publisher.key", "certs/client.pfx", ".env"} {
		if !sensitivePath(path) {
			t.Fatalf("sensitive path was accepted: %s", path)
		}
	}
	for _, path := range []string{"signature.json", "publisher-public-key.txt", "licenses/LICENSE", "ui/app.js"} {
		if sensitivePath(path) {
			t.Fatalf("safe path was rejected: %s", path)
		}
	}
	if !containsPrivateKey([]byte("-----BEGIN PRIVATE KEY-----\nsecret")) {
		t.Fatal("private key marker was not detected")
	}
	if containsPrivateKey([]byte("public key only")) {
		t.Fatal("ordinary public data was rejected")
	}
}

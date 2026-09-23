package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidation(t *testing.T) {
	c := Default()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"0.0.0.0:17841", "localhost:17841", "127.0.0.1:0", "127.0.0.1:70000", "10.0.0.1:17841"} {
		c := Default()
		c.Listen = address
		if c.Validate() == nil {
			t.Errorf("accepted %s", address)
		}
	}
	for _, address := range []string{"http://example.com/backend-api/codex", "https://user:pass@example.com/backend-api/codex", "https://example.com/v1", "https://example.com/backend-api/codex?token=secret"} {
		c := Default()
		c.Upstream = address
		if c.Validate() == nil {
			t.Error("accepted unsafe upstream")
		}
	}
}
func TestStrictJSONAndPaths(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	for _, s := range []string{`{"unknown":true}`, `{} {}`, `{"listen":"bad"}`} {
		os.WriteFile(p, []byte(s), 0600)
		if _, err := Load(p); err == nil {
			t.Fatal("bad config accepted")
		}
	}
	dir := t.TempDir()
	t.Setenv("CCODEX_STATE_HOME", dir)
	got, err := DataDir()
	if err != nil || got != dir {
		t.Fatal("data override ignored")
	}
	t.Setenv("CODEX_HOME", dir)
	got, err = Default().CodexDir()
	if err != nil || got != dir {
		t.Fatal("Codex override ignored")
	}
}

func TestSubscriptionHeaderAndFilterValidation(t *testing.T) {
	for _, source := range []Source{
		{UserAgent: "client\r\nAuthorization: secret"},
		{ExcludeKeywords: []string{""}},
		{ExcludeKeywords: make([]string, 65)},
	} {
		c := Default()
		c.Subscriptions = []Source{source}
		if c.Validate() == nil {
			t.Fatal("invalid subscription option accepted")
		}
	}
}

func TestRefreshModeNewAndExistingDefaults(t *testing.T) {
	if Default().StateRefreshMode != "on_demand" {
		t.Fatal("new installs must hold active")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil || c.StateRefreshMode != "standby" {
		t.Fatal(c.StateRefreshMode, err)
	}
	if err := os.WriteFile(path, []byte(`{"state_refresh_mode":"on_demand"}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err = Load(path)
	if err != nil || c.StateRefreshMode != "on_demand" {
		t.Fatal(c.StateRefreshMode, err)
	}
}

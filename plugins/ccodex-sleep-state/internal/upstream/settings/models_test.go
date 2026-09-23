package settings

import "testing"

func TestModelAndAccountSettings(t *testing.T) {
	for _, model := range append(SupportedModels(), "") {
		for _, mode := range []string{"auto", "personal", "team", ""} {
			c := Default()
			c.Model = model
			c.AccountMode = mode
			if err := c.Validate(); err != nil {
				t.Fatalf("model=%q mode=%q: %v", model, mode, err)
			}
		}
	}
	c := Default()
	c.Model = "gpt-unknown"
	if c.Validate() == nil {
		t.Fatal("accepted unknown model")
	}
	c = Default()
	c.AccountMode = "guess-from-length"
	if c.Validate() == nil {
		t.Fatal("accepted unknown account mode")
	}
	c.Model = ""
	if c.SelectedModel() != Model {
		t.Fatal("legacy config lost default model")
	}
}

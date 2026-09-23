package main

import (
	"reflect"

	"local.sub2api/ccodex-sleep-state/internal/config"
)

// These core setters are atomic and preserve active/ready state and the next
// allowed probe time. Source, account rules and budget changes still replace
// immutable engine generations; rejected credentials survive either path.
func sameExceptLivePolicies(left, right *config.Config) bool {
	if left == nil || right == nil {
		return false
	}
	strip := func(source *config.Config) *config.Config {
		copy := *source
		copy.Enabled, copy.InjectState = false, false
		copy.StateRefreshMode = ""
		copy.Accounts = make(map[string]config.AccountOverride)
		for id, override := range source.Accounts {
			override.Enabled, override.InjectState, override.StateRefreshMode = nil, nil, nil
			if !reflect.DeepEqual(override, config.AccountOverride{}) {
				copy.Accounts[id] = override
			}
		}
		return &copy
	}
	return sameRuntimeConfig(strip(left), strip(right))
}

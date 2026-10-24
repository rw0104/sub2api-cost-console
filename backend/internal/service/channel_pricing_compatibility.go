package service

import "encoding/json"

// UnmarshalJSON upgrades explicit legacy multipliers before API/import values
// are persisted. A supplied map, including {}, takes precedence over legacy max.
func (p *ChannelModelPricing) UnmarshalJSON(data []byte) error {
	type pricingJSON ChannelModelPricing
	if err := json.Unmarshal(data, (*pricingJSON)(p)); err != nil {
		return err
	}
	p.ReasoningEffortMultipliers = configuredReasoningEffortMultipliers(p)
	// Do not re-emit the legacy value: omitempty may omit an explicit empty map,
	// so retaining max here would restore a multiplier after a JSON round trip.
	p.MaxReasoningEffortMultiplier = nil
	return nil
}

// configuredReasoningEffortMultipliers also supports callers constructing
// pricing structs directly. Callers that modify the returned map must clone it.
func configuredReasoningEffortMultipliers(p *ChannelModelPricing) map[string]float64 {
	if p == nil {
		return nil
	}
	if p.ReasoningEffortMultipliers != nil || p.MaxReasoningEffortMultiplier == nil {
		return p.ReasoningEffortMultipliers
	}
	return map[string]float64{"max": *p.MaxReasoningEffortMultiplier}
}

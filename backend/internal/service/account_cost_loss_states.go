package service

import (
	"math"
	"sort"
)

// ConsolidateActiveCostLossStates reads legacy duplicate terminal events as
// one open lifecycle. The first terminal freezes accrual; refunds on any of
// its duplicate records remain recognized. No historical event is rewritten.
func ConsolidateActiveCostLossStates(states []AccountCostLossState) []AccountCostLossState {
	result := make([]AccountCostLossState, 0, len(states))
	active := make(map[int64]AccountCostLossState)
	refunds := make(map[int64]float64)
	eventIDs := make(map[int64]map[int64]struct{})
	for _, state := range states {
		if !state.Active {
			result = append(result, state)
			continue
		}
		refunds[state.AccountIDSnapshot] += state.RefundAmount
		if eventIDs[state.AccountIDSnapshot] == nil {
			eventIDs[state.AccountIDSnapshot] = make(map[int64]struct{})
		}
		for _, id := range append([]int64{state.TerminalEventID}, state.TerminalEventIDs...) {
			if id > 0 {
				eventIDs[state.AccountIDSnapshot][id] = struct{}{}
			}
		}
		previous, exists := active[state.AccountIDSnapshot]
		if !exists || costLossPrecedes(state, previous) {
			active[state.AccountIDSnapshot] = state
		}
	}
	for id, state := range active {
		state.NetLoss = math.Max(0, state.NetLoss-(refunds[id]-state.RefundAmount))
		state.RefundAmount = refunds[id]
		state.RecognizedCost = state.AccruedCost + state.NetLoss
		state.TerminalEventIDs = make([]int64, 0, len(eventIDs[id]))
		for eventID := range eventIDs[id] {
			state.TerminalEventIDs = append(state.TerminalEventIDs, eventID)
		}
		sort.Slice(state.TerminalEventIDs, func(i, j int) bool { return state.TerminalEventIDs[i] < state.TerminalEventIDs[j] })
		result = append(result, state)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].TerminalEventID > result[j].TerminalEventID
	})
	return result
}

func costLossPrecedes(left, right AccountCostLossState) bool {
	if left.TerminalEventID > 0 && right.TerminalEventID > 0 {
		return left.TerminalEventID < right.TerminalEventID
	}
	return left.OccurredAt.Before(right.OccurredAt)
}

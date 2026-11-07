package service

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// openAIWSReplayOutputItem builds an owned replay representation of an upstream
// output item. Token counts describe that output, not a new input item. Apply
// this only at the output collector: client-provided input and the original
// response event must remain unchanged.
func openAIWSReplayOutputItem(item gjson.Result) string {
	raw := strings.TrimSpace(item.Raw)
	const metadata = "internal_chat_message_metadata_passthrough"
	const outputCount = metadata + ".content_token_count"
	if !item.Get(outputCount).Exists() {
		return raw
	}
	updated, err := sjson.Delete(raw, outputCount)
	if err != nil {
		return raw
	}
	remaining := gjson.Get(updated, metadata)
	if remaining.IsObject() {
		hasFields := false
		remaining.ForEach(func(_, _ gjson.Result) bool {
			hasFields = true
			return false
		})
		if !hasFields {
			if withoutEmptyMetadata, err := sjson.Delete(updated, metadata); err == nil {
				return withoutEmptyMetadata
			}
		}
	}
	return updated
}

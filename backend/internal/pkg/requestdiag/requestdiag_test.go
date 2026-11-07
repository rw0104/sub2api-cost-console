package requestdiag

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestRecordRequestMeasuresRawUTF8JSONValues(t *testing.T) {
	input := `[{"content":"你好, } \\\"","tools":[1,2]}]`
	tools := `[{"name":"lookup","parameters":{"type":"object"}}]`
	instructions := `"line\n\u4f60"`
	body := []byte(` { "input" : ` + input + `, "tools": ` + tools + `, "instructions": ` + instructions + ` } `)
	ctx := WithContext(context.Background())
	RecordRequest(ctx, body)
	got := Load(ctx)
	if !got.RequestObserved || !got.JSONFieldsObserved || got.RequestBodyBytes != int64(len(body)) ||
		got.InputBytes != int64(len(input)) || got.ToolsBytes != int64(len(tools)) || got.InstructionsBytes != int64(len(instructions)) {
		t.Fatalf("unexpected byte accounting: %+v", got)
	}
	// The context retains scalars only, even after the caller reuses its buffer.
	for i := range body {
		body[i] = 'x'
	}
	RecordRequest(ctx, []byte(`{"input":"replacement"}`))
	if Load(ctx) != got {
		t.Fatal("first request metrics were mutated or replaced")
	}
}

func TestRecordRequestHandlesEscapedKeysMissingFieldsAndInvalidJSON(t *testing.T) {
	for _, tc := range []struct {
		body                       string
		valid                      bool
		input, tools, instructions int64
	}{
		{`{}`, true, 0, 0, 0},
		{`{"\u0069nput":null,"tools":[],"instructions":""}`, true, 4, 2, 2},
		{`{"input":1,"input":[2]}`, true, 4, 0, 0},
		{`{"input":`, false, 0, 0, 0},
		{`[]`, false, 0, 0, 0},
		{"", false, 0, 0, 0},
	} {
		t.Run(tc.body, func(t *testing.T) {
			ctx := WithContext(context.Background())
			RecordRequest(ctx, []byte(tc.body))
			got := Load(ctx)
			if !got.RequestObserved || got.RequestBodyBytes != int64(len(tc.body)) || got.JSONFieldsObserved != tc.valid ||
				got.InputBytes != tc.input || got.ToolsBytes != tc.tools || got.InstructionsBytes != tc.instructions {
				t.Fatalf("unexpected metrics: %+v", got)
			}
		})
	}
}

func TestLargeInputMeasurementDoesNotAllocatePayloadCopies(t *testing.T) {
	body := []byte(`{"input":"` + strings.Repeat("a", 1<<20) + `","tools":[]}`)
	allocations := testing.AllocsPerRun(5, func() {
		input, tools, _, valid := measureFields(body)
		if !valid || input != (1<<20)+2 || tools != 2 {
			t.Fatal("large input measurement failed")
		}
	})
	if allocations != 0 {
		t.Fatalf("measuring a large existing buffer must not allocate payload copies: %v allocations", allocations)
	}
}

func TestDispatchSummaryCountsHandoffsAndOmitsPartialPluginTotals(t *testing.T) {
	ctx := WithContext(context.Background())
	BeginUpstream(ctx)
	if got := Load(ctx); !got.UpstreamObserved || got.UpstreamAttempts != 0 || got.UpstreamBodyKnown {
		t.Fatalf("local rejection must have zero handoffs and no payload size: %+v", got)
	}
	RecordDispatch(ctx, 10)
	RecordDispatch(context.WithoutCancel(ctx), 0)
	if got := Load(ctx); got.UpstreamAttempts != 2 || !got.UpstreamBodyKnown || got.UpstreamBodyBytes != 0 {
		t.Fatalf("zero-length final dispatch must be measured: %+v", got)
	}
	OmitUpstream(ctx)
	RecordDispatch(ctx, 20)
	if got := Load(ctx); !got.UpstreamIncomplete {
		t.Fatal("a later native dispatch must not make a partial plugin count complete")
	}
}

func TestConcurrentDispatchesShareOneAccumulator(t *testing.T) {
	ctx := WithContext(context.Background())
	var group sync.WaitGroup
	for range 32 {
		group.Go(func() { RecordDispatch(ctx, 12); _ = Load(ctx) })
	}
	group.Wait()
	if got := Load(ctx); got.UpstreamAttempts != 32 {
		t.Fatalf("lost dispatches: %+v", got)
	}
}

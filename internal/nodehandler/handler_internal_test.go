package nodehandler

import (
	"context"
	"testing"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/xtls"
)

func TestResultAccumulatorRetainsFirstFailure(t *testing.T) {
	t.Parallel()
	var results resultAccumulator
	results.Add(xtls.HandlerResult{OK: true})
	results.Add(xtls.HandlerResult{OK: false, Message: "first"})
	results.Add(xtls.HandlerResult{OK: false, Message: "second"})
	response := results.Response()
	if response.Success || response.Error == nil || *response.Error != "first" {
		t.Fatalf("response = %#v", response)
	}
}

func TestBatchMutationTagsDeduplicatesWithoutInputSizedIntermediate(t *testing.T) {
	t.Parallel()
	got := batchMutationTags(
		[]string{"provider", "shared"},
		[]string{"affected", "shared"},
		[]BatchUser{{InboundData: []BatchInbound{{Tag: "inbound"}, {Tag: "provider"}}}},
	)
	want := []string{"provider", "shared", "affected", "inbound"}
	if len(got) != len(want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("tags = %v, want %v", got, want)
		}
	}
}

func TestAcquireMutationRejectsAlreadyCanceledContext(t *testing.T) {
	t.Parallel()
	service := NewService(nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 1000 {
		if service.acquireMutation(ctx) {
			service.releaseMutation()
			t.Fatal("already-canceled mutation acquired the operation gate")
		}
	}
}

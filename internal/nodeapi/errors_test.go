package nodeapi_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/contract"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/nodeapi"
)

func TestAsServiceErrorHandlesValuesPointersAndWrapping(t *testing.T) {
	t.Parallel()

	want := nodeapi.ServiceError{Status: 500, Code: "A010", Message: "failure"}
	tests := []error{
		want,
		&want,
		fmt.Errorf("wrapped: %w", want),
		fmt.Errorf("wrapped pointer: %w", &want),
	}
	for _, input := range tests {
		got, ok := nodeapi.AsServiceError(input)
		if !ok || got != want {
			t.Fatalf("AsServiceError(%T) = %+v, %v; want %+v, true", input, got, ok, want)
		}
	}
}

func TestApplicationErrorMatchesOfficialSchema(t *testing.T) {
	t.Parallel()

	response := nodeapi.NewApplicationError("/node/stats/get-users-stats", nodeapi.ServiceError{
		Status:  500,
		Code:    "A011",
		Message: "Failed to get users stats",
	})
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if err := contract.OfficialErrors.ApplicationResponse.ValidateJSON(raw); err != nil {
		t.Fatalf("application error violates official schema: %v\n%s", err, raw)
	}
}

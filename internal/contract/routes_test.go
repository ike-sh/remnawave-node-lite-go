package contract_test

import (
	"reflect"
	"sort"
	"testing"

	contractspec "github.com/Luxiaba/remnawave-node-lite-go/internal/contract"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/httpserver"
)

// The evidence package is independent from the dispatcher registry. Comparing
// them prevents a hand-maintained "implemented" map from self-passing.
var officialRoutes = func() []httpserver.NodeRoute {
	contracts := contractspec.OfficialRoutes()
	routes := make([]httpserver.NodeRoute, 0, len(contracts))
	for _, route := range contracts {
		routes = append(routes, httpserver.NodeRoute{Method: route.Method, Path: route.Path})
	}
	return routes
}()

func TestOfficialRouteRegistry(t *testing.T) {
	t.Parallel()

	want := append([]httpserver.NodeRoute(nil), officialRoutes...)
	sortRoutes(want)
	got := httpserver.RegisteredNodeRoutes()

	if len(got) != 26 {
		t.Fatalf("registered route count = %d, want 26", len(got))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("registered routes do not match official 2.8.0\n got: %#v\nwant: %#v", got, want)
	}
}

func sortRoutes(routes []httpserver.NodeRoute) {
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path == routes[j].Path {
			return routes[i].Method < routes[j].Method
		}
		return routes[i].Path < routes[j].Path
	})
}

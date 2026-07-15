package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSelectRoutesUsesSafeDefault(t *testing.T) {
	t.Parallel()

	routes, err := selectRoutes("", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 11 {
		t.Fatalf("route count = %d, want 11", len(routes))
	}
	for _, route := range routes {
		if !route.SafeForProbe() {
			t.Fatalf("unsafe route selected: %s", route.ID)
		}
	}
}

func TestSelectRoutesRequiresMutationOptIn(t *testing.T) {
	t.Parallel()

	if _, err := selectRoutes("xray.stop", false); err == nil {
		t.Fatal("mutating route did not require opt-in")
	}
	routes, err := selectRoutes("xray.stop", true)
	if err != nil || len(routes) != 1 || routes[0].ID != "xray.stop" {
		t.Fatalf("unexpected selection: routes=%#v err=%v", routes, err)
	}
}

func TestTargetFlagsRejectUnsafeURLs(t *testing.T) {
	t.Parallel()

	var targets targetFlags
	for _, value := range []string{
		"missing-separator",
		"plain=http://node.example:2222",
		"path=https://node.example:2222/node",
		"query=https://node.example:2222?secret=x",
	} {
		if err := targets.Set(value); err == nil {
			t.Errorf("accepted invalid target %q", value)
		}
	}
	if err := targets.Set("candidate=https://node.example:2222"); err != nil {
		t.Fatalf("valid target rejected: %v", err)
	}
	if err := targets.Set("candidate=https://other.example:2222"); err == nil {
		t.Fatal("duplicate target name accepted")
	}
}

func TestReadTokenFromEnvironment(t *testing.T) {
	t.Setenv(tokenEnvironment, "  header.payload.signature  \n")
	token, err := readToken("")
	if err != nil {
		t.Fatal(err)
	}
	if token != "header.payload.signature" {
		t.Fatalf("token = %q", token)
	}
}

func TestListDoesNotRequireCredentials(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"-list", "-pretty=false"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit = %d, stderr=%s", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"xray.start"`) || !strings.Contains(stdout.String(), `"safeByDefault":false`) {
		t.Fatalf("unexpected listing: %s", stdout.String())
	}
}

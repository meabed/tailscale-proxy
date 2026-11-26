package core

import (
	"os"
	"strings"
	"testing"
)

func TestChildArgs(t *testing.T) {
	orig := os.Args
	t.Cleanup(func() { os.Args = orig })
	os.Args = []string{"tsp", "--bg", "--private", "-bg", "--ports", "3000-4000"}
	if got := strings.Join(childArgs(), " "); got != "--private --ports 3000-4000" {
		t.Errorf("childArgs() = %q", got)
	}
}

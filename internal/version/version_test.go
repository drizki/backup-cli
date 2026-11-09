package version

import (
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	old := Version
	defer func() { Version = old }()
	Version = "1.2.3"
	if s := String(); !strings.HasPrefix(s, "1.2.3 ") || !strings.Contains(s, "commit") {
		t.Fatalf("unexpected %q", s)
	}
}

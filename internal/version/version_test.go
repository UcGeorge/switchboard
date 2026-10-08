package version

import (
	"strings"
	"testing"
)

func TestBuildVersion(t *testing.T) {
	old := Version
	defer func() { Version = old }()
	Version = "0.2.0"
	if Effective() != "0.2.0" || !strings.HasPrefix(String(), "0.2.0") {
		t.Fatal("build version not reported")
	}
}

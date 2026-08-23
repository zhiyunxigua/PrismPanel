package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteServerPortPreservesProperties(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "server.properties")
	original := "# generated\r\nmotd=Prism\r\nserver-port=25565\r\nonline-mode=true\r\n"
	if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := writeServerPort(workspace, 25570); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	if !strings.Contains(text, "motd=Prism\r\n") || !strings.Contains(text, "server-port=25570\r\n") {
		t.Fatalf("properties were not preserved: %q", text)
	}
	if strings.Count(text, "server-port=") != 1 {
		t.Fatalf("expected one server-port entry: %q", text)
	}
	port, err := readServerPort(workspace)
	if err != nil || port == nil || *port != 25570 {
		t.Fatalf("unexpected port: %v, %v", port, err)
	}
}

package api

import "testing"

func TestValidateFileSyncRequestNormalizesAndRejectsOverlappingPaths(t *testing.T) {
	request := fileSyncRequest{
		Source:  fileSyncResource{NodeID: "node-a", ResourceType: "instance", ResourceID: "server_1"},
		Paths:   []fileSyncPath{{Path: `plugins\\Example`, Type: "directory"}},
		Targets: []fileSyncTarget{{NodeID: "node-b", InstanceID: "server_2"}},
	}
	if err := validateFileSyncRequest(&request); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	if request.Paths[0].Path != "plugins/Example" {
		t.Fatalf("path was not normalized: %q", request.Paths[0].Path)
	}

	request.Paths = append(request.Paths, fileSyncPath{Path: "plugins/Example/config.yml", Type: "file"})
	if err := validateFileSyncRequest(&request); err == nil {
		t.Fatal("expected parent and child paths to be rejected")
	}
}

func TestValidateFileSyncRequestAllowsRootAndTrimmedRelativePaths(t *testing.T) {
	base := fileSyncRequest{
		Source:  fileSyncResource{NodeID: "node-a", ResourceType: "instance", ResourceID: "server_1"},
		Targets: []fileSyncTarget{{NodeID: "node-b", InstanceID: "server_2"}},
	}
	cases := map[string]string{".": ".", "./": ".", " ./plugins/Example ": "plugins/Example", `plugins\\Example`: "plugins/Example"}
	for value, expected := range cases {
		request := base
		request.Paths = []fileSyncPath{{Path: value, Type: "directory"}}
		if err := validateFileSyncRequest(&request); err != nil {
			t.Fatalf("relative path %q was rejected: %v", value, err)
		}
		if request.Paths[0].Path != expected {
			t.Fatalf("relative path %q normalized to %q, want %q", value, request.Paths[0].Path, expected)
		}
	}
}

func TestValidateFileSyncRequestRejectsUnsafeAndDuplicateTargets(t *testing.T) {
	base := fileSyncRequest{
		Source: fileSyncResource{NodeID: "node-a", ResourceType: "image", ResourceID: "image-1"},
		Paths:  []fileSyncPath{{Path: "server.properties", Type: "file"}},
	}
	for _, path := range []string{"../secret", "/absolute", ".prism-recycle-bin/manifest.json"} {
		request := base
		request.Paths = []fileSyncPath{{Path: path, Type: "file"}}
		request.Targets = []fileSyncTarget{{NodeID: "node-b", InstanceID: "server_2"}}
		if err := validateFileSyncRequest(&request); err == nil {
			t.Fatalf("unsafe path %q was accepted", path)
		}
	}

	request := base
	request.Targets = []fileSyncTarget{
		{NodeID: "node-b", InstanceID: "server_2"},
		{NodeID: "node-b", InstanceID: "server_2"},
	}
	if err := validateFileSyncRequest(&request); err == nil {
		t.Fatal("duplicate target was accepted")
	}
}

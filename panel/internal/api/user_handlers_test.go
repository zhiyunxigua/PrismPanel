package api

import "testing"

func TestPermissionSetContains(t *testing.T) {
	tests := []struct {
		name      string
		available []string
		required  []string
		want      bool
	}{
		{name: "wildcard", available: []string{"*"}, required: []string{"node.delete"}, want: true},
		{name: "subset", available: []string{"node.view", "user.update"}, required: []string{"node.view"}, want: true},
		{name: "missing", available: []string{"node.view"}, required: []string{"node.view", "node.delete"}, want: false},
		{name: "empty", available: []string{"node.view"}, required: nil, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := permissionSetContains(test.available, test.required); got != test.want {
				t.Fatalf("permissionSetContains() = %v, want %v", got, test.want)
			}
		})
	}
}

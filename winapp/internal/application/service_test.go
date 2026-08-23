package application

import "testing"

func TestNormalizePanelURL(t *testing.T) {
	tests := map[string]string{
		"https://panel.example.com/": "https://panel.example.com",
		" HTTP://127.0.0.1:8080 ":    "http://127.0.0.1:8080",
	}
	for input, expected := range tests {
		actual, err := NormalizePanelURL(input)
		if err != nil {
			t.Fatalf("normalize %q: %v", input, err)
		}
		if actual != expected {
			t.Fatalf("normalize %q = %q, want %q", input, actual, expected)
		}
	}
}

func TestNormalizePanelURLRejectsNonRootURL(t *testing.T) {
	for _, input := range []string{"panel.example.com", "ftp://panel.example.com", "https://panel.example.com/admin"} {
		if _, err := NormalizePanelURL(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}

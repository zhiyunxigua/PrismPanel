package nodes

import (
	"bytes"
	"testing"
)

func TestTokenEncryptionBindsNodeID(t *testing.T) {
	service, err := NewService(nil, nil, bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	const token = "node-token-that-must-not-appear-in-ciphertext"
	ciphertext, err := service.encryptToken("node-a", token)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte(token)) {
		t.Fatal("ciphertext contains plaintext token")
	}
	plain, err := service.decryptToken("node-a", ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if plain != token {
		t.Fatalf("unexpected token: %q", plain)
	}
	if _, err := service.decryptToken("node-b", ciphertext); err == nil {
		t.Fatal("ciphertext decrypted with the wrong node id")
	}
}

func TestValidateURLs(t *testing.T) {
	base, public, err := validateURLs(" HTTPS://Node.Example.COM:443/// ", "http://203.0.113.8:80/")
	if err != nil {
		t.Fatal(err)
	}
	if base != "https://node.example.com" || public != "http://203.0.113.8" {
		t.Fatalf("unexpected normalized urls: %q %q", base, public)
	}
	if _, _, err := validateURLs("ftp://node.example.com", ""); err == nil {
		t.Fatal("accepted unsupported url scheme")
	}
	if _, _, err := validateURLs("https://node.example.com?token=secret", ""); err == nil {
		t.Fatal("accepted a connection url with query parameters")
	}
}

func TestNormalizeHTTPURLPreservesPathCase(t *testing.T) {
	left, err := normalizeHTTPURL("连接地址", "HTTPS://NODE.EXAMPLE.COM:443/Control/", false)
	if err != nil {
		t.Fatal(err)
	}
	right, err := normalizeHTTPURL("连接地址", "https://node.example.com/control", false)
	if err != nil {
		t.Fatal(err)
	}
	if left != "https://node.example.com/Control" {
		t.Fatalf("unexpected normalized url: %q", left)
	}
	if left == right {
		t.Fatal("normalized case-sensitive paths as the same url")
	}
}

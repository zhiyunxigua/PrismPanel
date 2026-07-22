package game

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestFetchMinecraftClientLibsWithNetEaseAccount(t *testing.T) {
	email := os.Getenv("PRISM_TEST_NETEASE_EMAIL")
	password := os.Getenv("PRISM_TEST_NETEASE_PASSWORD")
	if email == "" || password == "" {
		t.Skip("set PRISM_TEST_NETEASE_EMAIL and PRISM_TEST_NETEASE_PASSWORD to run the real NetEase feasibility test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	client, err := NewClient(AccountState{Email: email, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	account, err := client.Login(ctx)
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if account.UserID == "" || account.UserToken == "" {
		t.Fatal("login did not return X19 user credentials")
	}

	base, err := client.FetchMinecraftClientLibs(ctx, VersionBase)
	if err != nil {
		t.Fatalf("fetch base package info failed: %v", err)
	}
	if base.URL == "" || base.MD5 == "" {
		t.Fatalf("incomplete base package info: name=%s mc=%d", base.Name, base.MCVersion)
	}

	libs, err := client.FetchMinecraftClientLibs(ctx, Version1_20_6)
	if err != nil {
		t.Fatalf("fetch game patch info failed: %v", err)
	}
	if libs.URL == "" || libs.MD5 == "" || libs.CoreLibURL == "" || libs.CoreLibMD5 == "" {
		t.Fatalf("incomplete game patch info: version=%s mc=%d", libs.Version, libs.MCVersion)
	}

	paths, err := DefaultCachePaths()
	if err != nil {
		t.Fatal(err)
	}
	downloads := VersionDownloads(paths, base, libs)
	if len(downloads) != 3 {
		t.Fatalf("unexpected download count: %d", len(downloads))
	}
}

package updater

import "testing"

func TestCompareVersions(t *testing.T) {
	if CompareVersions("0.0.1", "0.0.1") != 0 || CompareVersions("1.0.0", "0.99.99") <= 0 ||
		CompareVersions("0.0.1", "dev") <= 0 {
		t.Fatal("semantic version comparison failed")
	}
}

func TestValidateRelease(t *testing.T) {
	release := Release{
		Version: "0.0.2", Platform: "windows", Arch: "amd64", Size: 2,
		SHA256:      "0000000000000000000000000000000000000000000000000000000000000000",
		DownloadURL: "/api/v1/winapp/releases/0.0.2/download",
	}
	if err := validateRelease(release); err != nil {
		t.Fatal(err)
	}
	release.Arch = "arm64"
	if err := validateRelease(release); err == nil {
		t.Fatal("unsupported architecture was accepted")
	}
}

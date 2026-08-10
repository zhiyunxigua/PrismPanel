package game

import "testing"

func TestVersion1_21_10Mapping(t *testing.T) {
	if Version1_21_10 != 1021010 {
		t.Fatalf("version id mismatch: %d", Version1_21_10)
	}

	label, err := VersionLabel(Version1_21_10)
	if err != nil {
		t.Fatal(err)
	}
	if label != "1.21.10" {
		t.Fatalf("version label mismatch: %q", label)
	}

	version, err := VersionFromLabel("1.21.10")
	if err != nil {
		t.Fatal(err)
	}
	if version != Version1_21_10 {
		t.Fatalf("version mapping mismatch: %d", version)
	}

	options := SupportedVersions()
	latest := options[len(options)-1]
	if latest.Version != Version1_21_10 || latest.Label != "1.21.10" || latest.Java != "jdk21" {
		t.Fatalf("latest version option mismatch: %+v", latest)
	}
}

package game

import "fmt"

type Version uint32

const (
	VersionBase    Version = 0
	VersionUnknown Version = VersionBase
	Version1_8_9   Version = 1008009
	Version1_12_2  Version = 1012002
	Version1_16    Version = 1016000
	Version1_18    Version = 1018000
	Version1_19_2  Version = 1019002
	Version1_20    Version = 1020000
	Version1_20_6  Version = 1020006
	Version1_21    Version = 1021000
	Version1_21_8  Version = 1021008
)

type VersionOption struct {
	Label   string  `json:"label"`
	Version Version `json:"version"`
	Java    string  `json:"java"`
}

func SupportedVersions() []VersionOption {
	return []VersionOption{
		{Label: "1.8.9", Version: Version1_8_9, Java: "jre8"},
		{Label: "1.12.2", Version: Version1_12_2, Java: "jre8"},
		{Label: "1.16", Version: Version1_16, Java: "jdk17"},
		{Label: "1.18", Version: Version1_18, Java: "jdk17"},
		{Label: "1.19.2", Version: Version1_19_2, Java: "jdk17"},
		{Label: "1.20", Version: Version1_20, Java: "jdk17"},
		{Label: "1.20.6", Version: Version1_20_6, Java: "jdk21"},
		{Label: "1.21", Version: Version1_21, Java: "jdk21"},
		{Label: "1.21.8", Version: Version1_21_8, Java: "jdk21"},
	}
}

func VersionLabel(version Version) (string, error) {
	for _, option := range SupportedVersions() {
		if option.Version == version {
			return option.Label, nil
		}
	}
	return "", fmt.Errorf("unsupported game version: %d", version)
}

func (v Version) Validate() error {
	if v == VersionBase {
		return nil
	}
	_, err := VersionLabel(v)
	return err
}

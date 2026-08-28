//go:build !windows

package config

func defaultSessionSocket() string { return "/run/prism-sessiond/session.sock" }

func defaultSessionTokenFile() string { return "/etc/prism-sessiond/token" }

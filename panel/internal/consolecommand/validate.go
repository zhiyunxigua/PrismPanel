package consolecommand

import (
	"errors"
	"strings"
)

var ErrOperatorManagement = errors.New("operator management is controlled by the panel")

func Validate(command string, manageOperators bool) error {
	if !manageOperators {
		return nil
	}
	parts := strings.Fields(strings.TrimSpace(command))
	if len(parts) == 0 {
		return nil
	}
	root := strings.ToLower(strings.TrimPrefix(parts[0], "/"))
	if separator := strings.LastIndexByte(root, ':'); separator >= 0 {
		root = root[separator+1:]
	}
	if root == "op" || root == "deop" {
		return ErrOperatorManagement
	}
	return nil
}

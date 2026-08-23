//go:build linux

package firewall

import (
	"errors"
	"testing"
)

func TestNftElementErrorClassification(t *testing.T) {
	if !isExistingElementError(errors.New("Error: File exists")) {
		t.Fatal("did not recognize existing-element error")
	}
	if !isMissingElementError(errors.New("Error: No such file or directory")) {
		t.Fatal("did not recognize missing-element error")
	}
	if isExistingElementError(errors.New("permission denied")) {
		t.Fatal("misclassified unrelated nft error")
	}
}

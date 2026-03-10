package command

import (
	"context"
	"testing"
)

func TestQuotaSetCommandValidation(t *testing.T) {
	cmd := &QuotaSetCommand{}
	if err := cmd.Run(context.Background(), nil); err == nil {
		t.Fatal("expected missing selector error")
	}

	cmd = &QuotaSetCommand{userID: "u", bucket: "b", maxBytes: 1}
	if err := cmd.Run(context.Background(), nil); err == nil {
		t.Fatal("expected one-of user/bucket error")
	}

	cmd = &QuotaSetCommand{userID: "u", maxBytes: -1}
	if err := cmd.Run(context.Background(), nil); err == nil {
		t.Fatal("expected negative max_bytes error")
	}
}

func TestQuotaGetCommandValidation(t *testing.T) {
	cmd := &QuotaGetCommand{}
	if err := cmd.Run(context.Background(), nil); err == nil {
		t.Fatal("expected missing selector error")
	}

	cmd = &QuotaGetCommand{userID: "u", bucket: "b"}
	if err := cmd.Run(context.Background(), nil); err == nil {
		t.Fatal("expected one-of user/bucket error")
	}
}

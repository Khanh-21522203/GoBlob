package command

import (
	"context"
	"testing"
)

func TestLifecycleProcessCommandValidation(t *testing.T) {
	cmd := &LifecycleProcessCommand{filerAddr: ""}
	if err := cmd.Run(context.Background(), nil); err == nil {
		t.Fatal("expected missing filer error")
	}
}

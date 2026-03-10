package command

import (
	"context"
	"testing"
)

func TestVolumeECEncodeCommandValidation(t *testing.T) {
	cmd := &VolumeECEncodeCommand{}
	if err := cmd.Run(context.Background(), nil); err == nil {
		t.Fatal("expected missing vid error")
	}

	cmd = &VolumeECEncodeCommand{volumeID: 1, dataShards: 0, parityShards: 3}
	if err := cmd.Run(context.Background(), nil); err == nil {
		t.Fatal("expected invalid shard config error")
	}

	cmd = &VolumeECEncodeCommand{volumeID: 1, dataShards: 2, parityShards: 1, nodesCSV: "node-a"}
	if err := cmd.Run(context.Background(), nil); err == nil {
		t.Fatal("expected insufficient nodes error")
	}

	cmd = &VolumeECEncodeCommand{volumeID: 1, dataShards: 2, parityShards: 1, apply: true, outputDir: t.TempDir()}
	if err := cmd.Run(context.Background(), nil); err == nil {
		t.Fatal("expected missing source grpc error")
	}
}

func TestVolumeECEncodeCommandRun(t *testing.T) {
	cmd := &VolumeECEncodeCommand{volumeID: 7, dataShards: 2, parityShards: 1, nodesCSV: "node-a,node-b,node-c"}
	if err := cmd.Run(context.Background(), nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

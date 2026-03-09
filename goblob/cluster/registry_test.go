package cluster

import (
	"fmt"
	"testing"
	"time"

	"GoBlob/goblob/pb/master_pb"
)

func TestClusterRegistry(t *testing.T) {
	t.Run("new registry", func(t *testing.T) {
		cr := NewClusterRegistry()
		defer cr.Shutdown()

		if cr.GetNodeCount() != 0 {
			t.Errorf("expected 0 nodes, got %d", cr.GetNodeCount())
		}
	})

	t.Run("register node", func(t *testing.T) {
		cr := NewClusterRegistry()
		defer cr.Shutdown()

		req := &master_pb.KeepConnectedRequest{
			ClientType:    "filer",
			ClientAddress: "filer1:8080",
			Version:       "1.0.0",
			DataCenter:    "dc1",
			Rack:          "rack1",
			GrpcPort:      "18080",
		}

		node, err := cr.Register(req)
		if err != nil {
			t.Fatalf("failed to register node: %v", err)
		}

		if cr.GetNodeCount() != 1 {
			t.Errorf("expected 1 node, got %d", cr.GetNodeCount())
		}

		if node.GetClientType() != "filer" {
			t.Errorf("expected client type=filer, got %s", node.GetClientType())
		}

		if node.GetDataCenter() != "dc1" {
			t.Errorf("expected data center=dc1, got %s", node.GetDataCenter())
		}
	})

	t.Run("update last seen", func(t *testing.T) {
		cr := NewClusterRegistry()
		defer cr.Shutdown()

		req := &master_pb.KeepConnectedRequest{
			ClientType:    "filer",
			ClientAddress: "filer1:8080",
		}

		cr.Register(req)
		nodeId := req.GetClientAddress()

		// Wait a bit
		time.Sleep(100 * time.Millisecond)

		// Update last seen
		cr.UpdateLastSeen(nodeId)

		// Check node is alive
		node, ok := cr.GetNode(nodeId)
		if !ok {
			t.Fatal("node not found")
		}

		if !node.IsAlive() {
			t.Error("expected node to be alive")
		}
	})

	t.Run("unregister node", func(t *testing.T) {
		cr := NewClusterRegistry()
		defer cr.Shutdown()

		req := &master_pb.KeepConnectedRequest{
			ClientType:    "filer",
			ClientAddress: "filer1:8080",
		}

		cr.Register(req)
		nodeId := req.GetClientAddress()

		if cr.GetNodeCount() != 1 {
			t.Errorf("expected 1 node, got %d", cr.GetNodeCount())
		}

		// Unregister
		cr.Unregister(nodeId)

		if cr.GetNodeCount() != 0 {
			t.Errorf("expected 0 nodes after unregister, got %d", cr.GetNodeCount())
		}
	})

	t.Run("expire dead nodes", func(t *testing.T) {
		cr := NewClusterRegistry()
		defer cr.Shutdown()

		req := &master_pb.KeepConnectedRequest{
			ClientType:    "filer",
			ClientAddress: "filer1:8080",
		}

		cr.Register(req)

		// Manually set last seen to long ago
		if node, ok := cr.GetNode(req.GetClientAddress()); ok {
			node.mu.Lock()
			node.lastSeen = time.Now().Add(-60 * time.Second)
			node.mu.Unlock()
		}

		// Trigger expiration
		cr.ExpireDeadNodes()

		if cr.GetNodeCount() != 0 {
			t.Errorf("expected 0 nodes after expiration, got %d", cr.GetNodeCount())
		}
	})

	t.Run("for each node", func(t *testing.T) {
		cr := NewClusterRegistry()
		defer cr.Shutdown()

		// Register multiple nodes
		for i := 0; i < 3; i++ {
			req := &master_pb.KeepConnectedRequest{
				ClientType:    "filer",
				ClientAddress: fmt.Sprintf("filer%d:8080", i),
			}
			cr.Register(req)
		}

		count := 0
		cr.ForEachNode(func(node *ClusterNode) bool {
			count++
			return true
		})

		if count != 3 {
			t.Errorf("expected 3 nodes, got %d", count)
		}
	})

	t.Run("concurrent access", func(t *testing.T) {
		cr := NewClusterRegistry()
		defer cr.Shutdown()

		done := make(chan bool)

		// Register nodes concurrently
		for i := 0; i < 10; i++ {
			go func(idx int) {
				req := &master_pb.KeepConnectedRequest{
					ClientType:    "filer",
					ClientAddress: fmt.Sprintf("filer%d:8080", idx),
				}
				cr.Register(req)
				done <- true
			}(i)
		}

		// Wait for all goroutines
		for i := 0; i < 10; i++ {
			<-done
		}

		if cr.GetNodeCount() != 10 {
			t.Errorf("expected 10 nodes, got %d", cr.GetNodeCount())
		}
	})

	t.Run("node is alive", func(t *testing.T) {
		cr := NewClusterRegistry()
		defer cr.Shutdown()

		req := &master_pb.KeepConnectedRequest{
			ClientType:    "filer",
			ClientAddress: "filer1:8080",
		}

		cr.Register(req)

		node, ok := cr.GetNode(req.GetClientAddress())
		if !ok {
			t.Fatal("node not found")
		}

		if !node.IsAlive() {
			t.Error("expected node to be alive initially")
		}

		// Make node old
		node.mu.Lock()
		node.lastSeen = time.Now().Add(-60 * time.Second)
		node.mu.Unlock()

		if node.IsAlive() {
			t.Error("expected node to be dead after 60 seconds")
		}
	})
}

func TestClusterNode(t *testing.T) {
	t.Run("get methods", func(t *testing.T) {
		node := &ClusterNode{
			id:            "filer1:8080",
			clientType:    "filer",
			clientAddress: "filer1:8080",
			version:       "1.0.0",
			filerGroup:    "group1",
			dataCenter:    "dc1",
			rack:          "rack1",
			grpcPort:      "18080",
			lastSeen:      time.Now(),
		}

		if node.GetId() != "filer1:8080" {
			t.Errorf("expected id=filer1:8080, got %s", node.GetId())
		}

		if node.GetClientType() != "filer" {
			t.Errorf("expected client type=filer, got %s", node.GetClientType())
		}

		if node.GetVersion() != "1.0.0" {
			t.Errorf("expected version=1.0.0, got %s", node.GetVersion())
		}

		if node.GetFilerGroup() != "group1" {
			t.Errorf("expected filer group=group1, got %s", node.GetFilerGroup())
		}

		if node.GetDataCenter() != "dc1" {
			t.Errorf("expected data center=dc1, got %s", node.GetDataCenter())
		}

		if node.GetRack() != "rack1" {
			t.Errorf("expected rack=rack1, got %s", node.GetRack())
		}

		if node.GetGrpcPort() != "18080" {
			t.Errorf("expected grpc port=18080, got %s", node.GetGrpcPort())
		}
	})

	t.Run("to proto", func(t *testing.T) {
		node := &ClusterNode{
			id:            "filer1:8080",
			clientType:    "filer",
			clientAddress: "filer1:8080",
			lastSeen:      time.Now(),
		}

		resp := node.ToProto()
		if resp == nil {
			t.Error("expected non-nil proto response")
		}
	})
}

package conformance

import (
	"testing"
	"time"

	"GoBlob/goblob/consensus"
)

// StateProbe exposes the replicated test state for one consensus node.
type StateProbe interface {
	MaxFileID() uint64
}

// Node is one member of a conformance test cluster.
type Node struct {
	ID     string
	Engine consensus.Engine
	Probe  StateProbe
}

// Cluster is the engine-specific cluster lifecycle contract used by the shared
// conformance scenarios.
type Cluster interface {
	Nodes() []*Node
	Command(maxFileID uint64) consensus.Command
	ShutdownNode(t *testing.T, index int)
	RestartNode(t *testing.T, index int)
	Shutdown()
}

// Factory creates isolated consensus clusters for conformance scenarios.
type Factory interface {
	NewCluster(t *testing.T, size int) Cluster
}

// Run executes the shared consensus conformance suite against a factory.
func Run(t *testing.T, factory Factory) {
	t.Helper()

	t.Run("single node leadership and apply", func(t *testing.T) {
		cluster := factory.NewCluster(t, 1)
		defer cluster.Shutdown()

		leader := waitForLeader(t, cluster.Nodes(), 5*time.Second)
		if leader == nil {
			return
		}
		if err := leader.Engine.Apply(cluster.Command(1000), 5*time.Second); err != nil {
			t.Fatalf("Apply() error = %v", err)
		}
		if err := leader.Engine.Barrier(5 * time.Second); err != nil {
			t.Fatalf("Barrier() error = %v", err)
		}
		waitForValue(t, cluster.Nodes(), 1000, 5*time.Second)
	})

	t.Run("follower rejects apply", func(t *testing.T) {
		cluster := factory.NewCluster(t, 3)
		defer cluster.Shutdown()

		leader := waitForLeader(t, cluster.Nodes(), 5*time.Second)
		follower := firstLiveFollower(cluster.Nodes(), leader)
		if follower == nil {
			t.Fatal("no follower available")
		}

		if err := follower.Engine.Apply(cluster.Command(2000), 700*time.Millisecond); err == nil {
			t.Fatal("expected follower Apply() to fail")
		}
	})

	t.Run("leader failover preserves progress", func(t *testing.T) {
		cluster := factory.NewCluster(t, 3)
		defer cluster.Shutdown()

		nodes := cluster.Nodes()
		leader := waitForLeader(t, nodes, 5*time.Second)
		leaderIndex := indexOf(nodes, leader)
		if leaderIndex < 0 {
			t.Fatal("leader not found in cluster")
		}

		if err := leader.Engine.Apply(cluster.Command(3000), 5*time.Second); err != nil {
			t.Fatalf("initial Apply() error = %v", err)
		}
		waitForValue(t, nodes, 3000, 5*time.Second)

		cluster.ShutdownNode(t, leaderIndex)
		survivors := liveNodes(nodes)
		newLeader := waitForLeader(t, survivors, 5*time.Second)
		if err := newLeader.Engine.Apply(cluster.Command(4000), 5*time.Second); err != nil {
			t.Fatalf("Apply() after failover error = %v", err)
		}
		waitForValue(t, survivors, 4000, 5*time.Second)
	})

	t.Run("restart recovers committed state", func(t *testing.T) {
		cluster := factory.NewCluster(t, 3)
		defer cluster.Shutdown()

		nodes := cluster.Nodes()
		leader := waitForLeader(t, nodes, 5*time.Second)
		if err := leader.Engine.Apply(cluster.Command(5000), 5*time.Second); err != nil {
			t.Fatalf("initial Apply() error = %v", err)
		}
		waitForValue(t, nodes, 5000, 5*time.Second)

		follower := firstLiveFollower(nodes, leader)
		if follower == nil {
			t.Fatal("no follower available")
		}
		followerIndex := indexOf(nodes, follower)
		cluster.ShutdownNode(t, followerIndex)

		if err := leader.Engine.Apply(cluster.Command(6000), 5*time.Second); err != nil {
			t.Fatalf("Apply() while follower down error = %v", err)
		}
		waitForValue(t, liveNodes(nodes), 6000, 5*time.Second)

		cluster.RestartNode(t, followerIndex)
		waitForValue(t, nodes, 6000, 5*time.Second)
	})

	t.Run("quorum loss prevents commit", func(t *testing.T) {
		cluster := factory.NewCluster(t, 3)
		defer cluster.Shutdown()

		nodes := cluster.Nodes()
		leader := waitForLeader(t, nodes, 5*time.Second)
		leaderIndex := indexOf(nodes, leader)
		if leaderIndex < 0 {
			t.Fatal("leader not found in cluster")
		}

		var stopped []int
		for i := range nodes {
			if i != leaderIndex {
				cluster.ShutdownNode(t, i)
				stopped = append(stopped, i)
			}
		}

		if err := leader.Engine.Apply(cluster.Command(7000), 700*time.Millisecond); err == nil {
			t.Fatal("expected Apply() without quorum to fail")
		}

		for _, i := range stopped {
			cluster.RestartNode(t, i)
		}
		newLeader := waitForLeader(t, nodes, 5*time.Second)
		if err := newLeader.Engine.Apply(cluster.Command(8000), 5*time.Second); err != nil {
			t.Fatalf("Apply() after quorum recovery error = %v", err)
		}
		waitForValue(t, nodes, 8000, 5*time.Second)
	})
}

func waitForLeader(t *testing.T, nodes []*Node, timeout time.Duration) *Node {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var leader *Node
		leaderCount := 0
		for _, node := range nodes {
			if node == nil || node.Engine == nil {
				continue
			}
			if node.Engine.IsLeader() {
				leader = node
				leaderCount++
			}
		}
		if leaderCount == 1 {
			return leader
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timeout waiting for exactly one leader")
	return nil
}

func waitForValue(t *testing.T, nodes []*Node, want uint64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ok := true
		for _, node := range nodes {
			if node == nil || node.Engine == nil {
				continue
			}
			if node.Probe == nil || node.Probe.MaxFileID() != want {
				ok = false
				break
			}
		}
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, node := range nodes {
		if node == nil || node.Engine == nil || node.Probe == nil {
			continue
		}
		t.Errorf("node %s: max_file_id=%d, want %d", node.ID, node.Probe.MaxFileID(), want)
	}
	t.Fatal("timeout waiting for replicated value")
}

func firstLiveFollower(nodes []*Node, leader *Node) *Node {
	for _, node := range nodes {
		if node == nil || node.Engine == nil || node == leader {
			continue
		}
		if !node.Engine.IsLeader() {
			return node
		}
	}
	return nil
}

func liveNodes(nodes []*Node) []*Node {
	live := make([]*Node, 0, len(nodes))
	for _, node := range nodes {
		if node != nil && node.Engine != nil {
			live = append(live, node)
		}
	}
	return live
}

func indexOf(nodes []*Node, target *Node) int {
	for i, node := range nodes {
		if node == target {
			return i
		}
	}
	return -1
}

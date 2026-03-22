package raft

import (
	"io"
	"testing"
	"time"

	hraft "github.com/hashicorp/raft"
)

// inmemNode is a fully in-process Raft node used for multi-node tests.
// It bypasses NewRaftServer and talks directly to hashicorp/raft so tests
// have no TCP dependency.
type inmemNode struct {
	r           *hraft.Raft
	fsm         *MasterFSM
	transport   *hraft.InmemTransport
	addr        hraft.ServerAddress
	logStore    *hraft.InmemStore
	stableStore *hraft.InmemStore
	snapStore   hraft.SnapshotStore
	cfg         *hraft.Config
}

func newInmemNode(t *testing.T) *inmemNode {
	t.Helper()
	addr, transport := hraft.NewInmemTransport("")

	logStore := hraft.NewInmemStore()
	stableStore := hraft.NewInmemStore()
	snapStore, err := hraft.NewFileSnapshotStore(t.TempDir(), 1, io.Discard)
	if err != nil {
		t.Fatalf("snapshot store: %v", err)
	}

	cfg := hraft.DefaultConfig()
	cfg.LocalID = hraft.ServerID(addr)
	cfg.HeartbeatTimeout = 100 * time.Millisecond
	cfg.ElectionTimeout = 100 * time.Millisecond
	cfg.LeaderLeaseTimeout = 50 * time.Millisecond
	cfg.CommitTimeout = 10 * time.Millisecond
	cfg.Logger = nil // suppress hashicorp/raft log noise in tests

	return &inmemNode{
		fsm:         NewMasterFSM(nil, nil, nil),
		transport:   transport,
		addr:        addr,
		logStore:    logStore,
		stableStore: stableStore,
		snapStore:   snapStore,
		cfg:         cfg,
	}
}

// bootstrapAndStart bootstraps the cluster configuration on every node,
// connects transports, and starts Raft on each node.
func bootstrapAndStart(t *testing.T, nodes []*inmemNode) {
	t.Helper()
	servers := make([]hraft.Server, len(nodes))
	for i, n := range nodes {
		servers[i] = hraft.Server{
			ID:      hraft.ServerID(n.addr),
			Address: n.addr,
		}
	}
	clusterCfg := hraft.Configuration{Servers: servers}

	// Write bootstrap configuration to each node's stores before starting Raft.
	for _, n := range nodes {
		if err := hraft.BootstrapCluster(n.cfg, n.logStore, n.stableStore, n.snapStore, n.transport, clusterCfg); err != nil {
			t.Fatalf("bootstrap %s: %v", n.addr, err)
		}
	}

	// Connect all transports so they can reach each other.
	for i, n := range nodes {
		for j, peer := range nodes {
			if i != j {
				n.transport.Connect(peer.addr, peer.transport)
			}
		}
	}

	// Start Raft on each node.
	for _, n := range nodes {
		r, err := hraft.NewRaft(n.cfg, n.fsm, n.logStore, n.stableStore, n.snapStore, n.transport)
		if err != nil {
			t.Fatalf("new raft %s: %v", n.addr, err)
		}
		n.r = r
	}
}

func shutdownAll(nodes []*inmemNode) {
	for _, n := range nodes {
		if n.r != nil {
			n.r.Shutdown()
		}
	}
}

func waitForLeader(t *testing.T, nodes []*inmemNode, timeout time.Duration) *inmemNode {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, n := range nodes {
			if n.r != nil && n.r.State() == hraft.Leader {
				return n
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no leader elected within timeout")
	return nil
}

// ── Partition helpers ────────────────────────────────────────────────────────

// partitionAway performs a bidirectional disconnect: isolated cannot send to
// any of others, and none of others can send to isolated.
func partitionAway(isolated *inmemNode, others []*inmemNode) {
	isolated.transport.DisconnectAll()
	for _, o := range others {
		o.transport.Disconnect(isolated.addr)
	}
}

// healPartition reconnects isolated to all others in both directions.
func healPartition(isolated *inmemNode, others []*inmemNode) {
	for _, o := range others {
		isolated.transport.Connect(o.addr, o.transport)
		o.transport.Connect(isolated.addr, isolated.transport)
	}
}

// applyFileIdCmd applies a MaxFileIdCommand to leader and fatals on error.
func applyFileIdCmd(t *testing.T, leader *inmemNode, val uint64) {
	t.Helper()
	data, err := encodeLogEntry(MaxFileIdCommand{MaxFileId: val})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := leader.r.Apply(data, 5*time.Second).Error(); err != nil {
		t.Fatalf("apply %d: %v", val, err)
	}
}

// waitForFSM polls until every node in nodes has GetMaxFileId() == want.
func waitForFSM(t *testing.T, nodes []*inmemNode, want uint64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ok := true
		for _, n := range nodes {
			if n.fsm.GetMaxFileId() != want {
				ok = false
				break
			}
		}
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	for i, n := range nodes {
		t.Errorf("node %d: FSM=%d, want %d", i, n.fsm.GetMaxFileId(), want)
	}
	t.Fatal("FSM convergence timeout")
}

// restartNode shuts down a node and brings it back up on a fresh transport at
// the same address, reconnecting it to the given peers.  The existing durable
// stores are reused so log entries and snapshots survive the restart.
func restartNode(t *testing.T, node *inmemNode, peers []*inmemNode) {
	t.Helper()
	if node.r != nil {
		node.r.Shutdown()
		node.r = nil
	}
	_, newTransport := hraft.NewInmemTransport(node.addr)
	node.transport = newTransport
	for _, p := range peers {
		newTransport.Connect(p.addr, p.transport)
		p.transport.Connect(node.addr, newTransport)
	}
	r, err := hraft.NewRaft(node.cfg, node.fsm, node.logStore, node.stableStore, node.snapStore, newTransport)
	if err != nil {
		t.Fatalf("restart raft %s: %v", node.addr, err)
	}
	node.r = r
}

// ── Tests ─────────────────────────────────────────────────────────────────

func TestThreeNodeCluster(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-node raft test in short mode")
	}

	nodes := make([]*inmemNode, 3)
	for i := range nodes {
		nodes[i] = newInmemNode(t)
	}
	bootstrapAndStart(t, nodes)
	defer shutdownAll(nodes)

	t.Run("leader election", func(t *testing.T) {
		leader := waitForLeader(t, nodes, 5*time.Second)
		if leader == nil {
			return // waitForLeader already calls t.Fatal
		}
		leaderCount := 0
		for _, n := range nodes {
			if n.r.State() == hraft.Leader {
				leaderCount++
			}
		}
		if leaderCount != 1 {
			t.Errorf("expected exactly 1 leader, got %d", leaderCount)
		}
	})

	t.Run("command replication", func(t *testing.T) {
		leader := waitForLeader(t, nodes, 5*time.Second)

		data, err := encodeLogEntry(MaxFileIdCommand{MaxFileId: 42000})
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		f := leader.r.Apply(data, 5*time.Second)
		if err := f.Error(); err != nil {
			t.Fatalf("apply: %v", err)
		}

		// Wait for all FSMs to apply the entry.
		time.Sleep(200 * time.Millisecond)
		for i, n := range nodes {
			if got := n.fsm.GetMaxFileId(); got != 42000 {
				t.Errorf("node %d: expected max_file_id=42000, got %d", i, got)
			}
		}
	})

	t.Run("leader failover", func(t *testing.T) {
		leader := waitForLeader(t, nodes, 5*time.Second)

		// Shutdown the current leader.
		leader.r.Shutdown()
		leader.r = nil

		// Collect survivors.
		var survivors []*inmemNode
		for _, n := range nodes {
			if n.r != nil {
				survivors = append(survivors, n)
			}
		}

		// A new leader should be elected among the 2 survivors.
		newLeader := waitForLeader(t, survivors, 5*time.Second)
		if newLeader == nil {
			return
		}

		// The new leader must be able to commit a command.
		data, err := encodeLogEntry(MaxFileIdCommand{MaxFileId: 99000})
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		f := newLeader.r.Apply(data, 5*time.Second)
		if err := f.Error(); err != nil {
			t.Fatalf("apply on new leader: %v", err)
		}

		time.Sleep(200 * time.Millisecond)
		for i, n := range survivors {
			if got := n.fsm.GetMaxFileId(); got != 99000 {
				t.Errorf("survivor %d: expected max_file_id=99000, got %d", i, got)
			}
		}
	})
}

// TestFollowerPartition — 1 follower isolated; the 2-node majority keeps
// making progress, and the follower catches up once the partition heals.
func TestFollowerPartition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping partition test in short mode")
	}

	nodes := make([]*inmemNode, 3)
	for i := range nodes {
		nodes[i] = newInmemNode(t)
	}
	bootstrapAndStart(t, nodes)
	defer shutdownAll(nodes)

	leader := waitForLeader(t, nodes, 5*time.Second)

	// Split: one follower isolated, the other stays with the leader.
	var isolated *inmemNode
	var majority []*inmemNode
	for _, n := range nodes {
		if n != leader {
			if isolated == nil {
				isolated = n
			} else {
				majority = append(majority, n)
			}
		}
	}
	majority = append(majority, leader)

	partitionAway(isolated, majority)

	// Majority can still commit — 2 votes satisfy quorum.
	applyFileIdCmd(t, leader, 10000)

	// Give replication time to the majority; isolated must NOT see the update.
	time.Sleep(150 * time.Millisecond)
	if got := isolated.fsm.GetMaxFileId(); got == 10000 {
		t.Error("partitioned follower should not have received committed entry")
	}

	// Heal and verify full convergence.
	healPartition(isolated, majority)
	waitForFSM(t, nodes, 10000, 5*time.Second)
}

// TestLeaderPartition — leader isolated from both followers; followers elect a
// new leader, commit, and after the heal the old leader rejoins as follower.
func TestLeaderPartition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping partition test in short mode")
	}

	nodes := make([]*inmemNode, 3)
	for i := range nodes {
		nodes[i] = newInmemNode(t)
	}
	bootstrapAndStart(t, nodes)
	defer shutdownAll(nodes)

	leader := waitForLeader(t, nodes, 5*time.Second)

	var followers []*inmemNode
	for _, n := range nodes {
		if n != leader {
			followers = append(followers, n)
		}
	}

	// Isolate the leader from all followers.
	partitionAway(leader, followers)

	// Apply on the isolated leader must fail — it can no longer reach a quorum.
	data, _ := encodeLogEntry(MaxFileIdCommand{MaxFileId: 20000})
	applyErr := leader.r.Apply(data, 600*time.Millisecond).Error()
	if applyErr == nil {
		t.Error("expected Apply to fail on isolated leader, but it succeeded")
	}

	// Followers elect a new leader and can commit.
	newLeader := waitForLeader(t, followers, 5*time.Second)
	applyFileIdCmd(t, newLeader, 20000)
	waitForFSM(t, followers, 20000, 5*time.Second)

	// Heal: reconnect old leader.
	healPartition(leader, followers)

	// Old leader must catch up and step down.
	waitForFSM(t, []*inmemNode{leader}, 20000, 5*time.Second)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if leader.r.State() != hraft.Leader {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if leader.r.State() == hraft.Leader {
		t.Error("partitioned ex-leader should have stepped down after heal")
	}
}

// TestQuorumRequirement — all 3 nodes isolated; no commit is possible, and the
// cluster recovers as a single entity once the partition heals.
func TestQuorumRequirement(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping partition test in short mode")
	}

	nodes := make([]*inmemNode, 3)
	for i := range nodes {
		nodes[i] = newInmemNode(t)
	}
	bootstrapAndStart(t, nodes)
	defer shutdownAll(nodes)

	leader := waitForLeader(t, nodes, 5*time.Second)

	// Disconnect all nodes from each other.
	for _, n := range nodes {
		n.transport.DisconnectAll()
	}

	// Apply on any node must fail — no quorum anywhere.
	data, _ := encodeLogEntry(MaxFileIdCommand{MaxFileId: 30000})
	if err := leader.r.Apply(data, 600*time.Millisecond).Error(); err == nil {
		t.Error("expected Apply to fail with all nodes partitioned")
	}

	// Heal: reconnect everyone.
	for i, n := range nodes {
		for j, peer := range nodes {
			if i != j {
				n.transport.Connect(peer.addr, peer.transport)
			}
		}
	}

	// Cluster should converge to a single leader and accept new commits.
	newLeader := waitForLeader(t, nodes, 5*time.Second)
	applyFileIdCmd(t, newLeader, 30000)
	waitForFSM(t, nodes, 30000, 5*time.Second)
}

// TestPartitionHealConvergence — entries committed by the majority while a
// follower is partitioned are replicated to that follower after the heal.
func TestPartitionHealConvergence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping partition test in short mode")
	}

	nodes := make([]*inmemNode, 3)
	for i := range nodes {
		nodes[i] = newInmemNode(t)
	}
	bootstrapAndStart(t, nodes)
	defer shutdownAll(nodes)

	leader := waitForLeader(t, nodes, 5*time.Second)

	// Baseline: all 3 nodes at 1000.
	applyFileIdCmd(t, leader, 1000)
	waitForFSM(t, nodes, 1000, 5*time.Second)

	// Partition one follower.
	var partitioned *inmemNode
	var connected []*inmemNode
	for _, n := range nodes {
		if n != leader {
			if partitioned == nil {
				partitioned = n
			} else {
				connected = append(connected, n)
			}
		}
	}
	connected = append(connected, leader)
	partitionAway(partitioned, connected)

	// Majority commits three more entries.
	applyFileIdCmd(t, leader, 2000)
	applyFileIdCmd(t, leader, 3000)
	applyFileIdCmd(t, leader, 4000)

	// Partitioned node must still be at 1000.
	time.Sleep(150 * time.Millisecond)
	if got := partitioned.fsm.GetMaxFileId(); got != 1000 {
		t.Errorf("partitioned node: FSM=%d, want 1000", got)
	}

	// Heal and verify all 3 converge to 4000.
	healPartition(partitioned, connected)
	waitForFSM(t, nodes, 4000, 5*time.Second)
}

// TestFollowerCrashAndRestart — a follower crashes (shutdown), misses commits,
// then restarts and catches up from the leader's log.
func TestFollowerCrashAndRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping crash/restart test in short mode")
	}

	nodes := make([]*inmemNode, 3)
	for i := range nodes {
		nodes[i] = newInmemNode(t)
	}
	bootstrapAndStart(t, nodes)
	defer shutdownAll(nodes)

	leader := waitForLeader(t, nodes, 5*time.Second)

	// Commit baseline so all nodes are in sync.
	applyFileIdCmd(t, leader, 5000)
	waitForFSM(t, nodes, 5000, 5*time.Second)

	// Pick a follower to crash.
	var crashed *inmemNode
	var survivors []*inmemNode
	for _, n := range nodes {
		if n != leader {
			if crashed == nil {
				crashed = n
			} else {
				survivors = append(survivors, n)
			}
		}
	}
	survivors = append(survivors, leader)

	// Simulate crash.
	crashed.r.Shutdown()
	crashed.r = nil

	// Commit while the crashed node is down.
	applyFileIdCmd(t, leader, 6000)
	applyFileIdCmd(t, leader, 7000)
	waitForFSM(t, survivors, 7000, 5*time.Second)

	// Restart the crashed node using its existing (in-memory) stores.
	restartNode(t, crashed, survivors)

	// Restarted node must catch up to 7000 via log replication.
	waitForFSM(t, []*inmemNode{crashed}, 7000, 5*time.Second)
}

// TestSnapshotInstallOnLaggingNode — a follower misses enough entries that the
// leader has snapshotted and compacted them; the leader must install a snapshot
// on the rejoining follower rather than replaying individual log entries.
func TestSnapshotInstallOnLaggingNode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping snapshot install test in short mode")
	}

	nodes := make([]*inmemNode, 3)
	for i := range nodes {
		nodes[i] = newInmemNode(t)
		// Very low threshold so the leader snapshots after 10 entries and
		// compacts old log entries, forcing snapshot transfer to laggers.
		nodes[i].cfg.SnapshotThreshold = 10
		nodes[i].cfg.SnapshotInterval = 200 * time.Millisecond
		nodes[i].cfg.TrailingLogs = 5
	}
	bootstrapAndStart(t, nodes)
	defer shutdownAll(nodes)

	leader := waitForLeader(t, nodes, 5*time.Second)

	// Baseline commit — all nodes in sync.
	applyFileIdCmd(t, leader, 100)
	waitForFSM(t, nodes, 100, 5*time.Second)

	// Shut down one follower — it will miss the upcoming entries.
	var lagging *inmemNode
	var active []*inmemNode
	for _, n := range nodes {
		if n != leader {
			if lagging == nil {
				lagging = n
			} else {
				active = append(active, n)
			}
		}
	}
	active = append(active, leader)

	lagging.r.Shutdown()
	lagging.r = nil

	// Apply 25 entries on the 2-node majority.  With SnapshotThreshold=10 and
	// TrailingLogs=5 the leader will snapshot and compact old entries, making
	// incremental log replication to the lagging node impossible.
	const entryCount = 25
	for i := 0; i < entryCount; i++ {
		applyFileIdCmd(t, leader, uint64(200+i))
	}
	finalVal := uint64(200 + entryCount - 1)

	// Wait for the snapshot interval to fire so compaction occurs.
	time.Sleep(600 * time.Millisecond)

	// Restart the lagging node on its existing stores.
	restartNode(t, lagging, active)

	// The lagging node must install the leader's snapshot and converge.
	waitForFSM(t, []*inmemNode{lagging}, finalVal, 10*time.Second)
}

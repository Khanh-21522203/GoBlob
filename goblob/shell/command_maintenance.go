package shell

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/pb"
	"GoBlob/goblob/pb/filer_pb"
	"GoBlob/goblob/pb/volume_server_pb"
)

type volumeBalanceCommand struct{}
type volumeMoveCommand struct{}
type volumeVacuumCommand struct{}
type volumeFixReplicationCommand struct{}
type s3CleanUploadsCommand struct{}

type plannedVolumeMove struct {
	vid         uint32
	sourceID    string
	sourceGRPC  string
	targetID    string
	targetGRPC  string
	collection  string
	replication string
	ttl         string
	diskType    string
}

func init() {
	RegisterCommand(&volumeBalanceCommand{})
	RegisterCommand(&volumeMoveCommand{})
	RegisterCommand(&volumeVacuumCommand{})
	RegisterCommand(&volumeFixReplicationCommand{})
	RegisterCommand(&s3CleanUploadsCommand{})
}

func (c *volumeBalanceCommand) Name() string { return "volume.balance" }

func (c *volumeBalanceCommand) Help() string {
	return "rebalance volumes (dry-run by default): volume.balance [-dryRun=true] [-threshold=0.2] [-maxMoves=5]"
}

func (c *volumeBalanceCommand) Do(args []string, env *CommandEnv, writer io.Writer) error {
	dryRun := true
	threshold := 0.2
	maxMoves := 5

	for _, arg := range args[1:] {
		switch {
		case strings.HasPrefix(arg, "-dryRun="):
			v, err := strconv.ParseBool(strings.TrimPrefix(arg, "-dryRun="))
			if err != nil {
				return fmt.Errorf("invalid -dryRun: %w", err)
			}
			dryRun = v
		case strings.HasPrefix(arg, "-threshold="):
			v, err := strconv.ParseFloat(strings.TrimPrefix(arg, "-threshold="), 64)
			if err != nil {
				return fmt.Errorf("invalid -threshold: %w", err)
			}
			threshold = v
		case strings.HasPrefix(arg, "-maxMoves="):
			v, err := strconv.Atoi(strings.TrimPrefix(arg, "-maxMoves="))
			if err != nil {
				return fmt.Errorf("invalid -maxMoves: %w", err)
			}
			maxMoves = v
		}
	}
	if maxMoves <= 0 {
		maxMoves = 1
	}

	snap, err := fetchClusterSnapshot(env)
	if err != nil {
		return fmt.Errorf("fetch topology: %w", err)
	}
	if len(snap.nodes) <= 1 {
		_, _ = fmt.Fprintln(writer, "volume.balance: not enough nodes to balance")
		return nil
	}

	type nodeLoad struct {
		id    string
		count int
	}
	loads := make([]nodeLoad, 0, len(snap.nodes))
	total := 0
	for id, node := range snap.nodes {
		count := len(node.volumes)
		total += count
		loads = append(loads, nodeLoad{id: id, count: count})
	}
	sort.Slice(loads, func(i, j int) bool { return loads[i].count > loads[j].count })
	avg := float64(total) / float64(len(loads))
	high := int(math.Ceil(avg * (1.0 + threshold)))
	low := int(math.Floor(avg * (1.0 - threshold)))

	moves := make([]plannedVolumeMove, 0, maxMoves)

	for _, src := range loads {
		if src.count <= high {
			continue
		}
		srcNode := snap.nodes[src.id]
		vids := sortedVolumeIDs(srcNode.volumes)
		for _, dst := range loads {
			if len(moves) >= maxMoves {
				break
			}
			if dst.id == src.id || dst.count >= low {
				continue
			}
			dstNode := snap.nodes[dst.id]
			for _, vid := range vids {
				if _, exists := dstNode.volumes[vid]; exists {
					continue
				}
				volInfo, ok := snap.volumes[vid]
				if !ok {
					continue
				}
				moves = append(moves, plannedVolumeMove{
					vid:         vid,
					sourceID:    src.id,
					sourceGRPC:  nodeGRPCAddress(src.id, srcNode),
					targetID:    dst.id,
					targetGRPC:  nodeGRPCAddress(dst.id, dstNode),
					collection:  volInfo.collection,
					replication: volInfo.replication,
					ttl:         volInfo.ttl,
					diskType:    volInfo.diskType,
				})
				dst.count++
				src.count--
				break
			}
		}
		if len(moves) >= maxMoves {
			break
		}
	}

	if len(moves) == 0 {
		_, _ = fmt.Fprintln(writer, "volume.balance: cluster already balanced")
		return nil
	}

	for _, m := range moves {
		if dryRun {
			_, _ = fmt.Fprintf(writer, "would move volume %d from %s to %s\n", m.vid, m.sourceID, m.targetID)
			continue
		}
		if err := executeVolumeMove(env, m); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(writer, "moved volume %d from %s to %s\n", m.vid, m.sourceID, m.targetID)
	}
	return nil
}

func (c *volumeMoveCommand) Name() string { return "volume.move" }

func (c *volumeMoveCommand) Help() string {
	return "move one volume between nodes (dry-run by default): volume.move -vid=<id> -from=<node> -to=<node> [-dryRun=true]"
}

func (c *volumeMoveCommand) Do(args []string, env *CommandEnv, writer io.Writer) error {
	var (
		vid    uint64
		source string
		target string
		dryRun = true
	)
	for _, arg := range args[1:] {
		switch {
		case strings.HasPrefix(arg, "-vid="):
			v, err := strconv.ParseUint(strings.TrimPrefix(arg, "-vid="), 10, 32)
			if err != nil {
				return fmt.Errorf("invalid -vid: %w", err)
			}
			vid = v
		case strings.HasPrefix(arg, "-from="):
			source = strings.TrimSpace(strings.TrimPrefix(arg, "-from="))
		case strings.HasPrefix(arg, "-to="):
			target = strings.TrimSpace(strings.TrimPrefix(arg, "-to="))
		case strings.HasPrefix(arg, "-dryRun="):
			v, err := strconv.ParseBool(strings.TrimPrefix(arg, "-dryRun="))
			if err != nil {
				return fmt.Errorf("invalid -dryRun: %w", err)
			}
			dryRun = v
		}
	}
	if vid == 0 || source == "" || target == "" {
		return fmt.Errorf("usage: volume.move -vid=<id> -from=<node> -to=<node> [-dryRun=true]")
	}
	snap, err := fetchClusterSnapshot(env)
	if err != nil {
		return fmt.Errorf("fetch topology: %w", err)
	}
	sourceID, sourceNode, err := resolveNodeSnapshot(snap, source)
	if err != nil {
		return err
	}
	targetID, targetNode, err := resolveNodeSnapshot(snap, target)
	if err != nil {
		return err
	}
	volInfo, ok := snap.volumes[uint32(vid)]
	if !ok {
		return fmt.Errorf("volume %d not found in topology", vid)
	}
	if _, exists := sourceNode.volumes[uint32(vid)]; !exists {
		return fmt.Errorf("volume %d is not hosted on source node %s", vid, sourceID)
	}
	if _, exists := targetNode.volumes[uint32(vid)]; exists {
		return fmt.Errorf("volume %d already exists on target node %s", vid, targetID)
	}
	move := plannedVolumeMove{
		vid:         uint32(vid),
		sourceID:    sourceID,
		sourceGRPC:  nodeGRPCAddress(sourceID, sourceNode),
		targetID:    targetID,
		targetGRPC:  nodeGRPCAddress(targetID, targetNode),
		collection:  volInfo.collection,
		replication: volInfo.replication,
		ttl:         volInfo.ttl,
		diskType:    volInfo.diskType,
	}
	if dryRun {
		_, _ = fmt.Fprintf(writer, "would move volume %d from %s to %s\n", vid, move.sourceID, move.targetID)
		return nil
	}
	if err := executeVolumeMove(env, move); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(writer, "moved volume %d from %s to %s\n", vid, move.sourceID, move.targetID)
	return nil
}

func executeVolumeMove(env *CommandEnv, move plannedVolumeMove) error {
	if move.vid == 0 {
		return fmt.Errorf("invalid volume id")
	}
	if move.sourceGRPC == "" || move.targetGRPC == "" {
		return fmt.Errorf("source/target grpc address is required")
	}
	if move.sourceGRPC == move.targetGRPC {
		return fmt.Errorf("source and target are the same node")
	}

	if err := setVolumeReadonly(env, move.sourceGRPC, move.vid, true); err != nil {
		return fmt.Errorf("mark source readonly: %w", err)
	}
	restoreReadonly := true
	defer func() {
		if restoreReadonly {
			_ = setVolumeReadonly(env, move.sourceGRPC, move.vid, false)
		}
	}()

	if err := copyVolumeReplica(env, move); err != nil {
		return fmt.Errorf("copy to target failed: %w", err)
	}
	if err := deleteVolumeFromNode(env, move.sourceGRPC, move.vid, move.collection); err != nil {
		return fmt.Errorf("delete source volume failed: %w", err)
	}
	restoreReadonly = false
	return nil
}

func (c *volumeVacuumCommand) Name() string { return "volume.vacuum" }

func (c *volumeVacuumCommand) Help() string {
	return "trigger master vacuum endpoint: volume.vacuum [-collection=name] [-threshold=0.3] [-dryRun=false]"
}

func (c *volumeVacuumCommand) Do(args []string, env *CommandEnv, writer io.Writer) error {
	masterHTTP := env.masterHTTPAddress()
	if masterHTTP == "" {
		return fmt.Errorf("no master address configured")
	}
	collection := ""
	threshold := 0.3
	dryRun := false
	for _, arg := range args[1:] {
		switch {
		case strings.HasPrefix(arg, "-collection="):
			collection = strings.TrimPrefix(arg, "-collection=")
		case strings.HasPrefix(arg, "-threshold="):
			v, err := strconv.ParseFloat(strings.TrimPrefix(arg, "-threshold="), 64)
			if err != nil {
				return fmt.Errorf("invalid -threshold: %w", err)
			}
			threshold = v
		case strings.HasPrefix(arg, "-dryRun="):
			v, err := strconv.ParseBool(strings.TrimPrefix(arg, "-dryRun="))
			if err != nil {
				return fmt.Errorf("invalid -dryRun: %w", err)
			}
			dryRun = v
		}
	}

	q := url.Values{}
	if collection != "" {
		q.Set("collection", collection)
	}
	q.Set("threshold", strconv.FormatFloat(threshold, 'f', -1, 64))
	endpoint := fmt.Sprintf("http://%s/vol/vacuum?%s", masterHTTP, q.Encode())

	if dryRun {
		_, _ = fmt.Fprintf(writer, "would call %s\n", endpoint)
		return nil
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("vacuum request failed: %s", resp.Status)
	}
	_, _ = fmt.Fprintf(writer, "volume.vacuum triggered on %s\n", masterHTTP)
	return nil
}

func (c *volumeFixReplicationCommand) Name() string { return "volume.fix.replication" }

func (c *volumeFixReplicationCommand) Help() string {
	return "repair under-replicated volumes (dry-run default): volume.fix.replication [-dryRun=true]"
}

func (c *volumeFixReplicationCommand) Do(args []string, env *CommandEnv, writer io.Writer) error {
	dryRun := true
	for _, arg := range args[1:] {
		if strings.HasPrefix(arg, "-dryRun=") {
			v, err := strconv.ParseBool(strings.TrimPrefix(arg, "-dryRun="))
			if err != nil {
				return fmt.Errorf("invalid -dryRun: %w", err)
			}
			dryRun = v
		}
	}

	snap, err := fetchClusterSnapshot(env)
	if err != nil {
		return fmt.Errorf("fetch topology: %w", err)
	}

	issues := 0
	repairs := 0
	vids := make([]uint32, 0, len(snap.volumes))
	for vid := range snap.volumes {
		vids = append(vids, vid)
	}
	sort.Slice(vids, func(i, j int) bool { return vids[i] < vids[j] })

	for _, vid := range vids {
		vol := snap.volumes[vid]
		replication := vol.replication
		if replication == "" {
			replication = "000"
		}
		expected := types.ParseReplicaPlacementString(replication).TotalCopies()
		actual := len(vol.nodes)
		if actual >= expected {
			continue
		}
		issues++
		_, _ = fmt.Fprintf(writer, "under-replicated volume %d: have=%d expected=%d replication=%s\n", vid, actual, expected, replication)

		sourceID := pickSourceNodeID(vol, snap)
		if sourceID == "" {
			return fmt.Errorf("volume %d has no valid source replica", vid)
		}
		sourceNode := snap.nodes[sourceID]
		sourceGRPC := nodeGRPCAddress(sourceID, sourceNode)

		candidates := candidateTargetNodeIDs(snap, vol)
		missing := expected - actual
		if len(candidates) < missing {
			_, _ = fmt.Fprintf(writer, "volume %d: only %d candidate targets for %d missing replicas\n", vid, len(candidates), missing)
			missing = len(candidates)
		}
		if missing <= 0 {
			continue
		}

		plannedMoves := make([]plannedVolumeMove, 0, missing)
		for i := 0; i < missing; i++ {
			targetID := candidates[i]
			targetNode := snap.nodes[targetID]
			plannedMoves = append(plannedMoves, plannedVolumeMove{
				vid:         vid,
				sourceID:    sourceID,
				sourceGRPC:  sourceGRPC,
				targetID:    targetID,
				targetGRPC:  nodeGRPCAddress(targetID, targetNode),
				collection:  vol.collection,
				replication: vol.replication,
				ttl:         vol.ttl,
				diskType:    vol.diskType,
			})
		}

		if dryRun {
			for _, move := range plannedMoves {
				_, _ = fmt.Fprintf(writer, "would replicate volume %d from %s to %s\n", move.vid, move.sourceID, move.targetID)
			}
			continue
		}

		if err := setVolumeReadonly(env, sourceGRPC, vid, true); err != nil {
			return fmt.Errorf("mark source readonly for volume %d: %w", vid, err)
		}
		restoreErr := func() error {
			return setVolumeReadonly(env, sourceGRPC, vid, false)
		}

		for _, move := range plannedMoves {
			if err := copyVolumeReplica(env, move); err != nil {
				_ = restoreErr()
				return fmt.Errorf("replicate volume %d to %s failed: %w", move.vid, move.targetID, err)
			}
			repairs++
			_, _ = fmt.Fprintf(writer, "replicated volume %d from %s to %s\n", move.vid, move.sourceID, move.targetID)
			vol.nodes[move.targetID] = struct{}{}
			if n := snap.nodes[move.targetID]; n != nil && n.free > 0 {
				n.free--
			}
		}
		if err := restoreErr(); err != nil {
			return fmt.Errorf("restore writable on source for volume %d: %w", vid, err)
		}
	}

	if issues == 0 {
		_, _ = fmt.Fprintln(writer, "volume.fix.replication: no issues found")
		return nil
	}
	if !dryRun {
		_, _ = fmt.Fprintf(writer, "volume.fix.replication repaired=%d\n", repairs)
	}
	return nil
}

func (c *s3CleanUploadsCommand) Name() string { return "s3.clean.uploads" }

func (c *s3CleanUploadsCommand) Help() string {
	return "clean stale multipart uploads under /buckets/*/.uploads: s3.clean.uploads [-olderThan=24h] [-bucket=name] [-dryRun=false]"
}

func (c *s3CleanUploadsCommand) Do(args []string, env *CommandEnv, writer io.Writer) error {
	olderThan := 24 * time.Hour
	bucketFilter := ""
	dryRun := false
	for _, arg := range args[1:] {
		switch {
		case strings.HasPrefix(arg, "-olderThan="):
			d, err := time.ParseDuration(strings.TrimPrefix(arg, "-olderThan="))
			if err != nil {
				return fmt.Errorf("invalid -olderThan: %w", err)
			}
			olderThan = d
		case strings.HasPrefix(arg, "-bucket="):
			bucketFilter = strings.TrimSpace(strings.TrimPrefix(arg, "-bucket="))
		case strings.HasPrefix(arg, "-dryRun="):
			v, err := strconv.ParseBool(strings.TrimPrefix(arg, "-dryRun="))
			if err != nil {
				return fmt.Errorf("invalid -dryRun: %w", err)
			}
			dryRun = v
		}
	}

	filerAddr := env.filerGRPCAddress()
	if filerAddr == "" {
		return fmt.Errorf("no filer address configured")
	}
	cutoff := time.Now().Add(-olderThan)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var buckets []string
	if bucketFilter != "" {
		buckets = []string{bucketFilter}
	} else {
		rootEntries, err := listDirectoryEntries(ctx, env, "/buckets")
		if err != nil {
			return err
		}
		for _, e := range rootEntries {
			if e.GetIsDirectory() && e.GetName() != "" && !strings.HasPrefix(e.GetName(), ".") {
				buckets = append(buckets, e.GetName())
			}
		}
		sort.Strings(buckets)
	}

	totalCandidates := 0
	totalDeleted := 0
	for _, bucket := range buckets {
		uploadsDir := path.Join("/buckets", bucket, ".uploads")
		entries, err := listDirectoryEntries(ctx, env, uploadsDir)
		if err != nil {
			if isFilerNotFound(err) {
				continue
			}
			return err
		}
		for _, entry := range entries {
			if entry == nil || !entry.GetIsDirectory() {
				continue
			}
			attrs := entry.GetAttributes()
			ts := attrs.GetCrtime()
			if ts == 0 {
				ts = attrs.GetMtime()
			}
			if ts == 0 {
				continue
			}
			if time.Unix(ts, 0).After(cutoff) {
				continue
			}
			totalCandidates++
			uploadID := entry.GetName()
			if dryRun {
				_, _ = fmt.Fprintf(writer, "would delete %s/%s\n", uploadsDir, uploadID)
				continue
			}
			err := pb.WithFilerClient(filerAddr, env.GrpcDialOption, func(client filer_pb.FilerServiceClient) error {
				_, delErr := client.DeleteEntry(ctx, &filer_pb.DeleteEntryRequest{
					Directory:            uploadsDir,
					Name:                 uploadID,
					IsDeleteData:         true,
					IsRecursive:          true,
					IgnoreRecursiveError: true,
				})
				return delErr
			})
			if err != nil && !isFilerNotFound(err) {
				return err
			}
			if err == nil {
				totalDeleted++
				_, _ = fmt.Fprintf(writer, "deleted %s/%s\n", uploadsDir, uploadID)
			}
		}
	}
	_, _ = fmt.Fprintf(writer, "s3.clean.uploads candidates=%d deleted=%d\n", totalCandidates, totalDeleted)
	return nil
}

func copyVolumeReplica(env *CommandEnv, move plannedVolumeMove) error {
	if env == nil {
		return fmt.Errorf("command env is nil")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	return pb.WithVolumeServerClient(move.targetGRPC, env.GrpcDialOption, func(client volume_server_pb.VolumeServerClient) error {
		stream, err := client.VolumeCopy(ctx, &volume_server_pb.VolumeCopyRequest{
			VolumeId:       move.vid,
			Collection:     move.collection,
			Replication:    move.replication,
			Ttl:            move.ttl,
			SourceDataNode: move.sourceGRPC,
			DiskType:       move.diskType,
		})
		if err != nil {
			if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
				return nil
			}
			return err
		}
		for {
			_, recvErr := stream.Recv()
			if recvErr == io.EOF {
				return nil
			}
			if recvErr != nil {
				if st, ok := status.FromError(recvErr); ok && st.Code() == codes.AlreadyExists {
					return nil
				}
				return recvErr
			}
		}
	})
}

func setVolumeReadonly(env *CommandEnv, nodeGRPC string, vid uint32, readonly bool) error {
	if env == nil {
		return fmt.Errorf("command env is nil")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	return pb.WithVolumeServerClient(nodeGRPC, env.GrpcDialOption, func(client volume_server_pb.VolumeServerClient) error {
		resp, err := client.VolumeMarkReadonly(ctx, &volume_server_pb.VolumeMarkReadonlyRequest{
			VolumeId:   vid,
			IsReadonly: readonly,
		})
		if err != nil {
			return err
		}
		if resp.GetError() != "" {
			return fmt.Errorf("%s", resp.GetError())
		}
		return nil
	})
}

func deleteVolumeFromNode(env *CommandEnv, nodeGRPC string, vid uint32, collection string) error {
	if env == nil {
		return fmt.Errorf("command env is nil")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return pb.WithVolumeServerClient(nodeGRPC, env.GrpcDialOption, func(client volume_server_pb.VolumeServerClient) error {
		resp, err := client.VolumeDelete(ctx, &volume_server_pb.VolumeDeleteRequest{
			VolumeId:   vid,
			Collection: collection,
		})
		if err != nil {
			return err
		}
		if resp.GetError() != "" {
			return fmt.Errorf("%s", resp.GetError())
		}
		return nil
	})
}

func pickSourceNodeID(vol *volumeSnapshot, snap *clusterSnapshot) string {
	if vol == nil || snap == nil {
		return ""
	}
	ids := make([]string, 0, len(vol.nodes))
	for id := range vol.nodes {
		if _, ok := snap.nodes[id]; ok {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	sort.Slice(ids, func(i, j int) bool {
		li := snap.nodes[ids[i]]
		lj := snap.nodes[ids[j]]
		if li == nil || lj == nil {
			return ids[i] < ids[j]
		}
		if li.free == lj.free {
			return ids[i] < ids[j]
		}
		return li.free > lj.free
	})
	return ids[0]
}

func candidateTargetNodeIDs(snap *clusterSnapshot, vol *volumeSnapshot) []string {
	if snap == nil {
		return nil
	}
	ids := make([]string, 0, len(snap.nodes))
	for id, node := range snap.nodes {
		if node == nil || node.free == 0 {
			continue
		}
		if vol != nil {
			if _, exists := vol.nodes[id]; exists {
				continue
			}
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		li := snap.nodes[ids[i]]
		lj := snap.nodes[ids[j]]
		if li == nil || lj == nil {
			return ids[i] < ids[j]
		}
		if li.free == lj.free {
			return ids[i] < ids[j]
		}
		return li.free > lj.free
	})
	return ids
}

func listDirectoryEntries(ctx context.Context, env *CommandEnv, dir string) ([]*filer_pb.Entry, error) {
	filerAddr := env.filerGRPCAddress()
	if filerAddr == "" {
		return nil, fmt.Errorf("no filer address configured")
	}
	entries := make([]*filer_pb.Entry, 0, 64)
	err := pb.WithFilerClient(filerAddr, env.GrpcDialOption, func(client filer_pb.FilerServiceClient) error {
		stream, err := client.ListEntries(ctx, &filer_pb.ListEntriesRequest{
			Directory: dir,
			Limit:     10000,
		})
		if err != nil {
			return err
		}
		for {
			resp, recvErr := stream.Recv()
			if recvErr == io.EOF {
				return nil
			}
			if recvErr != nil {
				return recvErr
			}
			if e := resp.GetEntry(); e != nil {
				entries = append(entries, e)
			}
		}
	})
	return entries, err
}

func isFilerNotFound(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	return ok && st.Code() == codes.NotFound
}

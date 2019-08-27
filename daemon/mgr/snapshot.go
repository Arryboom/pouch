package mgr

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alibaba/pouch/ctrd"
	"github.com/alibaba/pouch/pkg/errtypes"
	"github.com/alibaba/pouch/pkg/log"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/snapshots"
	"github.com/pkg/errors"
)

// Snapshot contains the information about the snapshot.
type Snapshot struct {
	// Key is the key of the snapshot
	Key string
	// Kind is the kind of the snapshot (active, committed, view)
	Kind snapshots.Kind
	// Size is the size of the snapshot in bytes.
	Size uint64
	// Inodes is the number of inodes used by the snapshot
	Inodes uint64
	// UsagePercent is the percent of disk used by snapshotter
	UsagePercent uint64
	// Timestamp is latest update time (in nanoseconds) of the snapshot
	// information.
	Timestamp int64
}

// SnapshotStore stores all snapshots.
type SnapshotStore struct {
	lock      sync.RWMutex
	snapshots map[string]Snapshot
}

// NewSnapshotStore create a new snapshot store.
func NewSnapshotStore() *SnapshotStore {
	return &SnapshotStore{snapshots: make(map[string]Snapshot)}
}

// Add a snapshot into the store.
func (s *SnapshotStore) Add(sn Snapshot) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.snapshots[sn.Key] = sn
}

// Get returns the snapshot with specified key. Returns error if the
// snapshot doesn't exist.
func (s *SnapshotStore) Get(key string) (Snapshot, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	if sn, ok := s.snapshots[key]; ok {
		return sn, nil
	}
	return Snapshot{}, errors.Wrapf(errtypes.ErrNotfound, "snapshot %s", key)
}

// List lists all snapshots.
func (s *SnapshotStore) List() []Snapshot {
	s.lock.RLock()
	defer s.lock.RUnlock()
	var snapshots []Snapshot
	for _, sn := range s.snapshots {
		snapshots = append(snapshots, sn)
	}
	return snapshots
}

// Delete deletes the snapshot with specified key.
func (s *SnapshotStore) Delete(key string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	delete(s.snapshots, key)
}

// SnapshotsSyncer syncs snapshot stats periodically.
type SnapshotsSyncer struct {
	store      *SnapshotStore
	client     ctrd.APIClient
	syncPeriod time.Duration
}

// NewSnapshotsSyncer creates a snapshot syncer.
func NewSnapshotsSyncer(store *SnapshotStore, cli ctrd.APIClient, period time.Duration) *SnapshotsSyncer {
	return &SnapshotsSyncer{
		store:      store,
		client:     cli,
		syncPeriod: period,
	}
}

// Start starts the snapshots syncer.
func (s *SnapshotsSyncer) Start() {
	tick := time.NewTicker(s.syncPeriod)
	go func() {
		defer tick.Stop()
		for {
			err := s.Sync()
			if err != nil {
				// TODO track the error and report it to the monitor or something.
				log.With(nil).Errorf("failed to sync snapshot stats: %v", err)
			}
			<-tick.C
		}
	}()
}

// Sync updates the snapshots in the snapshot store.
func (s *SnapshotsSyncer) Sync() error {
	start := time.Now().UnixNano()
	var infos []snapshots.Info
	err := s.client.WalkSnapshot(context.Background(), "", func(ctx context.Context, info snapshots.Info) error {
		infos = append(infos, info)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to walk all snapshots: %v", err)
	}

	snapshotterTyp := ctrd.CurrentSnapshotterName(context.TODO())
	for _, info := range infos {
		sn, err := s.store.Get(info.Name)
		if err == nil {
			// Only update timestamp for non-active snapshot.
			if sn.Kind == info.Kind && sn.Kind != snapshots.KindActive {
				sn.Timestamp = time.Now().UnixNano()
				s.store.Add(sn)
				continue
			}
		}
		// Get newest stats if the snapshot is new or active.
		sn = Snapshot{
			Key:       info.Name,
			Kind:      info.Kind,
			Timestamp: time.Now().UnixNano(),
		}

		var (
			usage       snapshots.Usage
			usedPercent uint64
		)

		switch snapshotterTyp {
		case "overlayfs", "overlay1fs":
			if sn.Kind == snapshots.KindActive {
				usage, usedPercent, err = s.FetchActiveOverlaySnapshotDiskUsage(context.TODO(), info.Name)
				if err != nil && (errtypes.IsNotfound(err) || errtypes.IsPreCheckFailed(err)) {
					// when the container stops the container doesn't contain df command
					// the error should be not found or not running. we should skip the error
					log.With(nil).Debugf("failed to get usage for overlay kind of snapshot %q: %v", info.Name, err)
					continue
				}
			} else {
				usage, err = s.client.GetSnapshotUsage(context.Background(), info.Name)
			}
		default:
			usage, err = s.client.GetSnapshotUsage(context.Background(), info.Name)
		}
		if err != nil {
			log.With(nil).Warnf("failed to get usage for snapshot %q: %v", info.Name, err)
			continue
		}

		sn.Size = uint64(usage.Size)
		sn.Inodes = uint64(usage.Inodes)
		sn.UsagePercent = usedPercent
		s.store.Add(sn)
	}
	for _, sn := range s.store.List() {
		if sn.Timestamp > start {
			continue
		}
		// Delete the snapshot stats if it's not updated this time.
		// When remove a container,you also need to remove snapshot.
		// However, SnapshotStore will not be notified.
		// So wo need to delete snapshots from SnapshotStore that doesn't exist actually.
		s.store.Delete(sn.Key)
	}

	return nil
}

// FetchActiveOverlaySnapshotDiskUsage uses nsenter to get container disk usage.
//
// NOTE: if there is no disk quota to limit container, the result will be the
// the same to do df in host.
func (s *SnapshotsSyncer) FetchActiveOverlaySnapshotDiskUsage(ctx context.Context, key string) (snapshots.Usage, uint64, error) {
	pid, err := s.client.ContainerPID(ctx, key)
	if err != nil {
		return snapshots.Usage{}, 0, err
	}

	status, err := s.client.ContainerStatus(ctx, key)
	if err != nil {
		return snapshots.Usage{}, 0, err
	}

	if status.Status != containerd.Running {
		return snapshots.Usage{}, 0, errors.Wrapf(errtypes.ErrPreCheckFailed, "container(%s) is not running", key)
	}
	return s.doDiskUsageByNsenter(ctx, pid)
}

func (s *SnapshotsSyncer) doDiskUsageByNsenter(ctx context.Context, pid int) (snapshots.Usage, uint64, error) {
	args := []string{
		"--target", strconv.Itoa(pid),
		"--mount", "--uts", "--ipc", "--net", "--pid",
		"/bin/df", "-k", "/",
	}

	res, err := exec.CommandContext(ctx, "nsenter", args...).CombinedOutput()
	if err != nil {
		if strings.Contains(string(res), "No such file or directory") ||
			strings.Contains(err.Error(), "No such file or directory") {

			return snapshots.Usage{}, 0, errors.Wrapf(errtypes.ErrNotfound, "failed to exec nsenter: %v\n%s", err, string(res))
		}
		return snapshots.Usage{}, 0, errors.Wrapf(err, "failed to exec df: %s", string(res))
	}
	output := strings.TrimSpace(string(res))

	log.With(ctx).Debugf("nsenter %v:\n%s", args, output)

	lines := strings.Split(output, "\n")
	if len(lines) != 2 {
		return snapshots.Usage{}, 0, fmt.Errorf("df should return header and data:\n %s", output)
	}

	// NOTE: since different linux dist might use different version df, use
	// hash4Idx to store the index for the header instead of using
	// df --output=fieldlist
	hash4Idx := map[string]int{}
	header := strings.TrimSpace(lines[0])
	if header == "" {
		return snapshots.Usage{}, 0, fmt.Errorf("df return no header:\n%s", output)
	}

	for idx, key := range strings.Fields(header) {
		hash4Idx[strings.ToLower(key)] = idx
	}

	usedIdx, ok := hash4Idx["used"]
	if !ok {
		return snapshots.Usage{}, 0, fmt.Errorf("df return header without used colum:\n%s", output)
	}

	usedPercentIdx, ok := hash4Idx["use%"]
	if !ok {
		return snapshots.Usage{}, 0, fmt.Errorf("df return header without used%% colum:\n%s", output)
	}

	data := strings.TrimSpace(lines[1])
	values := strings.Fields(data)
	usedValue, err := strconv.Atoi(values[usedIdx])
	if err != nil {
		return snapshots.Usage{}, 0, fmt.Errorf("failed to parse df return data: %v\n%s", err, output)
	}

	percentValue := values[usedPercentIdx]
	usedPercentValue, err := strconv.Atoi(percentValue[:len(percentValue)-1])
	if err != nil {
		return snapshots.Usage{}, 0, fmt.Errorf("failed to parse df return used percent data: %v\n%s", err, output)
	}
	if usedPercentValue < 0 || usedPercentValue > 100 {
		return snapshots.Usage{}, 0, fmt.Errorf("invalid percent data: %v\n%s", err, output)
	}

	return snapshots.Usage{
		Size: int64(usedValue) * int64(1024),
	}, uint64(usedPercentValue), nil
}

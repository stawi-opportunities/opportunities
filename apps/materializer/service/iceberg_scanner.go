package service

// iceberg_scanner.go — snapshot-diff scan for the materializer.
//
// iceberg-go v0.5.0 has no FromSnapshotExclusive API. Instead we walk
// the snapshot ancestry to build the set of snapshot IDs that are
// "new" relative to the caller's watermark, then filter manifests by
// AddedSnapshotID before planning file tasks.

import (
	"context"
	"fmt"

	iceberg "github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/catalog"
	"github.com/apache/iceberg-go/table"
)

// SnapshotDiff is the result of a snapshot-diff scan. Files contains
// only the data files added in snapshots after prevSnapshotID. If
// prevSnapshotID == ToSnapID the table is up-to-date and Files is nil.
type SnapshotDiff struct {
	Files      []table.FileScanTask
	FromSnapID int64
	ToSnapID   int64
}

// ScanNewData returns the data files added to tbl since prevSnapshotID
// (exclusive). Pass prevSnapshotID = 0 to scan all files (cold start).
//
// Implementation notes for iceberg-go v0.5.0
//   - There is no FromSnapshotExclusive or incremental-scan API.
//   - Each ManifestFile carries an AddedSnapshotID (via SnapshotID()).
//   - We walk the snapshot list to collect every snapshot ID that came
//     after prevSnapshotID in the ancestry chain, then keep only the
//     manifests whose SnapshotID() is in that new-snapshot set.
//   - We call tbl.Scan(WithSnapshotID(current)) to get properly-wired
//     IO and metadata, but replace PlanFiles with a filtered variant
//     that reads only the new manifests.
func ScanNewData(
	ctx context.Context,
	cat catalog.Catalog,
	ident []string,
	prevSnapshotID int64,
) (*SnapshotDiff, error) {
	tbl, err := cat.LoadTable(ctx, ident)
	if err != nil {
		return nil, fmt.Errorf("load table %v: %w", ident, err)
	}

	current := tbl.CurrentSnapshot()
	if current == nil {
		// Table exists but has no data yet.
		return &SnapshotDiff{FromSnapID: prevSnapshotID, ToSnapID: prevSnapshotID}, nil
	}
	if current.SnapshotID == prevSnapshotID {
		// Already up-to-date.
		return &SnapshotDiff{FromSnapID: prevSnapshotID, ToSnapID: prevSnapshotID}, nil
	}

	// Collect the set of snapshot IDs that were added after prevSnapshotID.
	// We walk the snapshot list from the catalog metadata; these are in
	// chronological order (oldest first).
	newSnapIDs := collectNewSnapshotIDs(tbl.Metadata(), prevSnapshotID)

	// Scan the full current snapshot, then filter tasks to only those
	// whose manifest was introduced by a "new" snapshot.
	tasks, err := planFilteredFiles(ctx, tbl, current.SnapshotID, newSnapIDs)
	if err != nil {
		return nil, fmt.Errorf("plan filtered files %v: %w", ident, err)
	}

	return &SnapshotDiff{
		Files:      tasks,
		FromSnapID: prevSnapshotID,
		ToSnapID:   current.SnapshotID,
	}, nil
}

// collectNewSnapshotIDs returns the set of snapshot IDs that are
// strictly newer than prevSnapshotID. If prevSnapshotID == 0 all
// snapshot IDs are returned (cold start).
func collectNewSnapshotIDs(meta table.Metadata, prevSnapshotID int64) map[int64]struct{} {
	all := meta.Snapshots()
	newIDs := make(map[int64]struct{}, len(all))

	if prevSnapshotID == 0 {
		// Cold start — treat every snapshot as new.
		for _, s := range all {
			newIDs[s.SnapshotID] = struct{}{}
		}
		return newIDs
	}

	// Find the sequence number of the previous snapshot so we can
	// include everything strictly after it.
	var prevSeqNum int64 = -1
	for _, s := range all {
		if s.SnapshotID == prevSnapshotID {
			prevSeqNum = s.SequenceNumber
			break
		}
	}
	// If prevSnapshotID is not found (e.g. expired / compacted away),
	// fall back to full scan for safety.
	if prevSeqNum == -1 {
		for _, s := range all {
			newIDs[s.SnapshotID] = struct{}{}
		}
		return newIDs
	}

	for _, s := range all {
		if s.SequenceNumber > prevSeqNum {
			newIDs[s.SnapshotID] = struct{}{}
		}
	}
	return newIDs
}

// planFilteredFiles scans the snapshot at snapID and returns only the
// FileScanTasks whose backing manifest was added by one of the snapshot
// IDs in newSnapIDs.
//
// We do this at the ManifestFile level: each ManifestFile.SnapshotID()
// returns the snapshot that first introduced that manifest. If that
// snapshot is in newSnapIDs we know all its ADDED entries are new.
func planFilteredFiles(
	ctx context.Context,
	tbl *table.Table,
	snapID int64,
	newSnapIDs map[int64]struct{},
) ([]table.FileScanTask, error) {
	// Get the targeted snapshot so we can read its manifest list.
	snap := tbl.SnapshotByID(snapID)
	if snap == nil {
		return nil, fmt.Errorf("snapshot %d not found", snapID)
	}

	fs, err := tbl.FS(ctx)
	if err != nil {
		return nil, fmt.Errorf("get table fs: %w", err)
	}

	manifests, err := snap.Manifests(fs)
	if err != nil {
		return nil, fmt.Errorf("fetch manifests: %w", err)
	}

	var tasks []table.FileScanTask
	for _, mf := range manifests {
		// Only data manifests (not delete manifests).
		if mf.ManifestContent() != iceberg.ManifestContentData {
			continue
		}
		// Only manifests first written by a "new" snapshot.
		if _, ok := newSnapIDs[mf.SnapshotID()]; !ok {
			continue
		}

		entries, err := mf.FetchEntries(fs, true /* discardDeleted */)
		if err != nil {
			return nil, fmt.Errorf("fetch manifest entries: %w", err)
		}
		for _, e := range entries {
			// Only ADDED entries (not EXISTING or DELETED).
			if e.Status() != iceberg.EntryStatusADDED {
				continue
			}
			df := e.DataFile()
			tasks = append(tasks, table.FileScanTask{
				File:   df,
				Start:  0,
				Length: df.FileSizeBytes(),
			})
		}
	}
	return tasks, nil
}

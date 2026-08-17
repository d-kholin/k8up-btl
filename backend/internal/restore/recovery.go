package restore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/d-kholin/k8up-gui/internal/k8s"
)

// SQL dump point-in-time recovery: quiesce the app (never the whole
// namespace), take an optional safety dump, pipe `restic dump` of the .sql
// snapshot into the DB pod's git-defined restore command, restore any selected
// PVCs to the same point in time, then bring the app back and resume Argo.

type RecoveryPVC struct {
	SnapshotName string `json:"snapshotName"`
	PVCName      string `json:"pvcName"`
}

type RecoveryRequest struct {
	Namespace        string        `json:"namespace"`
	DumpSnapshotName string        `json:"dumpSnapshotName"`
	DBPodName        string        `json:"dbPodName"`
	SkipSafetyBackup bool          `json:"skipSafetyBackup"`
	PVCRestores      []RecoveryPVC `json:"pvcRestores"`
	Actor            string        `json:"-"`
}

// DumpFilePath returns the first .sql path in a snapshot's spec.paths — the
// file to pipe into the database client. Empty when the snapshot is not an
// application-level dump.
func DumpFilePath(paths []string) string {
	for _, p := range paths {
		if strings.HasSuffix(p, ".sql") {
			return p
		}
	}
	return ""
}

// SuggestRestoreCommand proposes a k8up-btl.local/restore-command value from
// the pod's K8up backup command. Suggestion only — recovery never runs an
// inferred command; the annotation must exist in git.
func SuggestRestoreCommand(backupCommand string) string {
	switch {
	case strings.Contains(backupCommand, "pg_dumpall"):
		return `psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" postgres`
	case strings.Contains(backupCommand, "pg_dump"):
		return `psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"`
	case strings.Contains(backupCommand, "mariadb-dump"):
		return `mariadb -u"$MARIADB_USER" -p"$MARIADB_PASSWORD" "$MARIADB_DATABASE"`
	case strings.Contains(backupCommand, "mysqldump"):
		return `mysql -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" "$MYSQL_DATABASE"`
	default:
		return ""
	}
}

// StartRecovery validates and begins an async SQL dump recovery.
func (o *Orchestrator) StartRecovery(ctx context.Context, req RecoveryRequest) (*State, error) {
	if o.Clients == nil {
		return nil, fmt.Errorf("kubernetes client not configured")
	}
	if req.Namespace == "" || req.DumpSnapshotName == "" || req.DBPodName == "" {
		return nil, fmt.Errorf("namespace, dumpSnapshotName and dbPodName are required")
	}
	if o.DumpSnapshot == nil {
		return nil, fmt.Errorf("dump streaming not configured")
	}
	// Refuse if Argo is already paused by a prior restore.
	if raw, reps, err := o.Clients.GetArgoControllerPausedState(ctx, o.ArgoNS); err == nil {
		if raw != "" || reps == 0 {
			return nil, fmt.Errorf("argo application-controller already paused (replicas=%d); resume or clear restore-gui state before starting a recovery", reps)
		}
	}

	// Dump snapshot must actually be an application-level .sql dump.
	snap, err := o.Clients.GetSnapshot(ctx, req.Namespace, req.DumpSnapshotName)
	if err != nil {
		return nil, fmt.Errorf("get dump snapshot: %w", err)
	}
	dumpPath := DumpFilePath(snapshotPaths(snap.Object))
	if dumpPath == "" {
		return nil, fmt.Errorf("snapshot %s/%s has no .sql path; use a plain PVC restore instead", req.Namespace, req.DumpSnapshotName)
	}
	dumpID := snapshotID(snap.Object, req.DumpSnapshotName)

	// The restore command must be declared in git on the live DB pod.
	db, err := o.Clients.GetBackupCommandPod(ctx, req.Namespace, req.DBPodName)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(db.RestoreCommand) == "" {
		msg := fmt.Sprintf("pod %s/%s has no %s annotation; recovery only runs commands declared in git", req.Namespace, db.PodName, k8s.AnnRestoreCommand)
		if sugg := SuggestRestoreCommand(db.BackupCommand); sugg != "" {
			msg += fmt.Sprintf(" (suggested value: %s)", sugg)
		}
		return nil, fmt.Errorf("%s", msg)
	}

	// Validate additional PVC restores under the same source-lock rule as
	// plain restores, and resolve their owner workloads for quiescing.
	parts := make([]PVCRestorePart, 0, len(req.PVCRestores))
	for _, pr := range req.PVCRestores {
		if pr.SnapshotName == "" || pr.PVCName == "" {
			return nil, fmt.Errorf("pvcRestores entries need snapshotName and pvcName")
		}
		psnap, err := o.Clients.GetSnapshot(ctx, req.Namespace, pr.SnapshotName)
		if err != nil {
			return nil, fmt.Errorf("get snapshot %s: %w", pr.SnapshotName, err)
		}
		candidates := SourcePVCCandidates(snapshotPaths(psnap.Object))
		if !containsString(candidates, pr.PVCName) {
			return nil, fmt.Errorf("PVC %q is not a source of snapshot %s/%s (backed up: %s)", pr.PVCName, req.Namespace, pr.SnapshotName, strings.Join(candidates, ", "))
		}
		part := PVCRestorePart{
			SnapshotName: pr.SnapshotName,
			SnapshotID:   snapshotID(psnap.Object, pr.SnapshotName),
			PVCName:      pr.PVCName,
			Status:       "pending",
		}
		if wl, err := o.Clients.ResolvePVCOwner(ctx, req.Namespace, pr.PVCName); err == nil && wl != nil {
			part.Workload = wl
		}
		parts = append(parts, part)
	}

	id := uuid.NewString()
	st := &State{
		RestoreID:         id,
		Kind:              "recovery",
		Step:              StepQueued,
		SnapshotID:        dumpID,
		SnapshotCR:        req.DumpSnapshotName,
		SnapshotNamespace: req.Namespace,
		PVCNamespace:      req.Namespace,
		DBPod:             db.PodName,
		DBContainer:       db.Container,
		DBWorkload:        db.Workload,
		RestoreCommand:    db.RestoreCommand,
		DumpPath:          dumpPath,
		PVCParts:          parts,
		StartedAt:         time.Now().UTC(),
		Actor:             req.Actor,
	}

	o.mu.Lock()
	if o.activeID != "" {
		active := o.activeID
		o.mu.Unlock()
		return nil, fmt.Errorf("restore %s already in progress; only one restore may run at a time", active)
	}
	o.activeID = id
	o.mu.Unlock()

	o.set(st)
	go o.runRecovery(st, db, req.SkipSafetyBackup)
	return st, nil
}

func (o *Orchestrator) runRecovery(st *State, db *k8s.DBPod, skipSafety bool) {
	ctx := context.Background()
	runCtx, cancelRun := context.WithCancel(ctx)
	o.mu.Lock()
	o.cancels[st.RestoreID] = cancelRun
	o.mu.Unlock()

	var runErr error
	argoPaused := false
	var stopped []k8s.ScalableWorkload

	defer func() {
		cancelRun()
		o.mu.Lock()
		delete(o.cancels, st.RestoreID)
		cancelled := st.CancelRequested
		o.mu.Unlock()

		if runErr != nil {
			if cancelled {
				st.Cancelled = true
				st.LastError = "recovery cancelled by operator"
			}
			// Delete any in-flight PVC Restore CR — an orphaned restic job must
			// not keep writing to a PVC the app is about to remount.
			for i := range st.PVCParts {
				p := &st.PVCParts[i]
				if p.Status == "running" && p.RestoreCRName != "" {
					if err := o.Clients.DeleteRestore(ctx, st.PVCNamespace, p.RestoreCRName); err != nil {
						o.Log.Warn("delete restore CR after abort", "cr", p.RestoreCRName, "err", err)
					} else {
						o.emitLog(st.RestoreID, fmt.Sprintf("··· deleted in-flight Restore CR %s", p.RestoreCRName))
					}
					p.Status = "failed"
				}
			}
			// Bring the app back on failure or cancel — Argo self-heal cannot be
			// relied on to restore replica counts that aren't tracked in git.
			for _, w := range stopped {
				wl := w.WorkloadRef
				if err := o.Clients.Scale(ctx, &wl, w.Replicas); err != nil {
					o.Log.Error("scale up after aborted recovery", "workload", wl.Name, "err", err)
					st.LastError = joinErr(st.LastError, fmt.Errorf("scale up %s/%s: %w", wl.Kind, wl.Name, err))
				} else {
					o.emitLog(st.RestoreID, fmt.Sprintf("··· scaled %s %s/%s back to %d replica(s) — verify application state before trusting it", wl.Kind, wl.Namespace, wl.Name, w.Replicas))
				}
			}
		}

		o.finalize(st, runErr, &argoPaused)
	}()

	// 1. Pause ALL Argo reconciliation.
	st.Step = StepPausingArgo
	o.set(st)
	o.emitLog(st.RestoreID, "pausing Argo CD application-controller…")
	b, _ := json.Marshal(st)
	origCtrl, err := o.Clients.PauseArgoController(runCtx, o.ArgoNS, string(b))
	if err != nil {
		runErr = fmt.Errorf("pause argo controller: %w", err)
		return
	}
	if origCtrl <= 0 {
		origCtrl = 1
	}
	st.ArgoControllerReplicas = origCtrl
	st.ArgoPausedGlobally = true
	argoPaused = true
	o.set(st)
	o.emitLog(st.RestoreID, fmt.Sprintf("Argo controller paused (was %d replica(s))", origCtrl))

	// 2. Quiesce: stop this app's writers only — grouped by instance label or
	// the git annotation, plus owners of PVCs being restored. The DB workload
	// itself stays up to receive the dump.
	st.Step = StepQuiescing
	o.set(st)
	quiesce, err := o.Clients.QuiesceSet(runCtx, db)
	if err != nil {
		runErr = fmt.Errorf("compute quiesce set: %w", err)
		return
	}
	quiesce = o.addPVCOwners(runCtx, quiesce, st.PVCParts, db)
	if len(quiesce) == 0 {
		o.emitLog(st.RestoreID, fmt.Sprintf("WARNING: no app workloads identified to stop (no app.kubernetes.io/instance label and no %s annotation) — the database may receive writes during recovery", k8s.AnnQuiesceWorkloads))
	}
	for _, w := range quiesce {
		wl := w.WorkloadRef
		o.emitLog(st.RestoreID, fmt.Sprintf("scaling down %s %s/%s → 0 (was %d)", wl.Kind, wl.Namespace, wl.Name, w.Replicas))
		if err := o.Clients.Scale(runCtx, &wl, 0); err != nil {
			runErr = fmt.Errorf("scale down %s/%s: %w", wl.Kind, wl.Name, err)
			return
		}
		stopped = append(stopped, w)
	}
	st.StoppedWorkloads = stopped
	o.set(st)
	scaleCtx, cancelScale := context.WithTimeout(runCtx, o.ScaleDownTimeout)
	defer cancelScale()
	for _, w := range stopped {
		wl := w.WorkloadRef
		if err := o.Clients.WaitPodsGone(scaleCtx, &wl); err != nil {
			runErr = fmt.Errorf("wait pods gone (%s/%s): %w", wl.Kind, wl.Name, err)
			return
		}
	}
	if len(stopped) > 0 {
		o.emitLog(st.RestoreID, "app workloads stopped — database is quiesced")
	}

	// 3. Safety backup: a fresh dump while quiesced = a clean undo point.
	if !skipSafety {
		st.Step = StepSafetyBackup
		crName := fmt.Sprintf("gui-safety-%s", st.RestoreID[:8])
		st.SafetyBackupCR = crName
		o.set(st)
		o.emitLog(st.RestoreID, fmt.Sprintf("taking safety backup %s before touching data…", crName))
		if _, err := o.Clients.CreateSimpleJobCR(runCtx, k8s.GVRBackup, "Backup", st.PVCNamespace, crName, map[string]any{}); err != nil {
			runErr = fmt.Errorf("create safety backup: %w", err)
			return
		}
		safetyCtx, cancelSafety := context.WithTimeout(runCtx, o.RestoreTimeout)
		err = o.waitJobComplete(safetyCtx, k8s.GVRBackup, st.PVCNamespace, crName)
		cancelSafety()
		if err != nil {
			runErr = fmt.Errorf("safety backup: %w", err)
			return
		}
		o.emitLog(st.RestoreID, "··· safety backup finished")
	}

	// 4. Pipe the dump into the DB pod's git-defined restore command.
	st.Step = StepRestoringDB
	o.set(st)
	o.emitLog(st.RestoreID, fmt.Sprintf("restoring database: restic dump %s | exec %s/%s (%s): sh -c %q", st.DumpPath, st.PVCNamespace, st.DBPod, st.DBContainer, st.RestoreCommand))
	dbCtx, cancelDB := context.WithTimeout(runCtx, o.RestoreTimeout)
	piped, err := o.pipeDumpToPod(dbCtx, st, db)
	cancelDB()
	st.BytesRecovered = piped
	if err != nil {
		runErr = fmt.Errorf("sql restore: %w", err)
		return
	}
	o.emitLog(st.RestoreID, fmt.Sprintf("··· database restore finished (%d bytes piped)", piped))

	// 5. Restore selected PVCs to the same point in time, sequentially.
	if len(st.PVCParts) > 0 {
		st.Step = StepRestoringPVCs
		o.set(st)
	}
	for i := range st.PVCParts {
		part := &st.PVCParts[i]
		crName := fmt.Sprintf("gui-restore-%s-%d", st.RestoreID[:8], i)
		part.RestoreCRName = crName
		part.Status = "running"
		o.set(st)
		o.emitLog(st.RestoreID, fmt.Sprintf("restoring PVC %s from snapshot %s (CR %s)…", part.PVCName, part.SnapshotName, crName))
		if _, err := o.Clients.CreateRestoreCR(runCtx, st.PVCNamespace, crName, part.SnapshotID, part.PVCName, nil); err != nil {
			part.Status = "failed"
			runErr = fmt.Errorf("create restore cr for %s: %w", part.PVCName, err)
			return
		}
		partCtx, cancelPart := context.WithTimeout(runCtx, o.RestoreTimeout)
		go o.followRestoreLogs(partCtx, st.RestoreID, st.PVCNamespace, crName)
		err := o.waitJobComplete(partCtx, k8s.GVRRestore, st.PVCNamespace, crName)
		cancelPart()
		if err != nil {
			part.Status = "failed"
			runErr = fmt.Errorf("restore PVC %s: %w", part.PVCName, err)
			return
		}
		part.Status = "done"
		o.set(st)
		o.emitLog(st.RestoreID, fmt.Sprintf("··· PVC %s restored", part.PVCName))
	}

	// 6. Bring the app back.
	st.Step = StepScalingUp
	o.set(st)
	for _, w := range stopped {
		wl := w.WorkloadRef
		if err := o.Clients.Scale(ctx, &wl, w.Replicas); err != nil {
			runErr = fmt.Errorf("scale up %s/%s: %w", wl.Kind, wl.Name, err)
			return
		}
		o.emitLog(st.RestoreID, fmt.Sprintf("scaled %s %s/%s back to %d replica(s)", wl.Kind, wl.Namespace, wl.Name, w.Replicas))
	}
	// 7. Resume Argo controller — finalize (via defer).
}

// addPVCOwners extends the quiesce set with owners of PVCs being restored
// (their pods must release the volume) — skipping the DB's own workload.
func (o *Orchestrator) addPVCOwners(ctx context.Context, set []k8s.ScalableWorkload, parts []PVCRestorePart, db *k8s.DBPod) []k8s.ScalableWorkload {
	seen := map[string]bool{}
	for _, w := range set {
		seen[w.Kind+"/"+w.Name] = true
	}
	if db.Workload != nil {
		seen[db.Workload.Kind+"/"+db.Workload.Name] = true
	}
	for _, p := range parts {
		if p.Workload == nil {
			continue
		}
		key := p.Workload.Kind + "/" + p.Workload.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		reps, err := o.Clients.GetReplicas(ctx, p.Workload)
		if err != nil {
			o.Log.Warn("get replicas for pvc owner", "workload", p.Workload.Name, "err", err)
			reps = 1
		}
		set = append(set, k8s.ScalableWorkload{WorkloadRef: *p.Workload, Replicas: reps})
	}
	return set
}

// pipeDumpToPod streams `restic dump` of the .sql file into the DB container's
// restore command via pod exec; returns bytes piped.
func (o *Orchestrator) pipeDumpToPod(ctx context.Context, st *State, db *k8s.DBPod) (int64, error) {
	pr, pw := io.Pipe()
	counter := &countingReader{r: pr}
	dumpErr := make(chan error, 1)
	go func() {
		err := o.DumpSnapshot(ctx, st.SnapshotNamespace, st.SnapshotCR, st.DumpPath, pw)
		pw.CloseWithError(err)
		dumpErr <- err
	}()

	execErr := o.Clients.ExecStdin(ctx, st.PVCNamespace, db.PodName, db.Container,
		[]string{"/bin/sh", "-c", db.RestoreCommand}, counter,
		func(line string) { o.emitLog(st.RestoreID, line) })
	// Unblock the dump goroutine if exec bailed before draining stdin.
	_ = pr.CloseWithError(fmt.Errorf("exec finished"))
	derr := <-dumpErr

	if execErr != nil {
		return counter.n, fmt.Errorf("exec %s: %w", db.RestoreCommand, execErr)
	}
	if derr != nil {
		return counter.n, fmt.Errorf("restic dump: %w", derr)
	}
	return counter.n, nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func snapshotID(obj map[string]any, fallback string) string {
	if id, ok, _ := unstructuredString(obj, "spec", "id"); ok && id != "" {
		return id
	}
	if id, ok, _ := unstructuredString(obj, "status", "id"); ok && id != "" {
		return id
	}
	if id, ok, _ := unstructuredString(obj, "spec", "snapshot"); ok && id != "" {
		return id
	}
	return fallback
}

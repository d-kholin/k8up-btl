package lab

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/d-kholin/k8up-gui/internal/audit"
	"github.com/d-kholin/k8up-gui/internal/k8s"
)

// run provisions a lab: PVCs → Restore CRs → (app tier) clone Application →
// SQL dump replay → ready. Everything lands in the lab namespace only.
func (m *Manager) run(st *State) {
	// ctx is cancelled by Teardown on a provisioning lab; the deferred state
	// handling below then reports the run as cancelled rather than failed,
	// and the teardown goroutine waits on `done` before touching resources.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.mu.Lock()
	m.cancels[st.LabID] = cancel
	m.dones[st.LabID] = done
	m.mu.Unlock()
	var runErr error

	defer func() {
		cancel()
		m.mu.Lock()
		delete(m.cancels, st.LabID)
		cancelled := st.CancelRequested
		// Belt-and-braces: Teardown flags the jobs-map entry; honor it even
		// if something replaced the pointer this goroutine holds.
		if j, ok := m.jobs[st.LabID]; ok && j.CancelRequested {
			cancelled = true
			st.CancelRequested = true
		}
		m.mu.Unlock()
		if runErr != nil {
			st.Step = StepFailed
			if cancelled {
				// No drill row: a cancelled run is not restore evidence either
				// way — the namespace's previous verification stands.
				st.LastError = "lab cancelled by operator"
				m.set(st)
				m.emitLog(st.LabID, "··· provisioning aborted (cancel requested)")
			} else {
				st.LastError = runErr.Error()
				m.set(st)
				m.emitLog(st.LabID, "LAB FAILED: "+runErr.Error())
				m.recordDrill(st, "failed", runErr.Error())
			}
		}
		close(done)
	}()

	// 1. Pre-create the restored PVCs with their production names/specs so the
	// Restore CRs have a target and (app tier) the clone's manifests adopt them.
	st.Step = StepCreatingPVCs
	m.set(st)
	for i := range st.PVCs {
		p := &st.PVCs[i]
		m.emitLog(st.LabID, fmt.Sprintf("creating lab PVC %s/%s (spec cloned from %s/%s)", st.LabNamespace, p.PVCName, st.SourceNamespace, p.PVCName))
		if err := m.Clients.CreateLabPVCFromSource(ctx, st.SourceNamespace, p.PVCName, st.LabNamespace, st.LabID); err != nil {
			runErr = fmt.Errorf("create lab PVC %s: %w (a leftover lab may need teardown)", p.PVCName, err)
			return
		}
	}

	// 2. Fill them. The Restore CRs run in the lab namespace but read from the
	// SOURCE namespace's repository; restic credentials are K8up's operator
	// globals, so nothing is copied.
	if len(st.PVCs) > 0 {
		st.Step = StepRestoring
		m.set(st)
		backend, err := m.Clients.ResolveRestoreBackend(ctx, st.SourceNamespace, "")
		if err != nil {
			runErr = fmt.Errorf("resolve source repository: %w", err)
			return
		}
		for i := range st.PVCs {
			p := &st.PVCs[i]
			crName := fmt.Sprintf("lab-restore-%s-%d", st.LabID[:8], i)
			p.RestoreCRName = crName
			p.Status = "running"
			m.set(st)
			m.emitLog(st.LabID, fmt.Sprintf("restoring PVC %s from snapshot %s (CR %s)…", p.PVCName, p.SnapshotName, crName))
			if _, err := m.Clients.CreateLabRestoreCR(ctx, st.LabNamespace, crName, p.SnapshotID, p.PVCName, backend); err != nil {
				p.Status = "failed"
				runErr = fmt.Errorf("create restore CR for %s: %w", p.PVCName, err)
				return
			}
			rctx, cancel := context.WithTimeout(ctx, m.RestoreTimeout)
			go func() {
				_ = m.Clients.FollowRestoreJobLogs(rctx, st.LabNamespace, crName, func(line string) {
					m.emitLog(st.LabID, line)
				})
			}()
			err = m.Clients.WaitJobDone(rctx, k8s.GVRRestore, st.LabNamespace, crName)
			cancel()
			if err != nil {
				p.Status = "failed"
				runErr = fmt.Errorf("restore PVC %s: %w", p.PVCName, err)
				return
			}
			p.Status = "done"
			m.set(st)
			m.emitLog(st.LabID, fmt.Sprintf("··· PVC %s restored", p.PVCName))
		}
	}

	switch st.Tier {
	case TierData:
		// 3a. Attach the read-only file browser.
		st.Step = StepInspecting
		m.set(st)
		m.emitLog(st.LabID, "starting inspection pod (file browser, read-only mount)…")
		if err := m.Clients.CreateInspectWorkload(ctx, st.LabNamespace, st.PVCs[0].PVCName, m.InspectImage, st.LabID); err != nil {
			runErr = fmt.Errorf("create inspection workload: %w", err)
			return
		}
		st.InspectService = "lab-inspect"
		m.emitLog(st.LabID, fmt.Sprintf("··· inspection UI at service %s/lab-inspect:8080 (route it through the tunnel or `kubectl -n %s port-forward svc/lab-inspect 8080`)", st.LabNamespace, st.LabNamespace))

	case TierApp:
		// 3b. Deploy the app from git into the lab.
		st.Step = StepDeploying
		m.set(st)
		srcApp, err := m.Clients.GetResource(ctx, k8s.GVRArgoApp, m.ArgoNamespace, st.AppName)
		if err != nil {
			runErr = fmt.Errorf("get source application %s: %w", st.AppName, err)
			return
		}
		clone, err := k8s.BuildLabApplication(srcApp, st.CloneAppName, m.ArgoNamespace, m.Project, st.LabNamespace, st.LabID)
		if err != nil {
			runErr = err
			return
		}
		m.emitLog(st.LabID, fmt.Sprintf("creating clone Application %s (project %s, kustomize namespace → %s, Namespace+Schedule stripped)…", st.CloneAppName, m.Project, st.LabNamespace))
		if _, err := m.Clients.CreateApplication(ctx, clone); err != nil {
			runErr = fmt.Errorf("create clone application: %w", err)
			return
		}
		dctx, cancel := context.WithTimeout(ctx, m.DeployTimeout)
		err = m.waitAppHealthy(dctx, st)
		cancel()
		if err != nil {
			runErr = fmt.Errorf("clone application: %w", err)
			return
		}
		now := time.Now().UTC()
		st.HealthyAt = &now
		m.set(st)
		m.emitLog(st.LabID, fmt.Sprintf("··· clone Synced + Healthy in %s (RTO actual, git deploy only)", now.Sub(st.StartedAt).Round(time.Second)))

		// 4. Replay the SQL dump into the lab database, if the app has one.
		if st.DumpSnapshot != "" {
			st.Step = StepRestoringDB
			m.set(st)
			if err := m.replayDump(ctx, st); err != nil {
				runErr = err
				return
			}
		}
	}

	st.Step = StepReady
	now := time.Now().UTC()
	st.ReadyAt = &now
	m.set(st)
	if st.ExpiresAt != nil {
		m.emitLog(st.LabID, fmt.Sprintf("LAB READY — auto-teardown at %s (extend from the Lab page)", st.ExpiresAt.Format(time.RFC3339)))
	}
	m.recordDrill(st, "success", "")
}

// waitAppHealthy polls the clone Application until Synced + Healthy, failing
// fast on sync error phases and Argo error conditions.
func (m *Manager) waitAppHealthy(ctx context.Context, st *State) error {
	lastHealth := ""
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("timed out waiting for Synced+Healthy (last: sync=%s health=%s): %w", st.Health, lastHealth, err)
		}
		status, err := m.Clients.GetApplicationStatus(ctx, m.ArgoNamespace, st.CloneAppName)
		if err != nil {
			return err
		}
		if status.Health != lastHealth {
			lastHealth = status.Health
			st.Health = status.Health
			m.set(st)
			m.emitLog(st.LabID, fmt.Sprintf("clone status: sync=%s health=%s", status.Sync, status.Health))
		}
		switch status.Phase {
		case "Failed", "Error":
			return fmt.Errorf("sync %s: %s", strings.ToLower(status.Phase), status.Message)
		}
		if status.Sync == "Synced" && status.Health == "Healthy" {
			return nil
		}
		t := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			t.Stop()
		case <-t.C:
		}
	}
}

// replayDump waits for the lab database pod (the clone's workload annotated
// with k8up.io/backupcommand) and pipes `restic dump` of the SQL snapshot into
// its git-sourced restore command — the same mechanics as a production SQL
// recovery, minus quiesce and safety backup (it's a lab).
func (m *Manager) replayDump(ctx context.Context, st *State) error {
	wctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	db, err := m.waitLabDBPod(wctx)
	cancel()
	if err != nil {
		return fmt.Errorf("waiting for lab database pod: %w", err)
	}
	st.DBPod = db.PodName
	cmd, source, err := m.ResolveRestoreCommand(db)
	if err != nil {
		return fmt.Errorf("resolve restore command: %w", err)
	}
	st.RestoreCommand = cmd
	m.set(st)
	m.emitLog(st.LabID, fmt.Sprintf("replaying SQL dump %s → %s/%s (%s): sh -c %q (command source: %s)", st.DumpPath, st.LabNamespace, db.PodName, db.Container, cmd, source))

	// The engine may accept connections a beat after the pod turns Running;
	// retry the pipe a few times before giving up.
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			m.emitLog(st.LabID, fmt.Sprintf("··· retrying dump replay (attempt %d/3): %v", attempt, lastErr))
			t := time.NewTimer(15 * time.Second)
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
		}
		pctx, cancel := context.WithTimeout(ctx, m.RestoreTimeout)
		n, err := m.pipeDump(pctx, st, db, cmd)
		cancel()
		if err == nil {
			m.emitLog(st.LabID, fmt.Sprintf("··· database replay finished (%d bytes piped)", n))
			return nil
		}
		lastErr = err
	}
	return fmt.Errorf("sql replay: %w", lastErr)
}

func (m *Manager) waitLabDBPod(ctx context.Context) (*k8s.DBPod, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pods, err := m.Clients.ListBackupCommandPods(ctx, m.LabNamespace)
		if err == nil && len(pods) > 0 {
			return &pods[0], nil
		}
		t := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}

func (m *Manager) pipeDump(ctx context.Context, st *State, db *k8s.DBPod, cmd string) (int64, error) {
	pr, pw := io.Pipe()
	counter := &countingReader{r: pr}
	dumpErr := make(chan error, 1)
	go func() {
		err := m.DumpSnapshot(ctx, st.SourceNamespace, st.DumpSnapshot, st.DumpPath, pw)
		pw.CloseWithError(err)
		dumpErr <- err
	}()

	execErr := m.Clients.ExecStdin(ctx, st.LabNamespace, db.PodName, db.Container,
		[]string{"/bin/sh", "-c", cmd}, counter,
		func(line string) { m.emitLog(st.LabID, line) })
	_ = pr.CloseWithError(fmt.Errorf("exec finished"))
	derr := <-dumpErr

	if execErr != nil {
		return counter.n, fmt.Errorf("exec: %w", execErr)
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

// recordDrill writes the restore-verification evidence row. Success is
// recorded the moment the lab is ready — the restore is proven then; teardown
// is housekeeping and audits separately.
func (m *Manager) recordDrill(st *State, status, detail string) {
	if m.Audit == nil {
		return
	}
	pvcs := make([]string, 0, len(st.PVCs))
	for _, p := range st.PVCs {
		pvcs = append(pvcs, p.PVCName)
	}
	summary := fmt.Sprintf("tier=%s", st.Tier)
	if st.AppName != "" {
		summary += " app=" + st.AppName
	}
	if len(pvcs) > 0 {
		summary += " pvcs=" + strings.Join(pvcs, ",")
	}
	if st.DumpSnapshot != "" {
		summary += " sql-dump=yes"
	}
	if st.HealthyAt != nil {
		summary += " healthy_in=" + st.HealthyAt.Sub(st.StartedAt).Round(time.Second).String()
	}
	if detail != "" {
		summary += " — " + detail
	}
	snap := st.DumpSnapshotID
	if snap == "" && len(st.PVCs) > 0 {
		snap = st.PVCs[0].SnapshotID
	}
	_, _ = m.Audit.Insert(context.Background(), audit.Entry{
		Kind:      "drill",
		Actor:     st.Actor,
		Namespace: st.SourceNamespace,
		Snapshot:  snap,
		PVC:       strings.Join(pvcs, ","),
		Status:    status,
		Detail:    summary,
		RestoreID: st.LabID,
	})
}

// Teardown removes everything a lab created. Callable from ANY live step:
// a provisioning run is cancelled first (its Restore CR wait aborts, the
// deferred handler marks it cancelled), then resources are removed once the
// run goroutine has fully stopped. Also driven by the TTL loop. Asynchronous.
func (m *Manager) Teardown(id, actor string) error {
	m.mu.Lock()
	st, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("lab %s not found", id)
	}
	switch st.Step {
	case StepTearingDown:
		m.mu.Unlock()
		return fmt.Errorf("lab %s teardown already in progress", id)
	case StepDeleted:
		m.mu.Unlock()
		return fmt.Errorf("lab %s is already torn down", id)
	case StepReady, StepFailed:
		st.Step = StepTearingDown
		st.TornDownBy = actor
		cp := *st
		m.mu.Unlock()
		m.set(&cp)
		go m.teardown(&cp, actor)
		return nil
	default:
		// Provisioning: cancel the run, wait for it to stop, then tear down.
		st.CancelRequested = true
		st.TornDownBy = actor
		cancel := m.cancels[id]
		done := m.dones[id]
		cp := *st
		m.mu.Unlock()
		m.set(&cp)
		m.emitLog(id, fmt.Sprintf("··· cancel requested by %s — aborting provisioning, then tearing down", actor))
		if cancel != nil {
			cancel()
		}
		go func() {
			if done != nil {
				select {
				case <-done:
				case <-time.After(3 * time.Minute):
					m.emitLog(id, "··· run did not stop within 3m; tearing down anyway")
				}
			}
			m.mu.Lock()
			j, ok := m.jobs[id]
			if !ok || j.Step == StepTearingDown || j.Step == StepDeleted {
				m.mu.Unlock()
				return
			}
			j.Step = StepTearingDown
			cp2 := *j
			m.mu.Unlock()
			m.set(&cp2)
			m.teardown(&cp2, actor)
		}()
		return nil
	}
}

func (m *Manager) teardown(st *State, actor string) {
	ctx, cancel := context.WithTimeout(context.Background(), m.TeardownTimeout)
	defer cancel()
	var errs []string
	m.emitLog(st.LabID, fmt.Sprintf("tearing down lab (requested by %s)…", actor))

	if err := m.Clients.DeleteInspectWorkload(ctx, st.LabNamespace); err != nil {
		errs = append(errs, fmt.Sprintf("inspection workload: %v", err))
	}
	for _, p := range st.PVCs {
		if p.RestoreCRName == "" {
			continue
		}
		if err := m.Clients.DeleteRestore(ctx, st.LabNamespace, p.RestoreCRName); err != nil && !k8s.IsNotFound(err) {
			errs = append(errs, fmt.Sprintf("restore CR %s: %v", p.RestoreCRName, err))
		}
	}
	if st.CloneAppName != "" {
		m.emitLog(st.LabID, fmt.Sprintf("deleting clone Application %s (cascade via resources finalizer)…", st.CloneAppName))
		if err := m.Clients.DeleteApplication(ctx, m.ArgoNamespace, st.CloneAppName); err != nil && !k8s.IsNotFound(err) {
			errs = append(errs, fmt.Sprintf("delete application: %v", err))
		} else if err := m.Clients.WaitApplicationGone(ctx, m.ArgoNamespace, st.CloneAppName); err != nil {
			errs = append(errs, fmt.Sprintf("wait application gone: %v", err))
		}
	}
	// Delete EVERY PVC in the lab namespace: Argo-created ones (empty database
	// volumes) carry Delete=false sync-options in git and survive the cascade.
	// The storage class retains PVs, so each released PV goes too.
	if pvcs, err := m.Clients.ListLabPVCs(ctx, st.LabNamespace); err != nil {
		errs = append(errs, fmt.Sprintf("list lab PVCs: %v", err))
	} else {
		for _, name := range pvcs {
			pv, err := m.Clients.DeleteLabPVCAndPV(ctx, st.LabNamespace, name)
			if err != nil {
				errs = append(errs, fmt.Sprintf("PVC %s: %v", name, err))
				continue
			}
			if pv != "" {
				m.emitLog(st.LabID, fmt.Sprintf("··· deleted PVC %s and retained PV %s", name, pv))
			} else {
				m.emitLog(st.LabID, fmt.Sprintf("··· deleted PVC %s", name))
			}
		}
	}

	now := time.Now().UTC()
	if len(errs) > 0 {
		st.Step = StepFailed
		st.LastError = "teardown incomplete: " + strings.Join(errs, "; ")
		m.set(st)
		m.emitLog(st.LabID, "TEARDOWN INCOMPLETE — retry teardown after fixing: "+strings.Join(errs, "; "))
	} else {
		st.Step = StepDeleted
		st.FinishedAt = &now
		st.LastError = ""
		m.set(st)
		m.emitLog(st.LabID, "··· lab torn down")
	}
	if m.Audit != nil {
		status := "lab_teardown"
		detail := fmt.Sprintf("lab %s (%s) torn down by %s", st.LabID, st.SourceNamespace, actor)
		if len(errs) > 0 {
			status = "lab_teardown_failed"
			detail += ": " + strings.Join(errs, "; ")
		}
		_, _ = m.Audit.Insert(context.Background(), audit.Entry{
			Kind: "system", Actor: actor, Namespace: st.SourceNamespace,
			Status: status, Detail: detail, RestoreID: st.LabID,
		})
	}
	m.mu.Lock()
	delete(m.dones, st.LabID)
	m.mu.Unlock()
}

package kubernetes

import (
	"context"
	"strings"
	"time"

	"emperror.dev/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/pelican-dev/wings/environment"
	"github.com/pelican-dev/wings/remote"
)

// OnBeforeStart is called before the server starts. It ensures the Pod is in a
// clean state by deleting any existing Pod and recreating it with the latest
// configuration from the Panel.
func (e *Environment) OnBeforeStart(ctx context.Context) error {
	// Delete any existing Pod to ensure fresh config is applied.
	gracePeriod := int64(0)
	_ = e.client.CoreV1().Pods(e.namespace()).Delete(ctx, e.Id, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
	})

	// Wait briefly for deletion to propagate.
	e.waitForPodDeletion(ctx, 10*time.Second)

	// Create the Pod with current configuration.
	if err := e.Create(); err != nil {
		return err
	}

	return nil
}

// Start boots the server by creating the Pod (if needed) and attaching to it.
// Since Kubernetes Pods start running immediately upon creation, this mainly
// ensures we're attached to capture output.
func (e *Environment) Start(ctx context.Context) error {
	sawError := false

	defer func() {
		if sawError {
			e.SetState(environment.ProcessStoppingState)
			e.SetState(environment.ProcessOfflineState)
		}
	}()

	// Check if Pod already exists and is running.
	if running, _ := e.IsRunning(ctx); running {
		e.SetState(environment.ProcessRunningState)
		return e.Attach(ctx)
	}

	e.SetState(environment.ProcessStartingState)
	sawError = true

	// Run pre-start to ensure the Pod exists with fresh configuration.
	if err := e.OnBeforeStart(ctx); err != nil {
		return errors.WrapIf(err, "environment/kubernetes: failed to run pre-boot process")
	}

	// Wait for the Pod to reach Running phase.
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	if err := e.waitForPodRunning(waitCtx); err != nil {
		return errors.WrapIf(err, "environment/kubernetes: pod did not reach running state")
	}

	// Attach to the Pod to stream output.
	if err := e.Attach(ctx); err != nil {
		return errors.WrapIf(err, "environment/kubernetes: failed to attach to pod")
	}

	e.SetState(environment.ProcessRunningState)
	sawError = false
	return nil
}

// Stop sends the configured stop command or deletes the Pod with a grace
// period to allow graceful shutdown.
func (e *Environment) Stop(ctx context.Context) error {
	e.mu.RLock()
	s := e.meta.Stop
	e.mu.RUnlock()

	// If using a signal-based stop, terminate with that signal.
	if s.Type == "" || s.Type == remote.ProcessStopSignal {
		return e.Terminate(ctx, "SIGTERM")
	}

	if e.st.Load() != environment.ProcessOfflineState {
		e.SetState(environment.ProcessStoppingState)
	}

	// If using a command-based stop and we're attached, send the command.
	if e.IsAttached() && s.Type == remote.ProcessStopCommand {
		return e.SendCommand(s.Value)
	}

	// Default: delete the Pod with a 30-second grace period.
	gracePeriod := int64(30)
	err := e.client.CoreV1().Pods(e.namespace()).Delete(ctx, e.Id, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
	})
	if err != nil && !isNotFound(err) {
		return errors.Wrap(err, "environment/kubernetes: failed to stop pod")
	}

	return nil
}

// WaitForStop attempts to gracefully stop the server and waits for the Pod to
// terminate. If the timeout is reached and terminate is true, the Pod is
// forcefully deleted.
func (e *Environment) WaitForStop(ctx context.Context, duration time.Duration, terminate bool) error {
	// If the Pod is already gone, or exists but is no longer running, the server
	// is effectively stopped and there is nothing to wait for. A stopped Pod is
	// not automatically removed, so proceeding into waitForPodDeletion would block
	// for the full duration (holding the power lock) waiting for a deletion that
	// never happens. This is what gets hit when a stop/restart is issued against a
	// server that is already offline.
	if pod, err := e.getPod(ctx); err != nil {
		if isNotFound(err) {
			e.markOffline()
			return nil
		}
		// Fall through on unexpected errors and attempt the normal stop flow.
	} else if !isPodRunning(pod) {
		e.markOffline()
		return nil
	}

	tctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-tctx.Done():
		}
	}()

	// Send the stop command/signal.
	if err := e.Stop(tctx); err != nil {
		if terminate && errors.Is(err, context.DeadlineExceeded) {
			return e.Terminate(ctx, "SIGKILL")
		}
		return err
	}

	// Wait for the Pod to be gone.
	if err := e.waitForPodDeletion(tctx, duration); err != nil {
		if terminate {
			e.log().Warn("pod did not terminate in time, forcing deletion")
			return e.Terminate(ctx, "SIGKILL")
		}
		return err
	}

	return nil
}

// Terminate forcefully stops the Pod by deleting it with a zero grace period.
func (e *Environment) Terminate(ctx context.Context, signal string) error {
	_ = signal // K8s doesn't support arbitrary signals; we just force-delete.

	pod, err := e.getPod(ctx)
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return errors.WithStack(err)
	}

	if !isPodRunning(pod) {
		e.markOffline()
		return nil
	}

	e.SetState(environment.ProcessStoppingState)

	gracePeriod := int64(0)
	err = e.client.CoreV1().Pods(e.namespace()).Delete(ctx, e.Id, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
	})
	if err != nil && !isNotFound(err) {
		return errors.WithStack(err)
	}

	e.SetState(environment.ProcessOfflineState)
	return nil
}

// markOffline transitions the environment to the offline state, first passing
// through the stopping state so that crash detection is not triggered. It is a
// no-op if the environment already considers itself offline.
func (e *Environment) markOffline() {
	if e.st.Load() != environment.ProcessOfflineState {
		e.SetState(environment.ProcessStoppingState)
		e.SetState(environment.ProcessOfflineState)
	}
}

// waitForPodRunning blocks until the Pod reaches Running phase or the context
// is canceled.
func (e *Environment) waitForPodRunning(ctx context.Context) error {
	// First check if already running.
	if running, _ := e.IsRunning(ctx); running {
		return nil
	}

	watcher, err := e.client.CoreV1().Pods(e.namespace()).Watch(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=" + e.Id,
	})
	if err != nil {
		return errors.Wrap(err, "environment/kubernetes: failed to watch pod")
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return errors.New("environment/kubernetes: watch channel closed")
			}
			if event.Type == watch.Modified || event.Type == watch.Added {
				pod, ok := event.Object.(*corev1.Pod)
				if !ok {
					continue
				}
				if isPodRunning(pod) {
					return nil
				}
				// Check for failure.
				if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
					return errors.New("environment/kubernetes: pod terminated before reaching running state")
				}
				// Check for container crashes during startup.
				for _, cs := range pod.Status.ContainerStatuses {
					if cs.State.Waiting != nil && strings.Contains(cs.State.Waiting.Reason, "CrashLoopBackOff") {
						return errors.New("environment/kubernetes: container in CrashLoopBackOff")
					}
					if cs.State.Waiting != nil && strings.Contains(cs.State.Waiting.Reason, "ErrImagePull") {
						return errors.New("environment/kubernetes: failed to pull container image")
					}
					if cs.State.Waiting != nil && strings.Contains(cs.State.Waiting.Reason, "ImagePullBackOff") {
						return errors.New("environment/kubernetes: image pull backoff")
					}
				}
			}
		}
	}
}

// waitForPodDeletion blocks until the Pod is fully deleted or the timeout
// elapses.
func (e *Environment) waitForPodDeletion(ctx context.Context, timeout time.Duration) error {
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-dctx.Done():
			return dctx.Err()
		case <-ticker.C:
			_, err := e.getPod(dctx)
			if err != nil && isNotFound(err) {
				return nil
			}
		}
	}
}

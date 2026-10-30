package action

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kairos-io/provider-kubernetes/internal/kubeadm"
	"github.com/kairos-io/provider-kubernetes/internal/kubeadm/credential"
	"github.com/kairos-io/provider-kubernetes/internal/kubeadmconfig"
	"github.com/kairos-io/provider-kubernetes/internal/reconcile"
	"github.com/kairos-io/provider-kubernetes/internal/reconcile/actualstate"
	"github.com/kairos-io/provider-kubernetes/internal/status"
)

func joinTestReconciler(e *KubeadmExecutor) reconcile.Reconciler {
	return reconcile.Reconciler{
		Budget: reconcile.Budget{PerAttempt: 5 * time.Second, MaxAttempts: 3, Total: 30 * time.Second},
		Exec:   e,
		Sleep:  func(time.Duration) {},
	}
}

func joinFailureStatus(t *testing.T, e *KubeadmExecutor) (status.Status, reconcile.RunResult) {
	t.Helper()
	res, err := joinTestReconciler(e).RunWithResult(context.Background(), []reconcile.Action{reconcile.ActionRunJoin})
	if err == nil {
		t.Fatal("expected the join to fail")
	}
	s := status.BuildStatus(status.BuildParams{
		Role:       e.Role,
		Membership: actualstate.Uninitialized,
		LastAction: res.LastAction,
		Err:        err,
		Result:     res,
		Now:        "2026-10-30T00:00:00Z",
	})
	return s, res
}

// A control plane that answers but rejects the join is not unreachable.
func TestRunJoinRejectedByControlPlaneStatusReason(t *testing.T) {
	fr := &fakeRunner{respond: func(args []string) (kubeadm.Result, error) {
		if args[0] == "join" {
			return kubeadm.Result{}, errors.New("error execution phase preflight: couldn't validate the identity of the API Server: cluster CA found in cluster-info ConfigMap is invalid")
		}
		return kubeadm.Result{}, nil
	}}
	e := &KubeadmExecutor{
		Runner:   fr,
		RootPath: "/",
		RunDir:   t.TempDir(),
		Role:     actualstate.RoleWorker,
		Input:    kubeadmconfig.Input{ControlPlaneEndpoint: "10.0.0.1:6443"},
		Join: &credential.JoinMaterial{
			Token:        "abcdef.0123456789abcdef",
			CACertHashes: []string{"sha256:deadbeef"},
		},
	}

	s, res := joinFailureStatus(t, e)
	if !res.BudgetExhausted {
		t.Fatalf("expected the join budget to be exhausted, got %+v", res)
	}
	if s.Reason != status.ReasonControlPlaneUnreachable {
		t.Errorf("reason = %q, want %q", s.Reason, status.ReasonControlPlaneUnreachable)
	}
}

// Missing join material is a config problem, not an unreachable control plane.
func TestRunJoinWithoutMaterialStatusReason(t *testing.T) {
	fr := &fakeRunner{}
	e := &KubeadmExecutor{
		Runner:   fr,
		RootPath: "/",
		RunDir:   t.TempDir(),
		Role:     actualstate.RoleWorker,
		Input:    kubeadmconfig.Input{ControlPlaneEndpoint: "10.0.0.1:6443"},
	}

	s, res := joinFailureStatus(t, e)
	if res.Attempts != 3 {
		t.Errorf("attempts = %d, want 3", res.Attempts)
	}
	if s.Terminal {
		t.Error("terminal = true, want false")
	}
	if s.Reason != status.ReasonControlPlaneUnreachable {
		t.Errorf("reason = %q, want %q", s.Reason, status.ReasonControlPlaneUnreachable)
	}
	if len(fr.calls) != 0 {
		t.Errorf("kubeadm was called without join material: %v", fr.calls)
	}
}

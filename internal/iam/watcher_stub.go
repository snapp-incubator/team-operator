//go:build !iam

package iam

import "context"

type noopWatcher struct{}

// NewWatcher returns a no-op watcher when the iam build tag is absent.
// The real-time CRB sync feature is disabled; the operator still reconciles
// admins from the IAM HTTP API on every normal reconcile cycle when
// --enable-iam-team-admin-access is set.
func NewWatcher(_ string) (AdminWatcher, error) { return &noopWatcher{}, nil }

func (n *noopWatcher) WatchAdmins(_ context.Context, _ string) <-chan struct{} { return nil }

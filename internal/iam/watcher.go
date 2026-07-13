package iam

import "context"

// AdminWatcher streams admin-change signals for a team. Each value received
// from WatchAdmins is a pure trigger — the reconciler re-fetches the
// authoritative admin list itself, so dropped or duplicated events never cause
// drift. Implementations must close the channel when the stream ends or ctx is
// cancelled.
type AdminWatcher interface {
	WatchAdmins(ctx context.Context, teamName string) <-chan struct{}
}

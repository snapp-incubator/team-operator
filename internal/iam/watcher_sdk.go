//go:build iam

package iam

import (
	"context"

	iamsdk "gitlab.snapp.ir/platform/iam-sdk/go"
)

type sdkWatcher struct{ client *iamsdk.Client }

// NewWatcher builds a real AdminWatcher backed by the IAM SDK.
// baseURL is the IAM host root (the SDK appends /api itself).
func NewWatcher(baseURL string) (AdminWatcher, error) {
	c, err := iamsdk.New(baseURL)
	if err != nil {
		return nil, err
	}
	return &sdkWatcher{client: c}, nil
}

// WatchAdmins wraps the SDK stream in a struct{} channel so the controller
// receives pure trigger signals without depending on SDK-internal types.
func (s *sdkWatcher) WatchAdmins(ctx context.Context, teamName string) <-chan struct{} {
	sdkCh := s.client.Teams.WatchAdmins(ctx, teamName)
	out := make(chan struct{})
	go func() {
		defer close(out)
		for range sdkCh {
			select {
			case out <- struct{}{}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

package instances_test

import (
	"context"
	"github.com/kandev/kandev/internal/instances"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestChangeNotifierOnlyAfterSuccessfulWrites(t *testing.T) {
	svc, _ := newTestService(t, true)
	count := 0
	svc.SetChangeNotifier(func() { count++ })
	ctx := t.Context()
	item, err := svc.CreateInstance(ctx, "owner", "Remote", "http://localhost", "", true)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	_, err = svc.UpdateInstance(ctx, "owner", item.ID, instances.InstanceUpdate{})
	require.NoError(t, err)
	require.Equal(t, 2, count)
	_, err = svc.UpdateInstance(ctx, "other", item.ID, instances.InstanceUpdate{})
	require.Error(t, err)
	require.Error(t, svc.DeleteInstance(ctx, "other", item.ID))
	require.Equal(t, 2, count)
	require.NoError(t, svc.DeleteInstance(ctx, "owner", item.ID))
	require.Equal(t, 3, count)
	svc.SetChangeNotifier(func() { panic("callback failure") })
	require.NotPanics(t, func() {
		_, err = svc.CreateInstance(ctx, "owner", "Remote", "http://localhost", "", true)
		require.NoError(t, err)
	})
	svc.SetChangeNotifier(func() { count++ })
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = svc.CreateInstance(canceled, "owner", "Remote", "http://localhost", "", true)
	require.Error(t, err)
	require.Equal(t, 3, count)
	disabled, _ := newTestService(t, false)
	disabled.SetChangeNotifier(func() { count++ })
	_, err = disabled.CreateInstance(ctx, "owner", "Remote", "http://localhost", "", true)
	require.ErrorIs(t, err, instances.ErrMultiInstanceDisabled)
	require.Equal(t, 3, count)
}

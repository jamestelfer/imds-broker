package imdsserver

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCached_ReturnsCachedValueWithinTTL(t *testing.T) {
	calls := 0
	c := NewCached(time.Minute, func() (int, error) {
		calls++
		return 42, nil
	})

	v, err := c.Get()
	require.NoError(t, err)
	assert.Equal(t, 42, v)

	v, err = c.Get()
	require.NoError(t, err)
	assert.Equal(t, 42, v)

	assert.Equal(t, 1, calls, "fetch should only be called once within TTL")
}

func TestCached_RefetchesAfterTTLExpiry(t *testing.T) {
	calls := 0
	c := NewCached(time.Millisecond, func() (int, error) {
		calls++
		return calls, nil
	})

	v, err := c.Get()
	require.NoError(t, err)
	assert.Equal(t, 1, v)

	time.Sleep(2 * time.Millisecond)

	v, err = c.Get()
	require.NoError(t, err)
	assert.Equal(t, 2, v)

	assert.Equal(t, 2, calls)
}

func TestCached_ErrorWithPopulatedCache_ReturnsStalValue(t *testing.T) {
	fetchErr := errors.New("fetch failed")
	calls := 0
	c := NewCached(time.Millisecond, func() (int, error) {
		calls++
		if calls > 1 {
			return 0, fetchErr
		}
		return 99, nil
	})

	// Populate cache.
	v, err := c.Get()
	require.NoError(t, err)
	assert.Equal(t, 99, v)

	time.Sleep(2 * time.Millisecond)

	// Fetch fails — stale value returned.
	v, err = c.Get()
	assert.ErrorIs(t, err, fetchErr)
	assert.Equal(t, 99, v, "stale value should be returned on error")
}

func TestCached_ErrorWithEmptyCache_ReturnsZeroValue(t *testing.T) {
	fetchErr := errors.New("fetch failed")
	c := NewCached(time.Minute, func() (int, error) {
		return 0, fetchErr
	})

	v, err := c.Get()
	assert.ErrorIs(t, err, fetchErr)
	assert.Equal(t, 0, v)
}

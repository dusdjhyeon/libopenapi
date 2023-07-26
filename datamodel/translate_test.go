package datamodel_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pb33f/libopenapi/datamodel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTranslateSliceParallel(t *testing.T) {
	testCases := []struct {
		MapSize int
	}{
		{MapSize: 1},
		{MapSize: 10},
		{MapSize: 100},
		{MapSize: 100_000},
	}

	for _, testCase := range testCases {
		mapSize := testCase.MapSize
		t.Run(fmt.Sprintf("Size %d", mapSize), func(t *testing.T) {
			t.Run("Happy path", func(t *testing.T) {
				var sl []int
				for i := 0; i < mapSize; i++ {
					sl = append(sl, i)
				}

				var translateCounter int64
				translateFunc := func(_, value int) (string, error) {
					result := fmt.Sprintf("foobar %d", value)
					atomic.AddInt64(&translateCounter, 1)
					return result, nil
				}
				var resultCounter int
				resultFunc := func(value string) error {
					assert.Equal(t, fmt.Sprintf("foobar %d", resultCounter), value)
					resultCounter++
					return nil
				}
				err := datamodel.TranslateSliceParallel[int, string](sl, translateFunc, resultFunc)
				time.Sleep(10 * time.Millisecond) // DEBUG
				require.NoError(t, err)
				assert.Equal(t, int64(mapSize), translateCounter)
				assert.Equal(t, mapSize, resultCounter)
			})

			t.Run("Error in translate", func(t *testing.T) {
				var sl []int
				for i := 0; i < mapSize; i++ {
					sl = append(sl, i)
				}

				var translateCounter int64
				translateFunc := func(_, _ int) (string, error) {
					atomic.AddInt64(&translateCounter, 1)
					return "", errors.New("Foobar")
				}
				var resultCounter int
				resultFunc := func(_ string) error {
					resultCounter++
					return nil
				}
				err := datamodel.TranslateSliceParallel[int, string](sl, translateFunc, resultFunc)
				require.ErrorContains(t, err, "Foobar")
				assert.Zero(t, resultCounter)
			})

			t.Run("Error in result", func(t *testing.T) {
				var sl []int
				for i := 0; i < mapSize; i++ {
					sl = append(sl, i)
				}

				translateFunc := func(_, value int) (string, error) {
					return "foobar", nil
				}
				var resultCounter int
				resultFunc := func(_ string) error {
					resultCounter++
					return errors.New("Foobar")
				}
				err := datamodel.TranslateSliceParallel[int, string](sl, translateFunc, resultFunc)
				require.ErrorContains(t, err, "Foobar")
			})

			t.Run("EOF in translate", func(t *testing.T) {
				var sl []int
				for i := 0; i < mapSize; i++ {
					sl = append(sl, i)
				}

				var translateCounter int64
				translateFunc := func(_, _ int) (string, error) {
					atomic.AddInt64(&translateCounter, 1)
					return "", io.EOF
				}
				var resultCounter int
				resultFunc := func(_ string) error {
					resultCounter++
					return nil
				}
				err := datamodel.TranslateSliceParallel[int, string](sl, translateFunc, resultFunc)
				require.NoError(t, err)
				assert.Zero(t, resultCounter)
			})

			t.Run("EOF in result", func(t *testing.T) {
				var sl []int
				for i := 0; i < mapSize; i++ {
					sl = append(sl, i)
				}

				translateFunc := func(_, value int) (string, error) {
					return "foobar", nil
				}
				var resultCounter int
				resultFunc := func(_ string) error {
					resultCounter++
					return io.EOF
				}
				err := datamodel.TranslateSliceParallel[int, string](sl, translateFunc, resultFunc)
				require.NoError(t, err)
			})

			t.Run("Continue in translate", func(t *testing.T) {
				var sl []int
				for i := 0; i < mapSize; i++ {
					sl = append(sl, i)
				}

				var translateCounter int64
				translateFunc := func(_, _ int) (string, error) {
					atomic.AddInt64(&translateCounter, 1)
					return "", datamodel.Continue
				}
				var resultCounter int
				resultFunc := func(_ string) error {
					resultCounter++
					return nil
				}
				err := datamodel.TranslateSliceParallel[int, string](sl, translateFunc, resultFunc)
				require.NoError(t, err)
				assert.Equal(t, int64(mapSize), translateCounter)
				assert.Zero(t, resultCounter)
			})
		})
	}
}

func TestTranslatePipeline(t *testing.T) {
	testCases := []struct {
		ItemCount int
	}{
		{ItemCount: 1},
		{ItemCount: 10},
		{ItemCount: 100},
		{ItemCount: 100_000},
	}

	for _, testCase := range testCases {
		itemCount := testCase.ItemCount
		t.Run(fmt.Sprintf("Size %d", itemCount), func(t *testing.T) {

			t.Run("Happy path", func(t *testing.T) {
				var inputErr error
				in := make(chan int)
				out := make(chan string)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var wg sync.WaitGroup
				wg.Add(2) // input and output goroutines.

				// Send input.
				go func() {
					defer wg.Done()
					for i := 0; i < itemCount; i++ {
						select {
						case in <- i:
						case <-ctx.Done():
							inputErr = errors.New("Context canceled unexpectedly")
						}
					}
					close(in)
				}()

				// Collect output.
				var resultCounter int
				go func() {
					defer func() {
						cancel()
						wg.Done()
					}()
					for {
						select {
						case result, ok := <-out:
							if !ok {
								return
							}
							assert.Equal(t, strconv.Itoa(resultCounter), result)
							resultCounter++
						case <-ctx.Done():
							return
						}
					}
				}()

				err := datamodel.TranslatePipeline[int, string](in, out,
					func(value int) (string, error) {
						return strconv.Itoa(value), nil
					},
				)
				wg.Wait()
				require.NoError(t, err)
				require.NoError(t, inputErr)
				assert.Equal(t, itemCount, resultCounter)
			})

			t.Run("Error in translate", func(t *testing.T) {
				in := make(chan int)
				out := make(chan string)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var wg sync.WaitGroup
				wg.Add(2) // input and output goroutines.

				// Send input.
				go func() {
					defer wg.Done()
					for i := 0; i < itemCount; i++ {
						select {
						case in <- i:
						case <-ctx.Done():
							// Context expected to cancel after the first translate.
						}
					}
					close(in)
				}()

				// Collect output.
				var resultCounter int
				go func() {
					defer func() {
						cancel()
						wg.Done()
					}()
					for {
						select {
						case _, ok := <-out:
							if !ok {
								return
							}
							resultCounter++
						case <-ctx.Done():
							return
						}
					}
				}()

				err := datamodel.TranslatePipeline[int, string](in, out,
					func(value int) (string, error) {
						return "", errors.New("Foobar")
					},
				)
				wg.Wait()
				require.ErrorContains(t, err, "Foobar")
				assert.Zero(t, resultCounter)
			})

			t.Run("Continue in translate", func(t *testing.T) {
				var inputErr error
				in := make(chan int)
				out := make(chan string)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var wg sync.WaitGroup
				wg.Add(2) // input and output goroutines.

				// Send input.
				go func() {
					defer wg.Done()
					for i := 0; i < itemCount; i++ {
						select {
						case in <- i:
						case <-ctx.Done():
							inputErr = errors.New("Context canceled unexpectedly")
						}
					}
					close(in)
				}()

				// Collect output.
				var resultCounter int
				go func() {
					defer func() {
						cancel()
						wg.Done()
					}()
					for {
						select {
						case _, ok := <-out:
							if !ok {
								return
							}
							resultCounter++
						case <-ctx.Done():
							return
						}
					}
				}()

				err := datamodel.TranslatePipeline[int, string](in, out,
					func(value int) (string, error) {
						return "", datamodel.Continue
					},
				)
				wg.Wait()
				require.NoError(t, err)
				require.NoError(t, inputErr)
				assert.Zero(t, resultCounter)
			})
		})
	}
}

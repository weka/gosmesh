package workers

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Result represents the result of processing an object.
type Result[T any] struct {
	Object T
	Err    error
}

// Results represents a collection of Result items and provides utility methods.
type Results[T any] struct {
	Items []Result[T]
}

// GetTopErrors returns the first three unique errors encountered.
func (r *Results[T]) GetTopErrors() []error {
	errorMap := make(map[error]struct{})
	topErrors := []error{}

	for _, result := range r.Items {
		if result.Err != nil {
			if _, exists := errorMap[result.Err]; !exists {
				errorMap[result.Err] = struct{}{}
				topErrors = append(topErrors, result.Err)
				if len(topErrors) == 3 {
					break
				}
			}
		}
	}

	return topErrors
}

// AllSucceeded checks if all results are successful (no errors).
func (r *Results[T]) AllSucceeded() bool {
	for _, result := range r.Items {
		if result.Err != nil {
			return false
		}
	}
	return true
}

func (r *Results[T]) GetErrors() []error {
	errors := []error{}
	for _, result := range r.Items {
		if result.Err != nil {
			errors = append(errors, result.Err)
		}
	}
	return errors
}

type MultiError struct {
	Errors []error
}

func (m *MultiError) Error() string {
	if len(m.Errors) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("Multiple errors:\n")
	for i, err := range m.Errors {
		sb.WriteString(fmt.Sprintf("  %d) %v\n", i+1, err))
	}
	return sb.String()
}

func (r *Results[T]) AsError() error {
	// serialize top three errors in single error, if none - return nil
	topErrors := r.GetTopErrors()
	if len(topErrors) == 0 {
		return nil
	}
	return &MultiError{Errors: topErrors}
}

// ProcessConcurrently processes a slice of objects with the given callback and worker count.
func ProcessConcurrently[T any](
	ctx context.Context,
	objects []T,
	numWorkers int,
	callback func(context.Context, T) error,
) *Results[T] {
	return ProcessConcurrentlyWithIndexes(ctx, objects, numWorkers, func(ctx context.Context, object T, i int) error {
		return callback(ctx, object)
	})
}

// ProcessConcurrentlyWithIndexes processes a slice of objects with the given callback and worker count.
func ProcessConcurrentlyWithIndexes[T any](
	ctx context.Context,
	objects []T,
	numWorkers int,
	callback func(context.Context, T, int) error,
) *Results[T] {
	var wg sync.WaitGroup
	results := &Results[T]{Items: make([]Result[T], len(objects))}
	jobs := make(chan int, len(objects))

	// Worker function
	worker := func() {
		defer wg.Done()
		for i := range jobs {
			select {
			case <-ctx.Done():
				results.Items[i] = Result[T]{Object: objects[i], Err: ctx.Err()}
			default:
				err := callback(ctx, objects[i], i)
				results.Items[i] = Result[T]{Object: objects[i], Err: err}
			}
		}
	}

	// Start workers
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go worker()
	}

	// Send jobs
	for i := range objects {
		jobs <- i
	}
	close(jobs)

	// Wait for workers to complete
	wg.Wait()

	return results
}

func Retry(ctx context.Context, numRetries int, callback func() error) error {
	for i := 0; i < numRetries; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := callback()
		if err == nil {
			return nil
		}
		if i == numRetries-1 {
			return err
		}
	}
	panic("unreachable")
}

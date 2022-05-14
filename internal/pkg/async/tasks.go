package async

import (
	"strings"
	"sync"

	"golang.org/x/exp/constraints"
)

type AsyncErrors[T any] struct {
	Errs []error
}

func (e AsyncErrors[T]) Error() any {
	var sb strings.Builder
	for _, err := range e.Errs {
		sb.WriteString(err.Error())
	}
	return sb.String()
}

func Map[T any, D any](src []T, concurrencyLimit int, f func(T) (D, error)) ([]D, error) {
	if len(src) == 0 {
		return []D{}, nil
	}

	if concurrencyLimit <= 0 {
		concurrencyLimit = len(src)
	}

	var wg sync.WaitGroup

	limiter := make(chan struct{}, concurrencyLimit)

	bufSize := min(len(src)/2, 32)
	resCh := make(chan D, bufSize)

	errCh := make(chan error, bufSize)

	errable := func(f func() error) {
		if err := f(); err != nil {
			errCh <- err
		}
	}

	// result fan-in
	results := []D{}
	go func() {
		for {
			res, ok := <-resCh
			if !ok {
				return
			}
			results = append(results, res)
			wg.Done()
		}
	}()

	// error fan-in
	errors := AsyncErrors[T]{}
	go func() {
		for {
			err, ok := <-errCh
			if !ok {
				return
			}
			errors.Errs = append(errors.Errs, err)
			wg.Done()
		}
	}()

	wg.Add(len(src))
	for _, element := range src {
		limiter <- struct{}{}
		go func(el T) {
			defer func() {
				<-limiter
			}()

			errable(func() error {
				r, err := f(el)
				if err != nil {
					return err
				}
				resCh <- r
				return nil
			})
		}(element)
	}

	wg.Wait()

	close(resCh)
	close(errCh)

	return results, nil
}

func FlatMap[T any, D any](src []T, concurrencyLimit int, f func(T) ([]D, error)) ([]D, error) {
	r, err := Map(src, concurrencyLimit, f)
	if err != nil {
		return nil, err
	}

	flattened := []D{}
	for _, v := range r {
		flattened = append(flattened, v...)
	}

	return flattened, nil
}

func min[T constraints.Ordered](a, b T) T {
	if a < b {
		return a
	} else {
		return b
	}
}

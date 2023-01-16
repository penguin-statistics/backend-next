package async

import (
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog/log"
)

// WaitAll waits for all the given errables to finish, and returns
// the last error occurred in all errables, if any.
func WaitAll(chans ...<-chan error) error {
	var wg sync.WaitGroup
	wg.Add(len(chans))

	var lastErr atomic.Value
	for _, ch := range chans {
		go func(ch <-chan error) {
			defer wg.Done()
			if err, open := <-ch; open {
				if err != nil {
					log.Error().Err(err).Msg("error occurred in async task")
					lastErr.Store(err)
				}
			} else {
				return
			}
		}(ch)
	}

	wg.Wait()

	if lastErr.Load() == nil {
		return nil
	}
	return lastErr.Load().(error)
}

package async

import "sync"

// WaitAll waits for all the given errables to finish, and returns
// the last error occurred in all errables, if any.
func WaitAll(chans ...<-chan error) error {
	var wg sync.WaitGroup
	wg.Add(len(chans))

	var lastErr error
	for _, ch := range chans {
		go func(ch <-chan error) {
			defer wg.Done()
			if err, open := <-ch; open {
				if err != nil {
					// TODO: lastErr should be atomically updated
					lastErr = err
				}
			} else {
				return
			}
		}(ch)
	}

	wg.Wait()
	return lastErr
}

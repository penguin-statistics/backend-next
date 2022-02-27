package async

func Errable(fn func() error) <-chan error {
	ch := make(chan error)
	go func() {
		ch <- fn()
		close(ch)
	}()
	return ch
}

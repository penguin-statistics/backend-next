package cache

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMutexGetSetDoesNotMakeOtherKeysWait(t *testing.T) {
	c := NewSet[int]("test")
	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		var v int
		_, _ = c.MutexGetSet("slow", &v, func() (*int, error) {
			close(started)
			<-release
			one := 1
			return &one, nil
		}, time.Minute)
	}()
	<-started

	done := make(chan int)
	go func() {
		var v int
		if _, err := c.MutexGetSet("fast", &v, func() (*int, error) {
			two := 2
			return &two, nil
		}, time.Minute); err != nil {
			t.Error(err)
		}
		done <- v
	}()
	select {
	case v := <-done:
		if v != 2 {
			t.Errorf("got %d, want 2", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("calculating a key waited for the calculation of another key")
	}
	close(release)
}

func TestMutexGetSetCalculatesAKeyOnce(t *testing.T) {
	c := NewSet[int]("test")
	var calculations atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var v int
			if _, err := c.MutexGetSet("key", &v, func() (*int, error) {
				calculations.Add(1)
				time.Sleep(20 * time.Millisecond)
				three := 3
				return &three, nil
			}, time.Minute); err != nil || v != 3 {
				t.Errorf("got %d, %v", v, err)
			}
		}()
	}
	wg.Wait()
	if n := calculations.Load(); n != 1 {
		t.Errorf("the key was calculated %d times, want once", n)
	}
}

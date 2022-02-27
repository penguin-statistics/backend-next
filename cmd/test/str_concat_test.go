package main

import (
	"strings"
	"testing"
)

func BenchmarkStrConcat(b *testing.B) {
	prefix := "item#itemId:"
	prefixBytes := []byte(prefix)
	key := "123"
	ch := make(chan struct{}, 1)
	b.Run("ConcatPlusSign", func(b *testing.B) {
		b.Log("ConcatPlusSign")
		for i := 0; i < b.N; i++ {
			_ = prefix + key
		}
	})
	b.Run("ConcatWithStringBuilder", func(b *testing.B) {
		b.Log("ConcatWithStringBuilder")
		for i := 0; i < b.N; i++ {
			var sb strings.Builder
			sb.WriteString(prefix)
			sb.WriteString(key)
			_ = sb.String()
		}
	})
	b.Run("ConcatWithByteSlice", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = append(prefixBytes, key...)
			// clear key from prefixBytes
			prefixBytes = prefixBytes[:len(prefix)]
		}
	})
	b.Run("ConcatWithByteSliceAndChanBlocker", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ch <- struct{}{}
			_ = append(prefixBytes, key...)
			// clear key from prefixBytes
			prefixBytes = prefixBytes[:len(prefix)]
			<-ch
		}
	})
}

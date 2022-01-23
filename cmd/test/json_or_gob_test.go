package main

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"io/ioutil"
	"testing"

	gojson "github.com/goccy/go-json"
	"github.com/uptrace/bun"

	"github.com/penguin-statistics/backend-next/internal/models"
)

func BenchmarkJsonOrGobEncoding(b *testing.B) {
	var db *bun.DB
	populate(&db)
	var stage models.Stage
	err := db.NewSelect().Model(&stage).Scan(context.Background())
	if err != nil {
		b.Fatal(err)
	}

	jsonEncoder := json.NewEncoder(ioutil.Discard)

	b.Run("jsonWithStaticEncoder", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			err := jsonEncoder.Encode(stage)
			if err != nil {
				b.Error(err)
			}
		}
	})

	b.Run("jsonWithoutStaticEncoder", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, err := json.Marshal(stage)
			if err != nil {
				b.Error(err)
			}
		}
	})

	gojsonEncoder := gojson.NewEncoder(ioutil.Discard)

	b.Run("gojsonWithStaticEncoder", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			err := gojsonEncoder.Encode(stage)
			if err != nil {
				b.Error(err)
			}
		}
	})

	b.Run("gojsonWithoutStaticEncoder", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, err := gojson.Marshal(stage)
			if err != nil {
				b.Error(err)
			}
		}
	})

	gobEncoder := gob.NewEncoder(ioutil.Discard)
	b.Run("gobWithStaticEncoder", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			err := gobEncoder.Encode(stage)
			if err != nil {
				b.Error(err)
			}
		}
	})

	b.Run("gobWithoutStaticEncoder", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			err := gob.NewEncoder(ioutil.Discard).Encode(stage)
			if err != nil {
				b.Error(err)
			}
		}
	})
}

func BenchmarkJsonOrGobDecoding(b *testing.B) {
	var db *bun.DB
	populate(&db)
	var stage models.Stage
	err := db.NewSelect().Model(&stage).Scan(context.Background())
	if err != nil {
		b.Fatal(err)
	}

	jsonToDecode, _ := json.Marshal(stage)

	b.Run("jsonWithoutStaticDecoder", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var unmarshalled models.Stage
			err := json.Unmarshal(jsonToDecode, &unmarshalled)
			if err != nil {
				b.Error(err)
			}
		}
	})

	b.Run("gojsonWithoutStaticDecoder", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var unmarshalled models.Stage
			err := gojson.Unmarshal(jsonToDecode, &unmarshalled)
			if err != nil {
				b.Error(err)
			}
		}
	})

	var buf bytes.Buffer
	_ = gob.NewEncoder(&buf).Encode(stage)
	reader := bytes.NewReader(buf.Bytes())

	b.Run("gobWithoutStaticDecoder", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			err := gob.NewDecoder(reader).Decode(&stage)
			if err != nil {
				b.Error(err)
			}
			reader.Seek(0, 0)
		}
	})
}

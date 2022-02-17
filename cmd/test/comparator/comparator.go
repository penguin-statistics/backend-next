package comparator

import (
	"fmt"
	"io/ioutil"

	"github.com/pkg/errors"

	"github.com/tidwall/gjson"
)

type Comparator struct {
	referenceJson gjson.Result
}

func NewComparator(referenceJson gjson.Result) *Comparator {
	return &Comparator{referenceJson: referenceJson}
}

func NewComparatorFromFilePath(filePath string) (*Comparator, error) {
	file, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	json := gjson.ParseBytes(file)
	if !json.IsObject() {
		return nil, errors.New("ambiguous comparison: reference is an array: unable to determine object to compare, use a specific reference object for comparison instead")
	}
	return NewComparator(json), nil
}

func (c *Comparison) shouldTolerance(key string) bool {
	for _, k := range c.toleranceKeys {
		if key == k {
			return true
		}
	}
	return false
}

func gjsonIsBool(r gjson.Result) bool {
	return r.Type == gjson.True || r.Type == gjson.False
}

func (c *Comparison) recursive(actual gjson.Result, ref gjson.Result) error {
	newError := func(a gjson.Result, b gjson.Result, msg string) error {
		return fmt.Errorf("comparison failed:\nactual at %s (value: %s)\nreference at %s (value: %s)\n%s", a.Path(c.actualJson.Raw), a.Raw, b.Path(c.referenceJson.Raw), b.Raw, msg)
	}

	if ref.IsObject() {
		for key, value := range ref.Map() {
			if c.shouldTolerance(key) {
				continue
			}

			if !actual.Get(key).Exists() {
				return newError(actual, value, "key "+key+" not found")
			}
			if err := c.recursive(actual.Get(key), value); err != nil {
				return err
			}
		}
	} else if gjsonIsBool(actual) && gjsonIsBool(ref) {
		return nil
	} else if ref.Type != actual.Type {
		return newError(actual, ref, "type mismatch: expect type to be "+ref.Type.String()+", but found "+actual.Type.String())
	}
	return nil
}

type Comparison struct {
	actualJson    gjson.Result
	referenceJson gjson.Result
	toleranceKeys []string
}

func (c *Comparator) Compare(actualJson []byte, toleranceKeys []string) error {
	res := gjson.ParseBytes(actualJson)
	if !res.IsObject() && !res.IsArray() {
		return errors.New("invalid actualJson structure")
	}

	comp := &Comparison{
		actualJson:    res,
		referenceJson: c.referenceJson,
		toleranceKeys: toleranceKeys,
	}

	if !res.IsArray() {
		return errors.New("ambiguous comparison: actual is not an array")
	}

	for _, value := range res.Array() {
		err := comp.recursive(value, c.referenceJson)
		if err != nil {
			return err
		}
	}
	return nil
}
